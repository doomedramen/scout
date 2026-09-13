import { afterEach, describe, expect, it } from "vitest";

import { sameOrigin } from "@/lib/server/http";

describe("same-origin request validation", () => {
  afterEach(() => {
    delete process.env.SCOUT_PUBLIC_URL;
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
});
