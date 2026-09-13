import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Account, AccountType } from "../../db";
import { ACCOUNT_TYPES } from "./accountTypes";

export interface AccountFormValues {
  name: string;
  type: AccountType;
  on_budget: boolean;
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
  const [error, setError] = useState<string | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError(t("accounts.nameRequired"));
      return;
    }
    onSubmit({ name: trimmed, type, on_budget: onBudget });
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

      <div>
        <button type="submit">{t("accounts.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("accounts.cancel")}
        </button>
      </div>
    </form>
  );
}
