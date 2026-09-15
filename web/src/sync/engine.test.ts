import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, setDeviceToken } from "../auth";
import { db } from "../db/db";
import { outbox } from "../db/helpers";
import type { Account, BudgetEntry, Transaction } from "../db/types";
import { getCursor, getSyncId } from "./cursor";
import { runSync } from "./engine";
import { getSyncStatus } from "./status";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn());
  Object.defineProperty(navigator, "onLine", { value: true, configurable: true });
});

const baseSync = {
  hlc_physical: 1_700_000_000_000,
  hlc_counter: 0,
  hlc_node_id: "node-1",
  server_version: undefined,
  deleted_at: null,
};

function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    name: "Checking",
    type: "checking",
    on_budget: true,
    closed: false,
    currency: "USD",
    notes: null,
    ...overrides,
  };
}

function okResponse(body: unknown): Response {
  return {
    ok: true,
    json: async () => body,
  } as Response;
}

function baseServerBody(overrides: Record<string, unknown> = {}) {
  return {
    server_version: 1,
    sync_id: "sync-1",
    schema_version: 1,
    results: [],
    changes: [],
    ...overrides,
  };
}

describe("runSync", () => {
  it("does nothing when the browser reports offline", async () => {
    Object.defineProperty(navigator, "onLine", { value: false, configurable: true });

    await runSync();

    expect(fetch).not.toHaveBeenCalled();
  });

  it("pushes unsynced outbox entries with the current cursor as `since`", async () => {
    const account = makeAccount();
    await outbox.enqueue({ table: "accounts", op: "upsert", group_id: null, row: account });
    vi.mocked(fetch).mockResolvedValue(okResponse(baseServerBody({ server_version: 5 })));

    await runSync();

    expect(fetch).toHaveBeenCalledWith(
      "/sync",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          since: 0,
          mutations: [{ table: "accounts", op: "upsert", group_id: null, row: account }],
        }),
      }),
    );
  });

  describe("device auth (§6.2)", () => {
    afterEach(() => {
      clearDeviceToken();
    });

    it("attaches this device's token as a bearer credential when it has one", async () => {
      setDeviceToken("device-token-123");
      vi.mocked(fetch).mockResolvedValue(okResponse(baseServerBody()));

      await runSync();

      expect(fetch).toHaveBeenCalledWith(
        "/sync",
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: "Bearer device-token-123" }),
        }),
      );
    });

    it("sends no Authorization header before this device has a token", async () => {
      vi.mocked(fetch).mockResolvedValue(okResponse(baseServerBody()));

      await runSync();

      const [, init] = vi.mocked(fetch).mock.calls[0];
      const headers = init?.headers as Record<string, string> | undefined;
      expect(headers?.Authorization).toBeUndefined();
    });
  });

  it("marks an applied mutation's outbox entry synced and stores the new cursor", async () => {
    const account = makeAccount();
    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: account,
    });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          server_version: 7,
          results: [{ table: "accounts", id: account.id, status: "applied" }],
        }),
      ),
    );

    await runSync();

    const entry = await db.outbox.get(id);
    expect(entry?.synced).toBe(true);
    expect(getCursor()).toBe(7);
  });

  it("marks a rejected_stale mutation synced too, since it's a resolved conflict, not a retryable error", async () => {
    const account = makeAccount();
    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: account,
    });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          results: [{ table: "accounts", id: account.id, status: "rejected_stale" }],
        }),
      ),
    );

    await runSync();

    expect((await db.outbox.get(id))?.synced).toBe(true);
  });

  it("leaves a rejected_invalid mutation unsynced so it's retried on the next cycle", async () => {
    const account = makeAccount();
    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: account,
    });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          results: [
            {
              table: "accounts",
              id: account.id,
              status: "rejected_invalid",
              error: { code: "SYNC_MUTATION_INVALID", message: "bad row" },
            },
          ],
        }),
      ),
    );

    await runSync();

    expect((await db.outbox.get(id))?.synced).toBe(false);
  });

  it("overwrites local rows with the winning rows sent back for a rejected_stale unit and marks its entries synced", async () => {
    const losingLocal = makeAccount({ name: "Local rename that lost" });
    await db.accounts.add(losingLocal);
    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: losingLocal,
    });
    const winner: Account = {
      ...losingLocal,
      name: "Rename from another device",
      hlc_physical: losingLocal.hlc_physical + 1000,
      server_version: 2,
    };
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          // The winner's server_version is below this device's cursor, so it
          // only arrives because the unit was stale (§2.4).
          server_version: 10,
          results: [{ table: "accounts", id: losingLocal.id, status: "rejected_stale" }],
          changes: [{ table: "accounts", row: winner }],
        }),
      ),
    );

    await runSync();

    expect(await db.accounts.get(losingLocal.id)).toEqual(winner);
    expect((await db.outbox.get(id))?.synced).toBe(true);
  });

  it("marks every entry in a group synced from the group's single reported result", async () => {
    const groupId = crypto.randomUUID();
    const left = makeAccount({ name: "From" });
    const right = makeAccount({ name: "To" });
    const leftId = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: groupId,
      row: left,
    });
    const rightId = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: groupId,
      row: right,
    });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          results: [{ table: "accounts", id: left.id, status: "applied" }],
        }),
      ),
    );

    await runSync();

    expect((await db.outbox.get(leftId))?.synced).toBe(true);
    expect((await db.outbox.get(rightId))?.synced).toBe(true);
  });

  it("applies pulled changes to the matching local table", async () => {
    const remoteAccount = makeAccount({ name: "From another device", server_version: 3 });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          server_version: 3,
          changes: [{ table: "accounts", row: remoteAccount }],
        }),
      ),
    );

    await runSync();

    expect(await db.accounts.get(remoteAccount.id)).toEqual(remoteAccount);
  });

  it("soft-deletes a local budget entry duplicating a pulled entry's (category_id, month) without enqueuing anything", async () => {
    const pair = { category_id: "cat-1", month: "2026-03" };
    const local: BudgetEntry = { id: "local-id", ...baseSync, ...pair, budgeted: 100 };
    await db.budget_entries.add(local);
    const remote: BudgetEntry = {
      id: "remote-id",
      ...baseSync,
      hlc_physical: baseSync.hlc_physical + 1,
      server_version: 4,
      ...pair,
      budgeted: 250,
    };
    vi.mocked(fetch).mockResolvedValue(
      okResponse(baseServerBody({ server_version: 4, changes: [{ table: "budget_entries", row: remote }] })),
    );

    await runSync();

    expect(await db.budget_entries.get(remote.id)).toEqual(remote);
    expect((await db.budget_entries.get(local.id))?.deleted_at).not.toBeNull();
    expect(await db.outbox.count()).toBe(0);
  });

  it("stores a pulled transaction's date as YYYY-MM-DD even if the server sent a timestamp", async () => {
    const remote: Transaction = {
      id: "txn-remote",
      ...baseSync,
      server_version: 2,
      account_id: "acc-1",
      category_id: null,
      payee_id: null,
      parent_id: null,
      date: "2026-09-01T00:00:00Z",
      amount: -500,
      cleared: false,
      notes: "",
      transfer_id: null,
    };
    vi.mocked(fetch).mockResolvedValue(
      okResponse(baseServerBody({ server_version: 2, changes: [{ table: "transactions", row: remote }] })),
    );

    await runSync();

    expect((await db.transactions.get(remote.id))?.date).toBe("2026-09-01");
  });

  describe("rejected_stale unit with rows the server never had (§2.4)", () => {
    const groupId = "group-stale";

    function budgetEntry(overrides: Partial<BudgetEntry>): BudgetEntry {
      return {
        id: crypto.randomUUID(),
        ...baseSync,
        category_id: "cat-target",
        month: "2026-03",
        budgeted: 1000,
        ...overrides,
      };
    }

    async function enqueueInGroup(row: BudgetEntry | Account, table: "budget_entries" | "accounts") {
      return outbox.enqueue({ table, op: "upsert", group_id: groupId, row });
    }

    it("purges a never-synced row that isn't returned, keeps an acknowledged one, and overwrites a returned one", async () => {
      const localOnly = budgetEntry({ id: "entry-local-only" });
      const acknowledged = budgetEntry({ id: "entry-acknowledged", category_id: "cat-other", server_version: 3 });
      const returnedLocal = makeAccount({ name: "Local rename" });
      await db.budget_entries.bulkAdd([localOnly, acknowledged]);
      await db.accounts.add(returnedLocal);
      const ids = [
        await enqueueInGroup(returnedLocal, "accounts"),
        await enqueueInGroup(localOnly, "budget_entries"),
        await enqueueInGroup(acknowledged, "budget_entries"),
      ];
      const winner: Account = { ...returnedLocal, name: "Server state", hlc_physical: returnedLocal.hlc_physical + 1, server_version: 5 };
      vi.mocked(fetch).mockResolvedValue(
        okResponse(
          baseServerBody({
            server_version: 9,
            results: [{ table: "accounts", id: returnedLocal.id, status: "rejected_stale" }],
            changes: [{ table: "accounts", row: winner }],
          }),
        ),
      );

      await runSync();

      expect(await db.budget_entries.get(localOnly.id)).toBeUndefined();
      expect(await db.budget_entries.get(acknowledged.id)).toEqual(acknowledged);
      expect(await db.accounts.get(returnedLocal.id)).toEqual(winner);
      for (const id of ids) {
        expect((await db.outbox.get(id))?.synced).toBe(true);
      }
    });

    it("keeps a never-synced row still referenced by an entry enqueued while the request was in flight", async () => {
      const localOnly = budgetEntry({ id: "entry-still-wanted" });
      await db.budget_entries.add(localOnly);
      await enqueueInGroup(localOnly, "budget_entries");
      const laterEdit: BudgetEntry = { ...localOnly, budgeted: 2000 };
      vi.mocked(fetch).mockImplementation(async () => {
        await outbox.enqueue({
          table: "budget_entries",
          op: "upsert",
          group_id: null,
          row: laterEdit,
        });
        return okResponse(
          baseServerBody({
            results: [{ table: "budget_entries", id: localOnly.id, status: "rejected_stale" }],
          }),
        );
      });

      await runSync();

      expect(await db.budget_entries.get(localOnly.id)).toBeDefined();
    });

    it("doesn't purge anything for an applied unit", async () => {
      const localOnly = budgetEntry({ id: "entry-applied" });
      await db.budget_entries.add(localOnly);
      await enqueueInGroup(localOnly, "budget_entries");
      vi.mocked(fetch).mockResolvedValue(
        okResponse(
          baseServerBody({
            results: [{ table: "budget_entries", id: localOnly.id, status: "applied" }],
          }),
        ),
      );

      await runSync();

      expect(await db.budget_entries.get(localOnly.id)).toBeDefined();
    });
  });

  it("pauses on a schema_version mismatch without applying changes or advancing the cursor", async () => {
    const remoteAccount = makeAccount({ server_version: 9 });
    vi.mocked(fetch).mockResolvedValue(
      okResponse(
        baseServerBody({
          server_version: 9,
          schema_version: 2,
          changes: [{ table: "accounts", row: remoteAccount }],
        }),
      ),
    );

    await runSync();

    expect(getSyncStatus().schemaMismatch).toBe(true);
    expect(await db.accounts.get(remoteAccount.id)).toBeUndefined();
    expect(getCursor()).toBe(0);
  });

  it("clears a prior schema mismatch once the server reports the expected version again", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(okResponse(baseServerBody({ schema_version: 2 })));
    await runSync();
    expect(getSyncStatus().schemaMismatch).toBe(true);

    vi.mocked(fetch).mockResolvedValueOnce(okResponse(baseServerBody({ schema_version: 1 })));
    await runSync();

    expect(getSyncStatus().schemaMismatch).toBe(false);
  });

  it("remembers the server's sync_id from the first response it sees", async () => {
    vi.mocked(fetch).mockResolvedValue(okResponse(baseServerBody({ sync_id: "sync-1" })));

    await runSync();

    expect(getSyncId()).toBe("sync-1");
  });

  it("discards a mismatched response and re-downloads from scratch when the server's sync_id changes", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      okResponse(baseServerBody({ sync_id: "sync-1", server_version: 5 })),
    );
    await runSync();
    expect(getSyncId()).toBe("sync-1");
    expect(getCursor()).toBe(5);

    const staleChange = makeAccount({ name: "From before the reset", server_version: 50 });
    const freshChange = makeAccount({ name: "From after the reset", server_version: 3 });
    vi.mocked(fetch)
      .mockResolvedValueOnce(
        okResponse(
          baseServerBody({
            sync_id: "sync-2",
            server_version: 50,
            changes: [{ table: "accounts", row: staleChange }],
          }),
        ),
      )
      .mockResolvedValueOnce(
        okResponse(
          baseServerBody({
            sync_id: "sync-2",
            server_version: 3,
            changes: [{ table: "accounts", row: freshChange }],
          }),
        ),
      );

    await runSync();

    expect(getSyncId()).toBe("sync-2");
    expect(getCursor()).toBe(3);
    expect(await db.accounts.get(staleChange.id)).toBeUndefined();
    expect(await db.accounts.get(freshChange.id)).toEqual(freshChange);

    const retryCall = vi.mocked(fetch).mock.calls[2];
    expect(JSON.parse(retryCall[1]?.body as string)).toMatchObject({ since: 0 });
  });

  it("leaves the outbox and cursor untouched when the request fails outright", async () => {
    const account = makeAccount();
    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: account,
    });
    vi.mocked(fetch).mockRejectedValue(new Error("network down"));

    await runSync();

    expect((await db.outbox.get(id))?.synced).toBe(false);
    expect(getCursor()).toBe(0);
  });
});
