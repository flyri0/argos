import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { Account } from "../../db";

interface AccountListProps {
  accounts: Account[];
  balances: Map<string, number>;
  onAdd: () => void;
  onEdit: (account: Account) => void;
  onToggleClosed: (account: Account) => void;
}

export function AccountList({
  accounts,
  balances,
  onAdd,
  onEdit,
  onToggleClosed,
}: AccountListProps) {
  const { t } = useTranslation();
  const [confirmingCloseId, setConfirmingCloseId] = useState<string | null>(
    null,
  );

  return (
    <section>
      <div>
        <h2>{t("nav.accounts")}</h2>
        <button type="button" onClick={onAdd}>
          {t("accounts.add")}
        </button>
      </div>

      {accounts.length === 0 ? (
        <p>{t("accounts.empty")}</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>{t("accounts.name")}</th>
              <th>{t("accounts.type")}</th>
              <th>{t("accounts.balance")}</th>
              <th aria-hidden="true" />
            </tr>
          </thead>
          <tbody>
            {accounts.map((account) => (
              <tr key={account.id}>
                <td>
                  {account.name}
                  {account.closed && ` (${t("accounts.closedBadge")})`}
                </td>
                <td>{t(`accountType.${account.type}`)}</td>
                <td>
                  {formatAmount(balances.get(account.id) ?? 0, account.currency)}
                </td>
                <td>
                  <button type="button" onClick={() => onEdit(account)}>
                    {t("accounts.edit")}
                  </button>
                  {confirmingCloseId === account.id ? (
                    <>
                      <span>{t("accounts.confirmClose")}</span>
                      <button
                        type="button"
                        onClick={() => {
                          onToggleClosed(account);
                          setConfirmingCloseId(null);
                        }}
                      >
                        {t("accounts.confirm")}
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmingCloseId(null)}
                      >
                        {t("accounts.cancel")}
                      </button>
                    </>
                  ) : account.closed ? (
                    <button type="button" onClick={() => onToggleClosed(account)}>
                      {t("accounts.reopen")}
                    </button>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setConfirmingCloseId(account.id)}
                    >
                      {t("accounts.close")}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

// Accounts always carry an ISO 4217 code (§5.2), but real multi-currency
// support is out of MVP scope, so a code Intl doesn't recognize falls back
// to a plain number rather than crashing the list.
function formatAmount(minorUnits: number, currency = "USD"): string {
  const amount = minorUnits / 100;
  try {
    return amount.toLocaleString(undefined, { style: "currency", currency });
  } catch {
    return `${amount.toFixed(2)} ${currency}`;
  }
}
