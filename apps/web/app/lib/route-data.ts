import type { Candidate, Device, Incident, Relationship, TopologyNode } from "@/lib/api";

export type OverviewRouteData = {
  kind: "overview";
  devices: Device[];
  candidates: Candidate[];
  incidents: Incident[];
  sourceErrors: Partial<Record<"devices" | "candidates" | "incidents", string>>;
};

export type SystemsRouteData = {
  kind: "systems";
  devices: Device[];
  candidates: Candidate[];
  error: string | null;
};

export type NetworkRouteData = {
  kind: "network";
  nodes: TopologyNode[];
  relationships: Relationship[];
  candidates: Candidate[];
  topologyError: string | null;
  candidateError: string | null;
};

export type RouteData = OverviewRouteData | SystemsRouteData | NetworkRouteData;
