import { useEffect, useState, type ReactNode } from "react";
import { Download, Fingerprint, ListMusic, Loader2, Square, Tags, Trash2 } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { LoudnessReport, Source } from "../types";
import { Button } from "./ui/button";
import { ResourceWarning, useResource, type Notice } from "./resource";

type ToolReport = LoudnessReport & { issues?: { id: number; error: string }[]; groups?: number[][] };
function useTool(kind: string) {
  const [poll, setPoll] = useState(false);
  const resource = useResource<ToolReport>(`/maintenance/${kind}`, poll);
  const running = resource.data?.state === "running";
  useEffect(() => { setPoll(running); }, [running]);
  return { ...resource, running };
}
function ToolStatus({ report, kind, retry, notify }: { report?: ToolReport; kind: string; retry: () => void; notify: Notice }) {
  const { t, number, errorMessage } = useI18n();
  const [busy, setBusy] = useState(false);
  async function stop() {
    setBusy(true);
    try { await api(`/maintenance/${kind}`, "DELETE", {}); retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  if (!report || report.state === "idle") return null;
  return <div className="maintenance-report" role="status"><p>{t(`gain.state.${report.state}`)} · {t("gain.progress", { done: number(report.processed), total: number(report.total), updated: number(report.updated), skipped: number(report.skipped), failed: number(report.failed) })}</p>{report.current && <code>{report.current}</code>}{report.state === "running" && <Button size="sm" variant="outline" disabled={busy} onClick={() => void stop()}><Square size={14} />{t("tools.stop")}</Button>}{report.issues?.map((issue, index) => <p className="repair-warning" key={`${issue.id}-${index}`}>#{issue.id}: {errorMessage(issue.error)}</p>)}</div>;
}

function ToolSection({ title, icon, children }: { title: string; icon: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return <details className="library-tool" onToggle={event => setOpen(event.currentTarget.open)}><summary>{icon}{title}</summary>{open && <div className="comparison-body">{children}</div>}</details>;
}

const tagKeys = ["title", "artist", "album", "album_artist", "track", "disc"] as const;
type TagKey = typeof tagKeys[number];
type TagPreview = { token: string; tracks: { id: number; path: string; before: Record<string, string>; after: Record<string, string> }[] };

function OutputTagEditor({ selected, notify }: { selected: Source[]; notify: Notice }) {
  const { t, errorMessage } = useI18n();
  const report = useTool("tags");
  const outputs = selected.filter(source => source.output_present && source.output);
  const [enabled, setEnabled] = useState<Set<TagKey>>(new Set());
  const [values, setValues] = useState<Record<TagKey, string>>({ title: "", artist: "", album: "", album_artist: "", track: "", disc: "" });
  const [preview, setPreview] = useState<TagPreview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const identity = outputs.map(source => source.id).join(",");
  useEffect(() => { setPreview(undefined); }, [identity]);
  async function review() {
    setBusy(true); setError(undefined);
    try { setPreview(await api<TagPreview>("/library/tags/preview", "POST", { ids: outputs.map(source => source.id), tags: Object.fromEntries([...enabled].map(key => [key, values[key]])) })); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function apply() {
    setBusy(true); setError(undefined);
    try { await api("/maintenance/tags", "POST", { token: preview?.token }); setPreview(undefined); report.retry(); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  return <section aria-label={t("tags.title")}><p>{t("tags.hint")}</p><p>{t("tags.scope", { count: outputs.length })}</p><ResourceWarning error={report.error} retry={report.retry} /><div className="tag-editor-fields">{tagKeys.map(key => <div className="field" key={key}><label className="check-field"><input type="checkbox" checked={enabled.has(key)} disabled={busy || report.running} onChange={event => { setEnabled(old => { const next = new Set(old); if (event.target.checked) next.add(key); else next.delete(key); return next; }); setPreview(undefined); }} />{t((key === "title" ? "tags.titleField" : `tags.${key}`) as MessageKey)}</label><input aria-label={t((key === "title" ? "tags.titleField" : `tags.${key}`) as MessageKey)} value={values[key]} maxLength={4096} disabled={busy || report.running || !enabled.has(key)} onChange={event => { setValues(old => ({ ...old, [key]: event.target.value })); setPreview(undefined); }} /></div>)}</div>{error && <p role="alert">{errorMessage(error.message)}</p>}<Button size="sm" variant="outline" disabled={busy || report.running || !enabled.size || !outputs.length || outputs.length > 1000} onClick={() => void review()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Tags size={14} />}{t("tags.preview")}</Button>
    {preview && <div className="maintenance-report"><p>{t("tags.previewHint")}</p><div className="tool-table-scroll"><table className="tool-table"><thead><tr><th>{t("tags.output")}</th><th>{t("tags.before")}</th><th>{t("tags.after")}</th></tr></thead><tbody>{preview.tracks.map(track => <tr key={track.id}><td><code>{track.path}</code></td><td>{Object.entries(track.before).map(([key, value]) => <p key={key}>{t((key === "title" ? "tags.titleField" : `tags.${key}`) as MessageKey)}: {value || "—"}</p>)}</td><td>{Object.entries(track.after).map(([key, value]) => <p key={key}>{t((key === "title" ? "tags.titleField" : `tags.${key}`) as MessageKey)}: {value || t("tags.clear")}</p>)}</td></tr>)}</tbody></table></div><Button size="sm" disabled={busy || report.running} onClick={() => void apply()}>{t("tags.apply", { count: preview.tracks.length })}</Button></div>}
    <ToolStatus kind="tags" report={report.data} retry={report.retry} notify={notify} />
  </section>;
}

function FingerprintTool({ sources, selected, select, notify }: { sources: Source[]; selected: Source[]; select: (ids: number[]) => void; notify: Notice }) {
  const { t } = useI18n();
  const report = useTool("fingerprints");
  const [busy, setBusy] = useState(false);
  async function start(scope: Source[]) {
    setBusy(true);
    try { await api("/maintenance/fingerprints", "POST", { ids: scope.filter(source => source.output_present && source.output).map(source => source.id) }); report.retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  const outputs = sources.filter(source => source.output_present && source.output);
  const chosen = selected.filter(source => source.output_present && source.output);
  const visibleIDs = new Set(sources.map(source => source.id));
  const groups = report.data?.groups?.map(ids => ids.filter(id => visibleIDs.has(id))).filter(ids => ids.length > 1) || [];
  return <section aria-label={t("fingerprint.title")}><p>{t("fingerprint.hint")}</p><ResourceWarning error={report.error} retry={report.retry} /><div className="actions"><Button size="sm" variant="outline" disabled={busy || report.running || outputs.length < 2 || outputs.length > 1000} onClick={() => void start(sources)}><Fingerprint size={14} />{t("fingerprint.scope")}</Button><Button size="sm" variant="outline" disabled={busy || report.running || chosen.length < 2 || chosen.length > 1000} onClick={() => void start(selected)}>{t("fingerprint.selected")}</Button></div><ToolStatus kind="fingerprints" report={report.data} retry={report.retry} notify={notify} />{report.data?.state === "complete" && <p>{t("fingerprint.groups", { count: groups.length })}</p>}{groups.map((ids, index) => <div className="maintenance-report" key={ids.join(",")}><p>{ids.map(id => sources.find(source => source.id === id)?.title || `#${id}`).join(" · ")}</p><Button size="sm" variant="outline" onClick={() => select(ids)}>{t("fingerprint.review", { count: index + 1 })}</Button></div>)}</section>;
}

type PlaylistResult = { id?: string; name: string; content: string; ready: number; missing: { path: string; original: string }[] };
type PlaylistInput = { name: string; text: string; base: string; ids?: number[] };

function PlaylistTool({ sources, selected, notify }: { sources: Source[]; selected: Source[]; notify: Notice }) {
  const { t, errorMessage } = useI18n();
  const saved = useResource<{ id: string; name: string; count: number }[]>("/playlists");
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [base, setBase] = useState("");
  const [preview, setPreview] = useState<PlaylistResult>();
  const [input, setInput] = useState<PlaylistInput>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  async function upload(file?: File) {
    if (!file) return;
    setPreview(undefined); setInput(undefined); setError(undefined); setBusy(true);
    try {
      if (file.size > 4*1024*1024) throw new Error(t("playlist.limit"));
      setText(new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer())); setName(file.name.replace(/\.m3u8?$/i, ""));
    } catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function review(ids?: number[]) {
    const body = { name, text: ids ? "" : text, base, ...(ids ? { ids } : {}) };
    setBusy(true); setError(undefined);
    try { setPreview(await api<PlaylistResult>("/library/playlists/preview", "POST", body)); setInput(body); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function save() {
    setBusy(true); setError(undefined);
    try { setPreview(await api<PlaylistResult>("/playlists", "POST", input)); setInput(undefined); saved.retry(); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function sync(id: string, remove = false) {
    setBusy(true); setError(undefined);
    try { if (remove) { await api(`/playlists/${id}`, "DELETE"); saved.retry(); } else { setPreview(await api<PlaylistResult>(`/playlists/${id}`)); setInput(undefined); } }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  function download() {
    if (!preview) return;
    const url = URL.createObjectURL(new Blob([preview.content], { type: "audio/x-mpegurl;charset=utf-8" }));
    const link = document.createElement("a"); link.href = url; link.download = `${preview.name.replace(/[\\/:*?"<>|\x00-\x1f]/g, "_")}.m3u8`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return <section aria-label={t("playlist.title")}><p>{t("playlist.hint")}</p><label className="field"><span className="field-label">{t("playlist.name")}</span><input value={name} disabled={busy} maxLength={160} onChange={event => { setName(event.target.value); setPreview(undefined); }} /></label><label className="field"><span className="field-label">{t("playlist.base")}</span><input value={base} placeholder="Artist/Album" disabled={busy} onChange={event => { setBase(event.target.value); setPreview(undefined); }} /></label><label className="field"><span className="field-label">{t("playlist.text")}</span><textarea rows={4} dir="auto" value={text} disabled={busy} onChange={event => { setText(event.target.value); setPreview(undefined); }} /></label><div className="actions"><label className="maintenance-upload">{t("playlist.import")}<input type="file" accept=".m3u,.m3u8,audio/x-mpegurl" disabled={busy} onChange={event => { void upload(event.target.files?.[0]); event.target.value = ""; }} /></label><Button size="sm" variant="outline" disabled={busy || !name.trim() || !text.trim()} onClick={() => void review()}>{t("playlist.preview")}</Button><Button size="sm" variant="outline" disabled={busy || !name.trim() || !sources.length || sources.length > 10000} onClick={() => void review(sources.map(source => source.id))}>{t("playlist.scope")}</Button><Button size="sm" variant="outline" disabled={busy || !name.trim() || !selected.length || selected.length > 10000} onClick={() => void review(selected.map(source => source.id))}>{t("playlist.selected")}</Button></div>{error && <p role="alert">{errorMessage(error.message)}</p>}{preview && <div className="maintenance-report"><h4>{preview.name}</h4><p>{t("playlist.result", { ready: preview.ready, missing: preview.missing.length })}</p>{preview.missing.length > 0 && <details><summary>{t("playlist.missing")}</summary><ul>{preview.missing.map((item, index) => <li key={index}><code>{item.original}</code></li>)}</ul></details>}<p>{t("playlist.location")}</p><div className="actions"><Button size="sm" disabled={busy || !preview.ready} onClick={download}><Download size={14} />{t("playlist.download")}</Button>{input && <Button size="sm" variant="outline" disabled={busy} onClick={() => void save()}>{t("playlist.save")}</Button>}</div></div>}<ResourceWarning error={saved.error} retry={saved.retry} />{!!saved.data?.length && <div className="saved-playlists"><h4>{t("playlist.saved")}</h4>{saved.data.map(playlist => <div className="actions" key={playlist.id}><Button size="sm" variant="outline" disabled={busy} onClick={() => void sync(playlist.id)}>{t("playlist.sync", { name: playlist.name, count: playlist.count })}</Button><Button size="icon" variant="ghost" aria-label={t("playlist.remove", { name: playlist.name })} disabled={busy} onClick={() => void sync(playlist.id, true)}><Trash2 size={14} /></Button></div>)}</div>}</section>;
}

export function LibraryTools({ sources, selected, select, notify }: { sources: Source[]; selected: Source[]; select: (ids: number[]) => void; notify: Notice }) {
  const { t } = useI18n();
  return <div className="library-tools"><ToolSection title={t("tags.title")} icon={<Tags size={16} />}><OutputTagEditor selected={selected} notify={notify} /></ToolSection><ToolSection title={t("playlist.title")} icon={<ListMusic size={16} />}><PlaylistTool sources={sources} selected={selected} notify={notify} /></ToolSection><ToolSection title={t("fingerprint.title")} icon={<Fingerprint size={16} />}><FingerprintTool sources={sources} selected={selected} select={select} notify={notify} /></ToolSection></div>;
}
