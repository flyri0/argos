import { useTranslation } from "react-i18next";

import { DevicesScreen } from "../devices/DevicesScreen";
import { setLanguage, SUPPORTED_LANGUAGES, type SupportedLanguage } from "../../i18n";

// §4: names shown to the user, not the raw tag — extend alongside
// SUPPORTED_LANGUAGES when a language is added.
const LANGUAGE_LABELS: Record<SupportedLanguage, string> = {
  en: "English",
};

function isSupportedLanguage(value: string): value is SupportedLanguage {
  return (SUPPORTED_LANGUAGES as readonly string[]).includes(value);
}

// The "Settings" tab: the §4 manual language override, followed by the
// existing device pairing/management screen.
export function SettingsScreen() {
  const { t, i18n } = useTranslation();

  return (
    <section>
      <h2>{t("nav.settings")}</h2>
      <label>
        {t("settings.language")}
        <select
          value={i18n.language}
          onChange={(event) => {
            const value = event.target.value;
            if (isSupportedLanguage(value)) setLanguage(value);
          }}
        >
          {SUPPORTED_LANGUAGES.map((lang) => (
            <option key={lang} value={lang}>
              {LANGUAGE_LABELS[lang]}
            </option>
          ))}
        </select>
      </label>
      <DevicesScreen />
    </section>
  );
}
