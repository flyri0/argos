import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Account, Transaction } from "./types";

// A fresh hlc/hlcSeed/db module set per test, so no in-memory clock state
// leaks between cases — only localStorage and IndexedDB carry over, and both
// are cleared here.
async function freshModules() {
  vi.resetModules();
  const { db } = await import("./db");
  const hlc = await import("./hlc");
  const { seedHlcFromLocalData } = await import("./hlcSeed");
  return { db, hlc, seedHlcFromLocalData };
}

beforeEach(async () => {
  vi.restoreAllMocks();
  localStorage.clear();
  const { db } = await freshModules();
  await Promise.all(db.tables.map((table) => table.clear()));
});

const sync = { hlc_node_id: "remote-node", server_version: 1, deleted_at: null };

function account(hlcPhysical: number, hlcCounter: number): Account {
  return {
    id: crypto.randomUUID(),
    ...sync,
    hlc_physical: hlcPhysical,
    hlc_counter: hlcCounter,
    name: "Checking",
    type: "checking",
    on_budget: true,
    closed: false,
    currency: "USD",
    notes: null,
  };
}

function transaction(hlcPhysical: number, hlcCounter: number): Transaction {
  return {
    id: crypto.randomUUID(),
    ...sync,
    hlc_physical: hlcPhysical,
    hlc_counter: hlcCounter,
    account_id: "acc-1",
    category_id: null,
    payee_id: null,
    parent_id: null,
    date: "2026-03-01",
    amount: -500,
    cleared: false,
    notes: "",
    transfer_id: null,
  };
}

describe("seedHlcFromLocalData", () => {
  it("advances a clock with no persisted value past the greatest HLC in the local replica", async () => {
    const { db, hlc, seedHlcFromLocalData } = await freshModules();
    await db.accounts.add(account(8_000_000, 2));
    await db.transactions.add(transaction(9_000_000, 4)); // the max
    await db.transactions.add(transaction(9_000_000, 1));

    // Only Date.now is mocked: faking timers would stall fake-indexeddb's
    // own scheduling and hang the Dexie reads the seed performs.
    vi.spyOn(Date, "now").mockReturnValue(1000); // this device's clock is far behind its data
    await seedHlcFromLocalData();
    const next = hlc.nextHlc();

    expect(
      hlc.compareHlc(next, { hlc_physical: 9_000_000, hlc_counter: 4, hlc_node_id: next.hlc_node_id }),
    ).toBe(1);
  });

  it("does nothing when a persisted clock already exists", async () => {
    const { db, seedHlcFromLocalData } = await freshModules();
    const persisted = JSON.stringify({ physical: 1000, counter: 0 });
    localStorage.setItem("argos.hlc_last", persisted);
    await db.transactions.add(transaction(9_000_000, 4));

    await seedHlcFromLocalData();

    expect(localStorage.getItem("argos.hlc_last")).toBe(persisted);
  });
});
