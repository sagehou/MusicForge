import { useEffect, useMemo, useState } from "react";
import { Check, Loader2, Play, RotateCcw, Upload, X } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { audioTime, editLyricRow, lyricRows, lyricsTimeline, shiftLyrics, type LyricRow } from "../lyrics-timeline";
import { Button } from "./ui/button";
import { EditHistory } from "./edit-history";
import { InfoHint } from "./info-hint";

type LyricsAssociation = { path: string; text: string; fingerprint: string; required: boolean };

export function LyricsEditor({ id, position, audition, canAudition, preview, saved, close }: { id: number; position: number; audition: (time: number) => void; canAudition: boolean; preview: (text: string) => void; saved: (text: string) => void; close: () => void }) {
  const { t, errorMessage } = useI18n();
  const [token, setToken] = useState("");
  const [text, setText] = useState("");
  const [initial, setInitial] = useState("");
  const [delay, setDelay] = useState(0);
  const [anchor, setAnchor] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [attempt, setAttempt] = useState(0);
  const [association, setAssociation] = useState<LyricsAssociation>();
  const cues = useMemo(() => lyricsTimeline(text), [text]);
  const timedText = useMemo(() => shiftLyrics(text, delay), [text, delay]);
  const rows = useMemo(() => lyricRows(timedText), [timedText]);
  function editRow(row: LyricRow, time = row.time, content = row.text) {
    setText(editLyricRow(timedText, row, time, content)); setDelay(0); setAnchor(0);
  }
  useEffect(() => {
    const controller = new AbortController();
    setToken(""); setError(undefined); setAssociation(undefined);
    void api<{ token: string; text: string }>(`/library/${id}/lyrics/edit`, "GET", undefined, controller.signal).then(value => {
      if (!controller.signal.aborted) { setToken(value.token); setText(value.text); setInitial(value.text); setDelay(0); }
    }).catch((err: Error) => { if (!controller.signal.aborted) setError(err); });
    return () => controller.abort();
  }, [id, attempt]);
  useEffect(() => { if (association) preview(association.text); else if (token) preview(shiftLyrics(text, delay)); }, [text, delay, token, association, preview]);
  async function reviewAssociation() {
    setBusy(true); setError(undefined);
    try { setAssociation(await api<LyricsAssociation>(`/library/${id}/lyrics/association`)); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function associate() {
    if (!association) return;
    setBusy(true); setError(undefined);
    try {
      const result = await api<{ text: string }>(`/library/${id}/lyrics/association`, "PUT", { fingerprint: association.fingerprint });
      saved(result.text);
    } catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function save() {
    setBusy(true); setError(undefined);
    try {
      const result = await api<{ text: string }>(`/library/${id}/lyrics/edit`, "PUT", { token, text, delay_ms: delay });
      saved(result.text);
    } catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function upload(file?: File) {
    if (!file) return;
    try {
      if (file.size > 1024 * 1024) throw new Error(t("lyricsEdit.fileLimit"));
      const bytes = await file.arrayBuffer();
      setText(new TextDecoder("utf-8", { fatal: true }).decode(bytes)); setDelay(0); setAnchor(0); setError(undefined);
    } catch (err) { setError(err as Error); }
  }
  return <section className="lyrics-editor" aria-label={t("lyricsEdit.title")}>
    <div className="section-heading"><div className="actions"><h3>{t("lyricsEdit.title")}</h3><InfoHint>{t("lyricsEdit.hint")}</InfoHint></div><Button variant="ghost" size="icon" disabled={busy} aria-label={t("lyricsEdit.cancel")} onClick={close}><X size={16} /></Button></div>
    <div className="actions"><Button size="sm" variant="outline" disabled={busy || !!token && (text !== initial || delay !== 0)} onClick={() => void reviewAssociation()}>{t("lyricsAssociation.review")}</Button><InfoHint>{t("lyricsAssociation.hint")}</InfoHint></div>
    {error && <div role="alert" className="error-box"><p>{errorMessage(error.message)}</p>{!token && !association && <Button size="sm" variant="outline" onClick={() => setAttempt(value => value + 1)}>{t("audition.reload")}</Button>}</div>}
    {association && <div className="maintenance-report" role="region" aria-label={t("lyricsAssociation.title")}>
      <code>{association.path}</code>
      <label className="field"><span className="field-label">{t("lyricsAssociation.preview")}</span><textarea readOnly dir="auto" rows={7} aria-label={t("lyricsAssociation.preview")} value={association.text} /></label>
      {!association.required && <p role="status">{t("lyricsAssociation.current")}</p>}
      <div className="actions">{association.required && <Button size="sm" disabled={busy} onClick={() => void associate()}>{t("lyricsAssociation.confirm")}</Button>}<Button size="sm" variant="ghost" disabled={busy} onClick={() => { setAssociation(undefined); preview(initial); if (!token) setAttempt(value => value + 1); }}>{t("lyricsAssociation.cancel")}</Button></div>
    </div>}
    {!token && !error && !association ? <Loader2 size={18} className="animate-spin" /> : !association && token && <>
      <label className="field"><span className="field-label">{t("lyricsEdit.text")}</span><textarea aria-label={t("lyricsEdit.text")} dir="auto" rows={7} disabled={busy} value={text} onChange={event => { setText(event.target.value); setAnchor(0); }} /></label>
      <details className="lyric-line-editor">
        <summary>{t("lyricsEdit.lines")}</summary>
        <p>{t("lyricsEdit.shortcuts")}</p>
        <div className="lyric-edit-rows">{rows.map((row, index) => <div className="lyric-edit-row" key={`${row.line}:${row.stamp}`} onKeyDown={event => {
          if (busy || !event.altKey || event.ctrlKey || event.metaKey || !["ArrowLeft", "ArrowRight", "Enter"].includes(event.key)) return;
          if (event.key === "Enter" && !canAudition) return;
          event.preventDefault();
          editRow(row, event.key === "Enter" ? position : row.time + (event.key === "ArrowLeft" ? -0.1 : 0.1));
        }}>
          <div className="actions"><span className="lyric-edit-number">{index + 1}</span><input type="number" min={0} max={599999.999} step={0.001} value={Number(row.time.toFixed(3))} disabled={busy} aria-label={t("lyricsEdit.lineTime", { line: index + 1 })} onChange={event => { if (Number.isFinite(event.target.valueAsNumber)) editRow(row, event.target.valueAsNumber); }} />
            <Button type="button" size="sm" variant="ghost" disabled={busy} aria-label={t("lyricsEdit.earlier", { line: index + 1 })} onClick={() => editRow(row, row.time - 0.1)}>−100 ms</Button><Button type="button" size="sm" variant="ghost" disabled={busy} aria-label={t("lyricsEdit.later", { line: index + 1 })} onClick={() => editRow(row, row.time + 0.1)}>+100 ms</Button>
            <Button type="button" size="sm" variant="outline" disabled={busy || !canAudition} aria-label={t("lyricsEdit.auditionLine", { line: index + 1 })} onClick={() => audition(row.time)}><Play size={14} />{audioTime(row.time)}</Button><Button type="button" size="sm" variant="outline" disabled={busy || !canAudition} onClick={() => editRow(row, position)}>{t("lyricsEdit.lineAlign")}</Button>
          </div>
          <input type="text" dir="auto" value={row.text} disabled={busy} aria-label={t("lyricsEdit.lineText", { line: index + 1 })} onChange={event => editRow(row, row.time, event.target.value)} />
        </div>)}</div>
      </details>
      <div className="actions"><label className="maintenance-upload"><Upload size={14} />{t("lyricsEdit.import")}<input type="file" accept=".lrc,text/plain" disabled={busy} onChange={event => { void upload(event.target.files?.[0]); event.target.value = ""; }} /></label><Button size="sm" variant="ghost" disabled={busy} onClick={() => { setText(initial); setDelay(0); setAnchor(0); }}><RotateCcw size={14} />{t("lyricsEdit.reset")}</Button></div>
      <div className="field"><span className="field-label">{t("lyricsEdit.delay")}</span><div className="actions"><Button size="sm" variant="outline" disabled={busy || delay <= -3600000} onClick={() => setDelay(value => Math.max(-3600000, value - 100))}>−100 ms</Button><input aria-label={t("lyricsEdit.delay")} type="number" min={-3600000} max={3600000} step={1} disabled={busy} value={delay} onChange={event => setDelay(Math.max(-3600000, Math.min(3600000, Math.round(Number(event.target.value)))))} /><Button size="sm" variant="outline" disabled={busy || delay >= 3600000} onClick={() => setDelay(value => Math.min(3600000, value + 100))}>+100 ms</Button></div></div>
      <label className="field"><span className="field-label">{t("lyricsEdit.anchor")}</span><select disabled={busy || !cues.length} value={Math.min(anchor, Math.max(0, cues.length - 1))} onChange={event => setAnchor(Number(event.target.value))}>{cues.map((cue, index) => <option key={cue.time} value={index}>{audioTime(cue.time)} · {cue.text.slice(0, 80)}</option>)}</select></label>
      <div className="actions"><Button size="sm" variant="outline" disabled={busy || !cues[anchor]} onClick={() => setDelay(Math.max(-3600000, Math.min(3600000, Math.round((position - cues[anchor].time) * 1000))))}>{t("lyricsEdit.align", { time: audioTime(position) })}</Button><Button size="sm" disabled={busy || !cues.length || text === initial && delay === 0} onClick={() => void save()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}{t("lyricsEdit.save")}</Button></div>
      <EditHistory id={id} kind="lyrics" disabled={busy} restore={content => { setText(content.text || ""); setDelay(0); setAnchor(0); }} />
    </>}
  </section>;
}
