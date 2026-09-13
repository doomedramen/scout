import { defineConfig } from "drizzle-kit";

export default defineConfig({
  schema: "db/schema.ts",
  out: "drizzle",
  dialect: "sqlite",
  dbCredentials: {
    url: process.env.SCOUT_DATABASE_URL ?? "file:./scout.sqlite",
  },
});
