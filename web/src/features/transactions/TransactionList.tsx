import { useTranslation } from "react-i18next";

import type { Account, Category, Payee, Transaction } from "../../db";
import { formatCurrency } from "../../lib/currency";

interface TransactionListProps {
  transactions: Transaction[];
  categoriesById: Map<string, Category>;
  payeesById: Map<string, Payee>;
  accountsById: Map<string, Account>;
  currency: string;
  siblingFor: (transaction: Transaction) => Transaction | undefined;
  onEdit: (transaction: Transaction) => void;
  onDelete: (transaction: Transaction) => void;
  onToggleCleared: (transaction: Transaction) => void;
}

export function TransactionList({
  transactions,
  categoriesById,
  payeesById,
  accountsById,
  currency,
  siblingFor,
  onEdit,
  onDelete,
  onToggleCleared,
}: TransactionListProps) {
  const { t } = useTranslation();

  if (transactions.length === 0) {
    return <p>{t("register.empty")}</p>;
  }

  return (
    <table>
      <thead>
        <tr>
          <th>{t("register.date")}</th>
          <th>{t("register.payee")}</th>
          <th>{t("register.category")}</th>
          <th>{t("register.notes")}</th>
          <th>{t("register.amount")}</th>
          <th aria-hidden="true" />
        </tr>
      </thead>
      <tbody>
        {transactions.map((transaction) => {
          const sibling = siblingFor(transaction);
          const categoryLabel = sibling
            ? t("register.transferLabel", {
                account: accountsById.get(sibling.account_id)?.name ?? "",
              })
            : transaction.category_id
              ? (categoriesById.get(transaction.category_id)?.name ?? "")
              : t("register.noCategory");

          return (
            <tr key={transaction.id}>
              <td>{transaction.date}</td>
              <td>
                {transaction.payee_id
                  ? (payeesById.get(transaction.payee_id)?.name ?? "")
                  : t("register.noPayee")}
              </td>
              <td>{categoryLabel}</td>
              <td>{transaction.notes}</td>
              <td>{formatCurrency(transaction.amount, currency)}</td>
              <td>
                {transaction.cleared && `(${t("register.clearedBadge")}) `}
                <button type="button" onClick={() => onToggleCleared(transaction)}>
                  {transaction.cleared
                    ? t("register.markUncleared")
                    : t("register.markCleared")}
                </button>
                <button type="button" onClick={() => onEdit(transaction)}>
                  {t("register.edit")}
                </button>
                <button type="button" onClick={() => onDelete(transaction)}>
                  {t("register.delete")}
                </button>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
