import { SignInForm } from "@/components/auth/sign-in-form";

export const dynamic = "force-dynamic";

export default function SignInPage() {
  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/30 p-6">
      <SignInForm />
    </main>
  );
}
