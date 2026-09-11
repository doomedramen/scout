import assert from "node:assert/strict";
import test from "node:test";

const baseURL = process.env.SCOUT_E2E_URL ?? "";
const setupToken = process.env.SCOUT_E2E_SETUP_TOKEN ?? "";
const password = process.env.SCOUT_E2E_PASSWORD ?? "ScoutAa1";

async function ownerSession() {
  if (setupToken) {
    const setup = await fetch(`${baseURL}/api/v1/setup`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ setupToken, password }),
    });
    assert.ok([201, 409].includes(setup.status), `unexpected setup status ${setup.status}`);
  }
  const signIn = await fetch(`${baseURL}/api/v1/sessions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ password }),
  });
  assert.equal(signIn.status, 200);
  const body = await signIn.json();
  const cookies = signIn.headers
    .getSetCookie()
    .map((value) => value.split(";", 1)[0])
    .join("; ");
  assert.ok(cookies.includes("scout_session="));
  assert.ok(body.csrfToken);
  return { cookies, csrfToken: body.csrfToken };
}

test("owner access is write-only, CSRF protected, and separate from worker access", { skip: !baseURL }, async () => {
  const session = await ownerSession();
  const worker = await fetch(`${baseURL}/api/v1/worker/v1/claim`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: "{}",
  });
  assert.equal(worker.status, 401);

  const credentials = await fetch(`${baseURL}/api/v1/credentials`, {
    headers: { cookie: session.cookies },
  });
  assert.equal(credentials.status, 200);
  const credentialBody = await credentials.text();
  assert.doesNotMatch(credentialBody, /ciphertext|wrappedDataKey|nonce|private-secret/i);

  const withoutCSRF = await fetch(`${baseURL}/api/v1/sites`, {
    method: "POST",
    headers: { "content-type": "application/json", cookie: session.cookies },
    body: JSON.stringify({ name: `csrf-rejection-${Date.now()}` }),
  });
  assert.equal(withoutCSRF.status, 403);

  const sensitiveWithoutMFA = await fetch(`${baseURL}/api/v1/credentials`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      cookie: session.cookies,
      "x-csrf-token": session.csrfToken,
    },
    body: JSON.stringify({
      kind: "ssh",
      secret: "never-returned",
      allowedUse: ["enrollment"],
      targets: ["192.0.2.10:22"],
    }),
  });
  const status = await fetch(`${baseURL}/api/status`);
  const statusBody = await status.json();
  if (statusBody.mode === "development") {
    assert.equal(sensitiveWithoutMFA.status, 201);
  } else {
    assert.equal(sensitiveWithoutMFA.status, 403);
    assert.doesNotMatch(await sensitiveWithoutMFA.text(), /never-returned/);
  }
});
