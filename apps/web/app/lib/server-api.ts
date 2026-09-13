import "server-only";

import { headers } from "next/headers";
import { apiOrigins } from "./api-origin";
import type { Candidate, Device, Incident, ListResponse, Topology } from "@/lib/api";
import type { RouteData } from "./route-data";

export type ShellData = {
  auth: "signedOut" | "signedIn";
  status: { mode: string; database: string; enrollmentAvailable: boolean; recoveryMode?: boolean } | null;
  statusError: boolean;
};

async function fetchApi(path: string, init: RequestInit): Promise<Response> {
  let lastError: unknown;
  for (const origin of apiOrigins()) {
    try {
      return await fetch(new URL(path, origin), init);
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError instanceof Error ? lastError : new Error("Scout API unavailable");
}

async function fetchApiJSON<T>(path: string, cookie: string | null): Promise<T> {
  const response = await fetchApi(path, {
    headers: cookie ? { cookie } : undefined,
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`Scout API request failed (${response.status})`);
  return (await response.json()) as T;
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export async function getShellData(): Promise<ShellData> {
  const requestHeaders = await headers();
  const cookie = requestHeaders.get("cookie");
  const headersWithCookie = cookie ? { cookie } : undefined;
  const [owner, status] = await Promise.allSettled([
    fetchApi("/api/v1/owner", { headers: headersWithCookie, cache: "no-store" }),
    fetchApi("/api/status", { headers: headersWithCookie, cache: "no-store" }),
  ]);
  const signedIn = owner.status === "fulfilled" && owner.value.ok;
  if (status.status !== "fulfilled" || !status.value.ok) {
    return { auth: signedIn ? "signedIn" : "signedOut", status: null, statusError: true };
  }
  return { auth: signedIn ? "signedIn" : "signedOut", status: await status.value.json(), statusError: false };
}

export async function getRouteData(route: string[]): Promise<RouteData | null> {
  const cookie = (await headers()).get("cookie");
  switch (route[0] ?? "overview") {
    case "overview": {
      const [devices, candidates, incidents] = await Promise.allSettled([
        fetchApiJSON<ListResponse<Device>>("/api/v1/devices?limit=500", cookie),
        fetchApiJSON<ListResponse<Candidate>>("/api/v1/candidates?limit=500", cookie),
        fetchApiJSON<ListResponse<Incident>>("/api/v1/incidents?status=active&limit=500", cookie),
      ]);
      return {
        kind: "overview",
        devices: devices.status === "fulfilled" ? devices.value.items : [],
        candidates: candidates.status === "fulfilled" ? candidates.value.items : [],
        incidents: incidents.status === "fulfilled" ? incidents.value.items : [],
        sourceErrors: {
          ...(devices.status === "rejected" ? { devices: errorMessage(devices.reason, "Systems unavailable") } : {}),
          ...(candidates.status === "rejected"
            ? { candidates: errorMessage(candidates.reason, "Access requests unavailable") }
            : {}),
          ...(incidents.status === "rejected"
            ? { incidents: errorMessage(incidents.reason, "Incidents unavailable") }
            : {}),
        },
      };
    }
    case "systems": {
      const [devices, candidates] = await Promise.allSettled([
        fetchApiJSON<ListResponse<Device>>("/api/v1/devices?limit=500", cookie),
        fetchApiJSON<ListResponse<Candidate>>("/api/v1/candidates?limit=500", cookie),
      ]);
      return {
        kind: "systems",
        devices: devices.status === "fulfilled" ? devices.value.items : [],
        candidates: candidates.status === "fulfilled" ? candidates.value.items : [],
        error:
          devices.status === "rejected"
            ? errorMessage(devices.reason, "Could not load systems")
            : candidates.status === "rejected"
              ? errorMessage(candidates.reason, "Could not load systems")
              : null,
      };
    }
    case "network": {
      const [topology, candidates] = await Promise.allSettled([
        fetchApiJSON<Topology>("/api/v1/topology", cookie),
        fetchApiJSON<ListResponse<Candidate>>("/api/v1/candidates?limit=100", cookie),
      ]);
      return {
        kind: "network",
        nodes: topology.status === "fulfilled" ? topology.value.nodes : [],
        relationships: topology.status === "fulfilled" ? topology.value.relationships : [],
        candidates: candidates.status === "fulfilled" ? candidates.value.items : [],
        topologyError: topology.status === "rejected" ? errorMessage(topology.reason, "Could not load topology") : null,
        candidateError:
          candidates.status === "rejected" ? errorMessage(candidates.reason, "Could not load found devices") : null,
      };
    }
    default:
      return null;
  }
}
