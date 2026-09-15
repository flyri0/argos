import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { seedHlcFromLocalData } from "./db";
import { AccountsScreen } from "./features/accounts/AccountsScreen";
import { BudgetScreen } from "./features/budget/BudgetScreen";
import { CategoriesScreen } from "./features/categories/CategoriesScreen";
import { PayeesScreen } from "./features/payees/PayeesScreen";
import { SettingsScreen } from "./features/settings/SettingsScreen";
import { startSyncWorker } from "./sync";
import { SyncNotice } from "./sync/SyncNotice";

type Tab = "accounts" | "budget" | "categories" | "payees" | "settings";

const TABS: { id: Tab; labelKey: string }[] = [
  { id: "accounts", labelKey: "nav.accounts" },
  { id: "budget", labelKey: "nav.budget" },
  { id: "categories", labelKey: "nav.categories" },
  { id: "payees", labelKey: "nav.payees" },
  { id: "settings", labelKey: "nav.settings" },
];

function App() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>("accounts");
  const [clockReady, setClockReady] = useState(false);

  // §2.3: the HLC must be seeded from the local replica before the first
  // sync or any write, or a device with a lagging clock stamps edits lower
  // than rows it already holds. A seeding failure (e.g. IndexedDB
  // unavailable) must not lock the user out, so the app starts regardless.
  useEffect(() => {
    let cancelled = false;
    let stopSyncWorker: (() => void) | undefined;
    seedHlcFromLocalData()
      .catch(() => undefined)
      .then(() => {
        if (cancelled) return;
        setClockReady(true);
        stopSyncWorker = startSyncWorker();
      });
    return () => {
      cancelled = true;
      stopSyncWorker?.();
    };
  }, []);

  if (!clockReady) {
    return <p>{t("app.loading")}</p>;
  }

  return (
    <div>
      <header>
        <h1>{t("app.title")}</h1>
        <SyncNotice />
        <nav>
          {TABS.map(({ id, labelKey }) => (
            <button
              key={id}
              type="button"
              aria-current={tab === id ? "page" : undefined}
              onClick={() => setTab(id)}
            >
              {t(labelKey)}
            </button>
          ))}
        </nav>
      </header>
      <main>
        {tab === "accounts" && <AccountsScreen />}
        {tab === "budget" && <BudgetScreen />}
        {tab === "categories" && <CategoriesScreen />}
        {tab === "payees" && <PayeesScreen />}
        {tab === "settings" && <SettingsScreen />}
      </main>
    </div>
  );
}

export default App;
