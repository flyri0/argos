import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Category, CategoryGroup } from "../../db";

export interface CategoryFormValues {
  name: string;
  group_id: string;
}

interface CategoryFormProps {
  initial?: Category;
  // The group a new category is being added under; ignored when editing
  // (renaming a category doesn't move it between groups in this UI).
  defaultGroupId: string;
  groups: CategoryGroup[];
  onSubmit: (values: CategoryFormValues) => void;
  onCancel: () => void;
}

export function CategoryForm({
  initial,
  defaultGroupId,
  groups,
  onSubmit,
  onCancel,
}: CategoryFormProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial?.name ?? "");
  const [groupId, setGroupId] = useState(initial?.group_id ?? defaultGroupId);
  const [error, setError] = useState<string | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError(t("categories.nameRequired"));
      return;
    }
    onSubmit({ name: trimmed, group_id: groupId });
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>
        {initial ? t("categories.editCategoryTitle") : t("categories.newCategoryTitle")}
      </h2>

      <div>
        <label htmlFor="category-name">{t("categories.name")}</label>
        <input
          id="category-name"
          value={name}
          placeholder={t("categories.namePlaceholder")}
          onChange={(event) => setName(event.target.value)}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <label htmlFor="category-group">{t("categories.group")}</label>
        <select
          id="category-group"
          value={groupId}
          onChange={(event) => setGroupId(event.target.value)}
        >
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
      </div>

      <div>
        <button type="submit">{t("categories.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("categories.cancel")}
        </button>
      </div>
    </form>
  );
}
