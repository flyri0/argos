import { useTranslation } from "react-i18next";

import { dismissConflicts, useSyncStatus } from "./status";

// §2.3: a schema_version mismatch pauses push/pull for the rest of this
// session and shows a persistent notice, but must never block the rest of
// the app — this renders nothing at all until that happens, and even then
// is just a banner alongside the normal UI, not a blocking overlay. The
// conflict notice (§2.4) tells the user some local edits were rolled back
// to another device's changes; it stays until dismissed.
export function SyncNotice() {
  const { t } = useTranslation();
  const { schemaMismatch, conflictCount } = useSyncStatus();

  if (!schemaMismatch && conflictCount === 0) return null;

  return (
    <>
      {schemaMismatch && <div role="status">{t("sync.schemaMismatch")}</div>}
      {conflictCount > 0 && (
        <div role="alert">
          {t("sync.conflictNotice")}{" "}
          <button type="button" onClick={dismissConflicts}>
            {t("sync.dismiss")}
          </button>
        </div>
      )}
    </>
  );
}
