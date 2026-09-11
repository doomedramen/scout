const baseURL = process.env.SCOUT_E2E_URL || "http://127.0.0.1:8080";

const response = await fetch(`${baseURL}/api/status`, {
  headers: { Accept: "application/json" },
});
if (!response.ok) {
  throw new Error(`Scout API returned HTTP ${response.status}`);
}
const status = await response.json();
if (status.mode !== "development" && status.mode !== "production") {
  throw new Error("Scout API returned an unknown mode");
}
if (typeof status.database !== "string") {
  throw new Error("Scout API status omitted database state");
}

const unauthenticated = await fetch(`${baseURL}/api/v1/devices`, {
  headers: { Accept: "application/json" },
});
if (![401, 404].includes(unauthenticated.status)) {
  throw new Error(`unauthenticated inventory request returned HTTP ${unauthenticated.status}`);
}
console.log(JSON.stringify({ baseURL, status: "passed", authenticatedInventoryRequired: true }));
