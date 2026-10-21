import { useEffect, useState, useSyncExternalStore } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { LanguageSelector, useI18n, type MessageKey } from "../i18n";

type Theme = "system" | "light" | "dark";
const presets = { apple: "#e6213c", spotify: "#1db954", violet: "#8b5cf6", amber: "#f59e0b" };
type Palette = keyof typeof presets | "custom";
type Appearance = { theme: Theme; palette: Palette; custom: string };
const keys = { theme: "musicforge.theme", palette: "musicforge.palette", custom: "musicforge.custom-color" };
function read(key: string) {
  try { return localStorage.getItem(key); } catch { return null; }
}
function hex(value: string | null) { return value && /^#[\da-f]{6}$/i.test(value) ? value.toLowerCase() : undefined; }
function savedAppearance(): Appearance {
  const theme = read(keys.theme), palette = read(keys.palette);
  return { theme: theme === "light" || theme === "dark" ? theme : "system", palette: palette && (Object.hasOwn(presets, palette) || palette === "custom") ? palette as Palette : "apple", custom: hex(read(keys.custom)) || presets.apple };
}
function rgb(color: string) { return [1, 3, 5].map(index => parseInt(color.slice(index, index + 2), 16)); }
function mix(color: string, target: number, amount: number) { return `#${rgb(color).map(channel => Math.round(channel + (target - channel) * amount).toString(16).padStart(2, "0")).join("")}`; }
function luminance(color: string) {
  const [r, g, b] = rgb(color).map(value => { const channel = value / 255; return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4; });
  return .2126 * r + .7152 * g + .0722 * b;
}
function contrast(a: string, b: string) { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05); }
let appearance = savedAppearance();
const media = matchMedia("(prefers-color-scheme: dark)");
function applyAppearance() {
  const root = document.documentElement;
  const dark = appearance.theme === "dark" || appearance.theme === "system" && media.matches;
  const color = appearance.palette === "custom" ? appearance.custom : presets[appearance.palette];
  const ink = contrast(color, "#ffffff") >= 4.5 ? "#ffffff" : "#000000";
  // Keep custom colors, adapting small text and focus rings to the page surface.
  let accent = color;
  for (let step = 1; contrast(accent, dark ? "#202024" : "#fafafa") < 4.5 && step <= 20; step++) accent = mix(color, dark ? 255 : 0, step / 20);
  root.dataset.theme = dark ? "dark" : "light";
  root.dataset.palette = appearance.palette;
  root.style.setProperty("--accent", accent);
  root.style.setProperty("--accent-fill", color);
  root.style.setProperty("--accent-hover", mix(color, ink === "#ffffff" ? 0 : 255, .10));
  root.style.setProperty("--accent-active", mix(color, ink === "#ffffff" ? 0 : 255, .18));
  root.style.setProperty("--accent-ink", ink);
}
function updateAppearance(patch: Partial<Appearance>) {
  appearance = { ...appearance, ...patch };
  for (const name of Object.keys(patch) as (keyof Appearance)[]) {
    try { localStorage.setItem(keys[name], appearance[name]); } catch { /* Preferences still apply for this visit. */ }
  }
  applyAppearance();
  window.dispatchEvent(new Event("appearance-changed"));
}
function subscribe(changed: () => void) { window.addEventListener("appearance-changed", changed); return () => window.removeEventListener("appearance-changed", changed); }
media.addEventListener("change", applyAppearance);
window.addEventListener("storage", event => {
  if (event.key === null || Object.values(keys).includes(event.key)) {
    appearance = savedAppearance(); applyAppearance(); window.dispatchEvent(new Event("appearance-changed"));
  }
});
applyAppearance();

export function AppearanceControls() {
  const { t } = useI18n();
  const { theme, palette, custom } = useSyncExternalStore(subscribe, () => appearance);
  const [draft, setDraft] = useState(custom);
  useEffect(() => setDraft(custom), [custom]);
  const Icon = theme === "system" ? Monitor : theme === "dark" ? Moon : Sun;
  const invalid = !hex(draft);
  return <div className="appearance-controls">
    <LanguageSelector />
    <label className="theme-selector" title={t("theme.label")}><Icon size={15} aria-hidden="true" /><span>{t("theme.label")}</span><select aria-label={t("theme.label")} value={theme} onChange={event => updateAppearance({ theme: event.target.value as Theme })}><option value="system">{t("theme.system")}</option><option value="light">{t("theme.light")}</option><option value="dark">{t("theme.dark")}</option></select></label>
    <details className="palette-menu" onKeyDown={event => {
      if (event.key === "Escape" && event.currentTarget.open) { event.preventDefault(); event.stopPropagation(); event.currentTarget.open = false; event.currentTarget.querySelector("summary")?.focus(); }
    }}><summary aria-label={t("theme.colors")} title={t("theme.colors")}><span className="color-swatch" aria-hidden="true" /><span className="palette-label">{t(`theme.${palette}` as MessageKey)}</span></summary>
      <div className="palette-popover"><fieldset><legend>{t("theme.colors")}</legend><div className="palette-presets">{([...Object.keys(presets), "custom"] as Palette[]).map(name => <button type="button" key={name} aria-pressed={palette === name} onClick={() => updateAppearance({ palette: name })}><span className="color-swatch" style={{ background: name === "custom" ? custom : presets[name] }} aria-hidden="true" />{t(`theme.${name}` as MessageKey)}</button>)}</div></fieldset>
        <div className="custom-color"><label><span>{t("theme.customColor")}</span><input type="color" aria-label={t("theme.customColor")} value={custom} onChange={event => updateAppearance({ palette: "custom", custom: event.target.value })} /></label><input className="color-hex" type="text" aria-label={t("theme.hex")} aria-invalid={invalid || undefined} value={draft} spellCheck={false} maxLength={7} placeholder="#RRGGBB" onChange={event => { const value = event.target.value; setDraft(value); const valid = hex(value); if (valid) updateAppearance({ palette: "custom", custom: valid }); }} /></div>
        {invalid && <small className="color-error" role="alert">{t("theme.invalidColor")}</small>}
      </div>
    </details>
  </div>;
}
