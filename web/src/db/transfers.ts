import { generateUUID } from "../lib/uuid";
import { db } from "./db";
import { enqueueRowMutation } from "./helpers";
import { nextHlc } from "./hlc";
import type { Transaction } from "./types";

export interface NewTransferInput {
  id: string;
  transferTransactionId: string;
  accountId: string;
  transferAccountId: string;
  payeeId: string | null;
  date: string;
  amountMinor: number;
  notes: string;
}

// §5.2's transfer_id pattern: a transfer is two ordinary transaction rows,
// one per account, sharing a freshly generated transfer_id and carrying
// opposite-signed amounts. Milestone 7 decided the UI generates both
// transaction ids (`id` and `transferTransactionId`) itself before
// submitting, the same way every other syncable row is client-generated
// (§5.1) — this mirrors the shape internal/db.CreateTransfer produces
// without calling it, since this milestone writes straight to the local
// Dexie replica rather than through the HTTP API.
export async function createTransfer(input: NewTransferInput): Promise<void> {
  const transferId = generateUUID();
  const groupId = generateUUID();

  await db.transaction("rw", db.transactions, db.outbox, async () => {
    const primary: Transaction = {
      id: input.id,
      account_id: input.accountId,
      category_id: null,
      payee_id: input.payeeId,
      parent_id: null,
      date: input.date,
      amount: input.amountMinor,
      cleared: false,
      notes: input.notes,
      transfer_id: transferId,
      deleted_at: null,
      ...nextHlc(),
    };
    const mirror: Transaction = {
      id: input.transferTransactionId,
      account_id: input.transferAccountId,
      category_id: null,
      payee_id: input.payeeId,
      parent_id: null,
      date: input.date,
      amount: -input.amountMinor,
      cleared: false,
      notes: input.notes,
      transfer_id: transferId,
      deleted_at: null,
      ...nextHlc(),
    };
    await db.transactions.add(primary);
    await db.transactions.add(mirror);
    // Both legs share one outbox group_id (§2.4) so the server commits or
    // rejects them atomically, never just one side. It's fresh per
    // operation, never the transfer_id: a later edit of this transfer must
    // not merge into the same unit as this create.
    await enqueueRowMutation("transactions", primary, groupId);
    await enqueueRowMutation("transactions", mirror, groupId);
  });
}

export interface TransferEditInput {
  date: string;
  payeeId: string | null;
  notes: string;
  amountMinor: number; // signed, relative to `transactionId`'s own account
}

// Edits both legs of an existing transfer together so they never drift
// apart: the shared descriptive fields (date/payee/notes) are copied
// verbatim, while the amount is mirrored with the opposite sign. `cleared`
// is deliberately left untouched here — it's a per-account reconciliation
// flag (the two legs can clear on different days in reality) and is only
// ever changed through the register's own per-row toggle, never through
// this edit path.
export async function updateTransfer(
  transactionId: string,
  changes: TransferEditInput,
): Promise<void> {
  await db.transaction("rw", db.transactions, db.outbox, async () => {
    const transaction = await db.transactions.get(transactionId);
    if (!transaction || transaction.transfer_id === null) return;
    // Fresh per operation (§2.4), so this edit can't merge into a unit with
    // an earlier, still-unsynced operation on the same transfer.
    const groupId = generateUUID();

    await db.transactions.update(transactionId, {
      date: changes.date,
      payee_id: changes.payeeId,
      notes: changes.notes,
      amount: changes.amountMinor,
      ...nextHlc(),
    });
    const updated = await db.transactions.get(transactionId);
    if (updated) await enqueueRowMutation("transactions", updated, groupId);

    const sibling = await db.transactions
      .where("transfer_id")
      .equals(transaction.transfer_id)
      .and((row) => row.id !== transactionId && row.deleted_at === null)
      .first();

    if (sibling) {
      await db.transactions.update(sibling.id, {
        date: changes.date,
        payee_id: changes.payeeId,
        notes: changes.notes,
        amount: -changes.amountMinor,
        ...nextHlc(),
      });
      const updatedSibling = await db.transactions.get(sibling.id);
      if (updatedSibling) await enqueueRowMutation("transactions", updatedSibling, groupId);
    }
  });
}

// A transfer is two independent rows linked only by transfer_id, with no
// foreign key between them, so deleting just one would leave the other
// pointing at money that silently vanished from one side of the ledger.
// Deleting either leg therefore soft-deletes both (§5.1: soft delete,
// never hard).
export async function deleteTransaction(transactionId: string): Promise<void> {
  await db.transaction("rw", db.transactions, db.outbox, async () => {
    const transaction = await db.transactions.get(transactionId);
    if (!transaction) return;
    // null for a standalone transaction (a lone mutation, per §2.4); for a
    // transfer leg, a fresh per-operation id so both sides of the delete
    // commit atomically together without merging into another operation's
    // unit.
    const groupId = transaction.transfer_id === null ? null : generateUUID();

    await db.transactions.update(transactionId, {
      deleted_at: Date.now(),
      ...nextHlc(),
    });
    const updated = await db.transactions.get(transactionId);
    if (updated) await enqueueRowMutation("transactions", updated, groupId);

    if (transaction.transfer_id !== null) {
      const sibling = await db.transactions
        .where("transfer_id")
        .equals(transaction.transfer_id)
        .and((row) => row.id !== transactionId && row.deleted_at === null)
        .first();

      if (sibling) {
        await db.transactions.update(sibling.id, {
          deleted_at: Date.now(),
          ...nextHlc(),
        });
        const updatedSibling = await db.transactions.get(sibling.id);
        if (updatedSibling) await enqueueRowMutation("transactions", updatedSibling, groupId);
      }
    }
  });
}

// Finds the other leg of a transfer transaction, for display (e.g.
// rendering "Transfer: <account name>" in the register). Returns undefined
// for a non-transfer transaction, or once its sibling has been deleted.
export function findTransferSibling(
  transaction: Transaction,
  allTransactions: Transaction[],
): Transaction | undefined {
  if (transaction.transfer_id === null) return undefined;
  return allTransactions.find(
    (candidate) =>
      candidate.transfer_id === transaction.transfer_id &&
      candidate.id !== transaction.id &&
      candidate.deleted_at === null,
  );
}
