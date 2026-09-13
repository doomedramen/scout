import Link from "next/link";
import { Bell, ChevronRight, CircleUserRound, Plus, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";

type AppShellProps = {
  children: React.ReactNode;
  username: string;
  title?: string;
  description?: string;
};

export function AppShell({ children, username, title, description }: AppShellProps) {
  return (
    <div className="min-h-svh bg-background">
      <header className="border-b">
        <div className="mx-auto flex min-h-16 max-w-7xl items-center gap-4 px-4 sm:px-6">
          <Link
            href="/systems"
            className="flex items-center gap-2 font-heading text-lg font-semibold"
          >
            <ShieldCheck className="size-5" aria-hidden="true" />
            <span>Scout</span>
          </Link>
          <Separator orientation="vertical" className="hidden h-6 sm:block" />
          <nav aria-label="Primary" className="hidden items-center gap-1 text-sm md:flex">
            <Link
              href="/systems"
              className="rounded-md px-3 py-2 text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              Systems
            </Link>
            <Link
              href="/network"
              className="rounded-md px-3 py-2 text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              Network
            </Link>
            <Link
              href="/settings"
              className="rounded-md px-3 py-2 text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              Settings
            </Link>
          </nav>
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="outline"
              size="icon"
              aria-label="Manual installation"
              title="Manual installation"
              render={<Link href="/systems?manual=1" />}
            >
              <Plus aria-hidden="true" />
            </Button>
            <Button variant="ghost" size="icon" aria-label="Notifications" title="Notifications">
              <Bell aria-hidden="true" />
            </Button>
            <div
              className="flex items-center gap-2 rounded-md border px-2 py-1.5 text-sm"
              title={username}
            >
              <CircleUserRound className="size-4" aria-hidden="true" />
              <span className="hidden max-w-28 truncate sm:inline">{username}</span>
            </div>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-4 py-8 sm:px-6">
        {title ? (
          <div className="mb-8">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Link href="/systems" className="hover:text-foreground">
                Systems
              </Link>
              <ChevronRight className="size-4" aria-hidden="true" />
              <span>{title}</span>
            </div>
            <h1 className="mt-3 font-heading text-3xl font-semibold tracking-tight">{title}</h1>
            {description ? (
              <p className="mt-2 max-w-2xl text-muted-foreground">{description}</p>
            ) : null}
          </div>
        ) : null}
        {children}
      </main>
    </div>
  );
}
