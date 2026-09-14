"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

type AuthError = { message?: string; error?: { message?: string } };

export function SettingsForm({
  initialPaused,
  initialTwoFactorEnabled,
}: {
  initialPaused: boolean;
  initialTwoFactorEnabled: boolean;
}) {
  const [paused, setPaused] = useState(initialPaused);
  const [twoFactorEnabled, setTwoFactorEnabled] = useState(initialTwoFactorEnabled);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [setup, setSetup] = useState<{ uri: string; backupCodes: string[] } | null>(null);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  function authMessage(payload: AuthError, fallback: string): string {
    return payload.message ?? payload.error?.message ?? fallback;
  }

  async function enableTwoFactor(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError("");
    setMessage("");
    try {
      const response = await fetch("/api/auth/two-factor/enable", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ method: "totp", password }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as AuthError & {
        totpURI?: string;
        backupCodes?: string[];
      };
      if (!response.ok || !payload.totpURI || !payload.backupCodes) {
        throw new Error(authMessage(payload, "MFA could not be enabled."));
      }
      setSetup({ uri: payload.totpURI, backupCodes: payload.backupCodes });
      setPassword("");
      setMessage("Scan the authenticator URI, then enter the six-digit code to finish.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "MFA could not be enabled.");
    } finally {
      setPending(false);
    }
  }

  async function verifyTwoFactor(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const response = await fetch("/api/auth/two-factor/verify-totp", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ code, trustDevice: true }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as AuthError;
      if (!response.ok) throw new Error(authMessage(payload, "The authenticator code is invalid."));
      setTwoFactorEnabled(true);
      setSetup(null);
      setCode("");
      setMessage("Authenticator MFA is enabled. Save your backup codes somewhere secure.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The authenticator code is invalid.");
    } finally {
      setPending(false);
    }
  }

  async function disableTwoFactor(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const response = await fetch("/api/auth/two-factor/disable", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ password }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as AuthError;
      if (!response.ok) throw new Error(authMessage(payload, "MFA could not be disabled."));
      setTwoFactorEnabled(false);
      setPassword("");
      setMessage("Authenticator MFA is disabled.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "MFA could not be disabled.");
    } finally {
      setPending(false);
    }
  }

  async function resume() {
    setPending(true);
    setMessage("");
    try {
      const response = await fetch("/api/v1/settings", {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ authorityPaused: false }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as {
        authorityPaused?: boolean;
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(payload.error?.message ?? "Settings could not be saved.");
      setPaused(payload.authorityPaused === true);
      setMessage("Discovery and installation authority resumed.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Settings could not be saved.");
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Owner settings</CardTitle>
          <CardDescription>Signed in as the single Scout owner.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4 text-sm text-muted-foreground">
          <p>
            Authenticator MFA is optional. Enabling it adds a code step to future sign-ins; it does
            not change the normal Systems workflow.
          </p>
          {!twoFactorEnabled && !setup ? (
            <form onSubmit={enableTwoFactor}>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="mfa-enable-password">Owner password</FieldLabel>
                  <Input
                    id="mfa-enable-password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    autoComplete="current-password"
                    required
                  />
                  <FieldDescription>Required to create the authenticator secret.</FieldDescription>
                </Field>
                <Button type="submit" variant="outline" disabled={pending}>
                  {pending ? (
                    <LoaderCircle className="animate-spin" data-icon="inline-start" />
                  ) : null}
                  Set up authenticator MFA
                </Button>
              </FieldGroup>
            </form>
          ) : null}
          {setup ? (
            <form onSubmit={verifyTwoFactor} className="space-y-4 rounded-lg border p-4">
              <div>
                <p className="font-medium text-foreground">Authenticator setup</p>
                <p className="mt-1 break-all font-mono text-xs">{setup.uri}</p>
              </div>
              <p>
                Store these one-time backup codes before leaving this page:{" "}
                {setup.backupCodes.join(", ")}
              </p>
              <Field>
                <FieldLabel htmlFor="mfa-code">Authenticator code</FieldLabel>
                <Input
                  id="mfa-code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  required
                />
              </Field>
              <Button type="submit" disabled={pending}>
                {pending ? (
                  <LoaderCircle className="animate-spin" data-icon="inline-start" />
                ) : null}
                Verify and enable MFA
              </Button>
            </form>
          ) : null}
          {twoFactorEnabled ? (
            <form onSubmit={disableTwoFactor}>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="mfa-disable-password">Owner password</FieldLabel>
                  <Input
                    id="mfa-disable-password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    autoComplete="current-password"
                    required
                  />
                </Field>
                <Button type="submit" variant="outline" disabled={pending}>
                  {pending ? (
                    <LoaderCircle className="animate-spin" data-icon="inline-start" />
                  ) : null}
                  Disable authenticator MFA
                </Button>
              </FieldGroup>
            </form>
          ) : null}
          {message ? (
            <Alert>
              <AlertTitle>Settings</AlertTitle>
              <AlertDescription>{message}</AlertDescription>
            </Alert>
          ) : null}
          {error ? (
            <Alert variant="destructive">
              <AlertTitle>Settings could not be saved</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
        </CardContent>
      </Card>
      {paused ? (
        <Card>
          <CardHeader>
            <CardTitle>Restore review required</CardTitle>
            <CardDescription>
              This instance was restored from a backup. New discovery and installation work is
              paused until you review the restored inventory.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <Button onClick={resume} disabled={pending}>
              {pending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : null}
              Resume Scout authority
            </Button>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
