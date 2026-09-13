import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { CategoryGroup } from "../../db";

export interface CategoryGroupFormValues {
  name: string;
  is_income: boolean;
}

interface CategoryGroupFormProps {
  initial?: CategoryGroup;
  // Only offered when creating a group and no income group exists yet
  // (§5.2: "exactly one income group exists and cannot be deleted") —
  // once set, it isn't editable through this form.
  canMarkIncome: boolean;
  onSubmit: (values: CategoryGroupFormValues) => void;
  onCancel: () => void;
}

export function CategoryGroupForm({
  initial,
  canMarkIncome,
  onSubmit,
  onCancel,
}: CategoryGroupFormProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial?.name ?? "");
  const [isIncome, setIsIncome] = useState(initial?.is_income ?? false);
  const [error, setError] = useState<string | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError(t("categories.groupNameRequired"));
      return;
    }
    onSubmit({ name: trimmed, is_income: isIncome });
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>
        {initial ? t("categories.editGroupTitle") : t("categories.newGroupTitle")}
      </h2>

      <div>
        <label htmlFor="group-name">{t("categories.groupName")}</label>
        <input
          id="group-name"
          value={name}
          placeholder={t("categories.groupNamePlaceholder")}
          onChange={(event) => setName(event.target.value)}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      {!initial && canMarkIncome && (
        <div>
          <label>
            <input
              type="checkbox"
              checked={isIncome}
              onChange={(event) => setIsIncome(event.target.checked)}
            />
            {t("categories.incomeGroupLabel")}
          </label>
        </div>
      )}

      <div>
        <button type="submit">{t("categories.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("categories.cancel")}
        </button>
      </div>
    </form>
  );
}
