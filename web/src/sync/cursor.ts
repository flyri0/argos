// The client's last-seen `server_version` cursor (§2.3/§2.4): sent as
// `since` on the next push, and replaced with the response's own
// `server_version` once a push completes. Persisted in localStorage (like
// the node id in web/src/db/hlc.ts) so it survives a reload instead of
// re-pulling the whole dataset every time the app starts.
const CURSOR_KEY = "argos.sync_cursor";

export function getCursor(): number {
  const raw = localStorage.getItem(CURSOR_KEY);
  if (raw === null) return 0;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

export function setCursor(serverVersion: number): void {
  localStorage.setItem(CURSOR_KEY, String(serverVersion));
}
