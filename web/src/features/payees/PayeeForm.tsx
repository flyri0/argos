import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Payee } from "../../db";

export interface PayeeFormValues {
  name: string;
}

interface PayeeFormProps {
  initial?: Payee;
  onSubmit: (values: PayeeFormValues) => void;
  onCancel: () => void;
}

export function PayeeForm({ initial, onSubmit, onCancel }: PayeeFormProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial?.name ?? "");
  const [error, setError] = useState<string | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError(t("payees.nameRequired"));
      return;
    }
    onSubmit({ name: trimmed });
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>{initial ? t("payees.editTitle") : t("payees.newTitle")}</h2>

      <div>
        <label htmlFor="payee-name">{t("payees.name")}</label>
        <input
          id="payee-name"
          value={name}
          placeholder={t("payees.namePlaceholder")}
          onChange={(event) => setName(event.target.value)}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <button type="submit">{t("payees.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("payees.cancel")}
        </button>
      </div>
    </form>
  );
}
