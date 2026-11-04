import { useEffect, useState, type FormEvent } from "react";
import { Bookmark, Trash2 } from "lucide-react";
import { useI18n } from "../i18n";
import { Button } from "./ui/button";

const storageKey = "musicforge.library.views";
const limit = 20;
type SavedView = { name: string; search: string };

// Store filter expressions, never selected track IDs or a cached result set.
export function libraryViewSearch(raw: string) {
  const input = new URLSearchParams(raw), output = new URLSearchParams();
  const choices: Record<string, readonly string[]> = {
    view: ["albums", "artists", "tracks"],
    status: ["failed", "possibly_damaged", "expired", "missing", "changed", "needs_rebuild", "unverified", "ready"],
    lyrics: ["ready", "unchecked", "not_found", "instrumental", "needs_review", "failed"],
    lyrics_script: ["traditional", "simplified", "unknown", "unidentified"],
    sort: ["attention"],
  };
  for (const [key, values] of Object.entries(choices)) {
    const value = input.get(key);
    if (value && values.includes(value)) output.set(key, value);
  }
  if (!output.has("view")) output.set("view", "albums");
  for (const key of ["artist", "album", "q"]) {
    const value = input.get(key)?.trim();
    if (value && value.length <= 2048) output.set(key, value);
  }
  return output.toString();
}

function readViews(): SavedView[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(storageKey) || "[]");
    if (!Array.isArray(raw)) return [];
    const views = new Map<string, SavedView>();
    for (const value of raw.slice(0, limit)) {
      if (!value || typeof value.name !== "string" || !value.name.trim() || value.name.length > 80 || typeof value.search !== "string" || value.search.length > 16384) continue;
      const name = value.name.trim();
      views.set(name, { name, search: libraryViewSearch(value.search) });
    }
    return [...views.values()];
  } catch { return []; }
}

export function SavedLibraryViews({ search, apply, disabled }: { search: string; apply: (search: string) => void; disabled: boolean }) {
  const { t } = useI18n();
  const [views, setViews] = useState(readViews);
  const [name, setName] = useState("");
  const [error, setError] = useState(false);
  useEffect(() => {
    const sync = (event: StorageEvent) => { if (event.key === storageKey || event.key === null) setViews(readViews()); };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  function write(next: SavedView[]) {
    try { localStorage.setItem(storageKey, JSON.stringify(next)); setViews(next); setError(false); return true; }
    catch { setError(true); return false; }
  }
  function save(event: FormEvent) {
    event.preventDefault();
    const label = name.trim();
    if (!label || label.length > 80) return;
    const current = readViews();
    if (current.length >= limit && !current.some(view => view.name === label)) return;
    const view = { name: label, search: libraryViewSearch(search) };
    if (write([...current.filter(item => item.name !== label), view])) setName("");
  }
  return <details className="saved-library-views">
    <summary><Bookmark size={15} />{t("views.title", { count: views.length })}</summary>
    <div className="comparison-body">
      <p>{t("views.hint")}</p>
      <form className="actions" onSubmit={save}>
        <label className="field"><span className="field-label">{t("views.name")}</span><input value={name} maxLength={80} disabled={disabled} onChange={event => setName(event.target.value)} /></label>
        <Button size="sm" variant="outline" disabled={disabled || !name.trim() || views.length >= limit && !views.some(view => view.name === name.trim())}>{t(views.some(view => view.name === name.trim()) ? "views.replace" : "views.save")}</Button>
      </form>
      {error && <p role="alert">{t("views.storageError")}</p>}
      {views.length >= limit && <p>{t("views.limit", { count: limit })}</p>}
      <div className="saved-view-list">{views.map(view => <div className="actions" key={view.name}>
        <Button type="button" size="sm" variant="ghost" disabled={disabled} onClick={() => apply(view.search)}>{view.name}</Button>
        <Button type="button" size="icon" variant="ghost" disabled={disabled} aria-label={t("views.delete", { name: view.name })} onClick={() => write(readViews().filter(item => item.name !== view.name))}><Trash2 size={14} /></Button>
      </div>)}</div>
    </div>
  </details>;
}
