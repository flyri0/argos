import { runSync } from "./engine";

// How often the worker polls while online, on top of the event-driven
// triggers below (§2.3: "the client's outbox is drained ... whenever
// connectivity is detected"). A reconnect event alone only catches this
// device's own connectivity flapping — it does nothing to pull changes
// another device pushed to the server in the meantime, which is what
// actually keeps every device converged without the user manually
// reloading the page. 10s is frequent enough to feel close to live without
// meaningfully taxing a server meant to run on something as small as a
// Raspberry Pi (§1).
const SYNC_INTERVAL_MS = 10_000;

// Drives the sync worker (§2.2/§2.3): a periodic poll while online, plus
// two event-driven triggers for a snappier feel — the browser regaining
// connectivity, and this tab regaining focus (switching back to Argos
// shouldn't require waiting out the rest of the poll interval to see what
// changed elsewhere). Returns a cleanup function for the component that
// started it.
export function startSyncWorker(): () => void {
  const trigger = () => {
    if (navigator.onLine) void runSync();
  };

  const handleVisibility = () => {
    if (document.visibilityState === "visible") trigger();
  };

  window.addEventListener("online", trigger);
  document.addEventListener("visibilitychange", handleVisibility);
  const intervalId = window.setInterval(trigger, SYNC_INTERVAL_MS);

  if (navigator.onLine) trigger();

  return () => {
    window.removeEventListener("online", trigger);
    document.removeEventListener("visibilitychange", handleVisibility);
    window.clearInterval(intervalId);
  };
}
