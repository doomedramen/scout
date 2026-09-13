import fs from "node:fs";
import path from "node:path";

import Database from "better-sqlite3";
import { drizzle, type BetterSQLite3Database } from "drizzle-orm/better-sqlite3";

import { migrateDatabase } from "@/db/migrations";
import { schema } from "@/db/schema";

export type ScoutDatabase = BetterSQLite3Database<typeof schema>;

export type DatabaseHandle = {
  sqlite: Database.Database;
  db: ScoutDatabase;
};

let singleton: DatabaseHandle | undefined;

function databasePath(): string {
  const configured = process.env.SCOUT_DATABASE_URL;
  if (configured?.startsWith("file:")) {
    return configured.slice("file:".length) || ":memory:";
  }

  if (configured && !configured.startsWith("file:")) {
    console.warn("Ignoring unsupported SCOUT_DATABASE_URL; Scout uses local SQLite.");
  }

  const dataDir = process.env.SCOUT_DATA_DIR ?? ".scout-data";
  fs.mkdirSync(dataDir, { recursive: true, mode: 0o700 });
  return path.join(dataDir, "scout.sqlite");
}

export function createDatabase(filename: string): DatabaseHandle {
  const sqlite = new Database(filename);
  migrateDatabase(sqlite);
  return { sqlite, db: drizzle(sqlite, { schema }) };
}

export function getDatabase(): DatabaseHandle {
  singleton ??= createDatabase(databasePath());
  return singleton;
}

export function closeDatabase(): void {
  singleton?.sqlite.close();
  singleton = undefined;
}
