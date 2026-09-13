import type { MouseEvent, ReactNode } from "react";

interface ModalProps {
  children: ReactNode;
  onDismiss: () => void;
}

export function Modal({ children, onDismiss }: ModalProps) {
  function stopPropagation(event: MouseEvent) {
    event.stopPropagation();
  }

  return (
    <div className="modal-backdrop" onClick={onDismiss}>
      <div
        className="modal-content"
        role="dialog"
        aria-modal="true"
        onClick={stopPropagation}
      >
        {children}
      </div>
    </div>
  );
}
