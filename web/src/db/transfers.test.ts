import { beforeEach, describe, expect, it } from "vitest";

import { db } from "./db";
import { outbox, transactions } from "./helpers";
import {
  createTransfer,
  deleteTransaction,
  findTransferSibling,
  updateTransfer,
} from "./transfers";
import type { Transaction } from "./types";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

const baseSync = {
  hlc_physical: 1_700_000_000_000,
  hlc_counter: 0,
  hlc_node_id: "node-1",
  server_version: undefined,
  deleted_at: null,
};

function makeTransaction(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    account_id: "acc-1",
    category_id: null,
    payee_id: null,
    parent_id: null,
    date: "2026-03-01",
    amount: -500,
    cleared: false,
    notes: "",
    transfer_id: null,
    ...overrides,
  };
}

describe("createTransfer", () => {
  it("creates two linked rows with opposite-signed amounts and no category", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: "payee-1",
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "Moving money",
    });

    const primary = await transactions.get("t1");
    const mirror = await transactions.get("t2");

    expect(primary?.account_id).toBe("acc-checking");
    expect(primary?.amount).toBe(-1000);
    expect(mirror?.account_id).toBe("acc-savings");
    expect(mirror?.amount).toBe(1000);

    expect(primary?.category_id).toBeNull();
    expect(mirror?.category_id).toBeNull();
    expect(primary?.transfer_id).not.toBeNull();
    expect(primary?.transfer_id).toBe(mirror?.transfer_id);
    expect(primary?.payee_id).toBe("payee-1");
    expect(mirror?.payee_id).toBe("payee-1");
    expect(primary?.date).toBe("2026-03-05");
    expect(mirror?.date).toBe("2026-03-05");
  });

  it("gives each leg its own id, distinct from the shared transfer_id", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "",
    });

    const primary = await transactions.get("t1");
    expect(primary?.transfer_id).not.toBe("t1");
    expect(primary?.transfer_id).not.toBe("t2");
  });

  it("enqueues both legs as one outbox group sharing the transfer_id (§2.4)", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "",
    });

    const primary = await transactions.get("t1");
    const entries = await outbox.listUnsynced();
    expect(entries).toHaveLength(2);
    expect(entries.every((e) => e.group_id === primary?.transfer_id)).toBe(true);
    expect(entries.map((e) => e.row.id).sort()).toEqual(["t1", "t2"]);
  });
});

describe("updateTransfer", () => {
  it("mirrors shared fields and negates the amount on the sibling leg", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "Original",
    });

    await updateTransfer("t1", {
      date: "2026-03-06",
      payeeId: "payee-2",
      notes: "Updated",
      amountMinor: -2500,
    });

    const primary = await transactions.get("t1");
    const mirror = await transactions.get("t2");

    expect(primary?.amount).toBe(-2500);
    expect(mirror?.amount).toBe(2500);
    expect(primary?.date).toBe("2026-03-06");
    expect(mirror?.date).toBe("2026-03-06");
    expect(primary?.notes).toBe("Updated");
    expect(mirror?.notes).toBe("Updated");
    expect(primary?.payee_id).toBe("payee-2");
    expect(mirror?.payee_id).toBe("payee-2");
  });

  it("leaves cleared untouched on both legs", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "",
    });
    await transactions.update("t1", { cleared: true });

    await updateTransfer("t1", {
      date: "2026-03-05",
      payeeId: null,
      notes: "",
      amountMinor: -1000,
    });

    expect((await transactions.get("t1"))?.cleared).toBe(true);
    expect((await transactions.get("t2"))?.cleared).toBe(false);
  });

  it("does nothing for a non-transfer transaction", async () => {
    const plain = makeTransaction({ id: "plain-1", notes: "keep me" });
    await transactions.create(plain);

    await updateTransfer("plain-1", {
      date: "2026-04-01",
      payeeId: "payee-9",
      notes: "changed",
      amountMinor: -1,
    });

    expect((await transactions.get("plain-1"))?.notes).toBe("keep me");
  });
});

describe("deleteTransaction", () => {
  it("soft-deletes only the transaction for a non-transfer row", async () => {
    const plain = makeTransaction({ id: "plain-1" });
    await transactions.create(plain);

    await deleteTransaction("plain-1");

    expect((await transactions.get("plain-1"))?.deleted_at).not.toBeNull();
  });

  it("soft-deletes both legs of a transfer, from either side", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "",
    });

    await deleteTransaction("t2");

    expect((await transactions.get("t1"))?.deleted_at).not.toBeNull();
    expect((await transactions.get("t2"))?.deleted_at).not.toBeNull();
  });

  it("enqueues both legs' deletes under one outbox group, keyed by transfer_id", async () => {
    await createTransfer({
      id: "t1",
      transferTransactionId: "t2",
      accountId: "acc-checking",
      transferAccountId: "acc-savings",
      payeeId: null,
      date: "2026-03-05",
      amountMinor: -1000,
      notes: "",
    });
    const transferId = (await transactions.get("t1"))?.transfer_id;

    await deleteTransaction("t2");

    const deletes = (await outbox.listUnsynced()).filter((e) => e.op === "delete");
    expect(deletes).toHaveLength(2);
    expect(deletes.every((e) => e.group_id === transferId)).toBe(true);
  });

  it("enqueues a \"delete\" op with a null group_id for a standalone (non-transfer) delete", async () => {
    const plain = makeTransaction({ id: "plain-1" });
    await transactions.create(plain);
    await deleteTransaction("plain-1");

    const plainEntries = await outbox.listUnsynced();
    const plainDelete = plainEntries[plainEntries.length - 1];
    expect(plainDelete.op).toBe("delete");
    expect(plainDelete.group_id).toBeNull();
  });
});

describe("findTransferSibling", () => {
  it("finds the other leg by shared transfer_id", () => {
    const t1 = makeTransaction({ id: "t1", transfer_id: "xfer-1", account_id: "acc-a" });
    const t2 = makeTransaction({ id: "t2", transfer_id: "xfer-1", account_id: "acc-b" });
    const other = makeTransaction({ id: "t3" });

    expect(findTransferSibling(t1, [t1, t2, other])?.id).toBe("t2");
  });

  it("returns undefined for a non-transfer transaction", () => {
    const plain = makeTransaction({ id: "plain-1" });
    expect(findTransferSibling(plain, [plain])).toBeUndefined();
  });

  it("returns undefined once the sibling has been soft-deleted", () => {
    const t1 = makeTransaction({ id: "t1", transfer_id: "xfer-1" });
    const t2 = makeTransaction({
      id: "t2",
      transfer_id: "xfer-1",
      deleted_at: Date.now(),
    });

    expect(findTransferSibling(t1, [t1, t2])).toBeUndefined();
  });
});
