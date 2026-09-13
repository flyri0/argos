// The client's last-seen `server_version` cursor (§2.3/§2.4): sent as
// `since` on the next push, and replaced with the response's own
// `server_version` once a push completes. Persisted in localStorage (like
// the node id in web/src/db/hlc.ts) so it survives a reload instead of
// re-pulling the whole dataset every time the app starts.
const CURSOR_KEY = "argos.sync_cursor";

// The server's `sync_id` (§2.3/§5.2) as last seen by this client, so a
// later response can be compared against it to detect a server-side reset
// (restore from backup) rather than assuming every response continues the
// same history as the cursor above.
const SYNC_ID_KEY = "argos.sync_id";

export function getCursor(): number {
  const raw = localStorage.getItem(CURSOR_KEY);
  if (raw === null) return 0;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

export function setCursor(serverVersion: number): void {
  localStorage.setItem(CURSOR_KEY, String(serverVersion));
}

export function getSyncId(): string | null {
  return localStorage.getItem(SYNC_ID_KEY);
}

export function setSyncId(syncId: string): void {
  localStorage.setItem(SYNC_ID_KEY, syncId);
}
