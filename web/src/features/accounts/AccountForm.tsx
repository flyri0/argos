import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Account, AccountType } from "../../db";
import { ACCOUNT_TYPES } from "./accountTypes";

export interface AccountFormValues {
  name: string;
  type: AccountType;
  on_budget: boolean;
  // Minor currency units (§5.1). Only meaningful on creation — see
  // project_spec.md §5.2 "Starting balance on account creation"; 0 when
  // the field is left blank, which creates no opening transaction at all.
  startingBalance: number;
}

function parseStartingBalance(text: string): number {
  const parsed = Number.parseFloat(text);
  return Number.isFinite(parsed) ? Math.round(parsed * 100) : 0;
}

interface AccountFormProps {
  initial?: Account;
  onSubmit: (values: AccountFormValues) => void;
  onCancel: () => void;
}

export function AccountForm({ initial, onSubmit, onCancel }: AccountFormProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial?.name ?? "");
  const [type, setType] = useState<AccountType>(initial?.type ?? "checking");
  const [onBudget, setOnBudget] = useState(initial?.on_budget ?? true);
  const [startingBalanceText, setStartingBalanceText] = useState("");
  const [error, setError] = useState<string | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError(t("accounts.nameRequired"));
      return;
    }
    onSubmit({
      name: trimmed,
      type,
      on_budget: onBudget,
      startingBalance: initial ? 0 : parseStartingBalance(startingBalanceText),
    });
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>{initial ? t("accounts.editTitle") : t("accounts.newTitle")}</h2>

      <div>
        <label htmlFor="account-name">{t("accounts.name")}</label>
        <input
          id="account-name"
          value={name}
          placeholder={t("accounts.namePlaceholder")}
          onChange={(event) => setName(event.target.value)}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <label htmlFor="account-type">{t("accounts.type")}</label>
        <select
          id="account-type"
          value={type}
          onChange={(event) => setType(event.target.value as AccountType)}
        >
          {ACCOUNT_TYPES.map((value) => (
            <option key={value} value={value}>
              {t(`accountType.${value}`)}
            </option>
          ))}
        </select>
      </div>

      <div>
        <label>
          <input
            type="checkbox"
            checked={onBudget}
            onChange={(event) => setOnBudget(event.target.checked)}
          />
          {t("accounts.onBudget")}
        </label>
      </div>

      {!initial && (
        <div>
          <label htmlFor="account-starting-balance">{t("accounts.startingBalance")}</label>
          <input
            id="account-starting-balance"
            inputMode="decimal"
            value={startingBalanceText}
            placeholder="0.00"
            onChange={(event) => setStartingBalanceText(event.target.value)}
          />
        </div>
      )}

      <div>
        <button type="submit">{t("accounts.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("accounts.cancel")}
        </button>
      </div>
    </form>
  );
}
