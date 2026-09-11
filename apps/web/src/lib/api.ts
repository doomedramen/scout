export type APIErrorBody = { code: string; message: string; requestId: string; retryable: boolean };

export class APIError extends Error {
  readonly status: number;
  readonly details: APIErrorBody | null;
  constructor(status: number, details: APIErrorBody | null, message = "Request failed") {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.details = details;
  }
}

export type Status = { mode: string; database: string; enrollmentAvailable: boolean; recoveryMode?: boolean; telemetry?: { samples: number; droppedSamples: number; maxSamples: number; backpressure: boolean; retentionHours: number } };
export type Metric = { value: number | null; availability: string; unit: string; observedAt: string; min?: number | null; max?: number | null };
export type MetricSeries = { metric: string; unit: string; points: Metric[] };
export type CollectorState = { id: string; provider: string; state: string; diagnostic?: string; lastSuccess?: string };
export type Device = {
  id: string;
  displayName: string;
  siteId?: string;
  platform: string;
  architecture: string;
  hostname?: string;
  lifecycle: string;
  addresses: string[];
  agentVersion?: string;
  lastHeartbeat?: string;
  availability: "connecting" | "online" | "offline" | "revoked";
  metricFreshness: Record<string, string>;
  currentMetrics?: Record<string, { value: number | null; unit: string; availability: string; observedAt: string }>;
  collectorStates: CollectorState[];
  excluded?: boolean;
  revision: number;
};
export type ListResponse<T> = { items: T[]; nextCursor: string | null };
export type Site = { id: string; name: string; addressContext: string; createdAt: string };
export type Scope = { id: string; siteId: string; ranges: string[]; exclusions: string[]; allowedMethods: string[]; ports: number[]; enabled: boolean; revision: number; credentialRef?: string; trustRef?: string; limits?: { probesPerSecond: number; concurrency: number; targetBudget: number } };
export type Invitation = { deviceId: string; invitation: string; expiresAt: string; instructions: string };

let csrfToken = "";

function csrfFromCookie(): string {
  const item = document.cookie.split("; ").find((value) => value.startsWith("scout_csrf="));
  return item ? decodeURIComponent(item.slice("scout_csrf=".length)) : "";
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) headers.set("X-CSRF-Token", csrfToken || csrfFromCookie());
  const response = await fetch(`/api/v1${path}`, { ...init, method, headers, credentials: "same-origin" });
  if (!response.ok) {
    let details: APIErrorBody | null = null;
    try { details = (await response.json()).error ?? null; } catch { /* empty response */ }
    throw new APIError(response.status, details, details?.message ?? `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  status: () => fetch("/api/status", { credentials: "same-origin" }).then(async (response) => { if (!response.ok) throw new APIError(response.status, null); return response.json() as Promise<Status>; }),
  owner: () => request<{ id: string; mfaEnabled: boolean; createdAt: string }>("/owner"),
  setup: (setupToken: string, password: string) => request<{ id: string }>("/setup", { method: "POST", body: JSON.stringify({ setupToken, password }) }),
  signIn: async (password: string, totpCode?: string, recoveryCode?: string) => { const result = await request<{ owner: { id: string }; csrfToken: string }>("/sessions", { method: "POST", body: JSON.stringify({ password, totpCode, recoveryCode }) }); csrfToken = result.csrfToken; return result; },
  signOut: () => request<void>("/sessions/current", { method: "DELETE" }),
  beginMFA: (password: string) => request<{ secret: string; otpauth: string }>("/owner/mfa/setup", { method: "POST", body: JSON.stringify({ password }) }),
  confirmMFA: (totpCode: string) => request<{ recoveryCodes: string[] }>("/owner/mfa/confirm", { method: "POST", body: JSON.stringify({ totpCode }) }),
  devices: (query = "") => request<ListResponse<Device>>(`/devices${query}`),
  device: (id: string) => request<Device>(`/devices/${encodeURIComponent(id)}`),
  metrics: (id: string, query = "") => request<{ series: MetricSeries[] }>(`/devices/${encodeURIComponent(id)}/metrics${query}`),
  sites: () => request<ListResponse<Site>>("/sites"),
  createSite: (name: string) => request<Site>("/sites", { method: "POST", body: JSON.stringify({ name }) }),
  scopes: () => request<ListResponse<Scope>>("/scopes"),
  invitation: (displayName: string, siteId: string) => request<Invitation>("/bootstrap-invitations", { method: "POST", body: JSON.stringify({ displayName, siteId }) }),
  topology: (siteId = "") => request<{ nodes: Array<Record<string, unknown>>; relationships: Array<Record<string, unknown>> }>(`/topology${siteId ? `?siteId=${encodeURIComponent(siteId)}` : ""}`),
  recoveryStatus: () => request<{ workspace: { recoveryMode: boolean; enrollmentPaused: boolean; updatesPaused: boolean }; telemetry: Record<string, unknown> }>("/recovery/status"),
  reconcileRecovery: () => request("/recovery/reconcile", { method: "POST", body: JSON.stringify({}) }),
  telemetrySettings: () => request<Record<string, unknown>>("/settings/telemetry"),
};
