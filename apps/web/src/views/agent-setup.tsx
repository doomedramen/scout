import { FormEvent, useEffect, useMemo, useState } from "react";
import { Check, Clipboard, KeyRound, ShieldCheck, Terminal, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, api, type Device, type Invitation, type Site } from "@/lib/api";

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

export function AgentSetupView({ onClose }: { onClose: () => void }) {
  const [sites, setSites] = useState<Site[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [siteId, setSiteId] = useState("");
  const [existingDeviceID, setExistingDeviceID] = useState("");
  const [displayName, setDisplayName] = useState("Scout server host");
  const [serverURL, setServerURL] = useState(() => window.location.origin);
  const [invitation, setInvitation] = useState<Invitation | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    Promise.all([api.sites(), api.devices()])
      .then(([siteResult, deviceResult]) => {
        setSites(siteResult.items);
        setDevices(deviceResult.items);
        if (siteResult.items[0]) setSiteId(siteResult.items[0].id);
      })
      .catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load sites"));
  }, []);

  const pendingDevices = useMemo(
    () =>
      devices
        .filter(
          (device) =>
            device.siteId === siteId &&
            !device.agentId &&
            !device.agentVersion &&
            !device.excluded &&
            device.lifecycle !== "decommissioned" &&
            device.availability !== "revoked",
        )
        .sort((left, right) => left.displayName.localeCompare(right.displayName) || left.id.localeCompare(right.id)),
    [devices, siteId],
  );

  useEffect(() => {
    if (existingDeviceID && pendingDevices.some((device) => device.id === existingDeviceID)) return;
    setExistingDeviceID(pendingDevices.length === 1 ? pendingDevices[0].id : "");
  }, [existingDeviceID, pendingDevices]);

  const nativeRecipe = useMemo(() => {
    const server = serverURL.trim().replace(/\/+$/, "") || window.location.origin;
    const installerURL = shellQuote(`${server}/api/v1/bootstrap/agent/install.sh`);
    const token = shellQuote(invitation?.invitation ?? "INVITATION_TOKEN");
    return [
      "# Run this on the Linux host Scout should monitor.",
      "# The installer checks the OS and sudo access, then uses sudo only where required.",
      `SCOUT_OTI=${token} bash -c \"$(curl -fsSL ${installerURL})\"`,
    ].join("\n");
  }, [invitation, serverURL]);

  const dockerRecipe = useMemo(() => {
    const url = JSON.stringify(serverURL.trim() || window.location.origin);
    return [
      "name: scout-agent",
      "services:",
      "  scout-agent:",
      "    image: ghcr.io/doomedramen/scout-agent:latest",
      "    restart: unless-stopped",
      "    network_mode: host",
      "    pid: host",
      "    uts: host",
      `    command: [\"--daemon\", \"--server\", ${url}, \"--invitation-file\", \"/run/secrets/scout_invitation\", \"--root\", \"/host\"]`,
      "    volumes:",
      "      - scout-agent-data:/var/lib/scout/agent",
      "      - /:/host:ro",
      "    secrets:",
      "      - scout_invitation",
      "    security_opt:",
      "      - no-new-privileges:true",
      "    cap_drop:",
      "      - ALL",
      "",
      "secrets:",
      "  scout_invitation:",
      "    file: ./scout-invitation",
      "",
      "volumes:",
      "  scout-agent-data:",
    ].join("\n");
  }, [serverURL]);

  async function createInvitation(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      setInvitation(
        await (existingDeviceID ? api.deviceInvitation(existingDeviceID) : api.invitation(displayName.trim(), siteId)),
      );
      setMessage("Invitation created. It is single-use and expires in five minutes.");
    } catch (caught) {
      setError(caught instanceof APIError ? caught.message : "Could not create invitation");
    } finally {
      setBusy(false);
    }
  }

  async function copy(value: string, label: string) {
    try {
      await copyText(value);
      setCopied(label);
      window.setTimeout(() => setCopied(""), 1800);
    } catch {
      setError("Copy failed. Select the text and copy it manually.");
    }
  }

  return (
    <section className="setup-panel agent-setup-panel" aria-labelledby="agent-setup-title">
      <div className="setup-panel-heading">
        <div>
          <Terminal size={21} />
          <h2 id="agent-setup-title">Install the first Linux agent</h2>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Close agent setup" onClick={onClose}>
          <X size={16} />
        </Button>
      </div>
      <p>
        Scout cannot install a privileged host service from the browser. Create a device-bound invitation here, then run
        the installer on the Linux host. The installer creates the service user, installs the binary, and starts
        systemd.
      </p>
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
      {!invitation ? (
        <form className="agent-setup-form" onSubmit={createInvitation}>
          <label>
            Device name
            <Input required value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
          </label>
          <label>
            Site
            <select
              required
              value={siteId}
              onChange={(event) => setSiteId(event.target.value)}
              disabled={!sites.length}
            >
              <option value="">{sites.length ? "Choose a site" : "Create a site under Scopes first"}</option>
              {sites.map((site) => (
                <option key={site.id} value={site.id}>
                  {site.name}
                </option>
              ))}
            </select>
          </label>
          {pendingDevices.length > 0 && (
            <label>
              Existing pending device
              <select
                aria-label="Existing pending device"
                value={existingDeviceID}
                onChange={(event) => setExistingDeviceID(event.target.value)}
              >
                <option value="">Create a new device record</option>
                {pendingDevices.map((device) => (
                  <option key={device.id} value={device.id}>
                    {device.displayName} · {device.addresses?.[0] ?? "address unavailable"} · {device.id.slice(0, 8)}
                  </option>
                ))}
              </select>
              <small>
                Reuse this record when retrying after an app restart; it will not create a duplicate system.
              </small>
            </label>
          )}
          <Button type="submit" disabled={busy || !siteId || (pendingDevices.length === 1 && !existingDeviceID)}>
            <KeyRound size={15} />
            {existingDeviceID ? "Create invitation for existing device" : "Create one-time invitation"}
          </Button>
        </form>
      ) : (
        <div className="agent-setup-recipe">
          <div className="agent-invitation">
            <div>
              <Badge variant="outline">
                <ShieldCheck size={13} />
                Copy once
              </Badge>
              <h3>One-time invitation</h3>
              <small>
                Expires {new Date(invitation.expiresAt).toLocaleTimeString()} · device {invitation.deviceId}
              </small>
            </div>
            <div className="secret-copy-row">
              <code>{invitation.invitation}</code>
              <Button size="sm" variant="outline" onClick={() => copy(invitation.invitation, "invitation")}>
                {copied === "invitation" ? <Check size={14} /> : <Clipboard size={14} />}
                {copied === "invitation" ? "Copied" : "Copy"}
              </Button>
            </div>
          </div>
          <label>
            Server URL reachable by the host
            <Input value={serverURL} onChange={(event) => setServerURL(event.target.value)} />
          </label>
          <div className="agent-recipe-heading">
            <div>
              <h3>Native Linux (recommended)</h3>
              <small>
                Checks the host, downloads the matching agent, verifies its checksum, and uses sudo internally to enable
                scout-agent.service.
              </small>
            </div>
            <Button size="sm" variant="outline" onClick={() => copy(nativeRecipe, "native")}>
              {copied === "native" ? <Check size={14} /> : <Clipboard size={14} />}
              {copied === "native" ? "Copied" : "Copy recipe"}
            </Button>
          </div>
          <pre className="agent-recipe">
            <code>{nativeRecipe}</code>
          </pre>
          <p className="agent-install-note">
            Official Scout server images include AMD64 and ARM64 bootstrap binaries. For a source checkout, set
            <code>SCOUT_AGENT_BOOTSTRAP_DIR</code> to a directory containing the matching binaries, or build{" "}
            <code>scout-agent</code> and add <code>--artifact ./scout-agent</code> to the installer command.
          </p>
          <details className="agent-docker-option">
            <summary>Run the agent in Docker on this Linux host</summary>
            <p>
              This uses host networking, PID/UTS namespaces, and a read-only host-root mount so the container can see
              host metrics. Use native installation when possible.
            </p>
            <div className="agent-recipe-heading">
              <small>Save the invitation as ./scout-invitation, then save this as compose.yaml.</small>
              <Button size="sm" variant="outline" onClick={() => copy(dockerRecipe, "docker")}>
                {copied === "docker" ? <Check size={14} /> : <Clipboard size={14} />}
                {copied === "docker" ? "Copied" : "Copy Compose"}
              </Button>
            </div>
            <pre className="agent-recipe">
              <code>{dockerRecipe}</code>
            </pre>
            <code className="agent-run-command">docker compose up -d</code>
          </details>
          <Button variant="ghost" size="sm" onClick={() => setInvitation(null)}>
            Create another invitation
          </Button>
        </div>
      )}
    </section>
  );
}
