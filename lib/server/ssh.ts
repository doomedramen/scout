import { Client } from "ssh2";

export type SshEndpoint = { address: string; port: number };

export type SshCredential = {
  authType: "password" | "private-key";
  secret: string;
  passphrase: string | null;
  privilegePassword: string | null;
};

export type SshCommandResult = {
  code: number | null;
  stdout: string;
  stderr: string;
};

export type SshConnection = {
  exec(command: string, input?: string): Promise<SshCommandResult>;
  close(): void;
};

export async function readSshFingerprint(
  endpoint: SshEndpoint,
  timeoutMs = 5_000,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const client = new Client();
    let settled = false;
    const finish = (error: Error | null, fingerprint?: string) => {
      if (settled) return;
      settled = true;
      client.end();
      if (error) reject(error);
      else resolve(fingerprint!);
    };

    client.once("error", (error: Error) => finish(error));
    client.connect({
      host: endpoint.address,
      port: endpoint.port,
      username: "scout-fingerprint",
      password: "",
      hostHash: "sha256",
      hostVerifier: (fingerprint: string) => {
        finish(null, fingerprint);
        return false;
      },
      readyTimeout: timeoutMs,
      timeout: timeoutMs,
    });
  });
}

export function connectSsh(
  endpoint: SshEndpoint,
  credential: SshCredential & { username: string },
  expectedFingerprint: string,
  timeoutMs = 15_000,
): Promise<SshConnection> {
  return new Promise((resolve, reject) => {
    const client = new Client();
    let settled = false;
    let verificationError: Error | undefined;
    const timeout = setTimeout(() => finish(new Error("SSH connection timed out.")), timeoutMs);
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      if (error) {
        client.end();
        reject(error);
      } else {
        resolve({
          exec: (command, input) => execCommand(client, command, input),
          close: () => client.end(),
        });
      }
    };

    client.once("ready", () => finish());
    client.once("error", (error: Error) =>
      finish(verificationError ?? new Error(`SSH connection failed: ${error.message}`)),
    );
    const authentication =
      credential.authType === "password"
        ? { password: credential.secret }
        : {
            privateKey: Buffer.from(credential.secret, "utf8"),
            passphrase: credential.passphrase ?? undefined,
          };
    client.connect({
      host: endpoint.address,
      port: endpoint.port,
      username: credential.username,
      ...authentication,
      hostHash: "sha256",
      hostVerifier: (fingerprint: string) => {
        if (fingerprint !== expectedFingerprint) {
          verificationError = new Error("The SSH host fingerprint changed during installation.");
          return false;
        }
        return true;
      },
      readyTimeout: timeoutMs,
      timeout: timeoutMs,
    });
  });
}

function execCommand(client: Client, command: string, input?: string): Promise<SshCommandResult> {
  return new Promise((resolve, reject) => {
    client.exec(command, (error, stream) => {
      if (error) {
        reject(error);
        return;
      }
      let stdout = "";
      let stderr = "";
      const append = (current: string, chunk: Buffer | string): string => {
        const next = current + chunk.toString();
        return next.length > 64_000 ? next.slice(-64_000) : next;
      };
      stream.on("data", (chunk: Buffer) => {
        stdout = append(stdout, chunk);
      });
      stream.stderr.on("data", (chunk: Buffer) => {
        stderr = append(stderr, chunk);
      });
      stream.once("error", reject);
      stream.once("close", (code: number | null) => resolve({ code, stdout, stderr }));
      if (input) stream.write(input);
      stream.end();
    });
  });
}
