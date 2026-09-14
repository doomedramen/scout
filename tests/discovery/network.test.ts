import net from "node:net";

import { afterAll, describe, expect, it } from "vitest";

import {
  boundedCidr,
  hostAddresses,
  parseDarwinDefaultRoute,
  parseLinuxDefaultRoute,
  parseLinuxProcRoute,
  configuredDiscoveryBoundary,
  configuredSshPort,
} from "@/lib/discovery/network";
import { scanHosts } from "@/lib/discovery/scanner";

describe("default-route discovery", () => {
  afterAll(() => {
    delete process.env.SCOUT_DISCOVERY_CIDR;
    delete process.env.SCOUT_DISCOVERY_SOURCE_ADDRESS;
    delete process.env.SCOUT_DISCOVERY_INTERFACE;
    delete process.env.SCOUT_SSH_PORT;
  });

  it("parses a Linux private default route", () => {
    expect(
      parseLinuxDefaultRoute(
        "default via 192.168.1.1 dev eth0 proto dhcp src 192.168.1.42 metric 100",
      ),
    ).toEqual({ interfaceName: "eth0", gateway: "192.168.1.1", sourceAddress: "192.168.1.42" });
  });

  it("parses a macOS default route", () => {
    expect(
      parseDarwinDefaultRoute(
        "   route to: default\n destination: default\n       gateway: 192.168.1.1\n     interface: en0\n",
      ),
    ).toEqual({ interfaceName: "en0", gateway: "192.168.1.1", sourceAddress: null });
  });

  it("parses Linux proc routes when iproute2 is not installed", () => {
    expect(
      parseLinuxProcRoute(
        "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n" +
          "eth0 00000000 0101A8C0 0003 0 0 100 00000000 0 0 0 0\n",
      ),
    ).toEqual({ interfaceName: "eth0", gateway: "192.168.1.1", sourceAddress: null });
  });

  it("caps broad networks at their containing /24 and preserves narrower prefixes", () => {
    expect(boundedCidr("10.20.30.40", "255.255.0.0")).toBe("10.20.30.0/24");
    expect(boundedCidr("10.20.30.40", "255.255.255.192")).toBe("10.20.30.0/26");
    expect(boundedCidr("192.0.2.10", "255.255.255.255")).toBe("192.0.2.10/32");
  });

  it("returns usable host addresses without exceeding the bounded segment", () => {
    expect(hostAddresses("192.0.2.0/30")).toEqual(["192.0.2.1", "192.0.2.2"]);
    expect(hostAddresses("192.0.2.10/32")).toEqual(["192.0.2.10"]);
    expect(hostAddresses("10.0.0.0/16")).toHaveLength(256);
  });

  it("accepts an explicit bounded test or operator CIDR and SSH port", () => {
    process.env.SCOUT_DISCOVERY_CIDR = "127.0.0.1/32";
    process.env.SCOUT_DISCOVERY_SOURCE_ADDRESS = "127.0.0.1";
    process.env.SCOUT_DISCOVERY_INTERFACE = "test-loopback";
    process.env.SCOUT_SSH_PORT = "2222";

    expect(configuredDiscoveryBoundary()).toEqual({
      cidr: "127.0.0.1/32",
      interfaceName: "test-loopback",
      gateway: null,
      sourceAddress: "127.0.0.1",
      provenanceKey: "configured|test-loopback|127.0.0.1/32",
    });
    expect(configuredSshPort()).toBe(2222);
  });
});

describe("bounded TCP scanner", () => {
  let server: net.Server | undefined;

  afterAll(async () => {
    if (server) await new Promise<void>((resolve) => server?.close(() => resolve()));
  });

  it("detects an open endpoint and reports a closed endpoint", async () => {
    server = net.createServer((socket) => socket.end());
    await new Promise<void>((resolve) => server?.listen(0, "127.0.0.1", () => resolve()));
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("test server did not bind");

    const open = await scanHosts(["127.0.0.1"], address.port, { timeoutMs: 100 });
    expect(open).toEqual([{ address: "127.0.0.1", port: address.port, outcome: "open" }]);

    await new Promise<void>((resolve) => server?.close(() => resolve()));
    server = undefined;
    const closed = await scanHosts(["127.0.0.1"], address.port, { timeoutMs: 100 });
    expect(closed).toEqual([{ address: "127.0.0.1", port: address.port, outcome: "closed" }]);
  });
});
