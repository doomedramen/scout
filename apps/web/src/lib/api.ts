export type APIErrorBody = {
  code: string;
  message: string;
  requestId: string;
  retryable: boolean;
};

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

export type Status = {
  mode: string;
  database: string;
  enrollmentAvailable: boolean;
  recoveryMode?: boolean;
  telemetry?: {
    samples: number;
    droppedSamples: number;
    maxSamples: number;
    usedBytes: number;
    budgetBytes: number;
    backpressure: boolean;
    retentionHours: number;
  };
};

export type Metric = {
  value: number | null;
  availability: string;
  unit: string;
  observedAt: string;
  min?: number | null;
  max?: number | null;
};

export type MetricPoint = {
  value: number | null;
  availability: string;
  observedAt: string;
  min?: number | null;
  max?: number | null;
};

export type MetricSeries = {
  seriesId?: string;
  entityId?: string;
  metric: string;
  unit: string;
  points: MetricPoint[];
};

export type CollectorState = {
  id: string;
  provider: string;
  state: string;
  diagnostic?: string;
  lastSuccess?: string;
};

export type DeviceMetric = {
  value: number | null;
  unit: string;
  availability: string;
  observedAt: string;
};

export type Device = {
  id: string;
  displayName: string;
  siteId?: string;
  platform: string;
  architecture: string;
  hostname?: string;
  addresses: string[];
  identifiers?: IdentifierEvidence[];
  agentId?: string;
  lifecycle: string;
  agentVersion?: string;
  lastHeartbeat?: string;
  availability: "connecting" | "online" | "offline" | "revoked";
  metricFreshness: Record<string, string>;
  currentMetrics?: Record<string, DeviceMetric>;
  collectorStates: CollectorState[];
  excluded?: boolean;
  revision: number;
};

export type IdentifierEvidence = {
  kind: string;
  namespace: string;
  value: string;
  source: string;
  confidence: number;
  firstSeen: string;
  lastSeen: string;
};

export type ListResponse<T> = {
  items: T[];
  nextCursor: string | null;
};

export type Site = {
  id: string;
  name: string;
  addressContext: string;
  createdAt: string;
};

export type Scope = {
  id: string;
  siteId: string;
  ranges: string[];
  exclusions: string[];
  allowedMethods: string[];
  ports: number[];
  enabled: boolean;
  revision: number;
  credentialRef?: string;
  trustRef?: string;
  limits?: {
    probesPerSecond: number;
    concurrency: number;
    targetBudget: number;
  };
};

export type NumericAlertCondition = {
  metric: string;
  entityId?: string;
  operator: "gt" | "lt";
  triggerValue: number;
  clearValue: number;
  triggerSeconds: number;
  clearSeconds: number;
};

export type StateAlertCondition = {
  state: string;
  entityId?: string;
  servicePattern?: string;
  collectorId?: string;
  triggerSeconds: number;
  clearSeconds: number;
  minimumConsecutiveSamples?: number;
};

export type AlertCondition = NumericAlertCondition | StateAlertCondition;

export type AlertRule = {
  id: string;
  name: string;
  targetKind: "fleet" | "site" | "device";
  targetId?: string;
  kind: "numeric" | "state";
  severity: "warning" | "critical";
  enabled: boolean;
  condition: AlertCondition;
  revision: number;
  createdAt: string;
  updatedAt: string;
  retiredAt?: string;
};

export type AlertRuleInput = Omit<AlertRule, "id" | "revision" | "createdAt" | "updatedAt" | "retiredAt">;

export type AlertRulePatch = {
  expectedRevision: number;
  name?: string;
  severity?: AlertRule["severity"];
  enabled?: boolean;
  condition?: AlertCondition;
};

export type AlertOverride = {
  id: string;
  lineageId: string;
  targetKind: "site" | "device";
  targetId: string;
  severity: AlertRule["severity"];
  enabled: boolean;
  condition: AlertCondition;
  revision: number;
  createdAt: string;
  updatedAt: string;
};

export type AlertOverrideInput = Omit<AlertOverride, "id" | "lineageId" | "revision" | "createdAt" | "updatedAt">;

export type AlertOverridePatch = {
  expectedRevision: number;
  severity?: AlertOverride["severity"];
  enabled?: boolean;
  condition?: AlertCondition;
};

export type IncidentEvidence = {
  value?: number | null;
  unit?: string | null;
  threshold?: number | null;
  source?: string;
  rawState?: string | null;
  observedAt?: string;
};

export type Incident = {
  id: string;
  lineageId: string;
  entityId: string;
  deviceId: string;
  siteId?: string | null;
  status: "active" | "resolved" | "closed";
  severity: AlertRule["severity"];
  ruleSnapshot: { name: string; kind: AlertRule["kind"]; condition: AlertCondition };
  openedAt: string;
  observedAt: string;
  acknowledgedAt?: string;
  acknowledgedBy?: string;
  closedAt?: string;
  closeReason?: string;
  evidenceState: "fresh" | "unknown" | "unsupported";
  evidence?: IncidentEvidence;
  notificationSuppression: { suppressed: boolean; reasons: string[] };
  revision: number;
};

export type IncidentTransition = {
  id: string;
  incidentId: string;
  sequence: number;
  kind: "triggered" | "acknowledged" | "unknown" | "fresh" | "recovered" | "administrative_close";
  occurredAt: string;
  actor?: string;
  evidence?: IncidentEvidence;
  effectiveRevision: number;
};

export type Invitation = {
  deviceId: string;
  invitation: string;
  expiresAt: string;
  instructions: string;
};

export type Owner = {
  id: string;
  mfaEnabled: boolean;
  createdAt: string;
};

export type NotificationDestination = {
  id: string;
  name: string;
  baseUrl: string;
  maskedTopic: string;
  hasToken: boolean;
  allowPlainHttp: boolean;
  enabled: boolean;
  revision: number;
  lastTestAt?: string | null;
};

export type NotificationDelivery = {
  id: string;
  destinationId: string;
  incidentId?: string | null;
  transitionId?: string | null;
  status: "queued" | "sending" | "retry" | "accepted" | "failed" | "cancelled" | "suppressed" | "expired";
  attempts: number;
  nextAttemptAt?: string | null;
  acceptedAt?: string | null;
  expiresAt: string;
  safeError?: string | null;
  destinationRevision: number;
};

export type SuppressionWindow = {
  id: string;
  name: string;
  targetKind: "fleet" | "site" | "device";
  targetId?: string;
  enabled: boolean;
  mode: "recurring" | "oneTime";
  timezone?: string;
  weekdays?: number[];
  startLocal?: string;
  endLocal?: string;
  startsAt?: string;
  endsAt?: string;
  revision: number;
  createdAt: string;
  updatedAt: string;
};

export type MonitoringWorkspace = {
  recoveryMode: boolean;
  discoveryPaused: boolean;
  enrollmentPaused: boolean;
  updatesPaused: boolean;
  notificationsPaused: boolean;
  policyRevision: number;
  notificationQueueOverflows: number;
};

export type MonitoringSettings = {
  revision: number;
  defaultsVersion: string;
  notificationsPaused: boolean;
  retention: { rawDays: number; fiveMinuteDays: number; hourlyDays: number };
  diskBudgetBytes: number;
  updatedAt: string;
};

export type AccessRequest = {
  id: string;
  deviceId: string;
  scopeId?: string;
  reasonCode: string;
  safeDetails: Record<string, string>;
  state: string;
  lastAttempt: string;
};

export type Credential = {
  id: string;
  kind: string;
  endpoint?: string;
  allowedUse: string[];
  targets: string[];
  metadata: Record<string, string>;
  revision: number;
  revokedAt?: string;
};

export type TrustRecord = {
  id: string;
  scopeId?: string;
  endpoint: string;
  host: string;
  fingerprint: string;
  publicKey?: string;
  revision: number;
  revokedAt?: string;
};

export type Candidate = {
  id: string;
  siteId: string;
  scopeId: string;
  address: string;
  hostname?: string;
  source: string;
  state: string;
  scopeRevision: number;
  firstSeen: string;
  lastSeen: string;
  expiresAt: string;
  excluded: boolean;
};

export type Job = {
  id: string;
  kind: string;
  deviceId: string;
  scopeId?: string;
  scopeRevision: number;
  state: string;
  leaseOwner?: string;
  epoch: number;
  attempts: number;
  destination?: string;
  result?: Record<string, string>;
};

export type Worker = {
  id: string;
  name: string;
  kind: string;
  siteIds: string[];
  createdAt: string;
  revokedAt?: string;
};

export type Rollout = {
  id: string;
  releaseId: string;
  mode: string;
  targets: string[];
  concurrency: number;
  canaries: number;
  failureThreshold: number;
  paused: boolean;
  revision: number;
  failureCount: number;
  createdAt: string;
};

export type Assignment = {
  id: string;
  rolloutId?: string;
  deviceId: string;
  desiredRelease: string;
  generation: number;
  expiresAt: string;
  state: string;
};

export type Release = {
  id: string;
  manifestHash: string;
  version: string;
  generation: number;
  platform: string;
  architecture: string;
  digest: string;
  bytes: number;
  trustKeyId: string;
  manifest?: Record<string, unknown>;
  revokedAt?: string;
};

export type CollectorDescriptor = {
  id: string;
  provider: string;
  version: string;
  requiredPermissions: string[];
  configSchema: Record<string, string>;
  entityLimit: number;
  interval: number | string;
  deadline: number | string;
};

export type CollectorConfig = {
  deviceId: string;
  collectorId: string;
  provider: string;
  enabled: boolean;
  config?: Record<string, string>;
  credentialRef?: string;
  revision: number;
  health: string;
  diagnostic?: string;
  lastSuccess?: string;
};

export type ServiceEntity = {
  id: string;
  provider: string;
  clusterId?: string;
  deviceId?: string;
  kind: string;
  name: string;
  status: string;
  labels?: Record<string, string>;
  observedAt: string;
  expiresAt: string;
};

export type DeviceUpdatePolicy = {
  deviceId: string;
  mode: string;
  releaseId?: string;
  version?: string;
  expectedRevision: number;
  revision: number;
  windowStart?: string;
  windowEnd?: string;
  updatedAt: string;
};

export type TopologyNode = {
  id: string;
  label?: string;
  addresses?: string[];
  availability?: string;
  lifecycle?: string;
};

export type Relationship = {
  id: string;
  fromEntity: string;
  toEntity: string;
  type: string;
  confidence: number;
  projectionRevision: number;
  evidenceIds: string[];
  observedAt: string;
  expiresAt: string;
  source: string;
};

export type Topology = {
  nodes: TopologyNode[];
  relationships: Relationship[];
  nextCursor: string | null;
};

export type DecommissionResult = {
  device: Device;
  identityRevoked: boolean;
  excluded: boolean;
  uninstall: "not_requested" | { state: string; jobId: string; confirmed: boolean };
};

export type RolloutResult = {
  rollout: Rollout;
  assignments: Assignment[];
};

let csrfToken = "";

function csrfFromCookie(): string {
  const item = document.cookie.split("; ").find((value) => value.startsWith("scout_csrf="));
  return item ? decodeURIComponent(item.slice("scout_csrf=".length)) : "";
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    headers.set("X-CSRF-Token", csrfToken || csrfFromCookie());
  }
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    method,
    headers,
    credentials: "same-origin",
  });
  if (!response.ok) {
    let details: APIErrorBody | null = null;
    try {
      details = (await response.json()).error ?? null;
    } catch {
      // Empty or non-JSON error response.
    }
    throw new APIError(response.status, details, details?.message ?? `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  status: () =>
    fetch("/api/status", { credentials: "same-origin" }).then(async (response) => {
      if (!response.ok) throw new APIError(response.status, null);
      return response.json() as Promise<Status>;
    }),
  owner: () => request<Owner>("/owner"),
  setup: (setupToken: string, password: string) =>
    request<{ id: string }>("/setup", {
      method: "POST",
      body: JSON.stringify({ setupToken, password }),
    }),
  signIn: async (password: string, totpCode?: string, recoveryCode?: string) => {
    const result = await request<{ owner: { id: string }; csrfToken: string }>("/sessions", {
      method: "POST",
      body: JSON.stringify({ password, totpCode, recoveryCode }),
    });
    csrfToken = result.csrfToken;
    return result;
  },
  signOut: () => request<void>("/sessions/current", { method: "DELETE" }),
  reauth: (password: string, totpCode: string) =>
    request<void>("/sessions/reauth", {
      method: "POST",
      body: JSON.stringify({ password, totpCode }),
    }),
  beginMFA: (password: string) =>
    request<{ secret: string; otpauth: string }>("/owner/mfa/setup", {
      method: "POST",
      body: JSON.stringify({ password }),
    }),
  confirmMFA: (totpCode: string) =>
    request<{ recoveryCodes: string[] }>("/owner/mfa/confirm", {
      method: "POST",
      body: JSON.stringify({ totpCode }),
    }),
  devices: (query = "") => request<ListResponse<Device>>(`/devices${query}`),
  device: (id: string) => request<Device>(`/devices/${encodeURIComponent(id)}`),
  metrics: (id: string, query = "") =>
    request<{ series: MetricSeries[] }>(`/devices/${encodeURIComponent(id)}/metrics${query}`),
  sites: () => request<ListResponse<Site>>("/sites"),
  createSite: (name: string) => request<Site>("/sites", { method: "POST", body: JSON.stringify({ name }) }),
  scopes: () => request<ListResponse<Scope>>("/scopes"),
  createScope: (value: {
    siteId: string;
    ranges: string[];
    exclusions: string[];
    methods: string[];
    ports: number[];
    credentialRef?: string;
    trustRef?: string;
    limits?: { probesPerSecond: number; concurrency: number; targetBudget: number };
    enabled: boolean;
  }) => request<Scope>("/scopes", { method: "POST", body: JSON.stringify(value) }),
  updateScope: (
    id: string,
    value: {
      expectedRevision: number;
      ranges: string[];
      exclusions: string[];
      methods: string[];
      ports: number[];
      credentialRef?: string;
      trustRef?: string;
      limits?: { probesPerSecond: number; concurrency: number; targetBudget: number };
      enabled: boolean;
    },
  ) => request<Scope>(`/scopes/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(value) }),
  alertRules: (query = "") => request<ListResponse<AlertRule>>(`/alert-rules${query}`),
  createAlertRule: (value: AlertRuleInput) =>
    request<AlertRule>("/alert-rules", { method: "POST", body: JSON.stringify(value) }),
  updateAlertRule: (id: string, value: AlertRulePatch) =>
    request<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(value) }),
  retireAlertRule: (id: string, expectedRevision: number) =>
    request<void>(`/alert-rules/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: JSON.stringify({ expectedRevision }),
    }),
  alertOverrides: (ruleId: string, query = "") =>
    request<ListResponse<AlertOverride>>(`/alert-rules/${encodeURIComponent(ruleId)}/overrides${query}`),
  createAlertOverride: (ruleId: string, value: AlertOverrideInput) =>
    request<AlertOverride>(`/alert-rules/${encodeURIComponent(ruleId)}/overrides`, {
      method: "POST",
      body: JSON.stringify(value),
    }),
  updateAlertOverride: (ruleId: string, overrideId: string, value: AlertOverridePatch) =>
    request<AlertOverride>(`/alert-rules/${encodeURIComponent(ruleId)}/overrides/${encodeURIComponent(overrideId)}`, {
      method: "PATCH",
      body: JSON.stringify(value),
    }),
  deleteAlertOverride: (ruleId: string, overrideId: string, expectedRevision: number) =>
    request<void>(`/alert-rules/${encodeURIComponent(ruleId)}/overrides/${encodeURIComponent(overrideId)}`, {
      method: "DELETE",
      body: JSON.stringify({ expectedRevision }),
    }),
  incidents: (query = "") => request<ListResponse<Incident>>(`/incidents${query}`),
  incident: (id: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}`),
  incidentTransitions: (id: string, query = "") =>
    request<ListResponse<IncidentTransition>>(`/incidents/${encodeURIComponent(id)}/transitions${query}`),
  acknowledgeIncident: (id: string, expectedRevision: number) =>
    request<Incident>(`/incidents/${encodeURIComponent(id)}/acknowledgment`, {
      method: "POST",
      body: JSON.stringify({ expectedRevision }),
    }),
  invitation: (displayName: string, siteId: string) =>
    request<Invitation>("/bootstrap-invitations", {
      method: "POST",
      body: JSON.stringify({ displayName, siteId }),
    }),
  deviceInvitation: (deviceId: string) =>
    request<Invitation>(`/devices/${encodeURIComponent(deviceId)}/bootstrap`, { method: "POST" }),
  topology: (siteId = "") => request<Topology>(`/topology${siteId ? `?siteId=${encodeURIComponent(siteId)}` : ""}`),
  notificationDestinations: () => request<ListResponse<NotificationDestination>>("/notification-destinations"),
  createNotificationDestination: (value: {
    name: string;
    baseUrl: string;
    topic: string;
    token?: string;
    enabled: boolean;
    allowPlainHttp: boolean;
  }) =>
    request<NotificationDestination>("/notification-destinations", {
      method: "POST",
      body: JSON.stringify(value),
    }),
  updateNotificationDestination: (
    id: string,
    value: {
      expectedRevision: number;
      name?: string;
      baseUrl?: string;
      topic?: string;
      token?: string | null;
      enabled?: boolean;
      allowPlainHttp?: boolean;
    },
  ) =>
    request<NotificationDestination>(`/notification-destinations/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(value),
    }),
  deleteNotificationDestination: (id: string, expectedRevision: number) =>
    request<void>(`/notification-destinations/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: JSON.stringify({ expectedRevision }),
    }),
  testNotificationDestination: (id: string, expectedRevision: number) =>
    request<{ deliveryId: string; status: string }>(`/notification-destinations/${encodeURIComponent(id)}/test`, {
      method: "POST",
      body: JSON.stringify({ expectedRevision }),
    }),
  notificationDeliveries: (query = "") =>
    request<ListResponse<NotificationDelivery>>(`/notification-deliveries${query}`),
  suppressionWindows: (query = "") => request<ListResponse<SuppressionWindow>>(`/suppression-windows${query}`),
  createSuppressionWindow: (value: {
    name: string;
    targetKind: SuppressionWindow["targetKind"];
    targetId?: string;
    enabled: boolean;
    mode: SuppressionWindow["mode"];
    timezone?: string;
    weekdays?: number[];
    startLocal?: string;
    endLocal?: string;
    startsAt?: string;
    endsAt?: string;
  }) => request<SuppressionWindow>("/suppression-windows", { method: "POST", body: JSON.stringify(value) }),
  updateSuppressionWindow: (
    id: string,
    value: {
      expectedRevision: number;
      name?: string;
      targetKind?: SuppressionWindow["targetKind"];
      targetId?: string;
      enabled?: boolean;
      mode?: SuppressionWindow["mode"];
      timezone?: string;
      weekdays?: number[];
      startLocal?: string;
      endLocal?: string;
      startsAt?: string;
      endsAt?: string;
    },
  ) =>
    request<SuppressionWindow>(`/suppression-windows/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(value),
    }),
  deleteSuppressionWindow: (id: string, expectedRevision: number) =>
    request<void>(`/suppression-windows/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: JSON.stringify({ expectedRevision }),
    }),
  resumeNotifications: (expectedRevision: number) =>
    request<MonitoringSettings>("/monitoring/notifications/resume", {
      method: "POST",
      body: JSON.stringify({ expectedRevision }),
    }),
  recoveryStatus: () =>
    request<{
      workspace: MonitoringWorkspace;
      telemetry: Record<string, unknown>;
    }>("/recovery/status"),
  reconcileRecovery: () => request("/recovery/reconcile", { method: "POST", body: JSON.stringify({}) }),
  telemetrySettings: () => request<Record<string, unknown>>("/settings/telemetry"),
  accessRequests: (state = "") =>
    request<ListResponse<AccessRequest>>(`/access-requests${state ? `?state=${encodeURIComponent(state)}` : ""}`),
  credentials: () => request<ListResponse<Credential>>("/credentials"),
  createCredential: (value: {
    kind: string;
    secret: string;
    allowedUse: string[];
    targets: string[];
    endpoint?: string;
  }) => request<Credential>("/credentials", { method: "POST", body: JSON.stringify(value) }),
  rotateCredential: (id: string, value: { secret: string; expectedRevision: number }) =>
    request<Credential>(`/credentials/${encodeURIComponent(id)}/rotate`, {
      method: "POST",
      body: JSON.stringify(value),
    }),
  revokeCredential: (id: string) => request<void>(`/credentials/${encodeURIComponent(id)}`, { method: "DELETE" }),
  trust: () => request<ListResponse<TrustRecord>>("/trust"),
  createTrust: (value: { scopeId?: string; host: string; endpoint: string; fingerprint: string; publicKey?: string }) =>
    request<TrustRecord>("/trust", { method: "POST", body: JSON.stringify(value) }),
  updateTrust: (id: string, value: Partial<TrustRecord> & { expectedRevision: number }) =>
    request<TrustRecord>(`/trust/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(value) }),
  jobs: (query = "") => request<ListResponse<Job>>(`/jobs${query}`),
  candidates: (query = "") => request<ListResponse<Candidate>>(`/candidates${query}`),
  discover: (
    scopeId: string,
    value: {
      source?: string;
      targetBudget?: number;
      probesPerSecond?: number;
      concurrency?: number;
      sightings?: Array<{ address: string; source: string; port?: number; reachable: boolean }>;
    },
  ) =>
    request<{ items: Candidate[] }>(`/scopes/${encodeURIComponent(scopeId)}/discover`, {
      method: "POST",
      body: JSON.stringify(value),
    }),
  enqueueCandidate: (id: string) =>
    request<{ eligible: boolean; reason?: string; job?: Job; request?: AccessRequest }>(
      `/candidates/${encodeURIComponent(id)}/enroll`,
      {
        method: "POST",
        body: JSON.stringify({}),
      },
    ),
  workers: () => request<ListResponse<Worker>>("/workers"),
  createWorker: (value: { name: string; siteIds: string[] }) =>
    request<Worker & { token: string }>("/workers", { method: "POST", body: JSON.stringify(value) }),
  pause: (value: { discovery: boolean; enrollment: boolean; updates: boolean }) =>
    request<Record<string, unknown>>("/control/pause", { method: "POST", body: JSON.stringify(value) }),
  controlState: () => request<Record<string, unknown>>("/control/state"),
  decommission: (id: string, value: { uninstall: boolean; reason: string }) =>
    request<DecommissionResult>(`/devices/${encodeURIComponent(id)}/decommission`, {
      method: "POST",
      body: JSON.stringify(value),
    }),
  reenable: (id: string, expectedRevision: number) =>
    request<Device>(`/devices/${encodeURIComponent(id)}/reenable`, {
      method: "POST",
      body: JSON.stringify({ expectedRevision }),
    }),
  updatePolicy: (id: string) => request<DeviceUpdatePolicy>(`/devices/${encodeURIComponent(id)}/update-policy`),
  setUpdatePolicy: (id: string, value: Omit<DeviceUpdatePolicy, "deviceId" | "revision" | "updatedAt">) =>
    request<DeviceUpdatePolicy>(`/devices/${encodeURIComponent(id)}/update-policy`, {
      method: "PATCH",
      body: JSON.stringify(value),
    }),
  releases: () => request<ListResponse<Release>>("/releases"),
  importRelease: (bundle: string) =>
    request<Release>("/releases/import", {
      method: "POST",
      body: bundle,
      headers: { "Content-Type": "application/json" },
    }),
  revokeRelease: (id: string) => request<void>(`/releases/${encodeURIComponent(id)}`, { method: "DELETE" }),
  rollouts: () => request<{ items: Rollout[]; assignments: Assignment[] }>("/rollouts"),
  createRollout: (value: {
    releaseId: string;
    mode: string;
    targets: string[];
    concurrency: number;
    canaries: number;
    failureThreshold: number;
  }) => request<RolloutResult>("/rollouts", { method: "POST", body: JSON.stringify(value) }),
  pauseRollout: (id: string) =>
    request<Rollout>(`/rollouts/${encodeURIComponent(id)}/pause`, { method: "POST", body: JSON.stringify({}) }),
  collectorDescriptors: () => request<ListResponse<CollectorDescriptor>>("/collectors"),
  services: (provider = "") =>
    request<ListResponse<ServiceEntity>>(`/services${provider ? `?provider=${encodeURIComponent(provider)}` : ""}`),
  collectors: (deviceId: string) =>
    request<ListResponse<CollectorConfig>>(`/devices/${encodeURIComponent(deviceId)}/collectors`),
  updateCollector: (
    deviceId: string,
    collectorId: string,
    value: {
      provider: string;
      enabled: boolean;
      config: Record<string, string>;
      credentialRef?: string;
      expectedRevision?: number;
    },
  ) =>
    request<CollectorConfig>(`/devices/${encodeURIComponent(deviceId)}/collectors/${encodeURIComponent(collectorId)}`, {
      method: "PATCH",
      body: JSON.stringify(value),
    }),
};
