import { afterEach, describe, expect, it } from "vitest";

import { closeDatabase } from "@/lib/server/db";
import { consumeSetupToken, provisionSetupToken, setupStatus } from "@/lib/server/setup";

describe("owner setup token", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    closeDatabase();
  });

  it("is single-use and expires", () => {
    process.env.SCOUT_DATABASE_URL = "file::memory:";
    expect(setupStatus()).toEqual({ required: true, tokenExpiresAt: null });
    const provisioned = provisionSetupToken(1_000);
    expect(consumeSetupToken(provisioned.token, 1_001)).toBe(true);
    expect(consumeSetupToken(provisioned.token, 1_002)).toBe(false);
    expect(consumeSetupToken("not-the-token", 1_003)).toBe(false);
  });
});
