"use client";

import { useEffect, useState } from "react";
import { LoaderCircle } from "lucide-react";

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
};

type Job = { jobId: string; status: string; stage: string; errorMessage?: string | null };

export function AccessGrantForm({ systemId }: { systemId: string }) {
  const [preflight, setPreflight] = useState<Preflight | null>(null);
  const [preflightError, setPreflightError] = useState("");
  const [username, setUsername] = useState("");
  const [authType, setAuthType] = useState<"password" | "private-key">("password");
  const [secret, setSecret] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [trust, setTrust] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
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
  }, [systemId]);

  useEffect(() => {
    if (!job?.jobId || ["complete", "failed", "blocked"].includes(job.status)) return;
    const timer = window.setInterval(() => {
      fetch(`/api/v1/enrollment-jobs/${job.jobId}`, { signal: AbortSignal.timeout(8_000) })
        .then((response) => response.json() as Promise<Job>)
        .then((next) => setJob((current) => (current ? { ...current, ...next } : next)))
        .catch(() => undefined);
    }, 2_000);
    return () => window.clearInterval(timer);
  }, [job]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!preflight) return;
    setPending(true);
    setError("");
    try {
      const response = await fetch(`/api/v1/systems/${systemId}/access-grants`, {
        method: "POST",
        headers: { "content-type": "application/json", "idempotency-key": crypto.randomUUID() },
        body: JSON.stringify({
          method: "ssh",
          username,
          authType,
          secret,
          passphrase: authType === "private-key" ? passphrase : null,
          fingerprint: preflight.fingerprint,
          trust,
          scope: "exact-host",
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
      {preflight ? (
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
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Installation could not be queued</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            <Button type="submit" disabled={pending || !trust || !username || !secret}>
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
          <AlertTitle>Installation: {job.stage}</AlertTitle>
          <AlertDescription>
            {job.errorMessage ?? "Scout is working on this system."}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
