"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

type ApiError = { error?: { message?: string } };

export function SetupForm() {
  const router = useRouter();
  const [setupToken, setSetupToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setPending(true);
    try {
      const response = await fetch("/api/v1/setup/owner", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ setupToken, username, password }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as ApiError;
      if (!response.ok) {
        setError(payload.error?.message ?? "Owner setup failed.");
        return;
      }
      router.push("/sign-in?created=1");
    } catch {
      setError("Scout did not respond. Check the server and try again.");
    } finally {
      setPending(false);
    }
  }

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>Protect your Scout workspace</CardTitle>
        <CardDescription>
          Create the single owner account. The setup token is provisioned locally and expires.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit}>
          <FieldGroup>
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Setup could not be completed</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            <Field>
              <FieldLabel htmlFor="setup-token">One-time setup token</FieldLabel>
              <Input
                id="setup-token"
                name="setupToken"
                type="password"
                autoComplete="off"
                value={setupToken}
                onChange={(event) => setSetupToken(event.target.value)}
                required
              />
              <FieldDescription>Find this token in the Scout server logs.</FieldDescription>
            </Field>
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
                autoComplete="new-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <FieldDescription>
                At least 8 characters, including upper, lower, and number.
              </FieldDescription>
            </Field>
            <Button type="submit" className="w-full" disabled={pending}>
              {pending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : null}
              Create owner account
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}
