import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { bootstrap, PairingApiError, setDeviceToken } from "../../auth";

interface BootstrapFormProps {
  onSwitchToWaiting: () => void;
}

// (a) Shown when this device has no token yet and the operator says this
// is the first device on this server (§6.1): prompts for the setup code
// printed to stdout/the tray icon and claims it via
// POST /api/pairing/bootstrap.
export function BootstrapForm({ onSwitchToWaiting }: BootstrapFormProps) {
  const { t } = useTranslation();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = code.trim();
    if (!trimmed) {
      setError(t("devices.bootstrap.codeRequired"));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      const result = await bootstrap(trimmed);
      setDeviceToken(result.token);
    } catch (err) {
      setSubmitting(false);
      if (err instanceof PairingApiError && err.code === "BOOTSTRAP_CLOSED") {
        setError(t("devices.bootstrap.errorClosed"));
      } else if (err instanceof PairingApiError && err.code === "PAIRING_RATE_LIMITED") {
        setError(t("devices.bootstrap.errorRateLimited"));
      } else {
        setError(t("devices.bootstrap.errorGeneric"));
      }
    }
  }

  return (
    <form onSubmit={handleSubmit}>
      <h2>{t("devices.bootstrap.title")}</h2>
      <p>{t("devices.bootstrap.instructions")}</p>

      <div>
        <label htmlFor="bootstrap-code">{t("devices.bootstrap.codeLabel")}</label>
        <input
          id="bootstrap-code"
          value={code}
          onChange={(event) => setCode(event.target.value)}
          disabled={submitting}
        />
      </div>
      {error && <p role="alert">{error}</p>}

      <div>
        <button type="submit" disabled={submitting}>
          {t("devices.bootstrap.submit")}
        </button>
        <button type="button" onClick={onSwitchToWaiting}>
          {t("devices.bootstrap.switchToRequest")}
        </button>
      </div>
    </form>
  );
}
