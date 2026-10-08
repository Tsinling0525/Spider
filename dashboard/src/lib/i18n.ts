import { derived, writable } from "svelte/store";
import { english } from "./translations";

export type Locale = "zh-CN" | "en";
export type TranslationParams = Record<string, string | number>;
export type Translator = (key: string, params?: TranslationParams) => string;
const storageKey = "spider.language";

function validLocale(value: unknown): value is Locale {
  return value === "zh-CN" || value === "en";
}

function savedLocale(): Locale {
  try {
    const saved = window.localStorage.getItem(storageKey);
    if (validLocale(saved)) return saved;
  } catch { /* Browser storage is optional. */ }
  return "zh-CN";
}

export const locale = writable<Locale>(savedLocale());

export function translate(language: Locale, key: string, params: TranslationParams = {}): string {
  const template = language === "en" && Object.hasOwn(english, key) ? english[key] : key;
  // Replace once so braces in user content are never interpreted as placeholders.
  return template.replace(/\{(\d+)\}/g, (placeholder, name: string) =>
    Object.hasOwn(params, name) ? String(params[name]) : placeholder);
}

export const t = derived(locale, (language): Translator =>
  (key, params) => translate(language, key, params));

export function setLocale(language: Locale): void {
  if (!validLocale(language)) return;
  locale.set(language);
  try { window.localStorage.setItem(storageKey, language); } catch { /* Keep working in memory. */ }
}

export function initializeLocale(): () => void {
  locale.set(savedLocale());
  function sync(event: StorageEvent) {
    if (event.storageArea === window.localStorage && (event.key === storageKey || event.key === null)) {
      locale.set(validLocale(event.newValue) ? event.newValue : "zh-CN");
    }
  }
  window.addEventListener("storage", sync);
  return () => window.removeEventListener("storage", sync);
}
