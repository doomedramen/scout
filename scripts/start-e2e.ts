import fs from "node:fs";
import net from "node:net";
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

const targetAddress = process.env.SCOUT_E2E_TARGET_ADDRESS;
const targetPort = Number(process.env.SCOUT_E2E_TARGET_PORT ?? "18022");
let target: net.Server | undefined;
if (targetAddress) {
  target = net.createServer((socket) => socket.end());
  await new Promise<void>((resolve, reject) => {
    target!.once("error", reject);
    target!.listen(targetPort, targetAddress, () => resolve());
  });
}

const server = spawn(process.execPath, [path.resolve("scripts/start-local.mjs")], {
  env: process.env,
  stdio: "inherit",
});

for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {
    target?.close();
    server.kill(signal);
  });
}

const exitCode = await new Promise<number>((resolve) => {
  server.on("exit", (code, signal) => {
    target?.close();
    resolve(code ?? (signal ? 1 : 0));
  });
});
process.exitCode = exitCode;
