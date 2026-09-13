import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { approvePairing, PairingApiError } from "../../auth";

interface ApproveDeviceFormProps {
  onApproved: () => void;
  onCancel: () => void;
}

// (c) Shown on an already-trusted device (or localhost) when another
// device requests pairing: the human reads the 4-digit code off the
// waiting device's screen and types it in here to confirm it via
// POST /api/pairing/approve (§6.2). There's no server-side push of
// pending requests to trusted devices — approval is always this manual,
// human-in-the-loop step.
export function ApproveDeviceForm({ onApproved, onCancel }: ApproveDeviceFormProps) {
  const { t } = useTranslation();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = code.trim();
    if (!trimmed) {
      setError(t("devices.approve.codeRequired"));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await approvePairing(trimmed);
      onApproved();
    } catch (err) {
      setSubmitting(false);
      if (err instanceof PairingApiError && err.code === "PAIRING_RATE_LIMITED") {
        setError(t("devices.approve.errorRateLimited"));
      } else if (err instanceof PairingApiError && err.code === "PAIRING_REQUIRED") {
        setError(t("devices.approve.errorNotTrusted"));
      } else {
        setError(t("devices.approve.errorInvalid"));
      }
    }
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>{t("devices.approve.title")}</h2>
      <p>{t("devices.approve.instructions")}</p>

      <div>
        <label htmlFor="approve-code">{t("devices.approve.codeLabel")}</label>
        <input
          id="approve-code"
          value={code}
          onChange={(event) => setCode(event.target.value)}
          disabled={submitting}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <button type="submit" disabled={submitting}>
          {t("devices.approve.submit")}
        </button>
        <button type="button" onClick={onCancel}>
          {t("devices.approve.cancel")}
        </button>
      </div>
    </form>
  );
}
