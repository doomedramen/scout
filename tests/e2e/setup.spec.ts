import assert from "node:assert/strict";
import test from "node:test";

const baseURL = process.env.SCOUT_E2E_URL ?? "";
const setupToken = process.env.SCOUT_E2E_SETUP_TOKEN ?? "";

test(
  "owner setup, session cookies, and CSRF protect inventory mutations",
  { skip: !baseURL || !setupToken },
  async () => {
    const password = "ScoutAa1";
    const setup = await fetch(`${baseURL}/api/v1/setup`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ setupToken, password }),
    });
    assert.equal(setup.status, 201);

    const secondSetup = await fetch(`${baseURL}/api/v1/setup`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ setupToken, password }),
    });
    assert.equal(secondSetup.status, 409);

    const signIn = await fetch(`${baseURL}/api/v1/sessions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ password }),
    });
    assert.equal(signIn.status, 200);
    const signInBody = await signIn.json();
    const cookies = signIn.headers
      .getSetCookie()
      .map((value) => value.split(";", 1)[0])
      .join("; ");
    assert.ok(cookies.includes("scout_session="));
    assert.ok(signInBody.csrfToken);

    const withoutCSRF = await fetch(`${baseURL}/api/v1/sites`, {
      method: "POST",
      headers: { "content-type": "application/json", cookie: cookies },
      body: JSON.stringify({ name: "should-fail" }),
    });
    assert.equal(withoutCSRF.status, 403);

    const withCSRF = await fetch(`${baseURL}/api/v1/sites`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        cookie: cookies,
        "x-csrf-token": signInBody.csrfToken,
      },
      body: JSON.stringify({ name: "e2e-lab" }),
    });
    assert.equal(withCSRF.status, 201);

    const inventory = await fetch(`${baseURL}/api/v1/devices`);
    assert.equal(inventory.status, 401);
  },
);
