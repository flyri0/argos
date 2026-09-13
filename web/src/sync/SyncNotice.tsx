import { useTranslation } from "react-i18next";

import { useSyncStatus } from "./status";

// §2.3: a schema_version mismatch pauses push/pull for the rest of this
// session and shows a persistent notice, but must never block the rest of
// the app — this renders nothing at all until that happens, and even then
// is just a banner alongside the normal UI, not a blocking overlay.
export function SyncNotice() {
  const { t } = useTranslation();
  const { schemaMismatch } = useSyncStatus();

  if (!schemaMismatch) return null;

  return <div role="status">{t("sync.schemaMismatch")}</div>;
}
