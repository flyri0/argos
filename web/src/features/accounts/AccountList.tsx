import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { Account } from "../../db";
import { formatCurrency } from "../../lib/currency";

interface AccountListProps {
  accounts: Account[];
  balances: Map<string, number>;
  onAdd: () => void;
  onOpenRegister: (account: Account) => void;
  onEdit: (account: Account) => void;
  onToggleClosed: (account: Account) => void;
}

export function AccountList({
  accounts,
  balances,
  onAdd,
  onOpenRegister,
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
                  <button type="button" onClick={() => onOpenRegister(account)}>
                    {account.name}
                    {account.closed && ` (${t("accounts.closedBadge")})`}
                  </button>
                </td>
                <td>{t(`accountType.${account.type}`)}</td>
                <td>
                  {formatCurrency(balances.get(account.id) ?? 0, account.currency)}
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
