import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Account, Category, Payee, Transaction } from "../../db";

export interface TransactionFormValues {
  date: string;
  payee_id: string | null;
  category_id: string | null;
  notes: string;
  amountMinor: number;
  // Set only when creating a new transfer (never on edit — an existing
  // transfer's target account isn't changeable in this milestone).
  transferAccountId: string | null;
}

interface TransactionFormProps {
  initial?: Transaction;
  // Whether `initial` is one leg of an existing transfer. Only meaningful
  // when `initial` is set; a create-mode form decides transfer-ness itself
  // via the checkbox below.
  isTransfer: boolean;
  transferAccountName?: string;
  // Candidate accounts to transfer to: already excludes the current
  // account and any closed account (a closed account can't accept a new
  // transaction on either side of a transfer, §7.1).
  transferTargets: Account[];
  categories: Category[];
  payees: Payee[];
  onSubmit: (values: TransactionFormValues) => void;
  onCancel: () => void;
}

function todayIso(): string {
  return new Date().toISOString().slice(0, 10);
}

export function TransactionForm({
  initial,
  isTransfer,
  transferAccountName,
  transferTargets,
  categories,
  payees,
  onSubmit,
  onCancel,
}: TransactionFormProps) {
  const { t } = useTranslation();
  const [date, setDate] = useState(initial?.date ?? todayIso());
  const [payeeId, setPayeeId] = useState(initial?.payee_id ?? "");
  const [categoryId, setCategoryId] = useState(initial?.category_id ?? "");
  const [notes, setNotes] = useState(initial?.notes ?? "");
  const [direction, setDirection] = useState<"outflow" | "inflow">(
    initial && initial.amount > 0 ? "inflow" : "outflow",
  );
  const [amountText, setAmountText] = useState(
    initial ? String(Math.abs(initial.amount) / 100) : "",
  );
  const [transferMode, setTransferMode] = useState(isTransfer);
  const [transferAccountId, setTransferAccountId] = useState(
    transferTargets[0]?.id ?? "",
  );
  const [error, setError] = useState<string | null>(null);

  // Transfer-ness can only be chosen at creation time; editing an existing
  // transaction (regular or transfer) never offers the checkbox.
  const canOfferTransfer = !initial && transferTargets.length > 0;

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!date) {
      setError(t("register.dateRequired"));
      return;
    }
    const parsed = Number.parseFloat(amountText);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      setError(t("register.amountRequired"));
      return;
    }
    if (transferMode && !initial && !transferAccountId) {
      setError(t("register.transferAccountRequired"));
      return;
    }

    const amountMinor = Math.round(parsed * 100) * (direction === "outflow" ? -1 : 1);
    onSubmit({
      date,
      payee_id: payeeId || null,
      category_id: transferMode ? null : categoryId || null,
      notes,
      amountMinor,
      transferAccountId: !initial && transferMode ? transferAccountId : null,
    });
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>{initial ? t("register.editTitle") : t("register.newTitle")}</h2>

      <div>
        <label htmlFor="transaction-date">{t("register.date")}</label>
        <input
          id="transaction-date"
          type="date"
          value={date}
          onChange={(event) => setDate(event.target.value)}
        />
      </div>

      <div>
        <label htmlFor="transaction-payee">{t("register.payee")}</label>
        <select
          id="transaction-payee"
          value={payeeId}
          onChange={(event) => setPayeeId(event.target.value)}
        >
          <option value="">{t("register.noPayee")}</option>
          {payees.map((payee) => (
            <option key={payee.id} value={payee.id}>
              {payee.name}
            </option>
          ))}
        </select>
      </div>

      {canOfferTransfer && (
        <div>
          <label>
            <input
              type="checkbox"
              checked={transferMode}
              onChange={(event) => setTransferMode(event.target.checked)}
            />
            {t("register.isTransfer")}
          </label>
        </div>
      )}

      {transferMode ? (
        initial ? (
          <p>{t("register.transferToStatic", { account: transferAccountName ?? "" })}</p>
        ) : (
          <div>
            <label htmlFor="transaction-transfer-account">{t("register.transferTo")}</label>
            <select
              id="transaction-transfer-account"
              value={transferAccountId}
              onChange={(event) => setTransferAccountId(event.target.value)}
            >
              {transferTargets.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name}
                </option>
              ))}
            </select>
          </div>
        )
      ) : (
        <div>
          <label htmlFor="transaction-category">{t("register.category")}</label>
          <select
            id="transaction-category"
            value={categoryId}
            onChange={(event) => setCategoryId(event.target.value)}
          >
            <option value="">{t("register.noCategory")}</option>
            {categories.map((category) => (
              <option key={category.id} value={category.id}>
                {category.name}
              </option>
            ))}
          </select>
        </div>
      )}

      <div>
        <label>
          <input
            type="radio"
            name="transaction-direction"
            checked={direction === "outflow"}
            onChange={() => setDirection("outflow")}
          />
          {t("register.outflow")}
        </label>
        <label>
          <input
            type="radio"
            name="transaction-direction"
            checked={direction === "inflow"}
            onChange={() => setDirection("inflow")}
          />
          {t("register.inflow")}
        </label>
      </div>

      <div>
        <label htmlFor="transaction-amount">{t("register.amount")}</label>
        <input
          id="transaction-amount"
          inputMode="decimal"
          value={amountText}
          placeholder="0.00"
          onChange={(event) => setAmountText(event.target.value)}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <label htmlFor="transaction-notes">{t("register.notes")}</label>
        <input
          id="transaction-notes"
          value={notes}
          onChange={(event) => setNotes(event.target.value)}
        />
      </div>

      <div>
        <button type="submit">{t("register.save")}</button>
        <button type="button" onClick={onCancel}>
          {t("register.cancel")}
        </button>
      </div>
    </form>
  );
}
