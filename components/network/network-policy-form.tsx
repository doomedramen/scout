"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import type { DiscoveryState } from "@/lib/server/discovery";

type Segment = {
  id: string;
  cidr: string;
  source: string;
  paused: boolean;
  policyVersion: number;
  lastScanAt: string | null;
};

type PolicyResponse = { discovery: DiscoveryState; segments: Segment[] };

export function NetworkPolicyForm({ initial }: { initial: PolicyResponse }) {
  const [cidr, setCidr] = useState("");
  const [state, setState] = useState(initial);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError("");
    setMessage("");
    try {
      const response = await fetch("/api/v1/network-policy", {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ cidr }),
        signal: AbortSignal.timeout(15_000),
      });
      const payload = (await response.json().catch(() => ({}))) as PolicyResponse & {
        error?: { message?: string };
      };
      if (!response.ok) {
        setError(payload.error?.message ?? "The network policy could not be saved.");
        return;
      }
      setState(payload);
      setCidr("");
      setMessage("Bounded discovery started.");
    } catch {
      setError("Scout did not respond. Try again when the server is available.");
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Discovery status</CardTitle>
          <CardDescription>
            Only a bounded private IPv4 network is scanned. Scout probes TCP port 22 and never
            guesses credentials.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{state.discovery.status}</span>
            {state.discovery.cidr ? (
              <span className="font-mono text-muted-foreground">{state.discovery.cidr}</span>
            ) : null}
          </div>
          {state.discovery.reason ? (
            <Alert>
              <AlertTitle>One bounded range is needed</AlertTitle>
              <AlertDescription>{state.discovery.reason}</AlertDescription>
            </Alert>
          ) : null}
          {message ? (
            <Alert>
              <AlertTitle>{message}</AlertTitle>
            </Alert>
          ) : null}
          {error ? (
            <Alert variant="destructive">
              <AlertTitle>Discovery policy failed</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Bounded network</CardTitle>
          <CardDescription>
            Use a /24 or narrower network, for example 192.168.1.0/24.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="network-cidr">IPv4 CIDR</FieldLabel>
                <Input
                  id="network-cidr"
                  value={cidr}
                  onChange={(event) => setCidr(event.target.value)}
                  placeholder="192.168.1.0/24"
                  required
                />
                <FieldDescription>
                  This authorizes TCP reachability checks only for the selected range.
                </FieldDescription>
              </Field>
              <Button type="submit" disabled={pending}>
                {pending ? (
                  <LoaderCircle className="animate-spin" data-icon="inline-start" />
                ) : null}
                Save and scan
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Authorized segments</CardTitle>
          <CardDescription>
            Network provenance keeps identical CIDRs from disconnected networks separate.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {state.segments.length ? (
            <div className="space-y-3">
              {state.segments.map((segment) => (
                <div
                  key={segment.id}
                  className="flex flex-col gap-1 rounded-md border p-3 text-sm sm:flex-row sm:items-center sm:justify-between"
                >
                  <div>
                    <span className="font-mono">{segment.cidr}</span>
                    <span className="ml-2 text-muted-foreground">{segment.source}</span>
                  </div>
                  <span className="text-muted-foreground">
                    {segment.lastScanAt
                      ? `Last scan ${new Date(segment.lastScanAt).toLocaleString()}`
                      : "Not scanned yet"}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No bounded network has been authorized.</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
