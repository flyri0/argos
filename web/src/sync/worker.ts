import { runSync } from "./engine";

// Drives the sync worker off browser connectivity (§2.2: "the client's
// outbox is drained ... whenever connectivity is detected"): one attempt
// right away if already online, then one on every subsequent online event.
// Returns a cleanup function for the component that started it.
export function startSyncWorker(): () => void {
  const handleOnline = () => {
    void runSync();
  };

  window.addEventListener("online", handleOnline);
  if (navigator.onLine) handleOnline();

  return () => window.removeEventListener("online", handleOnline);
}
