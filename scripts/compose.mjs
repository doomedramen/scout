import { spawnSync } from "node:child_process";

const plugin = spawnSync("docker", ["compose", "version"], { stdio: "ignore" });
const command = plugin.status === 0 ? "docker" : "docker-compose";
const args = plugin.status === 0 ? ["compose", ...process.argv.slice(2)] : process.argv.slice(2);
const result = spawnSync(command, args, { stdio: "inherit" });
if (result.error) console.error("Docker Compose is required. Install the Compose plugin or docker-compose.");
process.exitCode = result.status ?? 1;
