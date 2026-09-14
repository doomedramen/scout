"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

type ApiError = { message?: string; error?: { message?: string }; twoFactorRedirect?: boolean };

export function SignInForm() {
  const router = useRouter();
  const params = useSearchParams();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [twoFactorChallenge, setTwoFactorChallenge] = useState(false);
  const [twoFactorCode, setTwoFactorCode] = useState("");

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setPending(true);
    try {
      const response = await fetch(
        twoFactorChallenge ? "/api/auth/two-factor/verify-totp" : "/api/auth/sign-in/username",
        {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(
            twoFactorChallenge
              ? { code: twoFactorCode, trustDevice: true }
              : { username, password, rememberMe: true, callbackURL: "/systems" },
          ),
          signal: AbortSignal.timeout(15_000),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as ApiError;
      if (!response.ok) {
        setError(
          payload.message ?? payload.error?.message ?? "The username or password is incorrect.",
        );
        return;
      }
      if (!twoFactorChallenge && payload.twoFactorRedirect) {
        setTwoFactorChallenge(true);
        setTwoFactorCode("");
        return;
      }
      router.push("/systems");
      router.refresh();
    } catch {
      setError("Scout did not respond. Check the server and try again.");
    } finally {
      setPending(false);
    }
  }

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>Sign in to Scout</CardTitle>
        <CardDescription>
          Your session protects inventory, credentials, policies, and agent actions.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit}>
          <FieldGroup>
            {params.get("created") ? (
              <Alert>
                <AlertTitle>Owner account created</AlertTitle>
                <AlertDescription>Sign in to start discovering your network.</AlertDescription>
              </Alert>
            ) : null}
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Sign-in failed</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            {twoFactorChallenge ? (
              <Field>
                <FieldLabel htmlFor="two-factor-code">Authenticator code</FieldLabel>
                <Input
                  id="two-factor-code"
                  name="twoFactorCode"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={twoFactorCode}
                  onChange={(event) => setTwoFactorCode(event.target.value)}
                  required
                />
                <FieldError>Enter the code from your authenticator app.</FieldError>
              </Field>
            ) : (
              <>
                <Field>
                  <FieldLabel htmlFor="username">Username</FieldLabel>
                  <Input
                    id="username"
                    name="username"
                    autoComplete="username"
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    required
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="password">Password</FieldLabel>
                  <Input
                    id="password"
                    name="password"
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    required
                  />
                  {error ? <FieldError>{error}</FieldError> : null}
                </Field>
              </>
            )}
            <Button type="submit" className="w-full" disabled={pending}>
              {pending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : null}
              {twoFactorChallenge ? "Verify code" : "Sign in"}
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}
