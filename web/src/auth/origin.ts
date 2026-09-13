// §6.2: "a request originating from 127.0.0.1 ... does not need pairing."
// A client-side mirror of that rule, used only to decide which UI to show
// (skip straight to device management instead of the pairing flow) — the
// server is the actual authority and enforces this regardless of what the
// client guesses here.
export function isLocalOrigin(): boolean {
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1" || host === "::1";
}
