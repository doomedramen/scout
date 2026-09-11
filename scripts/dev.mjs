import { existsSync } from "node:fs";
import { loadEnvFile } from "node:process";
import { spawn } from "node:child_process";

const env = new URL("../.env", import.meta.url);
if (existsSync(env)) loadEnvFile(env);
const child = spawn("turbo", ["run", "dev", "--filter=@scout/web", "--filter=@scout/server"], { stdio: "inherit" });
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
child.on("error", (error) => {
  console.error(error.message);
  process.exitCode = 1;
});
child.on("exit", (code) => {
  process.exitCode = code ?? 1;
});
