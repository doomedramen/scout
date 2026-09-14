import { randomUUID } from "node:crypto";

import { decryptCredential } from "@/lib/server/credentials";
import { getDatabase } from "@/lib/server/db";
import { resolveSystemId } from "@/lib/server/system-identity";
import {
  connectSsh,
  type SshCommandResult,
  type SshConnection,
  type SshCredential,
  type SshEndpoint,
} from "@/lib/server/ssh";

export class LifecycleError extends Error {
  constructor(
    message: string,
    public readonly code: "not-found" | "invalid-state" | "no-credential",
  ) {
    super(message);
  }
}

export type RetryReceipt = { jobId: string; status: "queued"; stage: "queued" };

export type UninstallContext = {
  systemId: string;
  endpoint: SshEndpoint;
  fingerprint: string;
  credential: SshCredential & { username: string };
};

export type UninstallExecutor = (context: UninstallContext) => Promise<void>;

export type DecommissionResult = {
  systemId: string;
  status: "revoked";
  uninstall: "complete" | "pending/manual";
};

export function retryEnrollment(systemId: string, now = Date.now()): RetryReceipt {
  const { sqlite } = getDatabase();
  const retry = sqlite.transaction(() => {
    const canonicalId = resolveSystemId(sqlite, systemId);
    const system = sqlite
      .prepare("SELECT id, excluded FROM system WHERE id = ? LIMIT 1")
      .get(canonicalId) as { id: string; excluded: number } | undefined;
    if (!system || system.excluded) throw new LifecycleError("System not found.", "not-found");
    const credential = sqlite
      .prepare(
        "SELECT id FROM credential_grant WHERE system_id = ? AND method = 'ssh' AND enabled = 1 ORDER BY updated_at DESC LIMIT 1",
      )
      .get(canonicalId) as { id: string } | undefined;
    if (!credential)
      throw new LifecycleError("No stored SSH credential is available for retry.", "no-credential");
    const current = sqlite
      .prepare(
        "SELECT id, status FROM enrollment_job WHERE system_id = ? ORDER BY created_at DESC LIMIT 1",
      )
      .get(canonicalId) as { id: string; status: string } | undefined;
    if (current && ["queued", "running"].includes(current.status))
      throw new LifecycleError("An installation is already in progress.", "invalid-state");
    const jobId = current?.id ?? randomUUID();
    if (current) {
      sqlite
        .prepare(
          "UPDATE enrollment_job SET credential_id = ?, status = 'queued', stage = 'queued', error_code = NULL, error_message = NULL, lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ?",
        )
        .run(credential.id, now, jobId);
    } else {
      sqlite
        .prepare(
          "INSERT INTO enrollment_job (id, system_id, credential_id, status, stage, created_at, updated_at) VALUES (?, ?, ?, 'queued', 'queued', ?, ?)",
        )
        .run(jobId, canonicalId, credential.id, now, now);
    }
    sqlite
      .prepare("UPDATE system SET status = 'installing', updated_at = ? WHERE id = ?")
      .run(now, canonicalId);
    return { jobId, status: "queued" as const, stage: "queued" as const };
  });
  return retry();
}

export async function decommissionSystem(
  systemId: string,
  now = Date.now(),
  executor: UninstallExecutor = uninstallOverSsh,
): Promise<DecommissionResult> {
  const { sqlite } = getDatabase();
  const canonicalId = resolveSystemId(sqlite, systemId);
  const system = sqlite.prepare("SELECT id FROM system WHERE id = ? LIMIT 1").get(canonicalId);
  if (!system) throw new LifecycleError("System not found.", "not-found");

  let context: UninstallContext | null = null;
  try {
    context = loadUninstallContext(canonicalId, now);
  } catch {
    // Revocation must still complete when the stored credential is corrupt or
    // the target no longer has enough evidence for an SSH uninstall.
  }

  const revoke = sqlite.transaction(() => {
    sqlite
      .prepare(
        "UPDATE agent SET revoked_at = ?, updated_at = ? WHERE system_id = ? AND revoked_at IS NULL",
      )
      .run(now, now, canonicalId);
    sqlite
      .prepare(
        "UPDATE enrollment_job SET status = 'blocked', stage = 'queued', error_code = 'decommissioned', error_message = 'Installation was stopped because this system was decommissioned.', lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE system_id = ? AND status IN ('queued', 'running')",
      )
      .run(now, canonicalId);
    sqlite
      .prepare("UPDATE credential_grant SET enabled = 0, updated_at = ? WHERE system_id = ?")
      .run(now, canonicalId);
    sqlite
      .prepare("UPDATE system SET status = 'revoked', updated_at = ? WHERE id = ?")
      .run(now, canonicalId);
  });
  revoke();

  if (!context) {
    recordDecommissionAudit(canonicalId, "pending/manual", now);
    return { systemId: canonicalId, status: "revoked", uninstall: "pending/manual" };
  }

  try {
    await executor(context);
    recordDecommissionAudit(canonicalId, "complete", now);
    return { systemId: canonicalId, status: "revoked", uninstall: "complete" };
  } catch {
    recordDecommissionAudit(canonicalId, "pending/manual", now);
    return { systemId: canonicalId, status: "revoked", uninstall: "pending/manual" };
  }
}

function loadUninstallContext(systemId: string, now: number): UninstallContext | null {
  const { sqlite } = getDatabase();
  const row = sqlite
    .prepare(
      `
        SELECT
          cg.username,
          cg.method,
          cg.secret_ciphertext AS ciphertext,
          cg.nonce,
          ae.address,
          ae.port,
          tk.fingerprint
        FROM credential_grant cg
        INNER JOIN access_evidence ae
          ON ae.system_id = cg.system_id
          AND ae.method = 'ssh'
          AND ae.outcome = 'open'
          AND ae.expires_at > ?
        INNER JOIN trusted_host_key tk
          ON tk.system_id = cg.system_id
          AND tk.method = 'ssh'
          AND tk.revoked_at IS NULL
        WHERE cg.system_id = ?
          AND cg.method = 'ssh'
          AND cg.enabled = 1
        ORDER BY cg.updated_at DESC, ae.observed_at DESC, tk.accepted_at DESC
        LIMIT 1
      `,
    )
    .get(now, systemId) as
    | {
        username: string;
        method: string;
        ciphertext: string;
        nonce: string;
        address: string;
        port: number;
        fingerprint: string;
      }
    | undefined;
  if (!row || row.method !== "ssh") return null;
  const credential = decryptCredential(
    { ciphertext: row.ciphertext, nonce: row.nonce },
    { systemId, method: row.method, username: row.username },
  );
  return {
    systemId,
    endpoint: { address: row.address, port: row.port },
    fingerprint: row.fingerprint,
    credential: {
      ...credential,
      privilegePassword:
        credential.privilegePassword ??
        (credential.authType === "password" ? credential.secret : null),
      username: row.username,
    },
  };
}

async function uninstallOverSsh(context: UninstallContext): Promise<void> {
  let connection: SshConnection | undefined;
  try {
    connection = await connectSsh(context.endpoint, context.credential, context.fingerprint);
    const identity = await connection.exec("id -u");
    ensureSuccess(identity, "The SSH account could not run commands.");
    const script = uninstallScript();
    const result =
      identity.stdout.trim() === "0"
        ? await connection.exec(script)
        : await connection.exec(
            `sudo -S -p '' sh -c '${shellQuote(script)}'`,
            context.credential.privilegePassword
              ? `${context.credential.privilegePassword}\n`
              : undefined,
          );
    ensureSuccess(result, "The authorized SSH account could not remove the Scout agent.");
  } finally {
    connection?.close();
  }
}

function uninstallScript(): string {
  return [
    "set -eu",
    'case "$(uname -s)" in',
    "  Linux)",
    "    if command -v systemctl >/dev/null 2>&1; then",
    "      systemctl disable --now scout-agent.service >/dev/null 2>&1 || true",
    "      systemctl daemon-reload >/dev/null 2>&1 || true",
    "    fi",
    "    rm -f /etc/systemd/system/scout-agent.service /usr/local/lib/scout-agent/scout-agent-launcher",
    "    rm -rf /var/lib/scout-agent",
    "    ;;",
    "  Darwin)",
    "    launchctl bootout system /Library/LaunchDaemons/page.rtin.scout-agent.plist >/dev/null 2>&1 || true",
    "    rm -f /Library/LaunchDaemons/page.rtin.scout-agent.plist /usr/local/lib/scout-agent/scout-agent-launcher",
    "    rm -rf '/Library/Application Support/ScoutAgent'",
    "    ;;",
    '  *) printf "%s\\n" "unsupported target operating system" >&2; exit 2 ;;',
    "esac",
  ].join("\n");
}

function ensureSuccess(result: SshCommandResult, message: string): void {
  if (result.code !== 0) throw new Error(result.stderr.trim() || message);
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function recordDecommissionAudit(
  systemId: string,
  uninstall: "complete" | "pending/manual",
  now: number,
): void {
  const { sqlite } = getDatabase();
  sqlite
    .prepare(
      "INSERT INTO audit_event (id, action, resource_type, resource_id, metadata, created_at) VALUES (?, 'decommission', 'system', ?, ?, ?)",
    )
    .run(randomUUID(), systemId, JSON.stringify({ uninstall }), now);
}
