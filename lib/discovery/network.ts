import { execFileSync } from "node:child_process";
import os from "node:os";

export type DefaultRoute = {
  interfaceName: string;
  gateway: string | null;
  sourceAddress: string | null;
};

export type NetworkBoundary = {
  cidr: string;
  interfaceName: string;
  gateway: string | null;
  sourceAddress: string;
  provenanceKey: string;
};

export type RouteInference =
  { kind: "ready"; boundary: NetworkBoundary } | { kind: "needs-input"; reason: string };

function ipv4ToNumber(address: string): number | null {
  const parts = address.split(".");
  if (parts.length !== 4 || parts.some((part) => !/^\d+$/.test(part))) return null;
  const octets = parts.map(Number);
  if (octets.some((octet) => octet < 0 || octet > 255)) return null;
  return ((octets[0] << 24) | (octets[1] << 16) | (octets[2] << 8) | octets[3]) >>> 0;
}

function numberToIpv4(value: number): string {
  return [value >>> 24, (value >>> 16) & 255, (value >>> 8) & 255, value & 255].join(".");
}

function prefixFromNetmask(netmask: string): number | null {
  const value = ipv4ToNumber(netmask);
  if (value === null) return null;
  let prefix = 0;
  let bit = 0x80000000;
  let sawZero = false;
  for (let index = 0; index < 32; index += 1) {
    if (value & bit) {
      if (sawZero) return null;
      prefix += 1;
    } else {
      sawZero = true;
    }
    bit >>>= 1;
  }
  return prefix;
}

function prefixMask(prefix: number): number {
  if (prefix === 0) return 0;
  return (0xffffffff << (32 - prefix)) >>> 0;
}

export function isPrivateIpv4(address: string): boolean {
  const value = ipv4ToNumber(address);
  if (value === null) return false;
  return (
    (value >= ipv4ToNumber("10.0.0.0")! && value <= ipv4ToNumber("10.255.255.255")!) ||
    (value >= ipv4ToNumber("172.16.0.0")! && value <= ipv4ToNumber("172.31.255.255")!) ||
    (value >= ipv4ToNumber("192.168.0.0")! && value <= ipv4ToNumber("192.168.255.255")!)
  );
}

export function parseLinuxDefaultRoute(output: string): DefaultRoute | null {
  const line = output
    .split(/\r?\n/)
    .map((candidate) => candidate.trim())
    .find((candidate) => candidate.startsWith("default "));
  if (!line) return null;
  const interfaceName = line.match(/\bdev\s+(\S+)/)?.[1];
  if (!interfaceName) return null;
  return {
    interfaceName,
    gateway: line.match(/\bvia\s+(\S+)/)?.[1] ?? null,
    sourceAddress: line.match(/\bsrc\s+(\S+)/)?.[1] ?? null,
  };
}

export function parseDarwinDefaultRoute(output: string): DefaultRoute | null {
  const gateway = output.match(/^\s*gateway:\s*(\S+)/m)?.[1] ?? null;
  const interfaceName = output.match(/^\s*interface:\s*(\S+)/m)?.[1];
  if (!interfaceName) return null;
  return { interfaceName, gateway, sourceAddress: null };
}

export function boundedCidr(address: string, netmask: string): string {
  const value = ipv4ToNumber(address);
  const sourcePrefix = prefixFromNetmask(netmask);
  if (value === null || sourcePrefix === null) throw new Error("Invalid IPv4 address or netmask");
  const prefix = Math.max(sourcePrefix, 24);
  const network = value & prefixMask(prefix);
  return `${numberToIpv4(network)}/${prefix}`;
}

function parseCidr(cidr: string): { network: number; prefix: number } {
  const match = cidr.match(/^(\d+\.\d+\.\d+\.\d+)\/(\d|[12]\d|3[0-2])$/);
  if (!match) throw new Error("Invalid IPv4 CIDR");
  const address = ipv4ToNumber(match[1]);
  const prefix = Number(match[2]);
  if (address === null) throw new Error("Invalid IPv4 CIDR");
  return { network: address & prefixMask(prefix), prefix };
}

export function hostAddresses(cidr: string): string[] {
  const { network, prefix } = parseCidr(cidr);
  const size = 2 ** (32 - prefix);
  if (prefix === 32) return [numberToIpv4(network)];
  if (prefix === 31) return [numberToIpv4(network), numberToIpv4(network + 1)];

  const first = network + 1;
  const last = network + size - 2;
  const count = Math.min(last - first + 1, 256);
  return Array.from({ length: count }, (_, index) => numberToIpv4(first + index));
}

function interfaceLooksNonRoutable(name: string): boolean {
  return /^(lo|docker|br-|veth|virbr|tun|tap|utun|wg|tailscale)/i.test(name);
}

function localInterface(name: string): { address: string; netmask: string } | null {
  const entries = os.networkInterfaces()[name] ?? [];
  const entry = entries.find(
    (candidate) =>
      (candidate.family === "IPv4" || String(candidate.family) === "4") && !candidate.internal,
  );
  return entry ? { address: entry.address, netmask: entry.netmask } : null;
}

function routeOutput(): { route: DefaultRoute | null; routeKind: "linux" | "darwin" } {
  if (process.platform === "darwin") {
    try {
      return {
        route: parseDarwinDefaultRoute(
          execFileSync("route", ["-n", "get", "default"], { encoding: "utf8" }),
        ),
        routeKind: "darwin",
      };
    } catch {
      return { route: null, routeKind: "darwin" };
    }
  }

  try {
    return {
      route: parseLinuxDefaultRoute(
        execFileSync("ip", ["route", "show", "default"], { encoding: "utf8" }),
      ),
      routeKind: "linux",
    };
  } catch {
    return { route: null, routeKind: "linux" };
  }
}

export function inferDefaultRoute(): RouteInference {
  const { route } = routeOutput();
  if (!route) return { kind: "needs-input", reason: "Scout could not identify a default route." };
  if (interfaceLooksNonRoutable(route.interfaceName)) {
    return {
      kind: "needs-input",
      reason: "The default route uses a private, VPN, or container-only interface.",
    };
  }

  const local = localInterface(route.interfaceName);
  const sourceAddress = route.sourceAddress ?? local?.address ?? null;
  if (!sourceAddress || !local || !isPrivateIpv4(sourceAddress)) {
    return { kind: "needs-input", reason: "The default route is not a private IPv4 network." };
  }

  const cidr = boundedCidr(sourceAddress, local.netmask);
  return {
    kind: "ready",
    boundary: {
      cidr,
      interfaceName: route.interfaceName,
      gateway: route.gateway,
      sourceAddress,
      provenanceKey: [route.interfaceName, route.gateway ?? "direct", sourceAddress, cidr].join(
        "|",
      ),
    },
  };
}
