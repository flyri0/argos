import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearDeviceToken, setDeviceToken } from "../auth";
import { db } from "../db/db";
import { outbox } from "../db/helpers";
import type { Account } from "../db/types";
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
      expect((init?.headers as Record<string, string>).Authorization).toBeUndefined();
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
