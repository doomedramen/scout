"use client";

import { useState } from "react";
import { Check, Clipboard, LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { SystemSummary } from "@/lib/server/systems";

type ManualInstallResponse = {
  target: string | null;
  expiresAt: string;
  trustPin: string | null;
  command: string;
  error?: { message?: string };
};

export function ManualInstallPanel({ systems }: { systems: SystemSummary[] }) {
  const [systemId, setSystemId] = useState(systems[0]?.id ?? "");
  const [result, setResult] = useState<ManualInstallResponse | null>(null);
  const [pending, setPending] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");

  async function generateCommand() {
    if (!systemId) return;
    setPending(true);
    setCopied(false);
    setError("");
    setResult(null);
    try {
      const response = await fetch(`/api/v1/systems/${systemId}/manual-install`, {
        method: "POST",
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as ManualInstallResponse;
      if (!response.ok)
        throw new Error(payload.error?.message ?? "The installer could not be generated.");
      setResult(payload);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The installer could not be generated.");
    } finally {
      setPending(false);
    }
  }

  async function copyCommand() {
    if (!result) return;
    try {
      await navigator.clipboard.writeText(result.command);
      setCopied(true);
    } catch {
      setError("The command could not be copied. Select it and copy it manually.");
    }
  }

  return (
    <Card id="manual-installation">
      <CardHeader>
        <CardTitle>Manual installation</CardTitle>
        <CardDescription>
          Use this fallback when automatic SSH installation is not available. The one-time
          invitation is bound to the selected system and expires after ten minutes.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {systems.length ? (
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="manual-install-system">System</FieldLabel>
              <Select value={systemId} onValueChange={(value) => setSystemId(value ?? "")}>
                <SelectTrigger id="manual-install-system" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {systems.map((system) => (
                    <SelectItem key={system.id} value={system.id}>
                      {system.displayName} — {system.addresses.join(", ") || "address unavailable"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>
                Select a system with current open SSH evidence. Scout will not guess credentials.
              </FieldDescription>
            </Field>
            <Button
              type="button"
              onClick={() => void generateCommand()}
              disabled={pending || !systemId}
            >
              {pending ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : null}
              Generate installer command
            </Button>
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Manual installation unavailable</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            {result ? (
              <Alert>
                <AlertTitle>Run this on {result.target ?? "the selected system"}</AlertTitle>
                <AlertDescription>
                  <p>
                    Run the command as a user with root or sudo access. It installs and enrolls the
                    agent without requiring a separate agent download.
                  </p>
                  {result.trustPin ? (
                    <p className="mt-3">
                      HTTP bootstrap trust pin:{" "}
                      <span className="font-mono text-xs">{result.trustPin}</span>
                    </p>
                  ) : null}
                  <pre className="mt-3 overflow-x-auto rounded-md border bg-muted p-3 text-xs text-foreground">
                    <code>{result.command}</code>
                  </pre>
                  <div className="mt-3 flex flex-wrap items-center gap-3">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => void copyCommand()}
                    >
                      {copied ? (
                        <Check data-icon="inline-start" />
                      ) : (
                        <Clipboard data-icon="inline-start" />
                      )}
                      {copied ? "Copied" : "Copy command"}
                    </Button>
                    <span className="text-xs">
                      Expires {new Date(result.expiresAt).toLocaleString()}
                    </span>
                  </div>
                </AlertDescription>
              </Alert>
            ) : null}
          </FieldGroup>
        ) : (
          <p className="text-sm text-muted-foreground">
            No discovered system is ready for manual installation. Scout must first confirm an open
            SSH endpoint.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
