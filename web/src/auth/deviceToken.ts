import { useSyncExternalStore } from "react";

// This device's long-lived pairing token (§6.2), once it has one. Reactive
// (rather than a plain localStorage read) so DevicesScreen can switch from
// the pairing flow to device management the instant bootstrap/approval
// succeeds, with no reload — mirrors web/src/sync/status.ts's store shape.
const TOKEN_KEY = "argos.device_token";
const listeners = new Set<() => void>();

let token: string | null = localStorage.getItem(TOKEN_KEY);

export function getDeviceToken(): string | null {
  return token;
}

export function setDeviceToken(next: string): void {
  token = next;
  localStorage.setItem(TOKEN_KEY, next);
  for (const listener of listeners) listener();
}

export function clearDeviceToken(): void {
  token = null;
  localStorage.removeItem(TOKEN_KEY);
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useDeviceToken(): string | null {
  return useSyncExternalStore(subscribe, getDeviceToken);
}
