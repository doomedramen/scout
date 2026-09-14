export type ManualInstallerCommandInput = {
  serverUrl: string;
  invitation: string;
  trustPin: string | null;
};

export function renderManualInstallerCommand(input: ManualInstallerCommandInput): string {
  const serverUrl = normalizeOrigin(input.serverUrl);
  const trustPin =
    new URL(serverUrl).protocol === "http:" && input.trustPin
      ? `SCOUT_TRUST_PIN=${shellQuote(input.trustPin)} `
      : "";
  const installerUrl = `${serverUrl}/api/v1/bootstrap/agent/install.sh`;
  return `SCOUT_OTI=${shellQuote(input.invitation)} ${trustPin}bash -c \"$(curl -fsSL ${shellQuote(installerUrl)})\"`;
}

export function scoutOrigin(request: Request): string {
  const configured = process.env.SCOUT_PUBLIC_URL?.trim();
  return normalizeOrigin(configured || new URL(request.url).origin);
}

function normalizeOrigin(value: string): string {
  return new URL(value).origin;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
