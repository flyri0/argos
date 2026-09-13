import { useMemo, useState } from "react";
import { useLiveQuery } from "dexie-react-hooks";
import { useTranslation } from "react-i18next";

import {
  accounts,
  computeBalance,
  nextHlc,
  transactions,
  type Account,
} from "../../db";
import { AccountRegisterScreen } from "../transactions/AccountRegisterScreen";
import { AccountForm, type AccountFormValues } from "./AccountForm";
import { AccountList } from "./AccountList";

type View =
  | { mode: "list" }
  | { mode: "create" }
  | { mode: "edit"; account: Account }
  | { mode: "register"; account: Account };

export function AccountsScreen() {
  const { t } = useTranslation();
  const [view, setView] = useState<View>({ mode: "list" });

  // useLiveQuery re-runs (and re-renders) whenever the underlying Dexie
  // tables change, so create/edit/close below need no manual refetch.
  const allAccounts = useLiveQuery(() => accounts.list());
  const allTransactions = useLiveQuery(() => transactions.list());

  const openAccounts = useMemo(
    () => (allAccounts ?? []).filter((account) => account.deleted_at === null),
    [allAccounts],
  );

  const balances = useMemo(() => {
    const byAccount = new Map<string, number>();
    for (const account of openAccounts) {
      const forAccount = (allTransactions ?? []).filter(
        (transaction) => transaction.account_id === account.id,
      );
      byAccount.set(account.id, computeBalance(forAccount));
    }
    return byAccount;
  }, [openAccounts, allTransactions]);

  async function handleCreate(values: AccountFormValues) {
    await accounts.create({
      id: crypto.randomUUID(),
      ...values,
      closed: false,
      currency: "USD",
      notes: null,
      deleted_at: null,
      ...nextHlc(),
    });
    setView({ mode: "list" });
  }

  async function handleUpdate(id: string, values: AccountFormValues) {
    await accounts.update(id, { ...values, ...nextHlc() });
    setView({ mode: "list" });
  }

  async function handleToggleClosed(account: Account) {
    await accounts.update(account.id, {
      closed: !account.closed,
      ...nextHlc(),
    });
  }

  if (allAccounts === undefined) {
    return <p>{t("app.loading")}</p>;
  }

  if (view.mode === "create") {
    return (
      <AccountForm
        onSubmit={handleCreate}
        onCancel={() => setView({ mode: "list" })}
      />
    );
  }

  if (view.mode === "edit") {
    return (
      <AccountForm
        initial={view.account}
        onSubmit={(values) => handleUpdate(view.account.id, values)}
        onCancel={() => setView({ mode: "list" })}
      />
    );
  }

  if (view.mode === "register") {
    return (
      <AccountRegisterScreen
        account={openAccounts.find((a) => a.id === view.account.id) ?? view.account}
        onBack={() => setView({ mode: "list" })}
      />
    );
  }

  return (
    <AccountList
      accounts={openAccounts}
      balances={balances}
      onAdd={() => setView({ mode: "create" })}
      onOpenRegister={(account) => setView({ mode: "register", account })}
      onEdit={(account) => setView({ mode: "edit", account })}
      onToggleClosed={handleToggleClosed}
    />
  );
}
