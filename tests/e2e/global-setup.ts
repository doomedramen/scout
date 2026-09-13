import fs from "node:fs";

import { e2eDataDirectory } from "../../playwright.config";
import { closeDatabase } from "@/lib/server/db";
import { provisionSetupToken } from "@/lib/server/setup";

export default function globalSetup() {
  process.env.SCOUT_DATA_DIR = e2eDataDirectory;
  process.env.SCOUT_DISABLE_DISCOVERY = "true";
  fs.rmSync(e2eDataDirectory, { recursive: true, force: true });
  const token = provisionSetupToken();
  fs.writeFileSync(`${e2eDataDirectory}/setup-token`, token.token, { mode: 0o600 });
  closeDatabase();
}
