import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { execFileSync, spawn } from "node:child_process";

import { renderInstallerScript } from "@/lib/server/installer";

const launchdLabel = "page.rtin.scout-agent";
const launchdPlist = "/Library/LaunchDaemons/page.rtin.scout-agent.plist";
const agentRoot = "/Library/Application Support/ScoutAgent";
const launcher = "/usr/local/lib/scout-agent/scout-agent-launcher";

type Counts = {
  enrollments: number;
  heartbeats: number;
  telemetry: number;
};

function runPrivileged(...args: string[]): void {
  execFileSync("sudo", ["-n", ...args], { stdio: "inherit" });
}

function runQuietly(...args: string[]): boolean {
  try {
    execFileSync("sudo", ["-n", ...args], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

function responseJson(response: http.ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  response.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(payload),
    connection: "close",
  });
  response.end(payload);
}

async function waitFor(label: string, predicate: () => boolean): Promise<void> {
  const deadline = Date.now() + Number(process.env.SCOUT_MACOS_LIFECYCLE_TIMEOUT_MS ?? 90_000);
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`${label} did not complete before the acceptance timeout`);
}

function assertAbsent(target: string): void {
  if (fs.existsSync(target)) {
    throw new Error(`refusing to use a non-disposable existing path: ${target}`);
  }
}

function runInstaller(installerPath: string, environment: NodeJS.ProcessEnv): Promise<void> {
  return new Promise((resolve, reject) => {
    const timeoutMs = Number(process.env.SCOUT_MACOS_INSTALL_TIMEOUT_MS ?? 120_000);
    const child = spawn("sh", ["-x", installerPath], {
      env: environment,
      stdio: "inherit",
    });
    let timedOut = false;
    const timeout = setTimeout(() => {
      timedOut = true;
      child.kill("SIGTERM");
      setTimeout(() => child.kill("SIGKILL"), 5_000).unref();
    }, timeoutMs);
    child.once("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once("close", (code, signal) => {
      clearTimeout(timeout);
      if (timedOut) {
        reject(new Error(`installer exceeded the ${timeoutMs}ms timeout`));
      } else if (code === 0) {
        resolve();
      } else {
        reject(new Error(`installer exited with ${code ?? `signal ${signal}`}`));
      }
    });
  });
}

async function main(): Promise<void> {
  if (process.platform !== "darwin") {
    throw new Error("the macOS launchd acceptance harness must run on macOS");
  }

  const binaryPath = process.env.SCOUT_MACOS_AGENT_BINARY;
  if (!binaryPath || !fs.statSync(binaryPath, { throwIfNoEntry: false })?.isFile()) {
    throw new Error("SCOUT_MACOS_AGENT_BINARY must point to the built native agent");
  }
  if (!runQuietly("true")) {
    throw new Error("passwordless sudo is required by the disposable macOS acceptance harness");
  }

  assertAbsent(launchdPlist);
  assertAbsent(agentRoot);
  assertAbsent(launcher);
  if (runQuietly("launchctl", "print", `system/${launchdLabel}`)) {
    throw new Error(`launchd service ${launchdLabel} already exists`);
  }

  const workDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "scout-macos-launchd-"));
  const installerPath = path.join(workDirectory, "install.sh");
  const counts: Counts = { enrollments: 0, heartbeats: 0, telemetry: 0 };
  const agentArtifact = fs.readFileSync(binaryPath);
  const server = http.createServer((request, response) => {
    const requestUrl = new URL(request.url ?? "/", "http://127.0.0.1");
    request.on("data", () => undefined);
    request.on("end", () => {
      switch (requestUrl.pathname) {
        case "/api/v1/bootstrap/agent/preflight":
          responseJson(response, 200, { ok: true });
          return;
        case "/api/v1/bootstrap/agent/artifact":
          response.writeHead(200, {
            "content-type": "application/octet-stream",
            "content-length": agentArtifact.length,
            connection: "close",
          });
          response.end(agentArtifact);
          return;
        case "/api/agent/v1/enroll":
          counts.enrollments += 1;
          responseJson(response, 200, {
            agentId: "macos-acceptance-agent",
            systemId: "macos-acceptance-system",
            serverTime: new Date().toISOString(),
            heartbeatIntervalSeconds: 15,
            telemetryIntervalSeconds: 30,
            controlPublicKey: "",
          });
          return;
        case "/api/agent/v1/heartbeat":
          counts.heartbeats += 1;
          responseJson(response, 200, {
            ok: true,
            serverTime: new Date().toISOString(),
            controlPublicKey: "",
            task: null,
          });
          return;
        case "/api/agent/v1/telemetry":
          counts.telemetry += 1;
          responseJson(response, 200, {});
          return;
        default:
          responseJson(response, 404, { error: `unexpected path ${requestUrl.pathname}` });
      }
    });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });

  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("could not determine the local acceptance server port");
  }
  const serverUrl = `http://127.0.0.1:${address.port}`;
  const installer = renderInstallerScript({
    serverUrl,
    callbackUrl: serverUrl,
    invitation: "macos-acceptance-invitation",
    agentVersion: `acceptance-${process.pid}`,
  });
  fs.writeFileSync(installerPath, installer, { mode: 0o700 });

  let cleanupRequired = true;
  try {
    await runInstaller(installerPath, {
      ...process.env,
      SCOUT_SERVER_URL: serverUrl,
      SCOUT_CALLBACK_URL: serverUrl,
      SCOUT_OTI: "macos-acceptance-invitation",
      SCOUT_AGENT_VERSION: `acceptance-${process.pid}`,
    });

    if (counts.enrollments !== 1) {
      throw new Error(`expected one enrollment during installation, got ${counts.enrollments}`);
    }
    await waitFor("initial launchd heartbeat", () => counts.heartbeats >= 2);
    await waitFor("initial launchd telemetry", () => counts.telemetry >= 2);
    if (!runQuietly("launchctl", "print", `system/${launchdLabel}`)) {
      throw new Error("launchd did not keep the installed agent running");
    }

    const heartbeatBeforeRestart = counts.heartbeats;
    runPrivileged("launchctl", "kickstart", "-k", `system/${launchdLabel}`);
    await waitFor("launchd restart heartbeat", () => counts.heartbeats > heartbeatBeforeRestart);

    runPrivileged("launchctl", "bootout", "system", launchdPlist);
    await waitFor(
      "launchd service removal",
      () => !runQuietly("launchctl", "print", `system/${launchdLabel}`),
    );
    runPrivileged("rm", "-f", launchdPlist, launcher);
    runPrivileged("rm", "-rf", agentRoot);
    if (fs.existsSync(launchdPlist) || fs.existsSync(agentRoot) || fs.existsSync(launcher)) {
      throw new Error("macOS agent cleanup left installation files behind");
    }
    cleanupRequired = false;

    process.stdout.write(
      `Scout macOS launchd acceptance passed: installed, enrolled, received telemetry, restarted, and removed the agent (${counts.heartbeats} heartbeats).`,
    );
  } finally {
    if (cleanupRequired) {
      runQuietly("launchctl", "bootout", "system", launchdPlist);
      runQuietly("rm", "-f", launchdPlist, launcher);
      runQuietly("rm", "-rf", agentRoot);
    }
    server.close();
    fs.rmSync(workDirectory, { recursive: true, force: true });
  }
}

main().catch((error: unknown) => {
  console.error(
    `Scout macOS launchd acceptance failed: ${error instanceof Error ? error.message : error}`,
  );
  process.exitCode = 1;
});
