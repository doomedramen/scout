import { type FormEvent, useEffect, useMemo, useState } from "react";
import { BellRing, Check, Clock3, Plus, RefreshCw, Send, ShieldCheck, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  APIError,
  api,
  type Device,
  type NotificationDelivery,
  type NotificationDestination,
  type Site,
  type SuppressionWindow,
} from "@/lib/api";

const weekdayOptions = [
  { value: 1, label: "Mon" },
  { value: 2, label: "Tue" },
  { value: 3, label: "Wed" },
  { value: 4, label: "Thu" },
  { value: 5, label: "Fri" },
  { value: 6, label: "Sat" },
  { value: 7, label: "Sun" },
];

type WindowMode = SuppressionWindow["mode"];
type TargetKind = SuppressionWindow["targetKind"];

function caughtMessage(caught: unknown, fallback: string): string {
  return caught instanceof APIError ? (caught.details?.message ?? caught.message) : fallback;
}

function formatDate(value: string | null | undefined): string {
  if (!value) return "Never";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "Unknown" : date.toLocaleString();
}

function titleCase(value: string): string {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function targetLabel(window: SuppressionWindow, sites: Site[], devices: Device[]): string {
  if (window.targetKind === "fleet") return "Entire fleet";
  if (window.targetKind === "site") {
    return `Site · ${sites.find((site) => site.id === window.targetId)?.name ?? window.targetId ?? "unknown"}`;
  }
  return `Device · ${devices.find((device) => device.id === window.targetId)?.displayName ?? window.targetId ?? "unknown"}`;
}

function scheduleLabel(window: SuppressionWindow): string {
  if (window.mode === "oneTime") {
    return `${formatDate(window.startsAt)} → ${formatDate(window.endsAt)}`;
  }
  const days = (window.weekdays ?? [])
    .map((day) => weekdayOptions.find((option) => option.value === day)?.label ?? String(day))
    .join(", ");
  const overnight = window.startLocal && window.endLocal && window.startLocal > window.endLocal ? " · overnight" : "";
  return `${days} · ${window.startLocal}–${window.endLocal}${overnight} · ${window.timezone}`;
}

function toISOString(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.valueOf())) throw new Error("Enter valid start and end times");
  return parsed.toISOString();
}

function deliveryDescription(item: NotificationDelivery): string {
  switch (item.status) {
    case "accepted":
      return "Accepted by ntfy";
    case "suppressed":
      return "Held by a quiet window";
    case "cancelled":
      return item.safeError ?? "Cancelled before delivery";
    case "failed":
      return item.safeError ?? "Permanent delivery failure";
    case "expired":
      return "Expired before delivery";
    case "retry":
      return "Waiting for retry";
    case "sending":
      return "Sending to ntfy";
    default:
      return "Waiting for delivery";
  }
}

export function NotificationsView() {
  const [destinations, setDestinations] = useState<NotificationDestination[]>([]);
  const [windows, setWindows] = useState<SuppressionWindow[]>([]);
  const [deliveries, setDeliveries] = useState<NotificationDelivery[]>([]);
  const [sites, setSites] = useState<Site[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [deliveryFilter, setDeliveryFilter] = useState("");
  const [notificationsPaused, setNotificationsPaused] = useState(false);
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [policyRevision, setPolicyRevision] = useState(0);

  const [destinationName, setDestinationName] = useState("");
  const [baseUrl, setBaseUrl] = useState("https://ntfy.sh");
  const [topic, setTopic] = useState("");
  const [token, setToken] = useState("");
  const [destinationEnabled, setDestinationEnabled] = useState(true);
  const [allowPlainHttp, setAllowPlainHttp] = useState(false);

  const [windowName, setWindowName] = useState("");
  const [windowMode, setWindowMode] = useState<WindowMode>("recurring");
  const [targetKind, setTargetKind] = useState<TargetKind>("fleet");
  const [targetId, setTargetId] = useState("");
  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
  const [weekdays, setWeekdays] = useState<number[]>([1, 2, 3, 4, 5]);
  const [startLocal, setStartLocal] = useState("22:00");
  const [endLocal, setEndLocal] = useState("06:00");
  const [startsAt, setStartsAt] = useState("");
  const [endsAt, setEndsAt] = useState("");

  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const destinationByID = useMemo(
    () => new Map(destinations.map((destination) => [destination.id, destination])),
    [destinations],
  );

  async function refresh() {
    setBusy("refresh");
    setError("");
    try {
      const deliveryQuery = deliveryFilter ? `?status=${encodeURIComponent(deliveryFilter)}&limit=50` : "?limit=50";
      const [destinationPage, windowPage, deliveryPage, recovery, sitePage, devicePage] = await Promise.all([
        api.notificationDestinations(),
        api.suppressionWindows("?limit=100"),
        api.notificationDeliveries(deliveryQuery),
        api.recoveryStatus(),
        api.sites(),
        api.devices("?limit=100"),
      ]);
      setDestinations(destinationPage.items);
      setWindows(windowPage.items);
      setDeliveries(deliveryPage.items);
      setSites(sitePage.items);
      setDevices(devicePage.items);
      setNotificationsPaused(recovery.workspace.notificationsPaused);
      setRecoveryMode(recovery.workspace.recoveryMode);
      setPolicyRevision(recovery.workspace.policyRevision);
    } catch (caught) {
      setError(caughtMessage(caught, "Could not load notification settings"));
    } finally {
      setBusy("");
    }
  }

  useEffect(() => {
    refresh().catch(() => undefined);
  }, [deliveryFilter]);

  async function createDestination(event: FormEvent) {
    event.preventDefault();
    setBusy("destination");
    setError("");
    setMessage("");
    try {
      await api.createNotificationDestination({
        name: destinationName.trim(),
        baseUrl: baseUrl.trim(),
        topic: topic.trim(),
        ...(token.trim() ? { token: token.trim() } : {}),
        enabled: destinationEnabled,
        allowPlainHttp,
      });
      setDestinationName("");
      setTopic("");
      setToken("");
      setMessage("ntfy destination saved. The topic and token remain write-only.");
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "ntfy destination could not be saved"));
    } finally {
      setBusy("");
    }
  }

  async function toggleDestination(destination: NotificationDestination) {
    setBusy(`destination:${destination.id}`);
    setError("");
    setMessage("");
    try {
      await api.updateNotificationDestination(destination.id, {
        expectedRevision: destination.revision,
        enabled: !destination.enabled,
      });
      setMessage(`${destination.name} is now ${destination.enabled ? "paused" : "enabled"}.`);
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Destination changed elsewhere; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  async function testDestination(destination: NotificationDestination) {
    if (!destination.enabled) return;
    setBusy(`test:${destination.id}`);
    setError("");
    setMessage("");
    try {
      await api.testNotificationDestination(destination.id, destination.revision);
      setMessage(`${destination.name} accepted the test notification.`);
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "The ntfy test notification could not be delivered"));
    } finally {
      setBusy("");
    }
  }

  async function retireDestination(destination: NotificationDestination) {
    if (!window.confirm(`Remove ${destination.name}? Existing delivery history is retained.`)) return;
    setBusy(`remove:${destination.id}`);
    setError("");
    setMessage("");
    try {
      await api.deleteNotificationDestination(destination.id, destination.revision);
      setMessage(`${destination.name} was removed.`);
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Destination changed elsewhere; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  async function createWindow(event: FormEvent) {
    event.preventDefault();
    setBusy("window");
    setError("");
    setMessage("");
    try {
      const common = {
        name: windowName.trim(),
        targetKind,
        ...(targetKind === "fleet" ? {} : { targetId }),
        enabled: true,
        mode: windowMode,
      } as const;
      if (windowMode === "recurring") {
        await api.createSuppressionWindow({
          ...common,
          mode: "recurring",
          timezone: timezone.trim(),
          weekdays,
          startLocal,
          endLocal,
        });
      } else {
        await api.createSuppressionWindow({
          ...common,
          mode: "oneTime",
          startsAt: toISOString(startsAt),
          endsAt: toISOString(endsAt),
        });
      }
      setWindowName("");
      setMessage("Quiet window saved. Matching windows combine, so any active window suppresses delivery.");
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Quiet window could not be saved"));
    } finally {
      setBusy("");
    }
  }

  async function toggleWindow(windowItem: SuppressionWindow) {
    setBusy(`window:${windowItem.id}`);
    setError("");
    setMessage("");
    try {
      await api.updateSuppressionWindow(windowItem.id, {
        expectedRevision: windowItem.revision,
        enabled: !windowItem.enabled,
      });
      setMessage(`${windowItem.name} is now ${windowItem.enabled ? "paused" : "enabled"}.`);
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Quiet window changed elsewhere; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  async function retireWindow(windowItem: SuppressionWindow) {
    if (!window.confirm(`Remove ${windowItem.name}?`)) return;
    setBusy(`remove-window:${windowItem.id}`);
    setError("");
    setMessage("");
    try {
      await api.deleteSuppressionWindow(windowItem.id, windowItem.revision);
      setMessage(`${windowItem.name} was removed.`);
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Quiet window changed elsewhere; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  async function resumeNotifications() {
    if (!policyRevision || recoveryMode) return;
    setBusy("resume");
    setError("");
    setMessage("");
    try {
      await api.resumeNotifications(policyRevision);
      setMessage("Notifications resumed with a fresh summary of active incidents only.");
      await refresh();
    } catch (caught) {
      setError(caughtMessage(caught, "Notifications changed elsewhere; refresh and retry"));
    } finally {
      setBusy("");
    }
  }

  return (
    <section className="workspace-grid notifications-view">
      <div className="section-heading">
        <div>
          <Badge variant="outline">
            <BellRing size={13} />
            ntfy delivery
          </Badge>
          <h2>Alert delivery</h2>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh notification settings"
          onClick={() => refresh().catch(() => undefined)}
          disabled={busy === "refresh"}
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
      {notificationsPaused && (
        <section className="notification-fence" aria-labelledby="notification-fence-title">
          <div className="notification-fence-heading">
            <ShieldCheck size={19} />
            <div>
              <h3 id="notification-fence-title">External delivery is paused</h3>
              <p>Resume sends one current summary after review.</p>
            </div>
          </div>
          {recoveryMode ? (
            <p className="form-help">Review recovery before resuming.</p>
          ) : (
            <Button onClick={resumeNotifications} disabled={busy === "resume"}>
              {busy === "resume" ? "Resuming…" : "Resume notifications"}
            </Button>
          )}
        </section>
      )}
      <div className="notification-summary" aria-label="Notification summary">
        <div>
          <strong>{destinations.filter((destination) => destination.enabled).length}</strong>
          <span>enabled</span>
        </div>
        <div>
          <strong>{windows.filter((windowItem) => windowItem.enabled).length}</strong>
          <span>quiet windows</span>
        </div>
        <div>
          <strong>{deliveries.length}</strong>
          <span>recent deliveries</span>
        </div>
      </div>
      <div className="notification-columns">
        <form className="form-panel" onSubmit={createDestination}>
          <div className="panel-title">
            <Send size={17} />
            <h3>Add an ntfy destination</h3>
          </div>
          <p className="form-help">Tokens stay write-only.</p>
          <label>
            Destination name
            <Input required value={destinationName} onChange={(event) => setDestinationName(event.target.value)} />
          </label>
          <label>
            ntfy server URL
            <Input
              required
              type="url"
              placeholder="https://ntfy.sh"
              value={baseUrl}
              onChange={(event) => setBaseUrl(event.target.value)}
            />
          </label>
          <label>
            Topic
            <Input
              required
              pattern="[A-Za-z0-9_-]+"
              title="Use letters, numbers, hyphens, or underscores"
              value={topic}
              onChange={(event) => setTopic(event.target.value)}
            />
          </label>
          <label>
            Access token <span className="label-hint">optional, write-only</span>
            <Input
              type="password"
              autoComplete="new-password"
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={destinationEnabled}
              onChange={(event) => setDestinationEnabled(event.target.checked)}
            />
            <span>Enable destination after saving</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={allowPlainHttp}
              onChange={(event) => setAllowPlainHttp(event.target.checked)}
            />
            <span>Allow private HTTP for a local ntfy server</span>
          </label>
          <Button type="submit" disabled={busy !== ""}>
            <Plus size={15} />
            Save destination
          </Button>
        </form>
        <section className="data-panel" aria-labelledby="destinations-title">
          <div className="section-heading">
            <div>
              <h3 id="destinations-title">Destinations</h3>
              <p>Paused destinations do not receive alerts.</p>
            </div>
            <Badge variant="outline">{destinations.length}</Badge>
          </div>
          {destinations.length ? (
            <div className="notification-destination-list">
              {destinations.map((destination) => (
                <article
                  className="notification-destination"
                  data-testid={`destination-${destination.id}`}
                  key={destination.id}
                >
                  <div className="notification-destination-copy">
                    <div className="notification-destination-heading">
                      <strong>{destination.name}</strong>
                      <Badge variant="outline" className={destination.enabled ? "enabled-label" : "access-label"}>
                        {destination.enabled ? "Enabled" : "Paused"}
                      </Badge>
                    </div>
                    <span>
                      {destination.baseUrl} · {destination.maskedTopic}
                    </span>
                    <small>
                      {destination.hasToken ? "Access token stored" : "No access token"} · last test{" "}
                      {formatDate(destination.lastTestAt)} · revision {destination.revision}
                    </small>
                  </div>
                  <div className="notification-actions">
                    {destination.enabled && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => testDestination(destination)}
                        disabled={busy !== ""}
                      >
                        <Check size={14} />
                        {busy === `test:${destination.id}` ? "Testing…" : "Send test"}
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => toggleDestination(destination)}
                      disabled={busy !== ""}
                    >
                      {destination.enabled ? "Pause" : "Enable"}
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove ${destination.name}`}
                      onClick={() => retireDestination(destination)}
                      disabled={busy !== ""}
                    >
                      <Trash2 size={14} />
                    </Button>
                  </div>
                </article>
              ))}
            </div>
          ) : (
            <p className="empty-inline">No destinations.</p>
          )}
        </section>
      </div>
      <div className="notification-columns">
        <form className="form-panel" onSubmit={createWindow}>
          <div className="panel-title">
            <Clock3 size={17} />
            <h3>Add a quiet window</h3>
          </div>
          <p className="form-help">Matching windows suppress delivery. Incidents remain.</p>
          <label>
            Window name
            <Input required value={windowName} onChange={(event) => setWindowName(event.target.value)} />
          </label>
          <div className="notification-form-grid">
            <label>
              Schedule type
              <select value={windowMode} onChange={(event) => setWindowMode(event.target.value as WindowMode)}>
                <option value="recurring">Recurring quiet hours</option>
                <option value="oneTime">One-time maintenance</option>
              </select>
            </label>
            <label>
              Applies to
              <select
                value={targetKind}
                onChange={(event) => {
                  const value = event.target.value as TargetKind;
                  setTargetKind(value);
                  if (value === "fleet") setTargetId("");
                }}
              >
                <option value="fleet">Entire fleet</option>
                <option value="site">A site</option>
                <option value="device">A device</option>
              </select>
            </label>
          </div>
          {targetKind !== "fleet" && (
            <label>
              {targetKind === "site" ? "Site" : "Device"}
              <select required value={targetId} onChange={(event) => setTargetId(event.target.value)}>
                <option value="">Choose a {targetKind}</option>
                {(targetKind === "site" ? sites : devices).map((item) => (
                  <option key={item.id} value={item.id}>
                    {targetKind === "site" ? (item as Site).name : (item as Device).displayName}
                  </option>
                ))}
              </select>
            </label>
          )}
          {windowMode === "recurring" ? (
            <>
              <label>
                Timezone <span className="label-hint">IANA wall-clock rules</span>
                <Input
                  required
                  placeholder="Europe/London"
                  value={timezone}
                  onChange={(event) => setTimezone(event.target.value)}
                />
              </label>
              <fieldset className="weekday-fieldset">
                <legend>Days</legend>
                <div className="weekday-picker">
                  {weekdayOptions.map((option) => (
                    <label key={option.value} className="weekday-option">
                      <input
                        type="checkbox"
                        aria-label={`Weekday ${option.label}`}
                        checked={weekdays.includes(option.value)}
                        onChange={() =>
                          setWeekdays((current) =>
                            current.includes(option.value)
                              ? current.filter((day) => day !== option.value)
                              : [...current, option.value].sort(),
                          )
                        }
                      />
                      <span>{option.label}</span>
                    </label>
                  ))}
                </div>
              </fieldset>
              <div className="notification-form-grid">
                <label>
                  Start local time
                  <Input
                    required
                    type="time"
                    value={startLocal}
                    onChange={(event) => setStartLocal(event.target.value)}
                  />
                </label>
                <label>
                  End local time
                  <Input required type="time" value={endLocal} onChange={(event) => setEndLocal(event.target.value)} />
                </label>
              </div>
              <p className="form-help">
                Overnight windows such as 22:00–06:00 are supported. DST follows the selected timezone.
              </p>
            </>
          ) : (
            <div className="notification-form-grid">
              <label>
                Starts at
                <Input
                  required
                  type="datetime-local"
                  value={startsAt}
                  onChange={(event) => setStartsAt(event.target.value)}
                />
              </label>
              <label>
                Ends at
                <Input
                  required
                  type="datetime-local"
                  value={endsAt}
                  onChange={(event) => setEndsAt(event.target.value)}
                />
              </label>
            </div>
          )}
          <Button type="submit" disabled={busy !== "" || (windowMode === "recurring" && weekdays.length === 0)}>
            <Plus size={15} />
            Save quiet window
          </Button>
        </form>
        <section className="data-panel" aria-labelledby="quiet-windows-title">
          <div className="section-heading">
            <div>
              <h3 id="quiet-windows-title">Quiet windows</h3>
            </div>
            <Badge variant="outline">{windows.length}</Badge>
          </div>
          {windows.length ? (
            <div className="quiet-window-list">
              {windows.map((windowItem) => (
                <article className="quiet-window" data-testid={`quiet-window-${windowItem.id}`} key={windowItem.id}>
                  <div>
                    <div className="notification-destination-heading">
                      <strong>{windowItem.name}</strong>
                      <Badge variant="outline" className={windowItem.enabled ? "enabled-label" : "access-label"}>
                        {windowItem.enabled ? "Active" : "Paused"}
                      </Badge>
                    </div>
                    <span>{scheduleLabel(windowItem)}</span>
                    <small>
                      {targetLabel(windowItem, sites, devices)} · revision {windowItem.revision}
                    </small>
                  </div>
                  <div className="notification-actions">
                    <Button size="sm" variant="outline" onClick={() => toggleWindow(windowItem)} disabled={busy !== ""}>
                      {windowItem.enabled ? "Pause" : "Enable"}
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove ${windowItem.name}`}
                      onClick={() => retireWindow(windowItem)}
                      disabled={busy !== ""}
                    >
                      <Trash2 size={14} />
                    </Button>
                  </div>
                </article>
              ))}
            </div>
          ) : (
            <p className="empty-inline">
              No quiet windows configured. Alerts will be delivered whenever a destination is enabled.
            </p>
          )}
        </section>
      </div>
      <section className="data-panel delivery-history" aria-labelledby="delivery-history-title">
        <div className="section-heading">
          <div>
            <h3 id="delivery-history-title">Delivery status</h3>
          </div>
          <div className="delivery-filter">
            <label>
              <span className="sr-only">Delivery status</span>
              <select value={deliveryFilter} onChange={(event) => setDeliveryFilter(event.target.value)}>
                <option value="">All statuses</option>
                <option value="queued">Queued</option>
                <option value="sending">Sending</option>
                <option value="retry">Retrying</option>
                <option value="accepted">Accepted</option>
                <option value="failed">Failed</option>
                <option value="cancelled">Cancelled</option>
                <option value="suppressed">Suppressed</option>
                <option value="expired">Expired</option>
              </select>
            </label>
          </div>
        </div>
        {deliveries.length ? (
          <div className="delivery-list">
            {deliveries.map((delivery) => (
              <div className="delivery-row" data-testid={`delivery-${delivery.id}`} key={delivery.id}>
                <Badge variant="outline" className={`delivery-status ${delivery.status}`}>
                  {titleCase(delivery.status)}
                </Badge>
                <div>
                  <strong>{destinationByID.get(delivery.destinationId)?.name ?? "Removed destination"}</strong>
                  <span>{deliveryDescription(delivery)}</span>
                </div>
                <small>
                  {delivery.attempts} attempt{delivery.attempts === 1 ? "" : "s"} ·{" "}
                  {formatDate(delivery.acceptedAt ?? delivery.nextAttemptAt ?? delivery.expiresAt)}
                </small>
              </div>
            ))}
          </div>
        ) : (
          <p className="empty-inline">No delivery records match this filter.</p>
        )}
      </section>
    </section>
  );
}
