import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import common from "./locales/en/common.json";

// English is the only locale for MVP (project_spec.md §4), but resources
// are namespaced so adding a language later is just a new locale file.
i18n.use(initReactI18next).init({
  lng: "en",
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
