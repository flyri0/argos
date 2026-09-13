import { useMemo, useState } from "react";
import { useLiveQuery } from "dexie-react-hooks";
import { useTranslation } from "react-i18next";

import {
  budgetEntries,
  categories,
  categoryGroups,
  isCategoryInUse,
  nextHlc,
  reassignCategory,
  transactions,
  type Category,
  type CategoryGroup,
} from "../../db";
import { Modal } from "../../components/Modal";
import { ReassignPicker } from "../../components/ReassignPicker";
import { CategoryForm, type CategoryFormValues } from "./CategoryForm";
import { CategoryGroupForm, type CategoryGroupFormValues } from "./CategoryGroupForm";

type Dialog =
  | { kind: "addGroup" }
  | { kind: "editGroup"; group: CategoryGroup }
  | { kind: "addCategory"; groupId: string }
  | { kind: "editCategory"; category: Category }
  | { kind: "deleteCategory"; category: Category }
  | null;

function nextSortOrder(items: { sort_order: number }[]): number {
  return items.reduce((max, item) => Math.max(max, item.sort_order), -1) + 1;
}

export function CategoriesScreen() {
  const { t } = useTranslation();
  const [dialog, setDialog] = useState<Dialog>(null);

  const allGroups = useLiveQuery(() => categoryGroups.list());
  const allCategories = useLiveQuery(() => categories.list());
  const allTransactions = useLiveQuery(() => transactions.list());
  const allBudgetEntries = useLiveQuery(() => budgetEntries.list());

  const openGroups = useMemo(
    () =>
      (allGroups ?? [])
        .filter((group) => group.deleted_at === null)
        .sort((a, b) => a.sort_order - b.sort_order),
    [allGroups],
  );

  const openCategories = useMemo(
    () => (allCategories ?? []).filter((category) => category.deleted_at === null),
    [allCategories],
  );

  const categoriesByGroup = useMemo(() => {
    const map = new Map<string, Category[]>();
    for (const category of openCategories) {
      const list = map.get(category.group_id) ?? [];
      list.push(category);
      map.set(category.group_id, list);
    }
    for (const list of map.values()) {
      list.sort((a, b) => a.sort_order - b.sort_order);
    }
    return map;
  }, [openCategories]);

  const hasIncomeGroup = openGroups.some((group) => group.is_income);

  async function handleAddGroup(values: CategoryGroupFormValues) {
    await categoryGroups.create({
      id: crypto.randomUUID(),
      ...values,
      sort_order: nextSortOrder(openGroups),
      deleted_at: null,
      ...nextHlc(),
    });
    setDialog(null);
  }

  async function handleRenameGroup(id: string, values: CategoryGroupFormValues) {
    await categoryGroups.update(id, { name: values.name, ...nextHlc() });
    setDialog(null);
  }

  async function handleAddCategory(groupId: string, values: CategoryFormValues) {
    await categories.create({
      id: crypto.randomUUID(),
      name: values.name,
      group_id: values.group_id,
      hidden: false,
      sort_order: nextSortOrder(categoriesByGroup.get(groupId) ?? []),
      notes: null,
      deleted_at: null,
      ...nextHlc(),
    });
    setDialog(null);
  }

  async function handleEditCategory(id: string, values: CategoryFormValues) {
    await categories.update(id, { name: values.name, ...nextHlc() });
    setDialog(null);
  }

  async function handleToggleHidden(category: Category) {
    await categories.update(category.id, {
      hidden: !category.hidden,
      ...nextHlc(),
    });
  }

  async function handleConfirmSimpleDelete(category: Category) {
    await categories.update(category.id, { deleted_at: Date.now(), ...nextHlc() });
    setDialog(null);
  }

  async function handleConfirmReassign(category: Category, targetId: string) {
    await reassignCategory(category.id, targetId);
    setDialog(null);
  }

  if (allGroups === undefined || allCategories === undefined) {
    return <p>{t("app.loading")}</p>;
  }

  return (
    <section>
      <div>
        <h2>{t("nav.categories")}</h2>
        <button type="button" onClick={() => setDialog({ kind: "addGroup" })}>
          {t("categories.addGroup")}
        </button>
      </div>

      {openGroups.length === 0 ? (
        <p>{t("categories.emptyGroups")}</p>
      ) : (
        openGroups.map((group) => (
          <div key={group.id}>
            <h3>
              {group.name}
              <button type="button" onClick={() => setDialog({ kind: "editGroup", group })}>
                {t("categories.renameGroup")}
              </button>
              <button
                type="button"
                onClick={() => setDialog({ kind: "addCategory", groupId: group.id })}
              >
                {t("categories.addCategory")}
              </button>
            </h3>

            {(categoriesByGroup.get(group.id) ?? []).length === 0 ? (
              <p>{t("categories.empty")}</p>
            ) : (
              <ul>
                {(categoriesByGroup.get(group.id) ?? []).map((category) => (
                  <li key={category.id}>
                    {category.name}
                    {category.hidden && ` (${t("categories.hiddenBadge")})`}
                    <button
                      type="button"
                      onClick={() => setDialog({ kind: "editCategory", category })}
                    >
                      {t("categories.edit")}
                    </button>
                    <button type="button" onClick={() => handleToggleHidden(category)}>
                      {category.hidden ? t("categories.unhide") : t("categories.hide")}
                    </button>
                    <button
                      type="button"
                      onClick={() => setDialog({ kind: "deleteCategory", category })}
                    >
                      {t("categories.delete")}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ))
      )}

      {dialog?.kind === "addGroup" && (
        <Modal onDismiss={() => setDialog(null)}>
          <CategoryGroupForm
            canMarkIncome={!hasIncomeGroup}
            onSubmit={handleAddGroup}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "editGroup" && (
        <Modal onDismiss={() => setDialog(null)}>
          <CategoryGroupForm
            initial={dialog.group}
            canMarkIncome={false}
            onSubmit={(values) => handleRenameGroup(dialog.group.id, values)}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "addCategory" && (
        <Modal onDismiss={() => setDialog(null)}>
          <CategoryForm
            defaultGroupId={dialog.groupId}
            groups={openGroups}
            onSubmit={(values) => handleAddCategory(dialog.groupId, values)}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "editCategory" && (
        <Modal onDismiss={() => setDialog(null)}>
          <CategoryForm
            initial={dialog.category}
            defaultGroupId={dialog.category.group_id}
            groups={openGroups}
            onSubmit={(values) => handleEditCategory(dialog.category.id, values)}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "deleteCategory" &&
        (() => {
          const category = dialog.category;
          const inUse = isCategoryInUse(
            category.id,
            allTransactions ?? [],
            allBudgetEntries ?? [],
          );

          if (!inUse) {
            return (
              <Modal onDismiss={() => setDialog(null)}>
                <p>{t("categories.confirmDelete")}</p>
                <button
                  type="button"
                  onClick={() => handleConfirmSimpleDelete(category)}
                >
                  {t("categories.confirm")}
                </button>
                <button type="button" onClick={() => setDialog(null)}>
                  {t("categories.cancel")}
                </button>
              </Modal>
            );
          }

          const options = openCategories
            .filter((candidate) => candidate.id !== category.id)
            .map((candidate) => ({ id: candidate.id, label: candidate.name }));

          return (
            <Modal onDismiss={() => setDialog(null)}>
              <ReassignPicker
                prompt={t("categories.reassignPrompt")}
                options={options}
                confirmLabel={t("categories.reassignConfirm")}
                cancelLabel={t("categories.cancel")}
                emptyLabel={t("categories.noReassignTargets")}
                onConfirm={(targetId) => handleConfirmReassign(category, targetId)}
                onCancel={() => setDialog(null)}
              />
            </Modal>
          );
        })()}
    </section>
  );
}
