import { useEffect, useMemo, useState } from "react";
import { ClipboardCheck, HardDrive, History, Loader2, Wrench } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { libraryQuality, qualityKinds, type QualityKind } from "../library-quality";
import type { Source } from "../types";
import { Button } from "./ui/button";
import { ResourceWarning, useResource, type Notice } from "./resource";
import { ToolSection, ToolStatus, useTool } from "./library-tools";

type Decision = { id: number; kind: QualityKind; state: "todo" | "ignored"; reason: string; updated: number };

export function QualityReview({ sources, selected, select, notify }: { sources: Source[]; selected: Source[]; select: (ids: number[]) => void; notify: Notice }) {
  const { t, number } = useI18n();
  const saved = useResource<Decision[]>("/maintenance/issues");
  const quality = useMemo(() => libraryQuality(sources), [sources]);
  const [kind, setKind] = useState<QualityKind>("metadata");
  const [state, setState] = useState<"open" | "todo" | "ignored">("todo");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [showIgnored, setShowIgnored] = useState(false);
  const revision = JSON.stringify(sources.map(source => [source.id, source.path, source.hash, source.output, source.present, source.output_present, source.artist, source.album, source.title, source.track, source.disc, source.metadata, source.duration, source.error, source.possibly_damaged, source.artwork_url, source.lyrics?.state]));
  useEffect(() => { saved.retry(); }, [revision]);
  const selectedIDs = new Set(selected.map(source => source.id));
  const decisions = useMemo(() => new Map((saved.data || []).map(item => [`${item.kind}:${item.id}`, item] as const)), [saved.data]);
  const decision = (id: number, issue: QualityKind) => decisions.get(`${issue}:${id}`);
  const visible = quality[kind].filter(source => showIgnored || decision(source.id, kind)?.state !== "ignored");
  const chosen = visible.filter(source => selectedIDs.has(source.id));
  const ignored = quality[kind].filter(source => decision(source.id, kind)?.state === "ignored").length;
  async function save() {
    setBusy(true);
    try { await api("/maintenance/issues", "PUT", { ids: chosen.map(source => source.id), kind, state, reason }); saved.retry(); notify("review.saved"); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <section aria-label={t("quality.title")}><h3>{t("quality.title")}</h3><p>{t("quality.hint")}</p><ResourceWarning error={saved.error} retry={saved.retry} /><div className="quality-grid">{qualityKinds.map(issue => {
    const active = quality[issue].filter(source => decision(source.id, issue)?.state !== "ignored");
    return <Button key={issue} variant="outline" size="sm" disabled={!active.length} onClick={() => { setKind(issue); select(active.map(source => source.id)); }}><span>{t(`quality.${issue}`)}</span><strong>{number(active.length)}</strong></Button>;
  })}</div><p className="audition-note">{t("quality.caution")}</p>
    <details className="quality-decisions"><summary><ClipboardCheck size={15} />{t("review.title")}</summary><div className="comparison-body"><p>{t("review.hint")}</p><label className="field"><span className="field-label">{t("review.kind")}</span><select value={kind} onChange={event => setKind(event.target.value as QualityKind)}>{qualityKinds.map(issue => <option key={issue} value={issue}>{t(`quality.${issue}`)}</option>)}</select></label><label className="check-field"><input type="checkbox" checked={showIgnored} onChange={event => setShowIgnored(event.target.checked)} />{t("review.showIgnored", { count: ignored })}</label>
      <div className="actions"><Button size="sm" variant="outline" disabled={!visible.length} onClick={() => select(visible.map(source => source.id))}>{t("review.select")}</Button><p>{t("review.selection", { count: chosen.length })}</p></div>
      <label className="field"><span className="field-label">{t("review.state")}</span><select value={state} disabled={busy} onChange={event => setState(event.target.value as typeof state)}><option value="todo">{t("review.todo")}</option><option value="ignored">{t("review.ignored")}</option><option value="open">{t("review.open")}</option></select></label><label className="field"><span className="field-label">{t("review.reason")}</span><textarea rows={2} maxLength={1000} value={reason} disabled={busy} onChange={event => setReason(event.target.value)} /></label><Button size="sm" disabled={busy || !chosen.length || chosen.length > 1000 || state === "ignored" && !reason.trim()} onClick={() => void save()}>{t("review.save")}</Button>
      {visible.length > 0 && <ul className="review-issues">{visible.slice(0, 100).map(source => { const item = decision(source.id, kind); return <li key={source.id}><strong>{source.title || source.path}</strong> · {t(item?.state === "ignored" ? "review.ignored" : item?.state === "todo" ? "review.todo" : "review.open")}{item?.reason && <p>{item.reason}</p>}</li>; })}</ul>}{visible.length > 100 && <p>{t("review.truncated")}</p>}
    </div></details></section>;
}

type Suggestion = { id: number; title: string; path: string; kind: "build" | "lyrics" | "gain" };

function RepairSuggestions({ sources, select, notify }: { sources: Source[]; select: (ids: number[]) => void; notify: Notice }) {
  const { t, errorMessage } = useI18n();
  const [preview, setPreview] = useState<Suggestion[]>();
  const [chosen, setChosen] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ kind: Suggestion["kind"]; count: number; error?: string }[]>([]);
  function review() {
    const suggestions: Suggestion[] = [];
    for (const source of sources) {
      let kind: Suggestion["kind"] | undefined;
      if (source.present && source.hash && !source.possibly_damaged && ["missing", "changed", "needs_rebuild"].includes(source.status)) kind = "build";
      else if (source.status === "ready" && source.output_present) {
        if (!source.lyrics || !["ready", "instrumental"].includes(source.lyrics.state)) kind = "lyrics";
        else if (!source.output_gain) kind = "gain";
      }
      if (kind) suggestions.push({ id: source.id, title: source.title, path: source.path, kind });
    }
    setPreview(suggestions); setChosen(new Set()); setResults([]);
  }
  async function apply() {
    if (!preview) return;
    setBusy(true); setResults([]);
    let started = false;
    for (const kind of ["build", "lyrics", "gain"] as const) {
      const ids = preview.filter(item => item.kind === kind && chosen.has(item.id)).map(item => item.id);
      if (!ids.length) continue;
      try {
        await api(kind === "build" ? "/library/rebuild" : kind === "lyrics" ? "/lyrics/search" : "/maintenance/loudness", "POST", { ids });
        started = true;
        setResults(old => [...old, { kind, count: ids.length }]);
        setChosen(old => new Set([...old].filter(id => !ids.includes(id))));
      } catch (err) { setResults(old => [...old, { kind, count: ids.length, error: (err as Error).message }]); }
    }
    setBusy(false); if (started) notify("suggestions.started");
  }
  return <section aria-label={t("suggestions.title")}><p>{t("suggestions.hint")}</p><Button size="sm" variant="outline" disabled={busy} onClick={review}>{t("suggestions.preview")}</Button>{preview && <div className="maintenance-report"><p>{t("suggestions.count", { count: preview.length })}</p><p>{t("suggestions.confirmHint")}</p>{preview.length <= 1000 && <label className="check-field"><input type="checkbox" disabled={busy || !preview.length} checked={preview.length > 0 && chosen.size === preview.length} onChange={event => setChosen(new Set(event.target.checked ? preview.map(item => item.id) : []))} />{t("suggestions.selectAll")}</label>}
    <ul className="review-issues">{preview.slice(0, 1000).map(item => <li key={item.id}><label className="check-field"><input type="checkbox" disabled={busy} checked={chosen.has(item.id)} onChange={event => setChosen(old => { const next = new Set(old); if (event.target.checked) next.add(item.id); else next.delete(item.id); return next; })} /><span><strong>{item.title || item.path}</strong> · {t(`suggestions.${item.kind}`)}</span></label><code>{item.path}</code></li>)}</ul>{preview.length > 1000 && <p>{t("suggestions.limit")}</p>}<div className="actions"><Button size="sm" disabled={busy || !chosen.size} onClick={() => void apply()}>{busy && <Loader2 size={14} className="animate-spin" />}{t("suggestions.apply", { count: chosen.size })}</Button><Button size="sm" variant="outline" disabled={!chosen.size} onClick={() => select([...chosen])}>{t("suggestions.selectTracks")}</Button></div>
    </div>}{results.map(result => <p key={result.kind} role={result.error ? "alert" : "status"}>{t(`suggestions.${result.kind}`)} · {result.count}: {result.error ? errorMessage(result.error) : t("suggestions.queued")}</p>)}<p>{t("suggestions.manual")}</p></section>;
}

type OperationItem = { id: number; path: string; output: string; state: "prepared" | "applied" | "failed" | "interrupted" | "undone"; error?: string; undoable: boolean; before?: Record<string, string>; after?: Record<string, string>; before_hash?: string; after_hash?: string };
type Operation = { id: string; kind: "tags" | "lyrics" | "artwork"; created: number; items: OperationItem[] };

function OperationLog({ notify }: { notify: Notice }) {
  const { t, locale, errorMessage } = useI18n();
  const undo = useTool("undo");
  const operations = useResource<Operation[]>("/maintenance/operations", undo.running);
  const [review, setReview] = useState<{ operation: Operation; items: number[] }>();
  const [busy, setBusy] = useState(false);
  const [snapshot, setSnapshot] = useState<{ before: { text?: string; image?: string }; after: { text?: string; image?: string } }>();
  async function preview(operation: Operation) {
    setReview({ operation, items: operation.items.flatMap((item, index) => item.state === "applied" && item.undoable ? [index] : []) }); setSnapshot(undefined);
  }
  async function compare(index: number) {
    if (!review) return;
    setBusy(true);
    try {
      const path = `/maintenance/operations/${review.operation.id}/snapshot?item=${index}&side=`;
      const [before, after] = await Promise.all([api<{ text?: string; image?: string }>(path + "before"), api<{ text?: string; image?: string }>(path + "after")]);
      setSnapshot({ before, after });
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  async function restore() {
    if (!review) return;
    setBusy(true);
    try { await api(`/maintenance/operations/${review.operation.id}/undo`, "POST", { items: review.items }); setReview(undefined); setSnapshot(undefined); undo.retry(); operations.retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <section aria-label={t("operations.title")}><p>{t("operations.hint")}</p><ResourceWarning error={operations.error || undo.error} retry={() => { operations.retry(); undo.retry(); }} /><Button size="sm" variant="outline" disabled={busy} onClick={operations.retry}>{t("operations.refresh")}</Button>{operations.data?.length === 0 && <p>{t("operations.empty")}</p>}
    {operations.data?.map(operation => <div className="maintenance-report" key={operation.id}><p><strong>{t(`operations.${operation.kind}`)}</strong> · {new Date(operation.created / 1e6).toLocaleString(locale)} · {operation.items.length}</p><p>{operation.items.filter(item => item.state === "applied").length} {t("operations.applied")} · {operation.items.filter(item => item.state === "undone").length} {t("operations.undone")}</p><Button size="sm" variant="outline" disabled={busy || undo.running} onClick={() => void preview(operation)}>{t("operations.review")}</Button></div>)}
    {review && <div className="maintenance-report" aria-label={t("operations.preview")}><h4>{t("operations.preview")}</h4><p>{t("operations.conflictHint")}</p><ul className="review-issues">{review.operation.items.map((item, index) => <li key={index}><label className="check-field"><input type="checkbox" checked={review.items.includes(index)} disabled={busy || item.state !== "applied" || !item.undoable} onChange={event => setReview(old => old && ({ ...old, items: event.target.checked ? [...old.items, index] : old.items.filter(value => value !== index) }))} /><strong>{item.path}</strong> · {t(`operations.state.${item.state}`)}</label>{item.error && <p>{errorMessage(item.error)}</p>}{item.before && <p>{t("operations.before")}: {JSON.stringify(item.before)}</p>}{item.after && <p>{t("operations.after")}: {JSON.stringify(item.after)}</p>}{review.operation.kind !== "tags" && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void compare(index)}>{t("operations.compare")}</Button>}{!item.undoable && <p>{t("operations.manual")}</p>}</li>)}</ul>{snapshot && <div className="operation-snapshots">{(["before", "after"] as const).map(side => <div key={side}><strong>{t(`operations.${side}`)}</strong>{snapshot[side].image ? <img className="history-image" src={`data:image/jpeg;base64,${snapshot[side].image}`} alt={t(`operations.${side}`)} /> : <textarea readOnly rows={5} aria-label={t(`operations.${side}`)} value={snapshot[side].text || t("operations.absent")} />}</div>)}</div>}<Button size="sm" disabled={busy || undo.running || !review.items.length} onClick={() => void restore()}>{t("operations.undo", { count: review.items.length })}</Button></div>}
    <ToolStatus kind="undo" report={undo.data} retry={undo.retry} notify={notify} />
  </section>;
}

type Space = { path: string; free_bytes: number; total_bytes: number; empty: boolean; error?: string };
type Health = { source: string; output: string; config: string; online: boolean; checked_at?: number; message: string; tracks: number; outputs: number; output_bytes: number; staging_bytes: number; staging_limit_bytes: number };
type Migration = { source: string; output: Space; source_bytes: number; output_bytes: number; paths_applied: boolean };

function StorageHealth({ sources, selected, notify }: { sources: Source[]; selected: Source[]; notify: Notice }) {
  const { t, number, date, errorMessage } = useI18n();
  const health = useResource<Health>("/maintenance/storage/health", 10000);
  const verification = useTool("storage");
  const [spaces, setSpaces] = useState<Record<string, Space | string>>();
  const [source, setSource] = useState("");
  const [output, setOutput] = useState("");
  const [plan, setPlan] = useState<Migration>();
  const [busy, setBusy] = useState(false);
  const outputs = (selected.length ? selected : sources).filter(item => item.output_present && item.output);
  async function action(kind: "check" | "verify" | "migration") {
    setBusy(true);
    try {
      if (kind === "check") { setSpaces(await api<Record<string, Space | string>>("/maintenance/storage/check", "POST", {})); health.retry(); }
      else if (kind === "verify") { await api("/maintenance/storage/verify", "POST", { ids: outputs.map(item => item.id) }); verification.retry(); }
      else setPlan(await api<Migration>("/maintenance/migration/preview", "POST", { source, output }));
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <section aria-label={t("storage.title")}><p>{t("storage.hint")}</p><ResourceWarning error={health.error || verification.error} retry={() => { health.retry(); verification.retry(); }} />{health.data && <div className="maintenance-report"><p role="status">{health.data.online ? t("storage.online") : errorMessage(health.data.message)}</p>{health.data.checked_at ? <p>{t("storage.lastChecked", { time: date(health.data.checked_at) })}</p> : null}<dl><dt>{t("storage.source")}</dt><dd><code>{health.data.source}</code></dd><dt>{t("storage.output")}</dt><dd><code>{health.data.output}</code></dd><dt>{t("storage.config")}</dt><dd><code>{health.data.config}</code></dd><dt>{t("storage.indexed")}</dt><dd>{health.data.tracks} / {health.data.outputs} · {number(health.data.output_bytes)} B</dd><dt>{t("storage.staging")}</dt><dd>{number(health.data.staging_bytes)} / {number(health.data.staging_limit_bytes)} B</dd></dl></div>}<div className="actions"><Button size="sm" variant="outline" disabled={busy} onClick={() => void action("check")}>{t("storage.check")}</Button><Button size="sm" variant="outline" disabled={busy || verification.running || !outputs.length || outputs.length > 1000} onClick={() => void action("verify")}>{t("storage.verify", { count: outputs.length })}</Button></div>{spaces && <ul>{Object.entries(spaces).map(([key, value]) => <li key={key}><strong>{t(key === "ownership_error" ? "storage.ownership" : key === "source" ? "storage.source" : key === "output" ? "storage.output" : key === "config" ? "storage.config" : "storage.staging")}</strong>: {typeof value === "string" ? errorMessage(value) : value.error ? errorMessage(value.error) : "free_bytes" in value ? t("storage.space", { free: number(value.free_bytes), total: number(value.total_bytes) }) : t("storage.online")}</li>)}</ul>}<ToolStatus kind="storage" report={verification.data} retry={verification.retry} notify={notify} />
    <details className="migration-guide"><summary>{t("migration.title")}</summary><div className="comparison-body"><p>{t("migration.hint")}</p><label className="field"><span className="field-label">{t("migration.source")}</span><input value={source} disabled={busy} onChange={event => { setSource(event.target.value); setPlan(undefined); }} /></label><label className="field"><span className="field-label">{t("migration.output")}</span><input value={output} disabled={busy} onChange={event => { setOutput(event.target.value); setPlan(undefined); }} /></label><Button size="sm" variant="outline" disabled={busy || !source || !output} onClick={() => void action("migration")}>{t("migration.preview")}</Button>{plan && <div className="maintenance-report"><p>{t("migration.checked")}</p><code>{plan.source}</code><p><code>{plan.output.path}</code></p><p>{t("migration.size", { source: number(plan.source_bytes), output: number(plan.output_bytes), free: number(plan.output.free_bytes) })}</p><p>{plan.output.empty ? t("migration.empty") : t("migration.owned")}</p><ol>{(["pause", "backup", "copy", "mount", "verify", "resume"] as const).map(step => <li key={step}>{t(`migration.${step}`)}</li>)}</ol><p>{t("migration.restore")}</p><a href="/settings?section=library">{t("migration.settings")}</a></div>}</div></details>
  </section>;
}

export function MaintenanceReview({ sources, selected, select, notify }: { sources: Source[]; selected: Source[]; select: (ids: number[]) => void; notify: Notice }) {
  const { t } = useI18n();
  return <div className="library-tools"><ToolSection title={t("suggestions.title")} icon={<Wrench size={16} />}><RepairSuggestions sources={sources} select={select} notify={notify} /></ToolSection><ToolSection title={t("operations.title")} icon={<History size={16} />}><OperationLog notify={notify} /></ToolSection><ToolSection title={t("storage.title")} icon={<HardDrive size={16} />}><StorageHealth sources={sources} selected={selected} notify={notify} /></ToolSection></div>;
}
