import { useState } from "react";
import { useTranslation } from "react-i18next";

import { BootstrapForm } from "./BootstrapForm";
import { WaitingForApproval } from "./WaitingForApproval";

type Mode = "choice" | "bootstrap" | "waiting";

// Shown whenever this device has no token yet and isn't on localhost.
// project_spec.md gives no way for the client to know in advance whether
// bootstrap (§6.1) is still open or already closed — only the operator
// knows which situation they're in (did they just see a setup code
// printed, or not) — so this starts with an explicit choice between the
// two flows rather than guessing.
export function PairingScreen() {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>("choice");

  if (mode === "bootstrap") {
    return <BootstrapForm onSwitchToWaiting={() => setMode("waiting")} />;
  }
  if (mode === "waiting") {
    return <WaitingForApproval onSwitchToBootstrap={() => setMode("bootstrap")} />;
  }

  return (
    <div>
      <h2>{t("devices.choice.title")}</h2>
      <p>{t("devices.choice.instructions")}</p>
      <button type="button" onClick={() => setMode("bootstrap")}>
        {t("devices.choice.haveSetupCode")}
      </button>
      <button type="button" onClick={() => setMode("waiting")}>
        {t("devices.choice.requestAccess")}
      </button>
    </div>
  );
}
