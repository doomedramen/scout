import { randomUUID } from "node:crypto";
import { isIP } from "node:net";

import { decryptCredential } from "@/lib/server/credentials";
import { inferDefaultRoute } from "@/lib/discovery/network";
import { authorizeAgentInvitation, createAgentInvitation } from "@/lib/server/invitations";
import { renderInstallerScript } from "@/lib/server/installer";
import { readPublisherPublicKey } from "@/lib/server/releases";
import { getDatabase } from "@/lib/server/db";
import {
  connectSsh,
  type SshCommandResult,
  type SshConnection,
  type SshCredential,
  type SshEndpoint,
} from "@/lib/server/ssh";

export const ENROLLMENT_STAGES = [
  "queued",
  "connecting",
  "identity-verification",
  "authentication",
  "privilege-check",
  "installation",
  "agent-wait",
  "complete",
] as const;

export type EnrollmentStage = (typeof ENROLLMENT_STAGES)[number];

const JOB_LEASE_MS = 5 * 60 * 1_000;
const AGENT_WAIT_MS = 30 * 1_000;
const AGENT_POLL_MS = 500;

export type EnrollmentContext = {
  jobId: string;
  systemId: string;
  endpoint: SshEndpoint;
  fingerprint: string;
  credential: SshCredential & { username: string };
  serverUrl: string;
  callbackUrl: string;
  invitation: string;
  publisherPublicKey: string | null;
};

export type EnrollmentExecutor = (
  context: EnrollmentContext,
  setStage: (stage: EnrollmentStage) => void,
) => Promise<void>;

export type EnrollmentRun = {
  jobId: string;
  status: "complete" | "failed" | "blocked";
  stage: string;
  errorMessage: string | null;
};

export class EnrollmentFailure extends Error {
  constructor(
    message: string,
    public readonly code:
      | "missing-evidence"
      | "missing-credential"
      | "host-identity"
      | "authentication"
      | "privilege"
      | "callback"
      | "installer"
      | "agent-timeout",
  ) {
    super(message);
  }
}

type ClaimedJob = {
  id: string;
  systemId: string;
  credentialId: string | null;
  leaseOwner: string;
};

type JobContextRow = {
  id: string;
  systemId: string;
  credentialId: string | null;
  username: string | null;
  method: string | null;
  secretCiphertext: string | null;
  nonce: string | null;
  address: string | null;
  port: number | null;
  fingerprint: string | null;
};

export async function processNextEnrollmentJob(
  executor: EnrollmentExecutor = runSshInstallation,
  now = Date.now(),
): Promise<EnrollmentRun | null> {
  const claimed = claimNextEnrollmentJob(now);
  if (!claimed) return null;

  let stage: EnrollmentStage = "connecting";
  const setStage = (next: EnrollmentStage) => {
    stage = next;
    updateJobStage(claimed, next);
  };

  try {
    const context = buildEnrollmentContext(claimed, now);
    setStage("identity-verification");
    await executor(context, setStage);
    setStage("agent-wait");
    await waitForAgent(context.systemId);
    completeJob(claimed, now);
    return { jobId: claimed.id, status: "complete", stage: "complete", errorMessage: null };
  } catch (error) {
    const message = error instanceof Error ? error.message : "Enrollment failed.";
    const status =
      error instanceof EnrollmentFailure && error.code === "agent-timeout" ? "blocked" : "failed";
    failJob(
      claimed,
      stage,
      status,
      error instanceof EnrollmentFailure ? error.code : "installer",
      message,
      now,
    );
    return { jobId: claimed.id, status, stage, errorMessage: message };
  }
}

export function claimNextEnrollmentJob(now = Date.now()): ClaimedJob | null {
  const { sqlite } = getDatabase();
  const owner = randomUUID();
  const claim = sqlite.transaction(() => {
    const row = sqlite
      .prepare(
        "SELECT id, system_id AS systemId, credential_id AS credentialId FROM enrollment_job WHERE status = 'queued' OR (status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?) ORDER BY created_at ASC LIMIT 1",
      )
      .get(now) as { id: string; systemId: string; credentialId: string | null } | undefined;
    if (!row) return null;
    const changed = sqlite
      .prepare(
        "UPDATE enrollment_job SET status = 'running', stage = 'connecting', lease_owner = ?, lease_expires_at = ?, attempt = attempt + 1, updated_at = ? WHERE id = ? AND (status = 'queued' OR (status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
      )
      .run(owner, now + JOB_LEASE_MS, now, row.id, now);
    return changed.changes === 1 ? { ...row, leaseOwner: owner } : null;
  });
  return claim();
}

function buildEnrollmentContext(job: ClaimedJob, now: number): EnrollmentContext {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      `
        SELECT
          ej.id,
          ej.system_id AS systemId,
          ej.credential_id AS credentialId,
          cg.username,
          cg.method,
          cg.secret_ciphertext AS secretCiphertext,
          cg.nonce,
          ae.address,
          ae.port,
          tk.fingerprint
        FROM enrollment_job ej
        LEFT JOIN credential_grant cg ON cg.id = ej.credential_id AND cg.enabled = 1
        LEFT JOIN access_evidence ae
          ON ae.system_id = ej.system_id
          AND ae.method = 'ssh'
          AND ae.outcome = 'open'
          AND ae.expires_at > ?
        LEFT JOIN trusted_host_key tk
          ON tk.system_id = ej.system_id
          AND tk.method = 'ssh'
          AND tk.revoked_at IS NULL
        WHERE ej.id = ?
        ORDER BY ae.observed_at DESC, tk.accepted_at DESC
        LIMIT 1
      `,
    )
    .get(now, job.id) as JobContextRow | undefined;
  if (!row?.address || !row.port) {
    throw new EnrollmentFailure(
      "The system no longer has current SSH evidence.",
      "missing-evidence",
    );
  }
  if (!row.username || !row.method || !row.secretCiphertext || !row.nonce) {
    throw new EnrollmentFailure(
      "The encrypted SSH credential is unavailable.",
      "missing-credential",
    );
  }
  if (row.method !== "ssh") {
    throw new EnrollmentFailure(
      "The selected access method is not supported yet.",
      "missing-credential",
    );
  }
  if (!row.fingerprint) {
    throw new EnrollmentFailure("The SSH host identity has not been trusted.", "host-identity");
  }

  const decrypted = decryptCredential(
    { ciphertext: row.secretCiphertext, nonce: row.nonce },
    { systemId: row.systemId, method: row.method, username: row.username },
  );
  const credential = {
    ...decrypted,
    privilegePassword:
      decrypted.privilegePassword ?? (decrypted.authType === "password" ? decrypted.secret : null),
  };
  const callbackUrl = callbackOrigin(row.address);
  const invitation = createAgentInvitation(row.systemId, now);
  let publisherPublicKey: string | null = null;
  try {
    publisherPublicKey = readPublisherPublicKey();
  } catch {
    // The artifact endpoint remains the final verification boundary. A
    // missing local release key makes installation fail closed there, while
    // keeping unit tests and development installs able to render a script.
  }
  return {
    jobId: row.id,
    systemId: row.systemId,
    endpoint: { address: row.address, port: row.port },
    fingerprint: row.fingerprint,
    credential: { ...credential, username: row.username },
    serverUrl: callbackUrl,
    callbackUrl,
    invitation: invitation.token,
    publisherPublicKey,
  };
}

async function runSshInstallation(
  context: EnrollmentContext,
  setStage: (stage: EnrollmentStage) => void,
): Promise<void> {
  let connection: SshConnection | undefined;
  const remoteDirectory = `/tmp/scout-agent-${context.jobId}`;
  const scriptPath = `${remoteDirectory}/install.sh`;
  const privilegePath = `${remoteDirectory}/privilege-password`;
  try {
    connection = await connectSsh(context.endpoint, context.credential, context.fingerprint);
    setStage("authentication");
    const privilege = await connection.exec("id -u");
    ensureCommandSucceeded(privilege, "The SSH account could not run commands.", "privilege");
    setStage("privilege-check");
    await execOrThrow(
      connection,
      `umask 077; mkdir -p '${remoteDirectory}'; chmod 700 '${remoteDirectory}'`,
      "Could not prepare the remote installer directory.",
    );
    const script = renderInstallerScript({
      serverUrl: context.serverUrl,
      callbackUrl: context.callbackUrl,
      invitation: context.invitation,
      publisherPublicKey: context.publisherPublicKey ?? undefined,
    });
    await execOrThrow(
      connection,
      `cat > '${scriptPath}'; chmod 700 '${scriptPath}'`,
      "Could not transfer the Scout installer.",
      script,
    );
    if (context.credential.privilegePassword) {
      await execOrThrow(
        connection,
        `cat > '${privilegePath}'; chmod 600 '${privilegePath}'`,
        "Could not prepare the privilege credential.",
        `${context.credential.privilegePassword}\n`,
      );
    }
    setStage("installation");
    const privilegeEnvironment = context.credential.privilegePassword
      ? `SCOUT_PRIVILEGE_FILE='${privilegePath}' `
      : "";
    const install = await connection.exec(`${privilegeEnvironment}/bin/sh '${scriptPath}'`);
    if (install.code !== 0 && /Scout callback is unreachable/i.test(install.stderr)) {
      throw new EnrollmentFailure(
        `The target host ${context.endpoint.address} could not reach Scout at ${context.callbackUrl}. Set SCOUT_PUBLIC_URL to a URL reachable from that host.`,
        "callback",
      );
    }
    ensureCommandSucceeded(
      install,
      install.stderr.trim() || "The remote Scout installer failed.",
      "installer",
    );
  } catch (error) {
    if (error instanceof EnrollmentFailure) throw error;
    throw new EnrollmentFailure(
      error instanceof Error ? error.message : "SSH installation failed.",
      "authentication",
    );
  } finally {
    if (connection) {
      await connection.exec(`rm -rf '${remoteDirectory}'`).catch(() => undefined);
      connection.close();
    }
  }
}

async function execOrThrow(
  connection: SshConnection,
  command: string,
  message: string,
  input?: string,
): Promise<SshCommandResult> {
  const result = await connection.exec(command, input);
  ensureCommandSucceeded(result, result.stderr.trim() || message, "installer");
  return result;
}

function ensureCommandSucceeded(
  result: SshCommandResult,
  message: string,
  code: "privilege" | "installer",
): void {
  if (result.code !== 0) throw new EnrollmentFailure(message, code);
}

async function waitForAgent(systemId: string, timeoutMs = AGENT_WAIT_MS): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const { sqlite } = getDatabase();
    const agent = sqlite
      .prepare("SELECT 1 FROM agent WHERE system_id = ? AND revoked_at IS NULL LIMIT 1")
      .get(systemId);
    if (agent) return;
    await new Promise((resolve) => setTimeout(resolve, AGENT_POLL_MS));
  }
  throw new EnrollmentFailure(
    "The agent did not enroll before the installation window expired.",
    "agent-timeout",
  );
}

function updateJobStage(job: ClaimedJob, stage: EnrollmentStage, now = Date.now()): void {
  const { sqlite } = getDatabase();
  const changed = sqlite
    .prepare(
      "UPDATE enrollment_job SET stage = ?, lease_expires_at = ?, updated_at = ? WHERE id = ? AND lease_owner = ? AND status = 'running'",
    )
    .run(stage, now + JOB_LEASE_MS, now, job.id, job.leaseOwner);
  if (changed.changes !== 1) throw new Error("Enrollment job lease was lost.");
}

function completeJob(job: ClaimedJob, now: number): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "UPDATE enrollment_job SET status = 'complete', stage = 'complete', lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ? AND lease_owner = ?",
    )
    .run(now, job.id, job.leaseOwner);
}

function failJob(
  job: ClaimedJob,
  stage: EnrollmentStage,
  status: "failed" | "blocked",
  code: string,
  message: string,
  now: number,
): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "UPDATE enrollment_job SET status = ?, stage = ?, error_code = ?, error_message = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ? AND lease_owner = ?",
    )
    .run(status, stage, code, message, now, job.id, job.leaseOwner);
  sqlite
    .prepare(
      "UPDATE system SET status = 'blocked', updated_at = ? WHERE id = ? AND status = 'installing'",
    )
    .run(now, job.systemId);
}

function callbackOrigin(targetAddress?: string): string {
  const configured = process.env.SCOUT_PUBLIC_URL?.trim();
  if (configured) {
    let url: URL;
    try {
      url = new URL(configured);
    } catch {
      throw new EnrollmentFailure("SCOUT_PUBLIC_URL is not a valid callback URL.", "callback");
    }
    const origin = url.origin;
    if (targetAddress && isLoopbackHostname(url.hostname) && !isLoopbackHostname(targetAddress)) {
      throw new EnrollmentFailure(
        `SCOUT_PUBLIC_URL points to loopback-only address ${origin}, which target host ${targetAddress} cannot reach. Set SCOUT_PUBLIC_URL to the Scout host's LAN or DNS URL.`,
        "callback",
      );
    }
    return origin;
  }
  const inference = inferDefaultRoute();
  if (inference.kind !== "ready") {
    throw new EnrollmentFailure(
      "Scout cannot determine a callback address for the target host. Set SCOUT_PUBLIC_URL.",
      "callback",
    );
  }
  const port = process.env.SCOUT_PORT ?? process.env.PORT ?? "8080";
  return `http://${inference.boundary.sourceAddress}:${port}`;
}

function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  return (
    normalized === "localhost" ||
    normalized === "::1" ||
    normalized === "0.0.0.0" ||
    (isIP(normalized) === 4 && normalized.startsWith("127."))
  );
}

export function invitationIsValid(token: string, now = Date.now()): boolean {
  return authorizeAgentInvitation(token, now) !== null;
}
