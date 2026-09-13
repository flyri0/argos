import { useSyncExternalStore } from "react";

// Reactive status for the sync worker (web/src/sync/engine.ts), so the UI
// (SyncNotice.tsx) can show a persistent notice without polling. A plain
// module-level store rather than React context: the worker itself isn't a
// component and needs to update this from outside the render tree.
export interface SyncStatus {
  // §2.3: once the server reports a schema_version the client doesn't
  // expect, syncing pauses for the rest of this session — the rest of the
  // app must keep working against the local IndexedDB copy regardless.
  schemaMismatch: boolean;
}

let status: SyncStatus = { schemaMismatch: false };
const listeners = new Set<() => void>();

export function getSyncStatus(): SyncStatus {
  return status;
}

export function setSchemaMismatch(schemaMismatch: boolean): void {
  if (status.schemaMismatch === schemaMismatch) return;
  status = { ...status, schemaMismatch };
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useSyncStatus(): SyncStatus {
  return useSyncExternalStore(subscribe, getSyncStatus);
}
