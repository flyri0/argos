import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { PairingApiError, pollPairing, requestPairing, setDeviceToken } from "../../auth";

const POLL_INTERVAL_MS = 3000;

function formatCountdown(msRemaining: number): string {
  const totalSeconds = Math.max(0, Math.ceil(msRemaining / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

interface WaitingForApprovalProps {
  onSwitchToBootstrap: () => void;
}

// (b) Shown after POST /api/pairing/request (§6.2): displays the code and a
// countdown to its 10-minute expiry, and polls
// GET /api/pairing/request/:code (§7.3) until it's approved (store the
// token and let DevicesScreen switch views) or the code lapses — a 404
// from the poll, per §6.2 — at which point it offers a fresh one.
export function WaitingForApproval({ onSwitchToBootstrap }: WaitingForApprovalProps) {
  const { t } = useTranslation();
  const [code, setCode] = useState<string | null>(null);
  const [expiresAt, setExpiresAt] = useState<number | null>(null);
  const [expired, setExpired] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [requestError, setRequestError] = useState<string | null>(null);

  const startRequest = useCallback(async () => {
    setRequestError(null);
    setExpired(false);
    setCode(null);
    try {
      const result = await requestPairing();
      setCode(result.code);
      setExpiresAt(result.expires_at * 1000);
    } catch {
      setRequestError(t("devices.waiting.errorGeneric"));
    }
  }, [t]);

  useEffect(() => {
    startRequest();
  }, [startRequest]);

  // Ticks the displayed countdown once a second. The 404-driven `expired`
  // check below is what actually stops polling — this is display only.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    if (!code || expired) return;

    let cancelled = false;
    const id = setInterval(async () => {
      try {
        const result = await pollPairing(code);
        if (cancelled) return;
        if (result.status === "approved" && result.token) {
          setDeviceToken(result.token);
        }
      } catch (err) {
        if (cancelled) return;
        if (err instanceof PairingApiError && err.status === 404) {
          setExpired(true);
        }
        // Any other error (rate limited, transient network failure): say
        // nothing and retry on the next tick.
      }
    }, POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [code, expired]);

  const msRemaining = expiresAt ? expiresAt - now : 0;

  return (
    <div>
      <h2>{t("devices.waiting.title")}</h2>
      <p>{t("devices.waiting.instructions")}</p>

      {code && !expired && (
        <>
          <p>
            <strong aria-label={t("devices.waiting.codeLabel")}>{code}</strong>
          </p>
          <p>{t("devices.waiting.expiresIn", { time: formatCountdown(msRemaining) })}</p>
        </>
      )}

      {expired && (
        <>
          <p role="alert">{t("devices.waiting.expired")}</p>
          <button type="button" onClick={startRequest}>
            {t("devices.waiting.requestNew")}
          </button>
        </>
      )}

      {requestError && <p role="alert">{requestError}</p>}

      <button type="button" onClick={onSwitchToBootstrap}>
        {t("devices.waiting.switchToBootstrap")}
      </button>
    </div>
  );
}
