import { afterEach, describe, expect, it } from "vitest";

import {
  MAX_JSON_BODY_BYTES,
  rateLimit,
  readRequestBody,
  resetRateLimits,
  sameOrigin,
} from "@/lib/server/http";

describe("same-origin request validation", () => {
  afterEach(() => {
    delete process.env.SCOUT_PUBLIC_URL;
    resetRateLimits();
  });

  it("accepts a browser origin that matches the host header behind local URL normalization", () => {
    process.env.SCOUT_PUBLIC_URL = "http://192.168.1.14:18082";
    const request = new Request("http://localhost:18082/api/v1/systems", {
      headers: {
        host: "127.0.0.1:18082",
        origin: "http://127.0.0.1:18082",
      },
    });

    expect(sameOrigin(request)).toBe(true);
  });

  it("rejects an unrelated browser origin", () => {
    process.env.SCOUT_PUBLIC_URL = "http://192.168.1.14:18082";
    const request = new Request("http://localhost:18082/api/v1/systems", {
      headers: {
        host: "127.0.0.1:18082",
        origin: "https://attacker.example",
      },
    });

    expect(sameOrigin(request)).toBe(false);
  });

  it("rejects declared and actual JSON bodies over the limit", async () => {
    const declared = new Request("http://localhost/api", {
      headers: { "content-length": String(MAX_JSON_BODY_BYTES + 1) },
    });
    const declaredResult = await readRequestBody(declared);
    expect(declaredResult).toBeInstanceOf(Response);
    expect((declaredResult as Response).status).toBe(413);

    const actual = new Request("http://localhost/api", {
      method: "POST",
      body: "x".repeat(MAX_JSON_BODY_BYTES + 1),
    });
    const actualResult = await readRequestBody(actual);
    expect(actualResult).toBeInstanceOf(Response);
    expect((actualResult as Response).status).toBe(413);
  });

  it("returns a retry hint after a bounded request burst", () => {
    const request = new Request("http://localhost/api", {
      headers: { "x-forwarded-for": "192.0.2.10" },
    });
    expect(rateLimit(request, "test", 2, 60_000, 1_000)).toBeNull();
    expect(rateLimit(request, "test", 2, 60_000, 1_001)).toBeNull();
    const limited = rateLimit(request, "test", 2, 60_000, 1_002);
    expect(limited?.status).toBe(429);
    expect(limited?.headers.get("retry-after")).toBe("60");
  });
});
