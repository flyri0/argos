import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import common from "./locales/en/common.json";

// English is the only locale for MVP (project_spec.md §4), but resources
// are namespaced so adding a language later is just a new locale file plus
// an entry in SUPPORTED_LANGUAGES — no changes to the components that call
// t() (§4: "Adding a new language should only require adding a new locale
// file, with no code changes to the components themselves").
export const SUPPORTED_LANGUAGES = ["en"] as const;
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number];

const LANGUAGE_KEY = "argos.language";

function isSupported(lang: string): lang is SupportedLanguage {
  return (SUPPORTED_LANGUAGES as readonly string[]).includes(lang);
}

// §4: "default to the browser/OS locale on first run, with a manual
// override in settings." A stored manual override always wins; otherwise
// the first browser/OS language that matches a supported one — by exact
// tag or by primary subtag (e.g. "en-US" -> "en") — wins; otherwise
// English, the fallback locale.
export function detectInitialLanguage(): SupportedLanguage {
  const stored = typeof localStorage !== "undefined" ? localStorage.getItem(LANGUAGE_KEY) : null;
  if (stored !== null && isSupported(stored)) return stored;

  const candidates =
    typeof navigator !== "undefined"
      ? navigator.languages && navigator.languages.length > 0
        ? navigator.languages
        : [navigator.language]
      : [];
  for (const candidate of candidates) {
    if (!candidate) continue;
    if (isSupported(candidate)) return candidate;
    const primary = candidate.split("-")[0];
    if (isSupported(primary)) return primary;
  }
  return "en";
}

// The settings-page manual override (§4): persists the choice so it
// survives a reload, then switches react-i18next's active language.
export function setLanguage(lang: SupportedLanguage): void {
  localStorage.setItem(LANGUAGE_KEY, lang);
  i18n.changeLanguage(lang);
}

i18n.use(initReactI18next).init({
  lng: detectInitialLanguage(),
  fallbackLng: "en",
  defaultNS: "common",
  resources: {
    en: { common },
  },
  interpolation: {
    escapeValue: false,
  },
});

export default i18n;
