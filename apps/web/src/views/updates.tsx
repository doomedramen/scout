import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Check, Download, FileKey2, Pause, RefreshCw, ShieldCheck, Upload } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, APIError, type Assignment, type Device, type Release, type Rollout } from "@/lib/api";

function splitTargets(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function displayVersion(release: Release | undefined): string {
  return release ? release.version + " · generation " + release.generation : "Unknown release";
}

export function UpdatesView() {
  const [releases, setReleases] = useState<Release[]>([]);
  const [rollouts, setRollouts] = useState<Rollout[]>([]);
  const [assignments, setAssignments] = useState<Assignment[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedRelease, setSelectedRelease] = useState("");
  const [mode, setMode] = useState("manual");
  const [targets, setTargets] = useState("");
  const [concurrency, setConcurrency] = useState("1");
  const [canaries, setCanaries] = useState("0");
  const [failureThreshold, setFailureThreshold] = useState("3");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  async function refresh() {
    const [releaseList, rolloutState, deviceList] = await Promise.all([api.releases(), api.rollouts(), api.devices()]);
    setReleases(releaseList.items);
    setRollouts(rolloutState.items);
    setAssignments(rolloutState.assignments);
    setDevices(deviceList.items);
    if (!selectedRelease && releaseList.items[0]) setSelectedRelease(releaseList.items[0].id);
  }

  useEffect(() => {
    refresh().catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load update state"));
  }, []);

  const releaseById = useMemo(() => new Map(releases.map((release) => [release.id, release])), [releases]);
  const assignmentByDevice = useMemo(
    () => new Map(assignments.map((assignment) => [assignment.deviceId, assignment])),
    [assignments],
  );

  async function importRelease(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = event.currentTarget.elements.namedItem("releaseBundle");
    if (!(input instanceof HTMLInputElement) || !input.files?.[0]) {
      setError("Choose a signed release bundle first.");
      return;
    }
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const bundle = await input.files[0].text();
      const release = await api.importRelease(bundle);
      setMessage("Release " + release.version + " accepted after server-side manifest and signature verification.");
      event.currentTarget.reset();
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Release bundle could not be imported");
    } finally {
      setBusy(false);
    }
  }

  async function revokeRelease(release: Release) {
    if (!window.confirm("Revoke " + release.version + "? Agents will reject it for future downloads.")) return;
    setBusy(true);
    setError("");
    try {
      await api.revokeRelease(release.id);
      setMessage("Release " + release.version + " revoked.");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Release could not be revoked");
    } finally {
      setBusy(false);
    }
  }

  async function createRollout(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedRelease) {
      setError("Select a verified release before creating a rollout.");
      return;
    }
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api.createRollout({
        releaseId: selectedRelease,
        mode,
        targets: splitTargets(targets),
        concurrency: Number(concurrency) || 1,
        canaries: Number(canaries) || 0,
        failureThreshold: Number(failureThreshold) || 3,
      });
      setMessage(
        "Rollout created with " +
          result.assignments.length +
          " bounded assignment" +
          (result.assignments.length === 1 ? "" : "s") +
          ".",
      );
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Rollout could not be created");
    } finally {
      setBusy(false);
    }
  }

  async function pauseRollout(rollout: Rollout) {
    setBusy(true);
    setError("");
    try {
      await api.pauseRollout(rollout.id);
      setMessage("Rollout paused. Agents will revalidate desired state before continuing.");
      await refresh();
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Rollout could not be paused");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="workspace-grid updates-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <ShieldCheck size={13} />
            Signed lifecycle
          </Badge>
          <h2>Agent updates</h2>
          <p>Only verified compatible releases can be assigned.</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh update state"
          onClick={() => refresh().catch(() => setError("Could not refresh update state"))}
        >
          <RefreshCw size={16} />
        </Button>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {message && (
        <p className="form-success" role="status">
          {message}
        </p>
      )}
      <div className="access-columns">
        <form className="form-panel" onSubmit={importRelease}>
          <div className="panel-title">
            <Upload size={17} />
            <h3>Import signed bundle</h3>
          </div>
          <p className="form-help">
            The signing private key stays outside Scout. Altered, unsigned, oversized, or incompatible bundles are
            rejected.
          </p>
          <label>
            Bundle JSON
            <input name="releaseBundle" type="file" accept=".json,application/json" />
          </label>
          <Button type="submit" disabled={busy}>
            <FileKey2 size={15} />
            Import and verify
          </Button>
        </form>
        <form className="form-panel" onSubmit={createRollout}>
          <div className="panel-title">
            <Download size={17} />
            <h3>Create bounded rollout</h3>
          </div>
          <label>
            Release
            <select required value={selectedRelease} onChange={(event) => setSelectedRelease(event.target.value)}>
              <option value="">Choose a verified release</option>
              {releases
                .filter((release) => !release.revokedAt)
                .map((release) => (
                  <option key={release.id} value={release.id}>
                    {displayVersion(release)} · {release.platform}/{release.architecture}
                  </option>
                ))}
            </select>
          </label>
          <label>
            Mode
            <select value={mode} onChange={(event) => setMode(event.target.value)}>
              <option value="manual">Manual</option>
              <option value="automatic">Automatic</option>
              <option value="pinned">Pinned</option>
            </select>
          </label>
          <label>
            Device IDs <span className="label-hint">optional; empty means all compatible devices</span>
            <Input
              value={targets}
              onChange={(event) => setTargets(event.target.value)}
              placeholder="device-id-1, device-id-2"
            />
          </label>
          <div className="form-inline">
            <label>
              Concurrency
              <Input
                inputMode="numeric"
                min="1"
                max="100"
                value={concurrency}
                onChange={(event) => setConcurrency(event.target.value)}
              />
            </label>
            <label>
              Canaries
              <Input
                inputMode="numeric"
                min="0"
                value={canaries}
                onChange={(event) => setCanaries(event.target.value)}
              />
            </label>
            <label>
              Failure pause
              <Input
                inputMode="numeric"
                min="1"
                max="100"
                value={failureThreshold}
                onChange={(event) => setFailureThreshold(event.target.value)}
              />
            </label>
          </div>
          <Button type="submit" disabled={busy || !releases.length}>
            <Download size={15} />
            Plan rollout
          </Button>
        </form>
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Release catalog</h3>
          </div>
          <Badge variant="outline">{releases.length}</Badge>
        </div>
        {releases.length ? (
          <div className="release-list">
            {releases.map((release) => (
              <div key={release.id}>
                <Download size={17} />
                <strong>{release.version}</strong>
                <span>
                  {release.platform} / {release.architecture} · generation {release.generation} · {release.bytes} bytes
                </span>
                <Badge variant="outline" className={release.revokedAt ? "access-label" : "enabled-label"}>
                  {release.revokedAt ? "Revoked" : "Verified on import"}
                </Badge>
                {!release.revokedAt && (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => revokeRelease(release)}>
                    Revoke
                  </Button>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="update-features">
            {[
              ["Manual or automatic", "Select desired state; reconnecting agents revalidate assignments."],
              ["Offline-ready delivery", "Import a signed bundle into an isolated server."],
              ["Bounded rollback", "A previously verified local slot remains the only automatic rollback target."],
            ].map(([title, description]) => (
              <div key={title}>
                <Check size={17} />
                <h3>{title}</h3>
                <p>{description}</p>
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Rollouts</h3>
          </div>
          <Badge variant="outline">{rollouts.length}</Badge>
        </div>
        {rollouts.length ? (
          <div className="compact-list">
            {rollouts.map((rollout) => (
              <div key={rollout.id}>
                <strong>{displayVersion(releaseById.get(rollout.releaseId))}</strong>
                <span>
                  {rollout.mode} · {rollout.concurrency} concurrent · {rollout.canaries} canaries
                </span>
                <small>
                  {rollout.paused ? "Paused" : "Active"} · {rollout.failureCount} failures · revision {rollout.revision}
                </small>
                {!rollout.paused && (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => pauseRollout(rollout)}>
                    <Pause size={13} />
                    Pause
                  </Button>
                )}
              </div>
            ))}
          </div>
        ) : (
          <p className="empty-inline">No rollouts planned.</p>
        )}
      </div>
      <div className="data-panel">
        <div className="section-heading">
          <div>
            <h3>Desired versus installed</h3>
          </div>
          <Badge variant="outline">{devices.length}</Badge>
        </div>
        {devices.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Device</th>
                  <th>Installed</th>
                  <th>Desired</th>
                  <th>Assignment</th>
                </tr>
              </thead>
              <tbody>
                {devices.map((device) => {
                  const assignment = assignmentByDevice.get(device.id);
                  const release = assignment ? releaseById.get(assignment.desiredRelease) : undefined;
                  return (
                    <tr key={device.id}>
                      <td>
                        <strong>{device.displayName}</strong>
                        <small>{device.id}</small>
                      </td>
                      <td>{device.agentVersion ?? "Not installed"}</td>
                      <td>{release?.version ?? "No assignment"}</td>
                      <td>{assignment ? assignment.state + " · generation " + assignment.generation : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="empty-inline">No enrolled devices have reported an installed version.</p>
        )}
      </div>
    </section>
  );
}
