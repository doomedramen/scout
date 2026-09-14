import { afterEach, describe, expect, it } from "vitest";

import { decryptCredential, encryptCredential } from "@/lib/server/credentials";

describe("credential encryption", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATA_DIR;
  });

  it("round-trips the secret and binds it to its system context", () => {
    process.env.SCOUT_DATA_DIR = "/tmp/scout-test-credentials";
    const encrypted = encryptCredential(
      {
        authType: "password",
        secret: "correct horse",
        passphrase: null,
        privilegePassword: null,
      },
      { systemId: "system-1", method: "ssh", username: "root" },
    );

    expect(encrypted.ciphertext).not.toContain("correct horse");
    expect(
      decryptCredential(encrypted, { systemId: "system-1", method: "ssh", username: "root" }),
    ).toEqual({
      authType: "password",
      secret: "correct horse",
      passphrase: null,
      privilegePassword: null,
    });
    expect(() =>
      decryptCredential(encrypted, { systemId: "system-2", method: "ssh", username: "root" }),
    ).toThrow();
  });
});
