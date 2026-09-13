import { describe, expect, it } from "vitest";

import { computeBalance } from "./balance";
import type { Transaction } from "./types";

function makeTransaction(overrides: Partial<Transaction>): Transaction {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    account_id: "acc-1",
    category_id: null,
    payee_id: null,
    parent_id: null,
    date: "2026-03-01",
    amount: 0,
    cleared: false,
    notes: "",
    transfer_id: null,
    ...overrides,
  };
}

describe("computeBalance", () => {
  it("sums transaction amounts", () => {
    const balance = computeBalance([
      makeTransaction({ amount: 10000 }),
      makeTransaction({ amount: -2500 }),
    ]);
    expect(balance).toBe(7500);
  });

  it("is zero for no transactions", () => {
    expect(computeBalance([])).toBe(0);
  });

  it("ignores soft-deleted transactions", () => {
    const balance = computeBalance([
      makeTransaction({ amount: 10000 }),
      makeTransaction({ amount: -5000, deleted_at: Date.now() }),
    ]);
    expect(balance).toBe(10000);
  });
});
