import { useEffect, useState } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { useI18n } from "../i18n";

type Theme = "system" | "light" | "dark";
const key = "musicforge.theme";
function savedTheme(): Theme {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "dark") return value;
  } catch { /* Storage is optional. */ }
  return "system";
}
function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme === "system" ? matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light" : theme;
}
applyTheme(savedTheme());

export function ThemeSelector() {
  const { t } = useI18n();
  const [theme, setTheme] = useState<Theme>(savedTheme);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const changed = () => applyTheme(theme);
    const storage = (event: StorageEvent) => { if (event.key === key) setTheme(savedTheme()); };
    const selected = (event: Event) => setTheme((event as CustomEvent<Theme>).detail);
    applyTheme(theme);
    media.addEventListener("change", changed);
    window.addEventListener("storage", storage);
    window.addEventListener("theme-changed", selected);
    return () => { media.removeEventListener("change", changed); window.removeEventListener("storage", storage); window.removeEventListener("theme-changed", selected); };
  }, [theme]);
  const Icon = theme === "system" ? Monitor : theme === "dark" ? Moon : Sun;
  return <label className="theme-selector" title={t("theme.label")}><Icon size={17} aria-hidden="true" /><span>{t("theme.label")}</span>
    <select aria-label={t("theme.label")} value={theme} onChange={event => {
      const value = event.target.value as Theme;
      setTheme(value);
      applyTheme(value);
      try { localStorage.setItem(key, value); } catch { /* Theme still applies for this visit. */ }
      window.dispatchEvent(new CustomEvent("theme-changed", { detail: value }));
    }}><option value="system">{t("theme.system")}</option><option value="light">{t("theme.light")}</option><option value="dark">{t("theme.dark")}</option></select>
  </label>;
}
