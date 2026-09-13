import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { AccountsScreen } from "./features/accounts/AccountsScreen";
import { BudgetScreen } from "./features/budget/BudgetScreen";
import { CategoriesScreen } from "./features/categories/CategoriesScreen";
import { DevicesScreen } from "./features/devices/DevicesScreen";
import { PayeesScreen } from "./features/payees/PayeesScreen";
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

  useEffect(() => startSyncWorker(), []);

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
        {tab === "settings" && <DevicesScreen />}
      </main>
    </div>
  );
}

export default App;
