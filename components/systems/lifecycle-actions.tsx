"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function LifecycleActions({ systemId, canRetry }: { systemId: string; canRetry: boolean }) {
  const router = useRouter();
  const [pending, setPending] = useState<"retry" | "decommission" | null>(null);
  const [message, setMessage] = useState("");

  async function run(action: "retry" | "decommission") {
    if (
      action === "decommission" &&
      !window.confirm("Revoke this Scout agent and decommission the system?")
    )
      return;
    setPending(action);
    setMessage("");
    try {
      const response = await fetch(`/api/v1/systems/${systemId}/actions/${action}`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as {
        error?: { message?: string };
        uninstall?: string;
      };
      if (!response.ok) throw new Error(payload.error?.message ?? "The action failed.");
      setMessage(
        action === "retry"
          ? "Installation was queued again."
          : payload.uninstall === "complete"
            ? "The agent was revoked and removed from the system."
            : "The agent was revoked; uninstall is pending/manual.",
      );
      if (action === "retry") {
        window.location.reload();
      } else {
        router.refresh();
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The action failed.");
    } finally {
      setPending(null);
    }
  }

  return (
    <div className="space-y-3">
      {message ? (
        <Alert variant={message.includes("failed") ? "destructive" : undefined}>
          <AlertTitle>System action</AlertTitle>
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      ) : null}
      <div className="flex flex-wrap gap-2">
        {canRetry ? (
          <Button variant="outline" onClick={() => run("retry")} disabled={pending !== null}>
            {pending === "retry" ? (
              <LoaderCircle className="animate-spin" data-icon="inline-start" />
            ) : null}
            Retry installation
          </Button>
        ) : null}
        <Button
          variant="destructive"
          onClick={() => run("decommission")}
          disabled={pending !== null}
        >
          {pending === "decommission" ? (
            <LoaderCircle className="animate-spin" data-icon="inline-start" />
          ) : null}
          Decommission
        </Button>
      </div>
    </div>
  );
}
