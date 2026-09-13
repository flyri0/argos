import { useState } from "react";

export interface ReassignOption {
  id: string;
  label: string;
}

interface ReassignPickerProps {
  prompt: string;
  options: ReassignOption[];
  confirmLabel: string;
  cancelLabel: string;
  emptyLabel: string;
  onConfirm: (targetId: string) => void;
  onCancel: () => void;
}

// The picker for §5.4's reassignment flow: shown instead of a plain
// confirm when the item being deleted is still in use. Only performs the
// local Dexie write once confirmed — see db/reassign.ts.
export function ReassignPicker({
  prompt,
  options,
  confirmLabel,
  cancelLabel,
  emptyLabel,
  onConfirm,
  onCancel,
}: ReassignPickerProps) {
  const [targetId, setTargetId] = useState(options[0]?.id ?? "");

  return (
    <div>
      <p>{prompt}</p>
      {options.length === 0 ? (
        <p>{emptyLabel}</p>
      ) : (
        <select
          aria-label={prompt}
          value={targetId}
          onChange={(event) => setTargetId(event.target.value)}
        >
          {options.map((option) => (
            <option key={option.id} value={option.id}>
              {option.label}
            </option>
          ))}
        </select>
      )}
      <div>
        {options.length > 0 && (
          <button type="button" onClick={() => onConfirm(targetId)}>
            {confirmLabel}
          </button>
        )}
        <button type="button" onClick={onCancel}>
          {cancelLabel}
        </button>
      </div>
    </div>
  );
}
