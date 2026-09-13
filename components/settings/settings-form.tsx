"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function SettingsForm({ initialPaused }: { initialPaused: boolean }) {
  const [paused, setPaused] = useState(initialPaused);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");

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
        <CardContent className="text-sm text-muted-foreground">
          Optional MFA and transport controls will be available here without changing the normal
          Systems workflow.
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
            {message ? (
              <Alert>
                <AlertTitle>Settings</AlertTitle>
                <AlertDescription>{message}</AlertDescription>
              </Alert>
            ) : null}
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
