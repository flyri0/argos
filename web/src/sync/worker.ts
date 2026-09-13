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
  // The "online" event IS the browser's own signal that connectivity just
  // came back, so it always syncs unconditionally rather than re-checking
  // navigator.onLine — that property isn't guaranteed to already reflect
  // the new state by the time this handler runs (jsdom's synthetic event
  // in tests never flips it at all, since nothing else does that for it).
  const handleOnline = () => {
    void runSync();
  };

  // The periodic poll and a regained-visibility check have no such direct
  // signal, so both actually need to ask navigator.onLine first to avoid
  // a pointless request while offline.
  const triggerIfOnline = () => {
    if (navigator.onLine) void runSync();
  };

  const handleVisibility = () => {
    if (document.visibilityState === "visible") triggerIfOnline();
  };

  window.addEventListener("online", handleOnline);
  document.addEventListener("visibilitychange", handleVisibility);
  const intervalId = window.setInterval(triggerIfOnline, SYNC_INTERVAL_MS);

  if (navigator.onLine) handleOnline();

  return () => {
    window.removeEventListener("online", handleOnline);
    document.removeEventListener("visibilitychange", handleVisibility);
    window.clearInterval(intervalId);
  };
}
