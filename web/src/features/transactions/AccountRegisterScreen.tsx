import { useMemo, useState } from "react";
import { useLiveQuery } from "dexie-react-hooks";
import { useTranslation } from "react-i18next";

import {
  accounts,
  categories,
  computeBalance,
  createTransfer,
  deleteTransaction,
  findTransferSibling,
  nextHlc,
  payees,
  setTransferLegCleared,
  transactions,
  updateTransfer,
  type Account,
  type Category,
  type Payee,
  type Transaction,
} from "../../db";
import { formatCurrency } from "../../lib/currency";
import { generateUUID } from "../../lib/uuid";
import { Modal } from "../../components/Modal";
import { TransactionForm, type TransactionFormValues } from "./TransactionForm";
import { TransactionList } from "./TransactionList";

type Dialog =
  | { kind: "add" }
  | { kind: "edit"; transaction: Transaction }
  | { kind: "delete"; transaction: Transaction }
  | null;

interface AccountRegisterScreenProps {
  account: Account;
  onBack: () => void;
}

function byId<T extends { id: string }>(rows: T[]): Map<string, T> {
  const map = new Map<string, T>();
  for (const row of rows) map.set(row.id, row);
  return map;
}

export function AccountRegisterScreen({ account, onBack }: AccountRegisterScreenProps) {
  const { t } = useTranslation();
  const [dialog, setDialog] = useState<Dialog>(null);

  const allAccounts = useLiveQuery(() => accounts.list());
  const allCategories = useLiveQuery(() => categories.list());
  const allPayees = useLiveQuery(() => payees.list());
  const allTransactions = useLiveQuery(() => transactions.list());

  const openCategories = useMemo(
    () => (allCategories ?? []).filter((category) => category.deleted_at === null),
    [allCategories],
  );
  const openPayees = useMemo(
    () => (allPayees ?? []).filter((payee) => payee.deleted_at === null),
    [allPayees],
  );

  const accountsById = useMemo<Map<string, Account>>(
    () => byId(allAccounts ?? []),
    [allAccounts],
  );
  const categoriesById = useMemo<Map<string, Category>>(
    () => byId(openCategories),
    [openCategories],
  );
  const payeesById = useMemo<Map<string, Payee>>(() => byId(openPayees), [openPayees]);

  const transferTargets = useMemo(
    () =>
      (allAccounts ?? []).filter(
        (candidate) =>
          candidate.id !== account.id &&
          candidate.deleted_at === null &&
          !candidate.closed,
      ),
    [allAccounts, account.id],
  );

  const accountTransactions = useMemo(
    () =>
      (allTransactions ?? [])
        .filter((row) => row.account_id === account.id && row.deleted_at === null)
        .sort((a, b) => {
          if (a.date !== b.date) return a.date < b.date ? 1 : -1;
          return b.hlc_physical - a.hlc_physical;
        }),
    [allTransactions, account.id],
  );

  const balance = useMemo(
    () =>
      computeBalance((allTransactions ?? []).filter((row) => row.account_id === account.id)),
    [allTransactions, account.id],
  );

  function siblingFor(transaction: Transaction) {
    return findTransferSibling(transaction, allTransactions ?? []);
  }

  async function handleCreate(values: TransactionFormValues) {
    if (values.transferAccountId) {
      await createTransfer({
        id: generateUUID(),
        transferTransactionId: generateUUID(),
        accountId: account.id,
        transferAccountId: values.transferAccountId,
        payeeId: values.payee_id,
        date: values.date,
        amountMinor: values.amountMinor,
        notes: values.notes,
      });
    } else {
      await transactions.create({
        id: generateUUID(),
        account_id: account.id,
        category_id: values.category_id,
        payee_id: values.payee_id,
        parent_id: null,
        date: values.date,
        amount: values.amountMinor,
        cleared: false,
        notes: values.notes,
        transfer_id: null,
        deleted_at: null,
        ...nextHlc(),
      });
    }
    setDialog(null);
  }

  async function handleEdit(transaction: Transaction, values: TransactionFormValues) {
    if (transaction.transfer_id !== null) {
      await updateTransfer(transaction.id, {
        date: values.date,
        payeeId: values.payee_id,
        notes: values.notes,
        amountMinor: values.amountMinor,
      });
    } else {
      await transactions.update(transaction.id, {
        date: values.date,
        payee_id: values.payee_id,
        category_id: values.category_id,
        notes: values.notes,
        amount: values.amountMinor,
        ...nextHlc(),
      });
    }
    setDialog(null);
  }

  async function handleConfirmDelete(transaction: Transaction) {
    await deleteTransaction(transaction.id);
    setDialog(null);
  }

  async function handleToggleCleared(transaction: Transaction) {
    if (transaction.transfer_id !== null) {
      await setTransferLegCleared(transaction.id, !transaction.cleared);
      return;
    }
    await transactions.update(transaction.id, {
      cleared: !transaction.cleared,
      ...nextHlc(),
    });
  }

  if (allAccounts === undefined || allTransactions === undefined) {
    return <p>{t("app.loading")}</p>;
  }

  return (
    <section>
      <div>
        <button type="button" onClick={onBack}>
          {t("register.back")}
        </button>
        <h2>
          {account.name} — {formatCurrency(balance, account.currency)}
        </h2>
        {account.closed ? (
          <p>{t("register.closedNotice")}</p>
        ) : (
          <button type="button" onClick={() => setDialog({ kind: "add" })}>
            {t("register.add")}
          </button>
        )}
      </div>

      <TransactionList
        transactions={accountTransactions}
        categoriesById={categoriesById}
        payeesById={payeesById}
        accountsById={accountsById}
        currency={account.currency}
        siblingFor={siblingFor}
        onEdit={(transaction) => setDialog({ kind: "edit", transaction })}
        onDelete={(transaction) => setDialog({ kind: "delete", transaction })}
        onToggleCleared={handleToggleCleared}
      />

      {dialog?.kind === "add" && (
        <Modal onDismiss={() => setDialog(null)}>
          <TransactionForm
            isTransfer={false}
            transferTargets={transferTargets}
            categories={openCategories}
            payees={openPayees}
            onSubmit={handleCreate}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "edit" &&
        (() => {
          const transaction = dialog.transaction;
          const sibling = siblingFor(transaction);
          return (
            <Modal onDismiss={() => setDialog(null)}>
              <TransactionForm
                initial={transaction}
                isTransfer={transaction.transfer_id !== null}
                transferAccountName={
                  sibling ? accountsById.get(sibling.account_id)?.name : undefined
                }
                transferTargets={transferTargets}
                categories={openCategories}
                payees={openPayees}
                onSubmit={(values) => handleEdit(transaction, values)}
                onCancel={() => setDialog(null)}
              />
            </Modal>
          );
        })()}

      {dialog?.kind === "delete" &&
        (() => {
          const transaction = dialog.transaction;
          const sibling = siblingFor(transaction);
          return (
            <Modal onDismiss={() => setDialog(null)}>
              <p>{t("register.confirmDelete")}</p>
              {sibling && (
                <p>
                  {t("register.confirmDeleteTransferNote", {
                    account: accountsById.get(sibling.account_id)?.name ?? "",
                  })}
                </p>
              )}
              <button type="button" onClick={() => handleConfirmDelete(transaction)}>
                {t("register.confirm")}
              </button>
              <button type="button" onClick={() => setDialog(null)}>
                {t("register.cancel")}
              </button>
            </Modal>
          );
        })()}
    </section>
  );
}
