// The Hybrid Logical Clock (§2.3), ported from internal/sync/hlc.go
// (Milestone 10) so the client generates and advances HLC values with the
// exact same algorithm the server uses. `nextHlc` is the device's own
// advance (physical time, or the counter if it hasn't moved forward since
// the last write) — every write needs a fresh (hlc_physical, hlc_counter,
// hlc_node_id) triple, even before /sync exists (§5.1 — there's no
// server-side placeholder, the write path is the same before and after
// pairing). `observeHlc` is the other half: advancing local past the
// physical time seen in a message received from another device (via
// web/src/sync), mirroring Go's `Observe`.
import { generateUUID } from "../lib/uuid";

const NODE_ID_KEY = "argos.node_id";
const LAST_HLC_KEY = "argos.hlc_last";

// Only used when localStorage is unavailable (e.g. blocked site data): the
// clock keeps working for this page's lifetime instead of crashing writes.
let fallbackNodeId: string | null = null;

function nodeId(): string {
  try {
    let id = localStorage.getItem(NODE_ID_KEY);
    if (!id) {
      id = generateUUID();
      localStorage.setItem(NODE_ID_KEY, id);
    }
    return id;
  } catch {
    fallbackNodeId ??= generateUUID();
    return fallbackNodeId;
  }
}

let lastPhysical = 0;
let lastCounter = 0;

// §2.3: the clock state lives in localStorage so it survives reloads and is
// shared by every tab of this device. Loaded at the start of every call so a
// tab sees another tab's advances; the in-memory copy is kept as the floor in
// case a write back to storage ever failed.
function loadPersisted(): void {
  try {
    const raw = localStorage.getItem(LAST_HLC_KEY);
    if (raw === null) return;
    const parsed = JSON.parse(raw) as { physical?: unknown; counter?: unknown };
    if (typeof parsed.physical !== "number" || typeof parsed.counter !== "number") return;
    if (
      parsed.physical > lastPhysical ||
      (parsed.physical === lastPhysical && parsed.counter > lastCounter)
    ) {
      lastPhysical = parsed.physical;
      lastCounter = parsed.counter;
    }
  } catch {
    // Unavailable or corrupt storage: continue from the in-memory state.
  }
}

function savePersisted(): void {
  try {
    localStorage.setItem(
      LAST_HLC_KEY,
      JSON.stringify({ physical: lastPhysical, counter: lastCounter }),
    );
  } catch {
    // Unavailable storage: the in-memory state still keeps this page monotonic.
  }
}

// Whether a persisted clock exists — seedHlcFromLocalData only seeds a
// device that has none. Unreadable storage counts as missing, so seeding
// still protects a device whose storage is blocked.
export function hasPersistedHlc(): boolean {
  try {
    return localStorage.getItem(LAST_HLC_KEY) !== null;
  } catch {
    return false;
  }
}

export interface HlcStamp {
  hlc_physical: number;
  hlc_counter: number;
  hlc_node_id: string;
}

export function nextHlc(): HlcStamp {
  loadPersisted();
  const physical = Date.now();
  if (physical > lastPhysical) {
    lastPhysical = physical;
    lastCounter = 0;
  } else {
    lastCounter += 1;
  }
  savePersisted();
  return {
    hlc_physical: lastPhysical,
    hlc_counter: lastCounter,
    hlc_node_id: nodeId(),
  };
}

// Mirrors Go's `Observe`: merges a received HLC (from a /sync response) into
// this device's own clock so its next write is guaranteed to order after
// everything it has seen, even when the sender's physical clock is ahead of
// this device's. The physical component becomes whichever of the two is
// greater; when they're equal, the counter continues from whichever side's
// counter is greater, incremented by one — so the result is always strictly
// greater than both the prior local value and the received one.
export function observeHlc(received: HlcStamp): void {
  loadPersisted();
  if (lastPhysical > received.hlc_physical) {
    lastCounter += 1;
  } else if (received.hlc_physical > lastPhysical) {
    lastPhysical = received.hlc_physical;
    lastCounter = received.hlc_counter + 1;
  } else {
    lastCounter = Math.max(lastCounter, received.hlc_counter) + 1;
  }
  savePersisted();
}

// Mirrors Go's `Compare`: physical, then counter, then node id as the final
// deterministic tiebreak (§2.3). Not used for conflict resolution on the
// client (that's entirely server-side, §2.3) but kept alongside `nextHlc`
// and `observeHlc` as the third piece of the ported algorithm.
export function compareHlc(a: HlcStamp, b: HlcStamp): number {
  if (a.hlc_physical !== b.hlc_physical) return a.hlc_physical < b.hlc_physical ? -1 : 1;
  if (a.hlc_counter !== b.hlc_counter) return a.hlc_counter < b.hlc_counter ? -1 : 1;
  if (a.hlc_node_id !== b.hlc_node_id) return a.hlc_node_id < b.hlc_node_id ? -1 : 1;
  return 0;
}
