import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const directory = `/tmp/scout-test-mfa-${process.pid}-${Date.now()}`;
  process.env.SCOUT_DATABASE_URL = "file::memory:";
  process.env.SCOUT_DATA_DIR = directory;
});

import { POST as setupOwner } from "@/app/api/v1/setup/owner/route";
import { POST as authPost } from "@/app/api/auth/[...all]/route";
import { closeDatabase, getDatabase } from "@/lib/server/db";
import { provisionSetupToken } from "@/lib/server/setup";

describe("optional authenticator MFA", () => {
  afterEach(() => {
    delete process.env.SCOUT_DATABASE_URL;
    delete process.env.SCOUT_DATA_DIR;
    closeDatabase();
  });

  it("can be enabled from an authenticated Settings session without enabling it by default", async () => {
    const setupToken = provisionSetupToken(Date.now()).token;
    const setupResponse = await setupOwner(
      new Request("http://localhost/api/v1/setup/owner", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          setupToken,
          username: "owner",
          password: "ValidPass1",
        }),
      }),
    );
    expect(setupResponse.status).toBe(201);

    const signInResponse = await authPost(
      new Request("http://localhost/api/auth/sign-in/username", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ username: "owner", password: "ValidPass1" }),
      }),
    );
    expect(signInResponse.status).toBe(200);
    const cookie = signInResponse.headers.get("set-cookie");
    expect(cookie).toBeTruthy();

    const enableResponse = await authPost(
      new Request("http://localhost/api/auth/two-factor/enable", {
        method: "POST",
        headers: {
          cookie: cookie!.split(";", 1)[0],
          "content-type": "application/json",
        },
        body: JSON.stringify({ method: "totp", password: "ValidPass1" }),
      }),
    );
    expect(enableResponse.status).toBe(200);
    expect(await enableResponse.json()).toMatchObject({
      method: "totp",
      totpURI: expect.stringContaining("otpauth://"),
      backupCodes: expect.arrayContaining([expect.any(String)]),
    });
    expect(
      (
        getDatabase()
          .sqlite.prepare("SELECT two_factor_enabled AS enabled FROM user LIMIT 1")
          .get() as { enabled: number }
      ).enabled,
    ).toBe(0);
  });
});
