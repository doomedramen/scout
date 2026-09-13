import { Client } from "ssh2";

export type SshEndpoint = { address: string; port: number };

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
