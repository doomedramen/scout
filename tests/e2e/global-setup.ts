import fs from "node:fs";

import { e2eDataDirectory } from "../../playwright.config";
import { closeDatabase } from "@/lib/server/db";
import { authSecret, controlSigningKey, credentialKey } from "@/lib/server/keys";
import { provisionSetupToken } from "@/lib/server/setup";

export default function globalSetup() {
  if (
    process.env.SCOUT_E2E_PACKAGED === "1" ||
    process.env.SCOUT_E2E_WEB_SERVER === "1" ||
    process.env.PLAYWRIGHT_BASE_URL
  )
    return;
  process.env.SCOUT_DATA_DIR = e2eDataDirectory;
  process.env.SCOUT_DISABLE_DISCOVERY = "true";
  fs.rmSync(e2eDataDirectory, { recursive: true, force: true });
  authSecret();
  credentialKey();
  controlSigningKey();
  const token = provisionSetupToken();
  fs.writeFileSync(`${e2eDataDirectory}/setup-token`, token.token, { mode: 0o600 });
  closeDatabase();
}
