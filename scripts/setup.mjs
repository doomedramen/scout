import { randomBytes } from "node:crypto";
import { writeFileSync } from "node:fs";

const password = randomBytes(32).toString("hex");
try {
  writeFileSync(
    new URL("../.env", import.meta.url),
    `SCOUT_DB_PASSWORD=${password}\nSCOUT_DATABASE_URL=postgres://scout:${password}@127.0.0.1:5432/scout?sslmode=disable\n`,
    { mode: 0o600, flag: "wx" },
  );
  console.log(
    "Created private .env for local development. Run npm run db:up, then npm run dev.",
  );
} catch (error) {
  if (error.code === "EEXIST")
    console.log(".env already exists; left unchanged.");
  else throw error;
}
