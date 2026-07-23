// i18n bootstrap: the UI language follows the system (browser) language.
// Any Chinese variant maps to zh; everything else falls back to English.
// Resources are bundled synchronously, so components never suspend on
// translation loading.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { en } from "./locales/en";
import { zh } from "./locales/zh";

const systemLang = (navigator.language ?? "en").toLowerCase();
const lng = systemLang.startsWith("zh") ? "zh" : "en";

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, zh: { translation: zh } },
  lng,
  fallbackLng: "en",
  // React already escapes interpolated values.
  interpolation: { escapeValue: false },
});

document.documentElement.lang = lng;

// Debug handle: lets a console/devtools session flip the language at runtime
// (window.__i18n.changeLanguage("zh")) without spoofing navigator.language.
(window as unknown as { __i18n: typeof i18n }).__i18n = i18n;

export default i18n;
