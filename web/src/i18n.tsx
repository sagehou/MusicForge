import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import en from "./locales/en.json";
import zh from "./locales/zh-CN.json";

export type Locale = "en" | "zh-CN";
export type MessageKey = keyof typeof en;
type Message = string | { one: string; other: string };
type Catalog = Record<MessageKey, Message>;
export type Params = Record<string, string | number>;
const catalogs: Record<Locale, Catalog> = { en, "zh-CN": zh };
const storageKey = "musicforge.language";
const Context = createContext<ReturnType<typeof useLanguage> | undefined>(undefined);

function initialLocale(): Locale {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved === "en" || saved === "zh-CN") return saved;
  } catch { /* Browser privacy settings may disable storage. */ }
  for (const language of navigator.languages ?? [navigator.language]) {
    if (/^zh(?:-|$)/i.test(language)) return "zh-CN";
    if (/^en(?:-|$)/i.test(language)) return "en";
  }
  return "en";
}

function useLanguage() {
  const [locale, updateLocale] = useState<Locale>(initialLocale);
  const catalog = catalogs[locale];
  const number = (value: number) => new Intl.NumberFormat(locale).format(value);
  function t(key: MessageKey, params: Params = {}): string {
    const message = catalog[key];
    const template = typeof message === "string" ? message :
      new Intl.PluralRules(locale).select(Number(params.count)) === "one" ? message.one : message.other;
    return template.replace(/\{(\w+)\}/g, (_, name: string) => {
      const value = params[name];
      return value === undefined ? `{${name}}` : typeof value === "number" ? number(value) : value;
    });
  }
  const status = (value: string) => {
    const key = `status.${value}` as MessageKey;
    return key in catalog ? t(key) : value;
  };
  const kind = (value: string) => {
    const key = `kind.${value}` as MessageKey;
    return key in catalog ? t(key) : value;
  };
  // Known user-facing API diagnostics share their English wording with the catalog.
  // Unknown system/ffmpeg diagnostics retain the original detail for troubleshooting.
  function errorMessage(raw: string) {
    const scan = raw.match(/^scan found (\d+) unavailable source files and (\d+) invalid audio files; healthy tracks queued, no deletions applied; see Library errors/);
    if (scan) return t("error.scanIncomplete", { unavailable: scan[1], invalid: scan[2] }) + raw.slice(scan[0].length);
    const http = raw.match(/^Server returned(?: an unreadable response \(HTTP (\d+)\)| HTTP (\d+)); please retry(?=\n|$)/);
    if (http) return t("error.http", { status: http[1] || http[2] }) + raw.slice(http[0].length);
    const key = Object.keys(en).find(key => key.startsWith("error.") && en[key as MessageKey] === raw) as MessageKey | undefined;
    if (key) return t(key);
    const prefix = Object.keys(en).find(key => key.startsWith("error.") && key.endsWith("Prefix") &&
      raw.startsWith(en[key as MessageKey] as string)) as MessageKey | undefined;
    return prefix ? t(prefix) + raw.slice((en[prefix] as string).length) : raw;
  }
  function setLocale(value: Locale) {
    updateLocale(value);
    try { localStorage.setItem(storageKey, value); } catch { /* Selection still works for this visit. */ }
  }
  const date = (value: number) => new Intl.DateTimeFormat(locale, {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value * 1000));
  return { locale, setLocale, t, status, kind, number, date, errorMessage };
}

export function LanguageProvider({ children }: { children: ReactNode }) {
  const language = useLanguage();
  useEffect(() => {
    document.documentElement.lang = language.locale;
    document.title = language.t("app.title");
  }, [language.locale]);
  return <Context.Provider value={language}>{children}</Context.Provider>;
}

export function useI18n() {
  const language = useContext(Context);
  if (!language) throw new Error("LanguageProvider is required");
  return language;
}

export function LanguageSelector() {
  const { locale, setLocale, t } = useI18n();
  return <label className="language-selector"><span>{t("app.language")}</span>
    <select value={locale} aria-label={t("app.language")} onChange={event => setLocale(event.target.value as Locale)}>
      <option value="en" lang="en">English</option>
      <option value="zh-CN" lang="zh-CN">简体中文</option>
    </select>
  </label>;
}
