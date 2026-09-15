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

// §2.3: the clock is persisted in localStorage and shared across reloads and
// tabs. A "reload" or a second "tab" is a fresh module instance
// (vi.resetModules + re-import) sharing the same localStorage.
async function freshHlcModule() {
  vi.resetModules();
  return import("./hlc");
}

describe("persisted clock", () => {
  it("stays strictly increasing across a reload even when the wall clock moved backwards", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(5000);
    const before = nextHlc();

    const reloaded = await freshHlcModule();
    vi.setSystemTime(1000);
    const after = reloaded.nextHlc();

    expect(reloaded.compareHlc(after, before)).toBe(1);
    vi.useRealTimers();
  });

  it("remembers an observed far-ahead HLC after a reload", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(1000);
    observeHlc({ hlc_physical: 9_000_000, hlc_counter: 3, hlc_node_id: "remote-node" });

    const reloaded = await freshHlcModule();
    const next = reloaded.nextHlc();

    expect(next.hlc_physical).toBe(9_000_000);
    expect(next.hlc_counter).toBeGreaterThan(3);
    vi.useRealTimers();
  });

  it("gives two tabs sharing storage strictly increasing values when they alternate", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(2000);
    const tabA = await freshHlcModule();
    const tabB = await freshHlcModule();
    expect(tabA).not.toBe(tabB);

    const stamps = [
      tabA.nextHlc(),
      tabB.nextHlc(),
      tabA.nextHlc(),
      tabB.nextHlc(),
      tabA.nextHlc(),
      tabB.nextHlc(),
    ];

    for (let i = 1; i < stamps.length; i++) {
      expect(tabA.compareHlc(stamps[i], stamps[i - 1])).toBe(1);
    }
    vi.useRealTimers();
  });

  it("falls back to in-memory state without crashing when storage throws", async () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    try {
      vi.useFakeTimers();
      vi.setSystemTime(3000);
      const fresh = await freshHlcModule();

      const first = fresh.nextHlc();
      fresh.observeHlc({ hlc_physical: 3000, hlc_counter: 7, hlc_node_id: "remote-node" });
      const second = fresh.nextHlc();

      expect(fresh.compareHlc(second, first)).toBe(1);
      expect(second.hlc_counter).toBe(9);
      expect(fresh.hasPersistedHlc()).toBe(false);
    } finally {
      getItem.mockRestore();
      setItem.mockRestore();
      vi.useRealTimers();
    }
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
