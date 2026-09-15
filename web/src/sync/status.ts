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
  // §2.4: units rolled back by a conflict rejection since the user last
  // dismissed the notice — their local edits were replaced by server state.
  conflictCount: number;
}

let status: SyncStatus = { schemaMismatch: false, conflictCount: 0 };
const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

export function getSyncStatus(): SyncStatus {
  return status;
}

export function setSchemaMismatch(schemaMismatch: boolean): void {
  if (status.schemaMismatch === schemaMismatch) return;
  status = { ...status, schemaMismatch };
  notify();
}

export function recordConflicts(count: number): void {
  if (count <= 0) return;
  status = { ...status, conflictCount: status.conflictCount + count };
  notify();
}

export function dismissConflicts(): void {
  if (status.conflictCount === 0) return;
  status = { ...status, conflictCount: 0 };
  notify();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useSyncStatus(): SyncStatus {
  return useSyncExternalStore(subscribe, getSyncStatus);
}
