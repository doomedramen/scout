import { SetupForm } from "@/components/auth/setup-form";

export const dynamic = "force-dynamic";

export default function SetupPage() {
  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/30 p-6">
      <SetupForm />
    </main>
  );
}
