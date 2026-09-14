import { z } from "zod";

import { validateOwnerPassword } from "@/lib/auth/password";
import { auth } from "@/lib/server/auth";
import { requestDiscovery } from "@/lib/server/discovery";
import { jsonError, rateLimit, readRequestBody, sameOrigin } from "@/lib/server/http";
import {
  consumeSetupToken,
  ensureSetupToken,
  isSetupTokenValid,
  ownerExists,
  setupStatus,
  setupOwnerEmail,
} from "@/lib/server/setup";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const ownerInput = z.object({
  setupToken: z.string().min(1).max(200),
  username: z
    .string()
    .min(3)
    .max(32)
    .regex(/^[A-Za-z0-9_]+$/),
  password: z.string().min(8).max(128),
});

let ownerCreation: Promise<Response> | undefined;

export async function GET() {
  const status = ensureSetupToken();
  return Response.json(status, { headers: { "Cache-Control": "no-store" } });
}

async function createOwner(request: Request): Promise<Response> {
  if (!sameOrigin(request)) return jsonError("Request origin is not allowed.", 403);
  if (ownerExists()) return jsonError("Scout already has an owner. Sign in instead.", 409);

  const body = await readRequestBody(request, 32 * 1024);
  if (body instanceof Response) return body;
  let input: z.infer<typeof ownerInput>;
  try {
    input = ownerInput.parse(JSON.parse(body));
  } catch {
    return jsonError("Enter a username, setup token, and valid password.", 400);
  }

  const password = validateOwnerPassword(input.password);
  if (!password.valid) return jsonError(password.reason, 400);

  const current = setupStatus();
  if (!current.required || !current.tokenExpiresAt || !isSetupTokenValid(input.setupToken)) {
    return jsonError("The setup token is invalid or has expired.", 400);
  }

  try {
    const result = await auth.api.signUpEmail({
      body: {
        name: input.username,
        username: input.username,
        email: setupOwnerEmail(),
        password: input.password,
      },
      headers: new Headers(request.headers),
    });

    if (!result.user || !consumeSetupToken(input.setupToken)) {
      return jsonError("The setup token is invalid or has expired.", 400);
    }

    requestDiscovery();
    return Response.json(
      { created: true },
      { status: 201, headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    const message = error instanceof Error ? error.message : "Owner setup failed.";
    if (message.toLowerCase().includes("taken") || message.toLowerCase().includes("unique")) {
      return jsonError("Scout already has an owner. Sign in instead.", 409);
    }
    return jsonError("Owner setup failed. Check the setup token and try again.", 400);
  }
}

export async function POST(request: Request) {
  const limited = rateLimit(request, "owner-setup", 10, 60_000);
  if (limited) return limited;
  if (ownerCreation) return ownerCreation;
  ownerCreation = createOwner(request).finally(() => {
    ownerCreation = undefined;
  });
  return ownerCreation;
}
