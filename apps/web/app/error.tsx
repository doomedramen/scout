"use client";

export default function Error({ reset }: { error: Error; reset: () => void }) {
  return (
    <main className="app-shell">
      <div className="empty" role="alert">
        <p>Scout could not load.</p>
        <button type="button" onClick={reset}>
          Retry
        </button>
      </div>
    </main>
  );
}
