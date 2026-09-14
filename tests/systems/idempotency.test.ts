import { afterEach, describe, expect, it, vi } from "vitest";

import { createIdempotencyKey } from "@/lib/client/idempotency";

describe("browser idempotency keys", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses the native UUID implementation when available", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "native-uuid" });

    expect(createIdempotencyKey()).toBe("native-uuid");
  });

  it("works in insecure contexts where randomUUID is unavailable", () => {
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.set(Uint8Array.from({ length: 16 }, (_, index) => index));
        return bytes;
      },
    });

    expect(createIdempotencyKey()).toBe("00010203-0405-4607-8809-0a0b0c0d0e0f");
  });
});
