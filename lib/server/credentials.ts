import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";

import { credentialKey } from "@/lib/server/keys";

export type CredentialSecret = {
  authType: "password" | "private-key";
  secret: string;
  passphrase: string | null;
};

export type CredentialContext = {
  systemId: string;
  method: string;
  username: string;
};

export type EncryptedCredential = {
  ciphertext: string;
  nonce: string;
};

function aad(context: CredentialContext): Buffer {
  return Buffer.from(
    `scout-credential:v1:${context.systemId}:${context.method}:${context.username}`,
    "utf8",
  );
}

function encode(value: Buffer): string {
  return value.toString("base64url");
}

function decode(value: string): Buffer {
  return Buffer.from(value, "base64url");
}

export function encryptCredential(
  secret: CredentialSecret,
  context: CredentialContext,
): EncryptedCredential {
  const nonce = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", credentialKey(), nonce);
  cipher.setAAD(aad(context));
  const encrypted = Buffer.concat([
    cipher.update(JSON.stringify(secret), "utf8"),
    cipher.final(),
    cipher.getAuthTag(),
  ]);
  return { ciphertext: encode(encrypted), nonce: encode(nonce) };
}

export function decryptCredential(
  encrypted: EncryptedCredential,
  context: CredentialContext,
): CredentialSecret {
  const nonce = decode(encrypted.nonce);
  const payload = decode(encrypted.ciphertext);
  if (nonce.length !== 12 || payload.length < 16)
    throw new Error("Encrypted credential is malformed");

  const tag = payload.subarray(payload.length - 16);
  const ciphertext = payload.subarray(0, payload.length - 16);
  const decipher = createDecipheriv("aes-256-gcm", credentialKey(), nonce);
  decipher.setAAD(aad(context));
  decipher.setAuthTag(tag);
  const decoded = Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString("utf8");
  const secret = JSON.parse(decoded) as CredentialSecret;
  if (secret.authType !== "password" && secret.authType !== "private-key")
    throw new Error("Encrypted credential type is invalid");
  if (!secret.secret || (secret.passphrase !== null && typeof secret.passphrase !== "string")) {
    throw new Error("Encrypted credential contents are invalid");
  }
  return secret;
}
