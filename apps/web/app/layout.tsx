import type { Metadata, Viewport } from "next";
import "../src/styles.css";

export const metadata: Metadata = {
  title: { default: "Scout", template: "%s · Scout" },
  description: "Self-hosted server, network, and device monitor.",
};

export const viewport: Viewport = { themeColor: "#151e2b" };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
