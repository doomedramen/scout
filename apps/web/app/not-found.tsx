import Link from "next/link";

export default function NotFound() {
  return (
    <main className="app-shell">
      <div className="empty">
        <p>Page not found.</p>
        <Link href="/">Return to overview</Link>
      </div>
    </main>
  );
}
