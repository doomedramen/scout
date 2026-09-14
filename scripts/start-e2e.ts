import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";

import { closeDatabase } from "@/lib/server/db";
import { authSecret, controlSigningKey, credentialKey } from "@/lib/server/keys";
import { provisionSetupToken } from "@/lib/server/setup";

const dataDirectory = process.env.SCOUT_DATA_DIR ?? "/tmp/scout-playwright-data";
fs.rmSync(dataDirectory, { recursive: true, force: true });
authSecret();
credentialKey();
controlSigningKey();
const token = provisionSetupToken();
fs.writeFileSync(path.join(dataDirectory, "setup-token"), token.token, { mode: 0o600 });
closeDatabase();

const server = spawn(process.execPath, [path.resolve("scripts/start-local.mjs")], {
  env: process.env,
  stdio: "inherit",
});

for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => server.kill(signal));
}

const exitCode = await new Promise<number>((resolve) => {
  server.on("exit", (code, signal) => resolve(code ?? (signal ? 1 : 0)));
});
process.exitCode = exitCode;
