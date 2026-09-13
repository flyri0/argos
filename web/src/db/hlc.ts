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
const NODE_ID_KEY = "argos.node_id";

function nodeId(): string {
  let id = localStorage.getItem(NODE_ID_KEY);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(NODE_ID_KEY, id);
  }
  return id;
}

let lastPhysical = 0;
let lastCounter = 0;

export interface HlcStamp {
  hlc_physical: number;
  hlc_counter: number;
  hlc_node_id: string;
}

export function nextHlc(): HlcStamp {
  const physical = Date.now();
  if (physical > lastPhysical) {
    lastPhysical = physical;
    lastCounter = 0;
  } else {
    lastCounter += 1;
  }
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
  if (lastPhysical > received.hlc_physical) {
    lastCounter += 1;
  } else if (received.hlc_physical > lastPhysical) {
    lastPhysical = received.hlc_physical;
    lastCounter = received.hlc_counter + 1;
  } else {
    lastCounter = Math.max(lastCounter, received.hlc_counter) + 1;
  }
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
