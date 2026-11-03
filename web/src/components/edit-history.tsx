import { useEffect, useState } from "react";
import { History, Loader2 } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Button } from "./ui/button";

type Revision = { hash: string; at: number; bytes: number };
export type RevisionContent = { text?: string; image?: string };

export function EditHistory({ id, kind, disabled, restore }: { id: number; kind: "lyrics" | "artwork"; disabled: boolean; restore: (content: RevisionContent) => void }) {
  const { t, locale, errorMessage } = useI18n();
  const [open, setOpen] = useState(false);
  const [entries, setEntries] = useState<Revision[]>();
  const [selected, setSelected] = useState("");
  const [content, setContent] = useState<RevisionContent>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setEntries(undefined); setError(undefined); setSelected(""); setContent(undefined);
    void api<{ entries: Revision[] }>(`/library/${id}/history?kind=${kind}`, "GET", undefined, controller.signal).then(value => { if (!controller.signal.aborted) setEntries(value.entries); }).catch((err: Error) => { if (!controller.signal.aborted) setError(err); });
    return () => controller.abort();
  }, [id, kind, open, attempt]);
  async function preview(hash: string) {
    setBusy(true); setError(undefined); setSelected(hash); setContent(undefined);
    try { setContent(await api<RevisionContent>(`/library/${id}/history?kind=${kind}&revision=${hash}`)); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  return <details className="edit-history" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary><History size={15} />{t("history.title")}</summary>
    {open && <div className="comparison-body"><p>{t("history.hint")}</p>{error && <div role="alert"><p>{errorMessage(error.message)}</p><Button size="sm" variant="ghost" onClick={() => setAttempt(value => value + 1)}>{t("audition.reload")}</Button></div>}{!entries && !error && <Loader2 size={16} className="animate-spin" />}{entries?.length === 0 && <p>{t("history.empty")}</p>}
      {!!entries?.length && <label className="field"><span className="field-label">{t("history.revision")}</span><select value={selected} disabled={disabled || busy} onChange={event => void preview(event.target.value)}><option value="" disabled>{t("history.choose")}</option>{entries.map((entry, index) => <option key={`${entry.hash}-${index}`} value={entry.hash}>{new Date(entry.at * 1000).toLocaleString(locale)} · {entry.hash.slice(0, 8)} · {Math.ceil(entry.bytes / 1024)} KiB</option>)}</select></label>}
      {busy && <Loader2 size={16} className="animate-spin" />}{content && <>{kind === "lyrics" ? <label className="field"><span className="field-label">{t("history.preview")}</span><textarea rows={6} readOnly dir="auto" value={content.text || ""} /></label> : <img className="history-image" src={`data:image/jpeg;base64,${content.image}`} alt={t("history.preview")} />}<Button size="sm" variant="outline" disabled={disabled || busy} onClick={() => restore(content)}>{t("history.restore")}</Button></>}
    </div>}
  </details>;
}
