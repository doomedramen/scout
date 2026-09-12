import { useEffect, useMemo, useState } from "react";
import { Bell, CheckCircle2, CircleAlert, Clock3, LoaderCircle, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api, type NotificationDelivery, type NotificationDestination } from "@/lib/api";

type NotificationMenuProps = {
  active?: boolean;
  onViewAll: () => void;
};

type LoadState = "loading" | "ready" | "error";

const attentionStatuses = new Set<NotificationDelivery["status"]>(["queued", "retry", "failed"]);

function statusLabel(status: NotificationDelivery["status"]): string {
  return status.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function statusIcon(status: NotificationDelivery["status"]) {
  if (status === "accepted") return CheckCircle2;
  if (status === "failed" || status === "expired" || status === "cancelled") return CircleAlert;
  if (status === "sending") return LoaderCircle;
  return Clock3;
}

function statusTone(status: NotificationDelivery["status"]): string {
  if (status === "accepted") return "accepted";
  if (status === "failed" || status === "expired" || status === "cancelled") return "failed";
  if (status === "queued" || status === "retry" || status === "sending") return "pending";
  return "muted";
}

function deliveryTime(delivery: NotificationDelivery): string {
  const value = delivery.acceptedAt ?? delivery.nextAttemptAt ?? delivery.expiresAt;
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "Time unavailable";
  return date.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

function destinationName(destination: NotificationDestination | undefined, delivery: NotificationDelivery): string {
  return destination?.name ?? (delivery.destinationId ? "Removed destination" : "Unknown destination");
}

export function NotificationMenu({ active = false, onViewAll }: NotificationMenuProps) {
  const [open, setOpen] = useState(false);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [deliveries, setDeliveries] = useState<NotificationDelivery[]>([]);
  const [destinations, setDestinations] = useState<NotificationDestination[]>([]);
  const [error, setError] = useState("");

  async function refresh() {
    setLoadState("loading");
    setError("");
    const [deliveryResult, destinationResult] = await Promise.allSettled([
      api.notificationDeliveries("?limit=5"),
      api.notificationDestinations(),
    ]);

    if (deliveryResult.status === "fulfilled") {
      setDeliveries(deliveryResult.value.items);
    }
    if (destinationResult.status === "fulfilled") {
      setDestinations(destinationResult.value.items);
    }

    const deliveryFailed = deliveryResult.status === "rejected";
    const destinationFailed = destinationResult.status === "rejected";
    if (deliveryFailed && destinationFailed) {
      setError("Notification activity is unavailable.");
      setLoadState("error");
    } else {
      setLoadState("ready");
      if (deliveryFailed || destinationFailed) {
        setError("Some notification details are unavailable.");
      }
    }
  }

  useEffect(() => {
    refresh().catch(() => {
      setError("Notification activity is unavailable.");
      setLoadState("error");
    });
  }, []);

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (nextOpen) refresh().catch(() => undefined);
  }

  const destinationById = useMemo(
    () => new Map(destinations.map((destination) => [destination.id, destination])),
    [destinations],
  );
  const attentionCount = deliveries.filter((delivery) => attentionStatuses.has(delivery.status)).length;

  return (
    <DropdownMenu open={open} onOpenChange={handleOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Notifications"
          aria-current={active ? "page" : undefined}
          className={`topbar-notifications ${active ? "topbar-action-active" : ""}`}
        >
          <span className="notification-trigger-icon" aria-hidden="true">
            <Bell size={18} />
            {attentionCount > 0 && <span className="notification-trigger-dot" />}
          </span>
          <span className="notification-trigger-label">Notifications</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={8} className="notification-menu" aria-label="Notification activity">
        <DropdownMenuLabel className="notification-menu-heading">
          <span>
            <strong>Notification activity</strong>
            <small>Recent delivery status</small>
          </span>
          {attentionCount > 0 && <Badge variant="outline">{attentionCount} needs review</Badge>}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {loadState === "loading" && (
          <div className="notification-menu-state" role="status">
            <LoaderCircle size={16} className="spin" />
            Loading activity…
          </div>
        )}
        {loadState === "error" && (
          <div className="notification-menu-state notification-menu-error" role="alert">
            <CircleAlert size={16} />
            <span>{error}</span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Retry notification activity"
              title="Retry"
              onClick={() => refresh().catch(() => undefined)}
            >
              <RefreshCw size={14} />
            </Button>
          </div>
        )}
        {loadState !== "loading" && loadState !== "error" && deliveries.length === 0 && (
          <div className="notification-menu-state">No recent notification deliveries.</div>
        )}
        {loadState !== "loading" && loadState !== "error" && deliveries.length > 0 && (
          <div className="notification-menu-list">
            {deliveries.map((delivery) => {
              const Icon = statusIcon(delivery.status);
              return (
                <DropdownMenuItem key={delivery.id} className="notification-menu-item" onSelect={onViewAll}>
                  <Icon size={16} className={`notification-menu-status ${statusTone(delivery.status)}`} />
                  <span className="notification-menu-copy">
                    <strong>{destinationName(destinationById.get(delivery.destinationId), delivery)}</strong>
                    <small>
                      {statusLabel(delivery.status)} · {deliveryTime(delivery)}
                    </small>
                  </span>
                </DropdownMenuItem>
              );
            })}
          </div>
        )}
        {error && loadState === "ready" && <p className="notification-menu-partial">{error}</p>}
        <DropdownMenuSeparator />
        <DropdownMenuItem className="notification-menu-view-all" onSelect={onViewAll}>
          View all notifications
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
