import { db } from "./db";
import { hasPersistedHlc, observeHlc } from "./hlc";
import type { SyncMeta } from "./types";

// §2.3: a device with no persisted clock (first start after this was added,
// or cleared site storage while IndexedDB survived) seeds it from the
// greatest HLC in its local replica. Without this, a device whose wall clock
// is behind would stamp its next edit lower than rows it already holds and
// have it rejected as stale. Must finish before the first sync or write.
export async function seedHlcFromLocalData(): Promise<void> {
  if (hasPersistedHlc()) return;

  let max: SyncMeta | undefined;
  const visit = (row: SyncMeta) => {
    if (
      max === undefined ||
      row.hlc_physical > max.hlc_physical ||
      (row.hlc_physical === max.hlc_physical && row.hlc_counter > max.hlc_counter)
    ) {
      max = row;
    }
  };

  await db.accounts.each(visit);
  await db.category_groups.each(visit);
  await db.categories.each(visit);
  await db.payees.each(visit);
  await db.transactions.each(visit);
  await db.budget_entries.each(visit);

  if (max !== undefined) {
    observeHlc(max);
  }
}
