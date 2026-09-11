import assert from "node:assert/strict";
import test from "node:test";

const baseURL = process.env.SCOUT_E2E_URL ?? "";
const setupToken = process.env.SCOUT_E2E_SETUP_TOKEN ?? "";
const password = process.env.SCOUT_E2E_PASSWORD ?? "correct horse battery staple";

async function session() {
  if (setupToken) {
    const setup = await fetch(`${baseURL}/api/v1/setup`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ setupToken, password }),
    });
    assert.ok([201, 409].includes(setup.status));
  }
  const signIn = await fetch(`${baseURL}/api/v1/sessions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ password }),
  });
  assert.equal(signIn.status, 200);
  const cookies = signIn.headers
    .getSetCookie()
    .map((value) => value.split(";", 1)[0])
    .join("; ");
  return cookies;
}

test(
  "authenticated monitoring exposes real inventory boundaries and truthful empty/error states",
  { skip: !baseURL },
  async () => {
    const cookies = await session();
    const inventory = await fetch(`${baseURL}/api/v1/devices?limit=2`, { headers: { cookie: cookies } });
    assert.equal(inventory.status, 200);
    const inventoryBody = await inventory.json();
    assert.ok(Array.isArray(inventoryBody.items));
    assert.ok(inventoryBody.items.length <= 2);

    const noData = await fetch(`${baseURL}/api/v1/devices?query=this-device-does-not-exist`, {
      headers: { cookie: cookies },
    });
    assert.equal(noData.status, 200);
    assert.deepEqual((await noData.json()).items, []);

    const missingMetrics = await fetch(`${baseURL}/api/v1/devices/missing-device/metrics`, {
      headers: { cookie: cookies },
    });
    assert.equal(missingMetrics.status, 404);

    const topology = await fetch(`${baseURL}/api/v1/topology`, { headers: { cookie: cookies } });
    assert.equal(topology.status, 200);
    const topologyBody = await topology.json();
    assert.ok(Array.isArray(topologyBody.nodes));
    assert.ok(Array.isArray(topologyBody.relationships));
  },
);
