import { betterAuth } from "better-auth";
import { drizzleAdapter } from "@better-auth/drizzle-adapter";
import { nextCookies } from "better-auth/next-js";
import { twoFactor, username } from "better-auth/plugins";

import { schema } from "@/db/schema";
import { getDatabase } from "@/lib/server/db";
import { authSecret } from "@/lib/server/keys";

export const auth = betterAuth({
  database: drizzleAdapter(getDatabase().db, {
    provider: "sqlite",
    schema,
    // better-sqlite3 transactions are synchronous, while Better Auth invokes
    // the adapter transaction callback asynchronously. Keep the adapter's
    // writes explicit and short rather than asking it to await inside a
    // synchronous SQLite transaction.
    transaction: false,
  }),
  secret: authSecret(),
  baseURL: process.env.SCOUT_PUBLIC_URL || undefined,
  emailAndPassword: {
    enabled: true,
    minPasswordLength: 8,
    maxPasswordLength: 128,
  },
  plugins: [
    username({
      minUsernameLength: 3,
      maxUsernameLength: 32,
      immutableUsername: true,
      displayUsername: false,
    }),
    twoFactor({ issuer: "Scout" }),
    nextCookies(),
  ],
});
