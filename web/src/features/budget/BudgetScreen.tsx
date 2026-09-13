import { useMemo, useState } from "react";
import { useLiveQuery } from "dexie-react-hooks";
import { useTranslation } from "react-i18next";

import {
  budgetEntries,
  categories,
  categoryGroups,
  rollupCategory,
  setBudgetedAmount,
  transactions,
  type Category,
  type CategoryGroup,
  type CategoryMonthFigures,
} from "../../db";
import { formatCurrency } from "../../lib/currency";

function currentMonth(): string {
  return new Date().toISOString().slice(0, 7);
}

// Date-based arithmetic sidesteps manual year-rollover handling (Dec + 1 =>
// next January) — the Date constructor normalizes an out-of-range month.
function shiftMonth(month: string, delta: number): string {
  const [year, mon] = month.split("-").map(Number);
  const date = new Date(Date.UTC(year, mon - 1 + delta, 1));
  return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, "0")}`;
}

function formatMonthLabel(month: string): string {
  const [year, mon] = month.split("-").map(Number);
  return new Date(Date.UTC(year, mon - 1, 1)).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
}

function parseAmountInput(text: string): number {
  const parsed = Number.parseFloat(text);
  return Number.isFinite(parsed) ? Math.round(parsed * 100) : 0;
}

export function BudgetScreen() {
  const { t } = useTranslation();
  const [month, setMonth] = useState(currentMonth);

  const allGroups = useLiveQuery(() => categoryGroups.list());
  const allCategories = useLiveQuery(() => categories.list());
  const allBudgetEntries = useLiveQuery(() => budgetEntries.list());
  const allTransactions = useLiveQuery(() => transactions.list());

  const openGroups = useMemo(
    () =>
      (allGroups ?? [])
        .filter((group) => group.deleted_at === null)
        .sort((a, b) => a.sort_order - b.sort_order),
    [allGroups],
  );

  const categoriesByGroup = useMemo(() => {
    const map = new Map<string, Category[]>();
    for (const category of allCategories ?? []) {
      if (category.deleted_at !== null) continue;
      const list = map.get(category.group_id) ?? [];
      list.push(category);
      map.set(category.group_id, list);
    }
    for (const list of map.values()) {
      list.sort((a, b) => a.sort_order - b.sort_order);
    }
    return map;
  }, [allCategories]);

  const figuresByCategory = useMemo(() => {
    const map = new Map<string, CategoryMonthFigures>();
    for (const category of allCategories ?? []) {
      if (category.deleted_at !== null) continue;
      map.set(
        category.id,
        rollupCategory(allBudgetEntries ?? [], allTransactions ?? [], category.id, month),
      );
    }
    return map;
  }, [allCategories, allBudgetEntries, allTransactions, month]);

  async function handleBudgetedCommit(categoryId: string, text: string) {
    await setBudgetedAmount(categoryId, month, parseAmountInput(text));
  }

  if (
    allGroups === undefined ||
    allCategories === undefined ||
    allBudgetEntries === undefined ||
    allTransactions === undefined
  ) {
    return <p>{t("app.loading")}</p>;
  }

  return (
    <section>
      <div>
        <h2>{t("nav.budget")}</h2>
        <button type="button" onClick={() => setMonth((m) => shiftMonth(m, -1))}>
          {t("budget.prevMonth")}
        </button>
        <span>{formatMonthLabel(month)}</span>
        <button type="button" onClick={() => setMonth((m) => shiftMonth(m, 1))}>
          {t("budget.nextMonth")}
        </button>
      </div>

      {openGroups.length === 0 ? (
        <p>{t("budget.empty")}</p>
      ) : (
        openGroups.map((group: CategoryGroup) => {
          const groupCategories = categoriesByGroup.get(group.id) ?? [];
          if (groupCategories.length === 0) return null;

          return (
            <div key={group.id}>
              <h3>{group.name}</h3>
              <table>
                <thead>
                  <tr>
                    <th>{t("budget.category")}</th>
                    <th>{t("budget.budgeted")}</th>
                    <th>{t("budget.activity")}</th>
                    <th>{t("budget.available")}</th>
                  </tr>
                </thead>
                <tbody>
                  {groupCategories.map((category) => {
                    const figures = figuresByCategory.get(category.id) ?? {
                      budgeted: 0,
                      activity: 0,
                      available: 0,
                    };
                    return (
                      <tr key={category.id}>
                        <td>
                          {category.name}
                          {category.hidden && ` (${t("budget.hiddenBadge")})`}
                        </td>
                        <td>
                          <input
                            key={`${category.id}-${month}`}
                            aria-label={t("budget.budgetedFor", { category: category.name })}
                            inputMode="decimal"
                            defaultValue={(figures.budgeted / 100).toFixed(2)}
                            onBlur={(event) =>
                              handleBudgetedCommit(category.id, event.target.value)
                            }
                          />
                        </td>
                        <td>{formatCurrency(figures.activity)}</td>
                        <td>{formatCurrency(figures.available)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          );
        })
      )}
    </section>
  );
}
