import net from "node:net";

import { hostAddresses } from "@/lib/discovery/network";

export type ProbeOutcome = "open" | "closed" | "timeout";

export type ProbeResult = {
  address: string;
  port: number;
  outcome: ProbeOutcome;
  /** Optional identity evidence supplied by an agent vantage point. */
  macAddress?: string | null;
  fingerprint?: string | null;
};

export type ScanOptions = {
  timeoutMs?: number;
  concurrency?: number;
};

export function probeTcp(address: string, port: number, timeoutMs = 800): Promise<ProbeOutcome> {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host: address, port });
    let settled = false;
    const finish = (outcome: ProbeOutcome) => {
      if (settled) return;
      settled = true;
      socket.destroy();
      resolve(outcome);
    };

    socket.setTimeout(timeoutMs, () => finish("timeout"));
    socket.once("connect", () => finish("open"));
    socket.once("error", () => finish("closed"));
  });
}

export async function scanHosts(
  addresses: string[],
  port = 22,
  options: ScanOptions = {},
): Promise<ProbeResult[]> {
  if (addresses.length > 256) throw new Error("A scan may contain at most 256 addresses");
  const timeoutMs = options.timeoutMs ?? 800;
  const concurrency = Math.min(options.concurrency ?? 64, 64, Math.max(addresses.length, 1));
  const results: ProbeResult[] = [];
  let nextIndex = 0;

  async function worker() {
    while (true) {
      const index = nextIndex++;
      if (index >= addresses.length) return;
      results[index] = {
        address: addresses[index],
        port,
        outcome: await probeTcp(addresses[index], port, timeoutMs),
      };
    }
  }

  await Promise.all(Array.from({ length: concurrency }, () => worker()));
  return results;
}

export function scanSegment(
  cidr: string,
  port = 22,
  options: ScanOptions = {},
): Promise<ProbeResult[]> {
  return scanHosts(hostAddresses(cidr), port, options);
}
