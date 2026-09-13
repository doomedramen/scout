import { notFound } from "next/navigation";
import App, { type Route } from "../../src/App";
import { getShellData } from "../lib/server-api";

const sections = new Set([
  "overview",
  "systems",
  "incidents",
  "network",
  "notifications",
  "scopes",
  "enrollment",
  "access",
  "services",
  "hardware",
  "storage",
  "updates",
  "settings",
]);

export default async function Page({ params }: { params: Promise<{ route?: string[] }> }) {
  const { route = [] } = await params;
  if (route.length > 0 && !sections.has(route[0])) notFound();
  const shell = await getShellData();
  return (
    <App
      route={route as Route}
      initialAuth={shell.auth}
      initialStatus={shell.status}
      initialStatusError={shell.statusError}
    />
  );
}
