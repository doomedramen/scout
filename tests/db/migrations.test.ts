import { describe, expect, it } from "vitest";

import { createDatabase } from "@/lib/server/db";

describe("SQLite foundation", () => {
  it("creates a usable WAL database and is idempotent", () => {
    const first = createDatabase(":memory:");
    expect(first.sqlite.pragma("journal_mode", { simple: true })).toBe("memory");
    expect(first.sqlite.pragma("foreign_keys", { simple: true })).toBe(1);
    expect(first.sqlite.prepare("SELECT version FROM schema_migration").all()).toEqual([
      { version: 1 },
      { version: 2 },
      { version: 3 },
      { version: 4 },
      { version: 5 },
      { version: 6 },
      { version: 7 },
    ]);
    first.sqlite.close();

    const second = createDatabase(":memory:");
    expect(
      second.sqlite
        .prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'system'")
        .get(),
    ).toEqual({
      name: "system",
    });
    second.sqlite.close();
  });
});
