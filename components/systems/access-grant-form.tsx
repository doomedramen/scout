"use client";

import { useEffect, useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

type Preflight = {
  target: string;
  fingerprint: string;
  trustedFingerprint: string | null;
  trusted: boolean;
  automaticEnrollment?: {
    queued: boolean;
    reason: string;
    jobId: string | null;
  };
};

type Job = {
  jobId: string;
  systemId?: string;
  status: string;
  stage: string;
  errorCode?: string | null;
  errorMessage?: string | null;
  attempt?: number;
  createdAt?: string;
  updatedAt?: string;
  leaseExpiresAt?: string | null;
  target?: string | null;
};

const installationStages = [
  "queued",
  "connecting",
  "identity-verification",
  "authentication",
  "privilege-check",
  "installation",
  "agent-wait",
  "complete",
] as const;

const stageLabels: Record<(typeof installationStages)[number], string> = {
  queued: "Queued",
  connecting: "Connecting to SSH",
  "identity-verification": "Verifying host identity",
  authentication: "Authenticating",
  "privilege-check": "Checking privileges",
  installation: "Installing agent",
  "agent-wait": "Waiting for agent",
  complete: "Complete",
};

const stageDescriptions: Record<(typeof installationStages)[number], string> = {
  queued: "Waiting for an installation worker to pick up this job.",
  connecting: "Opening a verified SSH connection to the target host.",
  "identity-verification": "Checking the SSH host identity against the fingerprint you approved.",
  authentication: "Authenticating with the encrypted SSH credential.",
  "privilege-check": "Checking that the SSH account can install a system service.",
  installation: "Transferring and running the signed Scout agent installer.",
  "agent-wait": "Waiting for the new agent to check in with Scout.",
  complete: "The agent enrolled successfully; telemetry will appear shortly.",
};

function isTerminal(job: Job): boolean {
  return ["complete", "failed", "blocked"].includes(job.status);
}

function timestamp(value: string | undefined): string {
  if (!value) return "Not available";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "Not available" : date.toLocaleString();
}

function stageDescription(job: Job): string {
  if (job.errorMessage) return job.errorMessage;
  return (
    stageDescriptions[job.stage as (typeof installationStages)[number]] ??
    "Scout is working on this system."
  );
}

export function AccessGrantForm({
  systemId,
  initialJob = null,
  target = null,
}: {
  systemId: string;
  initialJob?: Job | null;
  target?: string | null;
}) {
  const router = useRouter();
  const [preflight, setPreflight] = useState<Preflight | null>(null);
  const [preflightError, setPreflightError] = useState("");
  const [username, setUsername] = useState("");
  const [authType, setAuthType] = useState<"password" | "private-key">("password");
  const [secret, setSecret] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [privilegePassword, setPrivilegePassword] = useState("");
  const [boundedScope, setBoundedScope] = useState(false);
  const [automaticEnrollment, setAutomaticEnrollment] = useState(false);
  const [firstSeenKeyPinning, setFirstSeenKeyPinning] = useState(false);
  const [trust, setTrust] = useState(false);
  const [job, setJob] = useState<Job | null>(initialJob);
  const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const jobId = job?.jobId;
  const jobStatus = job?.status;

  useEffect(() => {
    if (jobStatus && !["complete", "failed", "blocked"].includes(jobStatus)) return;
    let active = true;
    fetch(`/api/v1/systems/${systemId}/access-preflight`, { signal: AbortSignal.timeout(8_000) })
      .then(async (response) => {
        const payload = (await response.json().catch(() => ({}))) as Preflight & {
          error?: { message?: string };
        };
        if (!response.ok)
          throw new Error(payload.error?.message ?? "SSH fingerprint is not available yet.");
        if (active) {
          setPreflight(payload);
          setTrust(payload.trusted);
          if (payload.automaticEnrollment?.jobId) {
            setJob({
              jobId: payload.automaticEnrollment.jobId,
              status: payload.automaticEnrollment.queued ? "queued" : "blocked",
              stage: "queued",
              target: payload.target,
            });
          }
        }
      })
      .catch((reason: unknown) => {
        if (active)
          setPreflightError(
            reason instanceof Error ? reason.message : "SSH fingerprint is not available yet.",
          );
      });
    return () => {
      active = false;
    };
  }, [jobStatus, systemId]);

  useEffect(() => {
    if (!jobId || !jobStatus || ["complete", "failed", "blocked"].includes(jobStatus)) return;
    let active = true;
    const timer = window.setInterval(() => {
      fetch(`/api/v1/enrollment-jobs/${jobId}`, { signal: AbortSignal.timeout(8_000) })
        .then(async (response) => {
          if (!response.ok) return null;
          return (await response.json()) as Job;
        })
        .then((next) => {
          if (active && next) setJob((current) => (current ? { ...current, ...next } : next));
        })
        .catch(() => undefined);
    }, 2_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [jobId, jobStatus]);

  useEffect(() => {
    if (job?.status === "complete") router.refresh();
  }, [job?.status, router]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!preflight) return;
    setPending(true);
    setError("");
    const requestKey = idempotencyKey ?? crypto.randomUUID();
    if (!idempotencyKey) setIdempotencyKey(requestKey);
    try {
      const response = await fetch(`/api/v1/systems/${systemId}/access-grants`, {
        method: "POST",
        headers: { "content-type": "application/json", "idempotency-key": requestKey },
        body: JSON.stringify({
          method: "ssh",
          username,
          authType,
          secret,
          passphrase: authType === "private-key" ? passphrase : null,
          privilegePassword: privilegePassword || null,
          fingerprint: preflight.fingerprint,
          trust: trust || firstSeenKeyPinning,
          scope: boundedScope ? "bounded-subnet" : "exact-host",
          automaticEnrollment: boundedScope && automaticEnrollment,
          firstSeenKeyPinning: boundedScope && automaticEnrollment && firstSeenKeyPinning,
        }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as Job & {
        error?: { message?: string };
      };
      if (!response.ok) {
        setError(payload.error?.message ?? "The SSH credential could not be stored.");
        return;
      }
      setSecret("");
      setPassphrase("");
      setPrivilegePassword("");
      setIdempotencyKey(null);
      setJob(payload);
    } catch {
      setError(
        "Scout did not respond. Your credential remains in this form; retry when the server is available.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="mt-6 border-t pt-6">
      <h3 className="font-medium">Install Scout agent</h3>
      <p className="mt-1 text-sm text-muted-foreground">
        Scout verifies the SSH host identity before it accepts credentials.
      </p>
      {preflightError ? (
        <Alert className="mt-4" variant="destructive">
          <AlertTitle>SSH fingerprint unavailable</AlertTitle>
          <AlertDescription>{preflightError}</AlertDescription>
        </Alert>
      ) : null}
      {preflight && (!job || isTerminal(job)) ? (
        <form className="mt-4" onSubmit={submit}>
          <FieldGroup>
            <Alert>
              <AlertTitle>{preflight.target}</AlertTitle>
              <AlertDescription>
                <span className="font-mono text-xs break-all">{preflight.fingerprint}</span>
                {preflight.trusted
                  ? " This fingerprint is already trusted."
                  : " This is the first fingerprint Scout has seen."}
              </AlertDescription>
            </Alert>
            {!preflight.trusted ? (
              <label className="flex items-start gap-3 text-sm">
                <Checkbox
                  checked={trust}
                  onCheckedChange={(checked) => setTrust(checked === true)}
                />
                <span>I have verified this SSH fingerprint belongs to the intended system.</span>
              </label>
            ) : null}
            <Field>
              <FieldLabel htmlFor="ssh-username">SSH username</FieldLabel>
              <Input
                id="ssh-username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
                required
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="ssh-auth-type">Authentication</FieldLabel>
              <Select
                value={authType}
                onValueChange={(value) => setAuthType(value as "password" | "private-key")}
              >
                <SelectTrigger id="ssh-auth-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="password">Username and password</SelectItem>
                  <SelectItem value="private-key">Private key and passphrase</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="ssh-secret">
                {authType === "password" ? "SSH password" : "Private key"}
              </FieldLabel>
              {authType === "password" ? (
                <Input
                  id="ssh-secret"
                  type="password"
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                  autoComplete="current-password"
                  required
                />
              ) : (
                <Textarea
                  id="ssh-secret"
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                  autoComplete="off"
                  required
                />
              )}
              <FieldDescription>
                The secret is encrypted before it is written and is never returned.
              </FieldDescription>
            </Field>
            {authType === "private-key" ? (
              <Field>
                <FieldLabel htmlFor="ssh-passphrase">Private key passphrase</FieldLabel>
                <Input
                  id="ssh-passphrase"
                  type="password"
                  value={passphrase}
                  onChange={(event) => setPassphrase(event.target.value)}
                  autoComplete="off"
                />
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="ssh-privilege-password">
                Privilege password (optional)
              </FieldLabel>
              <Input
                id="ssh-privilege-password"
                type="password"
                value={privilegePassword}
                onChange={(event) => setPrivilegePassword(event.target.value)}
                autoComplete="off"
              />
              <FieldDescription>
                Used for sudo when your SSH password differs. Leave blank to reuse the SSH password;
                root accounts do not need one.
              </FieldDescription>
            </Field>
            <div className="space-y-3 rounded-lg border p-3">
              <label className="flex items-start gap-3 text-sm">
                <Checkbox
                  checked={boundedScope}
                  onCheckedChange={(checked) => {
                    const enabled = checked === true;
                    setBoundedScope(enabled);
                    if (!enabled) {
                      setAutomaticEnrollment(false);
                      setFirstSeenKeyPinning(false);
                    }
                  }}
                />
                <span>
                  <span className="font-medium">Reuse this credential on this bounded network</span>
                  <span className="mt-1 block text-muted-foreground">
                    The encrypted grant stays limited to this discovered network segment.
                  </span>
                </span>
              </label>
              {boundedScope ? (
                <label className="ml-7 flex items-start gap-3 text-sm">
                  <Checkbox
                    checked={automaticEnrollment}
                    onCheckedChange={(checked) => {
                      const enabled = checked === true;
                      setAutomaticEnrollment(enabled);
                      if (!enabled) setFirstSeenKeyPinning(false);
                    }}
                  />
                  <span>
                    <span className="font-medium">Automatically enroll matching hosts</span>
                    <span className="mt-1 block text-muted-foreground">
                      Scout will use this grant after SSH identity preflight succeeds.
                    </span>
                  </span>
                </label>
              ) : null}
              {boundedScope && automaticEnrollment ? (
                <label className="ml-7 flex items-start gap-3 text-sm">
                  <Checkbox
                    checked={firstSeenKeyPinning}
                    onCheckedChange={(checked) => setFirstSeenKeyPinning(checked === true)}
                  />
                  <span>
                    <span className="font-medium">Pin first-seen SSH keys automatically</span>
                    <span className="mt-1 block text-muted-foreground">
                      Changed keys still block installation and require review.
                    </span>
                  </span>
                </label>
              ) : null}
            </div>
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Installation could not be queued</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            <Button
              type="submit"
              disabled={pending || (!trust && !firstSeenKeyPinning) || !username || !secret}
            >
              {pending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : null}
              Store encrypted credential and install
            </Button>
          </FieldGroup>
        </form>
      ) : null}
      {job ? (
        <Alert
          className="mt-4"
          variant={job.status === "failed" || job.status === "blocked" ? "destructive" : undefined}
        >
          <AlertTitle>
            {job.status === "complete"
              ? "Installation complete"
              : job.status === "blocked"
                ? "Installation blocked"
                : job.status === "failed"
                  ? "Installation failed"
                  : `Installation in progress: ${stageLabels[job.stage as (typeof installationStages)[number]] ?? job.stage}`}
          </AlertTitle>
          <AlertDescription>
            <div className="space-y-4">
              <p role="status" aria-live="polite">
                {stageDescription(job)}
              </p>
              <dl className="grid gap-3 text-sm sm:grid-cols-3">
                <div>
                  <dt className="text-muted-foreground">Target</dt>
                  <dd className="mt-1 font-mono text-xs">
                    {job.target ?? target ?? "Not available"}
                  </dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Attempt</dt>
                  <dd className="mt-1">{job.attempt ?? 0}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Last update</dt>
                  <dd className="mt-1">{timestamp(job.updatedAt)}</dd>
                </div>
              </dl>
              <ol aria-label="Installation progress" className="grid gap-2 text-sm">
                {installationStages.map((stage, index) => {
                  const currentIndex = installationStages.indexOf(
                    job.stage as (typeof installationStages)[number],
                  );
                  const complete =
                    job.status === "complete" || (currentIndex >= 0 && index < currentIndex);
                  const current = !isTerminal(job) && stage === job.stage;
                  const blocker =
                    isTerminal(job) && job.status !== "complete" && stage === job.stage;
                  return (
                    <li key={stage} className="flex items-center gap-2">
                      <span
                        aria-hidden="true"
                        className={
                          complete
                            ? "text-green-600"
                            : current
                              ? "text-foreground"
                              : blocker
                                ? "text-destructive"
                                : "text-muted-foreground"
                        }
                      >
                        {complete ? "✓" : current || blocker ? "•" : "○"}
                      </span>
                      <span className={current || blocker ? "font-medium" : undefined}>
                        {stageLabels[stage]}
                        {current ? " — current" : null}
                        {blocker ? " — stopped here" : null}
                      </span>
                    </li>
                  );
                })}
              </ol>
              {job.leaseExpiresAt && !isTerminal(job) ? (
                <p className="text-xs text-muted-foreground">
                  Worker lease expires {timestamp(job.leaseExpiresAt)}. Scout will retry safely if
                  the worker stops responding.
                </p>
              ) : null}
            </div>
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
