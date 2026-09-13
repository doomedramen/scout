import { useEffect, useMemo, useState } from "react";
import { Network as NetworkIcon, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  api,
  APIError,
  type Candidate,
  type Device,
  type Relationship,
  type ScanStatus,
  type TopologyNode,
} from "@/lib/api";
import {
  formatScanTime,
  humanizeScanValue,
  scanActiveRunLabel,
  scanCoverageLabel,
  scanVantageLabel,
} from "@/lib/scan-status";

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

export function NetworkView({
  onSelect,
  onCandidateSelect,
}: {
  onSelect: (device: Device) => void;
  onCandidateSelect: (candidate: Candidate) => void;
}) {
  const [nodes, setNodes] = useState<TopologyNode[]>([]);
  const [relationships, setRelationships] = useState<Relationship[]>([]);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [scanStatuses, setScanStatuses] = useState<Record<string, ScanStatus>>({});
  const [candidateState, setCandidateState] = useState("");
  const [candidateQuery, setCandidateQuery] = useState("");
  const [nodeQuery, setNodeQuery] = useState("");
  const [viewMode, setViewMode] = useState<"list" | "map">("map");
  const [error, setError] = useState("");
  const [candidateError, setCandidateError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    const syncView = () => setViewMode(window.innerWidth < 1024 ? "list" : "map");
    syncView();
    window.addEventListener("resize", syncView);
    return () => window.removeEventListener("resize", syncView);
  }, []);

  useEffect(() => {
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
  }, [retry]);

  useEffect(() => {
    if (!candidates.length) {
      setScanStatuses({});
      return;
    }
    let cancelled = false;
    const scopeIDs = [...new Set(candidates.map((candidate) => candidate.scopeId).filter(Boolean))];
    const loadStatuses = async () => {
      const next: Record<string, ScanStatus> = {};
      await Promise.all(
        scopeIDs.map(async (scopeID) => {
          try {
            next[scopeID] = await api.scanStatus(scopeID);
          } catch {
            // Candidate evidence stays visible when one scope status request is unavailable.
          }
        }),
      );
      if (!cancelled) setScanStatuses(next);
    };
    void loadStatuses();
    const timer = window.setInterval(() => void loadStatuses(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [candidates]);

  useEffect(() => {
    let cancelled = false;
    const loadCandidates = () =>
      api
        .candidates("?limit=100")
        .then((result) => {
          if (cancelled) return;
          setCandidates(result.items);
          setCandidateError("");
        })
        .catch((caught) => {
          if (!cancelled)
            setCandidateError(caught instanceof APIError ? caught.message : "Could not load found devices");
        });
    void loadCandidates();
    const timer = window.setInterval(loadCandidates, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [retry]);

  const visibleNodes = nodes;
  const nodeLabels = useMemo(
    () => new Map(visibleNodes.map((node) => [node.id, node.label ?? node.id])),
    [visibleNodes],
  );
  const shownNodes = useMemo(() => {
    const query = nodeQuery.trim().toLowerCase();
    if (!query) return visibleNodes;
    return visibleNodes.filter((node) =>
      [node.label, node.id, ...(node.addresses ?? [])]
        .filter(Boolean)
        .some((value) => value!.toLowerCase().includes(query)),
    );
  }, [nodeQuery, visibleNodes]);

  const shownRelationships = relationships;
  const shownCandidates = useMemo(() => {
    const query = candidateQuery.trim().toLowerCase();
    return candidates.filter((candidate) => {
      if (candidateState && candidate.state !== candidateState) return false;
      if (!query) return true;
      return [candidate.displayName, candidate.hostname, candidate.address, candidate.source]
        .filter(Boolean)
        .some((value) => value!.toLowerCase().includes(query));
    });
  }, [candidateQuery, candidateState, candidates]);

  return (
    <section className="network-panel">
      <div className="map-header">
        <Badge variant="outline">
          <ShieldCheck size={13} />
          Evidence-backed
        </Badge>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh topology"
          onClick={() => setRetry((value) => value + 1)}
        >
          <RefreshCw size={16} />
        </Button>
      </div>
      <div className="network-view-controls">
        <div className="network-view-toggle" role="group" aria-label="Network view">
          <button
            type="button"
            aria-pressed={viewMode === "list"}
            className={viewMode === "list" ? "active" : ""}
            onClick={() => setViewMode("list")}
          >
            List
          </button>
          <button
            type="button"
            aria-pressed={viewMode === "map"}
            className={viewMode === "map" ? "active" : ""}
            onClick={() => setViewMode("map")}
          >
            Map
          </button>
        </div>
        <label className="network-search">
          <span>Search observed devices</span>
          <input
            value={nodeQuery}
            onChange={(event) => setNodeQuery(event.target.value)}
            placeholder="Name or address"
          />
        </label>
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
          <section className="found-devices-panel" aria-labelledby="found-devices-heading">
            <div className="section-heading">
              <div>
                <Badge variant="outline">
                  <ShieldCheck size={13} />
                  Scan evidence
                </Badge>
                <h2 id="found-devices-heading">Found devices</h2>
              </div>
              <Badge variant="outline">{candidates.length}</Badge>
            </div>
            <div className="filter-row candidate-filters">
              <label>
                Search found devices
                <input
                  value={candidateQuery}
                  onChange={(event) => setCandidateQuery(event.target.value)}
                  placeholder="Address or hostname"
                />
              </label>
              <label>
                State
                <select value={candidateState} onChange={(event) => setCandidateState(event.target.value)}>
                  <option value="">All states</option>
                  <option value="needs_credentials">Needs credentials</option>
                  <option value="needs_host_trust">Needs host trust</option>
                  <option value="needs_privilege">Needs privilege</option>
                  <option value="needs_server_connectivity">Needs server connectivity</option>
                  <option value="queued">Queued</option>
                  <option value="enrolling">Enrolling</option>
                  <option value="enrolled">Enrolled</option>
                  <option value="unsupported">Unsupported service</option>
                  <option value="stale">Stale evidence</option>
                </select>
              </label>
            </div>
            {candidateError ? (
              <div className="empty-inline" role="alert">
                {candidateError}
              </div>
            ) : shownCandidates.length ? (
              <div className="candidate-list" role="list" aria-label="Found devices">
                {shownCandidates.map((candidate) => (
                  <div className="candidate-row" key={candidate.id} role="listitem">
                    <button type="button" className="candidate-row-main" onClick={() => onCandidateSelect(candidate)}>
                      <span className="candidate-row-icon" aria-hidden="true">
                        <Server size={18} />
                      </span>
                      <span className="candidate-row-copy">
                        <strong>{candidate.displayName || candidate.hostname || candidate.address}</strong>
                        <small>
                          {candidate.address} · {candidate.entryPointCount ?? candidate.entryPointIds?.length ?? 0}{" "}
                          known service
                          {(candidate.entryPointCount ?? candidate.entryPointIds?.length ?? 0) === 1 ? "" : "s"} · last
                          seen{" "}
                          {candidate.lastScannedAt ? new Date(candidate.lastScannedAt).toLocaleString() : "not yet"}
                        </small>
                        <small className="candidate-row-scan-status">
                          {scanStatuses[candidate.scopeId] ? (
                            <>
                              Scan coverage: {scanCoverageLabel(scanStatuses[candidate.scopeId], true)} ·{" "}
                              {scanStatuses[candidate.scopeId].vantages.length
                                ? scanStatuses[candidate.scopeId].vantages
                                    .filter((vantage) => vantage.assigned)
                                    .map(
                                      (vantage) =>
                                        `${scanVantageLabel(vantage)} ${humanizeScanValue(vantage.state).toLowerCase()}`,
                                    )
                                    .join(" · ")
                                : "No assigned vantage"}
                              {scanStatuses[candidate.scopeId].activeRun && (
                                <>
                                  {" · "}
                                  {scanActiveRunLabel(scanStatuses[candidate.scopeId])}
                                </>
                              )}
                              {scanStatuses[candidate.scopeId].lastCompletedAt && (
                                <> · completed {formatScanTime(scanStatuses[candidate.scopeId].lastCompletedAt)}</>
                              )}
                            </>
                          ) : (
                            "Scan status loading…"
                          )}
                        </small>
                      </span>
                      <span className="candidate-row-state">
                        <Badge variant="outline">{candidate.state.replaceAll("_", " ")}</Badge>
                        <small>{candidate.action?.label ?? "No action"}</small>
                      </span>
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="empty-inline">
                {candidates.length ? "No found devices match this filter." : "No devices found."}
              </p>
            )}
          </section>
          <div className={`network-observed ${viewMode}`}>
            <div className="map-root">
              <NetworkIcon size={22} />
              <strong>Scout network</strong>
              <span>{shownNodes.length} observed devices</span>
            </div>
            <div className="map-devices" role="list" aria-label="Topology nodes">
              {shownNodes.map((node) => {
                const selected = nodeDevice(node);
                return (
                  <button key={node.id} onClick={() => onSelect(selected)} role="listitem">
                    <Server size={20} aria-hidden="true" />
                    <strong>{node.label ?? node.id}</strong>
                    <small>{node.addresses?.join(", ") || "Address unavailable"}</small>
                    <span
                      className={
                        "dot " +
                        (node.availability === "online" || node.availability === "healthy" ? "healthy" : "muted")
                      }
                      aria-hidden="true"
                    />
                    <span className="sr-only">{node.availability ?? "unknown"}</span>
                  </button>
                );
              })}
            </div>
            {!shownNodes.length && <p className="empty-inline">No observed devices match this search.</p>}
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
              Owner corrections remain separate from observed evidence; expired relationships disappear from this view.
            </p>
          </div>
        </>
      )}
    </section>
  );
}
