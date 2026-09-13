import type { Device, ScanRun, ScanStatus, ScanVantage } from "@/lib/api";

export function humanizeScanValue(value: string): string {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function formatScanTime(value: string | null | undefined): string {
  if (!value) return "never";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "unknown" : parsed.toLocaleString();
}

export function scanCoverageLabel(status: ScanStatus | undefined, scopeEnabled: boolean): string {
  if (scanStatusIsPaused(status, scopeEnabled)) return "Paused";
  if (!status) return "Loading";
  if (status.activeRun?.cancellationRequested) return "Cancelling";
  if (status.activeRun) return `Running · ${humanizeScanValue(status.activeRun.state)}`;
  return humanizeScanValue(status.coverageState);
}

export function scanStatusIsPaused(status: ScanStatus | undefined, scopeEnabled: boolean): boolean {
  return Boolean(
    !scopeEnabled || (status && (!status.enabled || !status.vantages.some((vantage) => vantage.assigned))),
  );
}

export function scanVantageLabel(vantage: ScanVantage, devices: Device[] = []): string {
  if (vantage.scanner.kind === "server") return "Server vantage";
  const device = devices.find((item) => item.agentId === vantage.scanner.id);
  return device ? `Agent · ${device.displayName}` : `Agent · ${vantage.scanner.id.slice(0, 12)}`;
}

export function scanVantageFreshness(vantage: ScanVantage): string {
  if (vantage.scanner.kind === "server") return "managed by Scout";
  return vantage.lastSeen ? `last seen ${formatScanTime(vantage.lastSeen)}` : "not seen yet";
}

export function scanVantageCapabilities(vantage: ScanVantage): string {
  return vantage.capabilities.length ? vantage.capabilities.join(", ") : "none reported";
}

export function scanActiveRunLabel(status: ScanStatus | undefined): string | null {
  const run = status?.activeRun;
  if (!run) return null;
  if (run.cancellationRequested) return "Cancellation requested; waiting for the scanner to acknowledge.";
  return `Active ${humanizeScanValue(run.state)} run · ${run.attemptsCompleted}/${run.attemptsPlanned} attempts`;
}

export function scanRunOutcomeSummary(run: ScanRun | null | undefined): string {
  if (!run) return "none reported";
  const counts = run.outcomeCounts;
  return [
    `open ${counts.open}`,
    `closed ${counts.closed}`,
    `filtered ${counts.filtered}`,
    `unreachable ${counts.unreachable}`,
    `skipped ${counts.skipped}`,
    `scanner errors ${counts.scannerError}`,
  ].join(" · ");
}

export function scanRunSummaryLabel(run: ScanRun | null | undefined): string {
  if (!run) return "No scan has completed yet.";
  const timestamp = run.finishedAt ?? run.scheduledAt;
  return `${humanizeScanValue(run.state)} · ${formatScanTime(timestamp)} · ${run.attemptsCompleted}/${run.attemptsPlanned} attempts`;
}

export function scanStatusNote(status: ScanStatus | undefined): string | null {
  const run = status?.lastRun;
  if (run?.errorCode) return `Error: ${humanizeScanValue(run.errorCode)}`;
  if (run?.partialReason) return `Partial: ${humanizeScanValue(run.partialReason)}`;
  if (status?.coverageState === "stale") return "Evidence is stale until a complete run succeeds.";
  if (status?.coverageState === "unknown") return "No scan evidence has been completed yet.";
  return null;
}
