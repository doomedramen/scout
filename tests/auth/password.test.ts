import { describe, expect, it } from "vitest";

import { validateOwnerPassword } from "@/lib/auth/password";

describe("owner password policy", () => {
  it("accepts an eight-character password with upper, lower, and number", () => {
    expect(validateOwnerPassword("Pa55w0rd")).toEqual({ valid: true });
  });

  it.each([
    ["too short", "Pa55w0"],
    ["missing uppercase", "pa55w0rd"],
    ["missing lowercase", "PA55W0RD"],
    ["missing number", "Password"],
  ])("rejects %s", (_reason, password) => {
    expect(validateOwnerPassword(password)).toMatchObject({ valid: false });
  });
});
