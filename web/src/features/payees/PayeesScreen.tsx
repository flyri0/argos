import { useState } from "react";
import { useLiveQuery } from "dexie-react-hooks";
import { useTranslation } from "react-i18next";

import {
  isPayeeInUse,
  nextHlc,
  payees,
  reassignPayee,
  transactions,
  type Payee,
} from "../../db";
import { Modal } from "../../components/Modal";
import { ReassignPicker } from "../../components/ReassignPicker";
import { PayeeForm, type PayeeFormValues } from "./PayeeForm";

type Dialog =
  | { kind: "add" }
  | { kind: "edit"; payee: Payee }
  | { kind: "delete"; payee: Payee }
  | null;

export function PayeesScreen() {
  const { t } = useTranslation();
  const [dialog, setDialog] = useState<Dialog>(null);

  const allPayees = useLiveQuery(() => payees.list());
  const allTransactions = useLiveQuery(() => transactions.list());

  const openPayees = (allPayees ?? []).filter((payee) => payee.deleted_at === null);

  async function handleAdd(values: PayeeFormValues) {
    await payees.create({
      id: crypto.randomUUID(),
      ...values,
      deleted_at: null,
      ...nextHlc(),
    });
    setDialog(null);
  }

  async function handleEdit(id: string, values: PayeeFormValues) {
    await payees.update(id, { ...values, ...nextHlc() });
    setDialog(null);
  }

  async function handleConfirmSimpleDelete(payee: Payee) {
    await payees.update(payee.id, { deleted_at: Date.now(), ...nextHlc() });
    setDialog(null);
  }

  async function handleConfirmReassign(payee: Payee, targetId: string) {
    await reassignPayee(payee.id, targetId);
    setDialog(null);
  }

  if (allPayees === undefined) {
    return <p>{t("app.loading")}</p>;
  }

  return (
    <section>
      <div>
        <h2>{t("nav.payees")}</h2>
        <button type="button" onClick={() => setDialog({ kind: "add" })}>
          {t("payees.add")}
        </button>
      </div>

      {openPayees.length === 0 ? (
        <p>{t("payees.empty")}</p>
      ) : (
        <ul>
          {openPayees.map((payee) => (
            <li key={payee.id}>
              {payee.name}
              <button type="button" onClick={() => setDialog({ kind: "edit", payee })}>
                {t("payees.edit")}
              </button>
              <button type="button" onClick={() => setDialog({ kind: "delete", payee })}>
                {t("payees.delete")}
              </button>
            </li>
          ))}
        </ul>
      )}

      {dialog?.kind === "add" && (
        <Modal onDismiss={() => setDialog(null)}>
          <PayeeForm onSubmit={handleAdd} onCancel={() => setDialog(null)} />
        </Modal>
      )}

      {dialog?.kind === "edit" && (
        <Modal onDismiss={() => setDialog(null)}>
          <PayeeForm
            initial={dialog.payee}
            onSubmit={(values) => handleEdit(dialog.payee.id, values)}
            onCancel={() => setDialog(null)}
          />
        </Modal>
      )}

      {dialog?.kind === "delete" &&
        (() => {
          const payee = dialog.payee;
          const inUse = isPayeeInUse(payee.id, allTransactions ?? []);

          if (!inUse) {
            return (
              <Modal onDismiss={() => setDialog(null)}>
                <p>{t("payees.confirmDelete")}</p>
                <button type="button" onClick={() => handleConfirmSimpleDelete(payee)}>
                  {t("payees.confirm")}
                </button>
                <button type="button" onClick={() => setDialog(null)}>
                  {t("payees.cancel")}
                </button>
              </Modal>
            );
          }

          const options = openPayees
            .filter((candidate) => candidate.id !== payee.id)
            .map((candidate) => ({ id: candidate.id, label: candidate.name }));

          return (
            <Modal onDismiss={() => setDialog(null)}>
              <ReassignPicker
                prompt={t("payees.reassignPrompt")}
                options={options}
                confirmLabel={t("payees.reassignConfirm")}
                cancelLabel={t("payees.cancel")}
                emptyLabel={t("payees.noReassignTargets")}
                onConfirm={(targetId) => handleConfirmReassign(payee, targetId)}
                onCancel={() => setDialog(null)}
              />
            </Modal>
          );
        })()}
    </section>
  );
}
