import { useEffect, useMemo, useState } from "react";
import { Network as NetworkIcon, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, APIError, type Device, type Relationship, type TopologyNode } from "@/lib/api";
import { demoDevices, type Device as DemoDevice } from "@/demo";

function demoDevice(item: DemoDevice, index: number): Device {
  const observedAt = new Date().toISOString();
  return {
    id: "demo-" + index,
    displayName: item.name,
    platform: "linux",
    architecture: "amd64",
    addresses: [item.address],
    lifecycle: "enrolled",
    agentVersion: item.version === "—" ? undefined : item.version,
    availability: item.status === "Offline" ? "offline" : "online",
    metricFreshness: {},
    currentMetrics: {
      "cpu.utilization": { value: item.cpu, unit: "percent", availability: "current", observedAt },
      "memory.used_percent": { value: item.memory, unit: "percent", availability: "current", observedAt },
      "filesystem.used_percent": { value: item.disk, unit: "percent", availability: "current", observedAt },
    },
    collectorStates: [],
    revision: 1,
  };
}

function nodeDevice(node: TopologyNode): Device {
  return {
    id: node.id,
    displayName: node.label ?? node.id,
    platform: "linux",
    architecture: "unknown",
    addresses: node.addresses ?? [],
    lifecycle: node.lifecycle ?? "enrolled",
    availability: node.availability === "online" ? "online" : node.availability === "revoked" ? "revoked" : "offline",
    metricFreshness: {},
    collectorStates: [],
    revision: 1,
  };
}

function relationshipKind(type: string): string {
  const value = type.toLowerCase();
  return value.includes("physical") || value.includes("link") ? "Physical evidence" : "Logical evidence";
}

function confidenceLabel(value: number): string {
  return (value * 100).toFixed(0) + "%";
}

export function NetworkView({ demo, onSelect }: { demo: boolean; onSelect: (device: Device) => void }) {
  const [nodes, setNodes] = useState<TopologyNode[]>([]);
  const [relationships, setRelationships] = useState<Relationship[]>([]);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    if (demo) {
      setError("");
      return;
    }
    let cancelled = false;
    setError("");
    api
      .topology()
      .then((result) => {
        if (cancelled) return;
        setNodes(result.nodes);
        setRelationships(result.relationships);
      })
      .catch((caught) => {
        if (!cancelled) setError(caught instanceof APIError ? caught.message : "Could not load topology");
      });
    return () => {
      cancelled = true;
    };
  }, [demo, retry]);

  const visibleNodes = demo
    ? demoDevices.map((item, index) => ({
        id: "demo-" + index,
        label: item.name,
        addresses: [item.address],
        availability: item.status.toLowerCase(),
      }))
    : nodes;
  const nodeLabels = useMemo(
    () => new Map(visibleNodes.map((node) => [node.id, node.label ?? node.id])),
    [visibleNodes],
  );

  const demoRelationships: Relationship[] = demo
    ? [
        {
          id: "demo-edge-1",
          fromEntity: "demo-0",
          toEntity: "demo-1",
          type: "logical-membership",
          confidence: 0.98,
          projectionRevision: 1,
          evidenceIds: ["demo-observation"],
          observedAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
          source: "illustrative-fixture",
        },
      ]
    : relationships;
  const shownRelationships = demo ? demoRelationships : relationships;

  return (
    <section className="network-panel">
      <div className="map-header">
        <Badge variant="outline">
          <ShieldCheck size={13} />
          Evidence-backed
        </Badge>
        <span>
          {demo
            ? "Illustrative observations · not physical cabling"
            : "Relationships expire unless refreshed; inspect source, confidence, and age."}
        </span>
        {!demo && (
          <Button
            variant="ghost"
            size="icon"
            aria-label="Refresh topology"
            onClick={() => setRetry((value) => value + 1)}
          >
            <RefreshCw size={16} />
          </Button>
        )}
      </div>
      {error ? (
        <div className="empty" role="alert">
          <NetworkIcon size={32} />
          <h2>Topology unavailable</h2>
          <p>{error}</p>
          <Button variant="outline" onClick={() => setRetry((value) => value + 1)}>
            Retry
          </Button>
        </div>
      ) : (
        <>
          <div className="map-root">
            <NetworkIcon size={22} />
            <strong>{demo ? "Lab network" : "Scout network"}</strong>
            <span>{visibleNodes.length} observed devices</span>
          </div>
          <div className="map-devices" role="list" aria-label="Topology nodes">
            {visibleNodes.map((node, index) => {
              const selected = demo ? demoDevice(demoDevices[index], index) : nodeDevice(node);
              return (
                <button key={node.id} onClick={() => onSelect(selected)} role="listitem">
                  <Server size={20} aria-hidden="true" />
                  <strong>{node.label ?? node.id}</strong>
                  <small>{node.addresses?.join(", ") || "Address unavailable"}</small>
                  <span
                    className={
                      "dot " + (node.availability === "online" || node.availability === "healthy" ? "healthy" : "muted")
                    }
                    aria-hidden="true"
                  />
                  <span className="sr-only">{node.availability ?? "unknown"}</span>
                </button>
              );
            })}
          </div>
          <section className="topology-inspector" aria-labelledby="relationship-list-heading">
            <div className="section-heading">
              <div>
                <h3 id="relationship-list-heading">Relationship evidence</h3>
                <p>
                  Logical membership does not prove physical cabling. Confidence is shown separately from relationship
                  type.
                </p>
              </div>
              <Badge variant="outline">{shownRelationships.length}</Badge>
            </div>
            {!shownRelationships.length ? (
              <p className="empty-inline">No relationships have been observed yet.</p>
            ) : (
              <div className="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>From</th>
                      <th>To</th>
                      <th>Evidence type</th>
                      <th>Confidence</th>
                      <th>Source</th>
                      <th>Observed / expires</th>
                      <th>Evidence IDs</th>
                    </tr>
                  </thead>
                  <tbody>
                    {shownRelationships.map((relationship) => (
                      <tr key={relationship.id}>
                        <td>{nodeLabels.get(relationship.fromEntity) ?? relationship.fromEntity}</td>
                        <td>{nodeLabels.get(relationship.toEntity) ?? relationship.toEntity}</td>
                        <td>
                          <strong>{relationshipKind(relationship.type)}</strong>
                          <small>{relationship.type}</small>
                        </td>
                        <td>{confidenceLabel(relationship.confidence)}</td>
                        <td>{relationship.source || "Unknown"}</td>
                        <td>
                          <small>{new Date(relationship.observedAt).toLocaleString()}</small>
                          <small>expires {new Date(relationship.expiresAt).toLocaleString()}</small>
                        </td>
                        <td>
                          {relationship.evidenceIds.length ? relationship.evidenceIds.join(", ") : "Not attached"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
          <div className="evidence-list" aria-label="Topology interpretation">
            <p>
              {demo
                ? "Demo relationship evidence is isolated and labeled. It never enters operational storage."
                : "Owner corrections remain separate from observed evidence; expired relationships disappear from this view."}
            </p>
          </div>
        </>
      )}
    </section>
  );
}
