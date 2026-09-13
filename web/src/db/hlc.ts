// Local half of the Hybrid Logical Clock (§2.3): every write needs a fresh
// (hlc_physical, hlc_counter, hlc_node_id) triple, even before /sync exists
// (§5.1 — there's no server-side placeholder, the write path is the same
// before and after pairing). What's implemented here is only the device's
// own advance (physical time, or the counter if it hasn't moved forward
// since the last write); the other half of §2.3 — advancing past the
// physical time seen in a received message — has nothing to compare
// against until a later milestone wires up /sync, and slots in here
// without changing this shape.
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
