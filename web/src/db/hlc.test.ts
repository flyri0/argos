import { beforeEach, describe, expect, it, vi } from "vitest";

// nextHlc/observeHlc share module-level clock state, so each test needs a
// fresh module instance (vi.resetModules + a dynamic re-import) rather than
// resuming wherever the previous test's clock left off.
let compareHlc: typeof import("./hlc").compareHlc;
let nextHlc: typeof import("./hlc").nextHlc;
let observeHlc: typeof import("./hlc").observeHlc;

beforeEach(async () => {
  vi.resetModules();
  localStorage.clear();
  vi.useRealTimers();
  ({ compareHlc, nextHlc, observeHlc } = await import("./hlc"));
});

describe("nextHlc", () => {
  // Mirrors TestNow_FrozenClockStrictlyIncreases: consecutive calls at the
  // same wall-clock instant must still strictly increase.
  it("increments the counter instead of repeating when the clock hasn't moved", () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_700_000_000_000);

    const first = nextHlc();
    const second = nextHlc();

    expect(second.hlc_physical).toBe(first.hlc_physical);
    expect(second.hlc_counter).toBe(first.hlc_counter + 1);
    expect(compareHlc(second, first)).toBe(1);

    vi.useRealTimers();
  });
});

describe("observeHlc", () => {
  // Each case probes the post-observe state via one more nextHlc() call at
  // the same frozen instant, so that call's own "clock hasn't moved"
  // increment (see the nextHlc test above) applies on top of whatever
  // observeHlc left behind — accounted for in each expected counter below.
  // Mirrors TestObserve_ReceivedAheadOfLocal.
  it("jumps local physical forward and continues the received counter when the message is ahead", () => {
    vi.useFakeTimers();
    vi.setSystemTime(1000);
    nextHlc(); // seeds local clock at physical=1000, counter=0

    observeHlc({ hlc_physical: 2000, hlc_counter: 1, hlc_node_id: "remote-node" });
    const next = nextHlc(); // still frozen at 1000, behind the observed 2000

    expect(next.hlc_physical).toBe(2000);
    expect(next.hlc_counter).toBe(3); // observeHlc -> 2 (received.counter + 1), then +1

    vi.useRealTimers();
  });

  it("keeps local physical and bumps its own counter when local is already ahead", () => {
    vi.useFakeTimers();
    vi.setSystemTime(5000);
    const local = nextHlc(); // physical=5000, counter=0

    observeHlc({ hlc_physical: 1000, hlc_counter: 9, hlc_node_id: "remote-node" });
    const next = nextHlc();

    expect(next.hlc_physical).toBe(local.hlc_physical);
    expect(next.hlc_counter).toBe(2); // observeHlc -> 1 (local.counter + 1), then +1

    vi.useRealTimers();
  });

  it("takes the greater counter plus one when physical components are equal", () => {
    vi.useFakeTimers();
    vi.setSystemTime(3000);
    nextHlc(); // physical=3000, counter=0

    observeHlc({ hlc_physical: 3000, hlc_counter: 4, hlc_node_id: "remote-node" });
    const next = nextHlc();

    expect(next.hlc_physical).toBe(3000);
    expect(next.hlc_counter).toBe(6); // observeHlc -> 5 (max(0, 4) + 1), then +1

    vi.useRealTimers();
  });
});

describe("compareHlc", () => {
  // Mirrors TestCompare_TiebreaksOnNodeID.
  it("tiebreaks on node id when physical and counter are equal", () => {
    const a = { hlc_physical: 1000, hlc_counter: 5, hlc_node_id: "aaa" };
    const b = { hlc_physical: 1000, hlc_counter: 5, hlc_node_id: "bbb" };

    expect(compareHlc(a, b)).toBe(-1);
    expect(compareHlc(b, a)).toBe(1);
    expect(compareHlc(a, a)).toBe(0);
  });

  it("orders by physical first, then counter", () => {
    const earlier = { hlc_physical: 1000, hlc_counter: 99, hlc_node_id: "z" };
    const later = { hlc_physical: 1001, hlc_counter: 0, hlc_node_id: "a" };
    expect(compareHlc(earlier, later)).toBe(-1);

    const lowerCounter = { hlc_physical: 1000, hlc_counter: 1, hlc_node_id: "z" };
    const higherCounter = { hlc_physical: 1000, hlc_counter: 2, hlc_node_id: "a" };
    expect(compareHlc(lowerCounter, higherCounter)).toBe(-1);
  });
});
