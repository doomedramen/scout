import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const standaloneDirectory = path.join(root, ".next", "standalone");
const standaloneNextDirectory = path.join(standaloneDirectory, ".next");
const artifactDirectory = path.join(root, "agent-artifacts");

fs.mkdirSync(standaloneNextDirectory, { recursive: true });
fs.cpSync(path.join(root, ".next", "static"), path.join(standaloneNextDirectory, "static"), {
  recursive: true,
});
if (fs.existsSync(path.join(root, "public"))) {
  fs.cpSync(path.join(root, "public"), path.join(standaloneDirectory, "public"), {
    recursive: true,
  });
}

const server = spawn(process.execPath, [path.join(standaloneDirectory, "server.js")], {
  cwd: standaloneDirectory,
  env: {
    ...process.env,
    SCOUT_AGENT_ARTIFACT_DIR: artifactDirectory,
  },
  stdio: "inherit",
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.kill(signal));
}

server.on("exit", (code, signal) => {
  process.exitCode = code ?? (signal ? 1 : 0);
});
