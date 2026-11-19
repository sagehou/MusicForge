import { InfoHint } from "./info-hint";
import { InlineDetails } from "./inline-details";
import { useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, Search } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { libraryQuality, numberingIssues, qualityKinds, sourceDiscKey, type QualityKind } from "../library-quality";
import type { Source } from "../types";
import { Button } from "./ui/button";
import { ResourceWarning, useResource, type Notice } from "./resource";
import { ToolStatus, useTool } from "./library-tools";
import { useRevealPreview } from "./use-reveal-preview";

type ReviewKind = Exclude<QualityKind, "lyrics" | "covers">;
const reviewKinds = qualityKinds.filter(kind => kind !== "lyrics" && kind !== "covers");

type Decision = { id: number; kind: QualityKind; state: "todo" | "ignored"; reason: string; updated: number };

export function QualityReview({ sources, select, notify, onBusyChange }: { sources: Source[]; select: (ids: number[]) => void; notify: Notice; onBusyChange: (busy: boolean) => void }) {
  const { t, number, errorMessage } = useI18n();
  const saved = useResource<Decision[]>("/maintenance/issues");
  const quality = useMemo(() => libraryQuality(sources), [sources]);
  const [kind, setKind] = useState<ReviewKind>(() => reviewKinds.find(issue => quality[issue].length > 0) || "metadata");
  const [state, setState] = useState<"open" | "todo" | "ignored">("todo");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [showIgnored, setShowIgnored] = useState(false);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  useEffect(() => { onBusyChange(busy); return () => onBusyChange(false); }, [busy, onBusyChange]);
  const revision = JSON.stringify(sources.map(source => [source.id, source.path, source.hash, source.output, source.present, source.output_present, source.artist, source.album, source.title, source.track, source.disc, source.metadata, source.duration, source.error, source.possibly_damaged, source.artwork_url, source.lyrics?.state]));
  useEffect(() => { saved.retry(); }, [revision]);
  const [selectedIDs, setSelectedIDs] = useState<Set<number>>(new Set());
  const decisions = useMemo(() => new Map((saved.data || []).map(item => [`${item.kind}:${item.id}`, item] as const)), [saved.data]);
  const decision = (id: number, issue: QualityKind) => decisions.get(`${issue}:${id}`);
  const visible = quality[kind].filter(source => (showIgnored || decision(source.id, kind)?.state !== "ignored") && `${source.title} ${source.artist} ${source.album} ${source.path} ${source.error}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
  const offset = Math.min(page * 20, Math.max(0, Math.floor((visible.length - 1) / 20) * 20));
  const shown = visible.slice(offset, offset + 20);
  const chosen = visible.filter(source => selectedIDs.has(source.id));
  const ignored = quality[kind].filter(source => decision(source.id, kind)?.state === "ignored").length;
  async function save() {
    setBusy(true);
    try { await api("/maintenance/issues", "PUT", { ids: chosen.map(source => source.id), kind, state, reason }); saved.retry(); notify("review.saved"); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  function evidence(source: Source) {
    if (kind === "metadata") return t("quality.missingFields", { fields: (["title", "artist", "album"] as const).filter(field => !source[field]?.trim()).map(field => t(`quality.field.${field}`)).join(" / ") });
    if (kind === "duration") return t("quality.durationValue", { seconds: Math.round(source.duration || 0) });
    if (kind === "duplicates") return t("quality.duplicateCount", { count: quality.duplicates.filter(item => item.hash === source.hash && item.id !== source.id).length });
    if (kind === "failed") return errorMessage(source.error) || t("quality.failed");
    if (kind === "damaged") return errorMessage(source.error) || t("quality.damageEvidence");
    const issues = numberingIssues(quality.numbering.filter(item => sourceDiscKey(item) === sourceDiscKey(source)));
    return [t("quality.trackNumber", { disc: source.disc || 1, track: source.track || "—" }), issues.missing.length && t("quality.numberGaps", { numbers: issues.missing.join(", ") }), issues.repeated.length && t("quality.numberRepeats", { numbers: issues.repeated.join(", ") }), issues.untagged > 0 && t("quality.numberUntagged", { count: issues.untagged })].filter(Boolean).join(" · ");
  }
  return <section className="source-checks" aria-label={t("quality.title")}>
    <div className="section-heading"><h3>{t("quality.title")}</h3><InfoHint label={`${t("quality.title")} · ${t("app.moreInfo")}`}><p>{t("quality.hint")}</p><p>{t(`quality.action.${kind}`)}</p><p>{t("quality.caution")}</p></InfoHint></div>
    <ResourceWarning error={saved.error} retry={saved.retry} />
    <div className="quality-grid" role="group" aria-label={t("review.kind")}>{reviewKinds.map(issue => {
      const active = quality[issue].filter(source => decision(source.id, issue)?.state !== "ignored");
      return <Button key={issue} variant="outline" size="sm" disabled={busy} aria-pressed={kind === issue} onClick={() => { setKind(issue); setPage(0); setSelectedIDs(new Set()); }}><span>{t(`quality.${issue}`)}</span><strong>{number(active.length)}</strong></Button>;
    })}</div>
    <div className="maintenance-query"><label className="field"><span>{t("workbench.findTrack")}</span><input type="search" value={query} disabled={busy} onChange={event => { setQuery(event.target.value); setPage(0); setSelectedIDs(new Set()); }} /></label><label className="check-field"><input type="checkbox" disabled={busy} checked={showIgnored} onChange={event => { setShowIgnored(event.target.checked); setPage(0); }} />{t("review.showIgnored", { count: ignored })}</label></div>
    {visible.length ? <>
      <div className="actions quality-selection"><Button size="sm" variant="outline" disabled={busy} onClick={() => setSelectedIDs(new Set(shown.map(source => source.id)))}>{t("review.select")}</Button><span>{t("review.selection", { count: chosen.length })}</span></div>
      <ul className="review-issues source-check-list" role="list">{shown.map(source => {
        const item = decision(source.id, kind);
        return <li key={source.id}><div className="review-issue-row"><label className="check-field"><input type="checkbox" disabled={busy} checked={selectedIDs.has(source.id)} onChange={event => setSelectedIDs(old => { const next = new Set(old); if (event.target.checked) next.add(source.id); else next.delete(source.id); return next; })} /><span className="source-check-identity"><strong dir="auto">{source.title || source.path}</strong><small dir="auto">{source.artist} · {source.album}</small></span></label><span>{t(item?.state === "ignored" ? "review.ignored" : item?.state === "todo" ? "review.todo" : "review.open")}</span><Button size="sm" variant="ghost" disabled={busy} onClick={() => select([source.id])}>{t("workbench.viewTrack")}</Button></div><p className="source-check-evidence">{evidence(source)}</p><code className="source-check-path">{source.path}</code>{kind === "duplicates" && quality.duplicates.filter(peer => peer.hash === source.hash && peer.id !== source.id).map(peer => <code className="source-check-path" key={peer.id}>{peer.path}</code>)}{item?.reason && <p>{item.reason}</p>}</li>;
      })}</ul>
      {visible.length > 20 && <div className="pagination"><span>{t("library.pagination", { start: offset + 1, end: Math.min(offset + 20, visible.length), total: visible.length })}</span><Button size="icon" variant="ghost" disabled={busy || offset === 0} aria-label={t("review.previous")} onClick={() => setPage(Math.max(0, page - 1))}><ChevronLeft size={16} /></Button><Button size="icon" variant="ghost" disabled={busy || offset + 20 >= visible.length} aria-label={t("review.next")} onClick={() => setPage(Math.floor(offset / 20) + 1)}><ChevronRight size={16} /></Button></div>}
      {chosen.length > 0 && <section className="quality-decisions"><div className="section-heading"><h4>{t("review.title")}</h4><InfoHint>{t("review.hint")}</InfoHint></div>
        <div className="quality-decision-fields"><label className="field"><span className="field-label">{t("review.state")}</span><select value={state} disabled={busy} onChange={event => setState(event.target.value as typeof state)}><option value="todo">{t("review.todo")}</option><option value="ignored">{t("review.ignored")}</option><option value="open">{t("review.open")}</option></select></label><label className="field"><span className="field-label">{t("review.reason")}</span><textarea rows={2} maxLength={1000} value={reason} disabled={busy} onChange={event => setReason(event.target.value)} /></label></div>
        <Button size="sm" disabled={busy || chosen.length > 1000 || state === "ignored" && !reason.trim()} onClick={() => void save()}>{t("review.save")}</Button>
      </section>}
    </> : <div className="content-maintenance-empty"><p>{t(query ? "library.noMatch" : "workbench.noIssues")}</p><Button size="sm" variant="outline" onClick={() => { setQuery(""); saved.retry(); }}>{t(query ? "library.clearSearch" : "review.refresh")}</Button></div>}
  </section>;
}

type OperationItem = { id: number; path: string; output: string; state: "prepared" | "applied" | "failed" | "interrupted" | "undone"; error?: string; undoable: boolean; before?: Record<string, string>; after?: Record<string, string>; before_hash?: string; after_hash?: string };
type Operation = { id: string; kind: "tags" | "lyrics" | "artwork"; created: number; items: OperationItem[] };

export function OperationLog({ notify, onBusyChange }: { notify: Notice; onBusyChange: (busy: boolean) => void }) {
  const { t, locale, errorMessage } = useI18n();
  const undo = useTool("undo");
  const operations = useResource<Operation[]>("/maintenance/operations", undo.running);
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("all");
  const [state, setState] = useState("all");
  const [page, setPage] = useState(0);
  const [review, setReview] = useState<{ operation: Operation; items: number[] }>();
  const [busy, setBusy] = useState(false);
  const [snapshot, setSnapshot] = useState<{ path: string; before: { text?: string; image?: string }; after: { text?: string; image?: string } }>();
  const comparison = useRevealPreview<HTMLElement>(snapshot, true);
  useEffect(() => { onBusyChange(busy); return () => onBusyChange(false); }, [busy, onBusyChange]);
  const matching = (operations.data || []).filter(operation => (kind === "all" || operation.kind === kind) && operation.items.some(item => (state === "all" || item.state === state) && `${item.path} ${item.output} ${item.error || ""}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
  const offset = Math.min(page * 10, Math.max(0, Math.floor((matching.length - 1) / 10) * 10));
  function preview(operation: Operation) {
    setReview({ operation, items: [] }); setSnapshot(undefined);
  }
  async function compare(index: number) {
    if (!review) return;
    setBusy(true);
    try {
      const path = `/maintenance/operations/${review.operation.id}/snapshot?item=${index}&side=`;
      const [before, after] = await Promise.all([api<{ text?: string; image?: string }>(path + "before"), api<{ text?: string; image?: string }>(path + "after")]);
      setSnapshot({ path: review.operation.items[index].path, before, after });
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
  return <section className="operation-log" aria-label={t("operations.title")}>
    <div className="section-heading"><h3>{t("operations.title")}</h3><div className="actions"><InfoHint label={`${t("operations.title")} · ${t("app.moreInfo")}`}>{t("operations.hint")}</InfoHint><Button asChild size="sm" variant="ghost"><a href="/jobs">{t("operations.jobs")}</a></Button>{!!operations.data?.length && <Button size="sm" variant="outline" disabled={busy} onClick={operations.retry}>{t("operations.refresh")}</Button>}</div></div>
    <ResourceWarning error={operations.error || undo.error} retry={() => { operations.retry(); undo.retry(); }} />
    <div className="maintenance-query operation-filters">
      <label className="field"><span><Search size={14} aria-hidden="true" />{t("operations.search")}</span><input type="search" value={query} disabled={busy} onChange={event => { setQuery(event.target.value); setPage(0); }} /></label>
      <label className="field"><span>{t("operations.type")}</span><select value={kind} disabled={busy} onChange={event => { setKind(event.target.value); setPage(0); }}><option value="all">{t("workbench.showAll")}</option>{(["tags", "lyrics", "artwork"] as const).map(item => <option key={item} value={item}>{t(`operations.${item}`)}</option>)}</select></label>
      <label className="field"><span>{t("operations.result")}</span><select value={state} disabled={busy} onChange={event => { setState(event.target.value); setPage(0); }}><option value="all">{t("workbench.showAll")}</option>{(["applied", "failed", "undone", "interrupted", "prepared"] as const).map(item => <option key={item} value={item}>{t(`operations.state.${item}`)}</option>)}</select></label>
    </div>
    {matching.length ? <>
      <div className="operation-table-wrap" role="region" aria-label={t("operations.results")} tabIndex={0}><table className="operation-table"><caption className="sr-only">{t("operations.results")}</caption><thead><tr><th scope="col">{t("operations.time")}</th><th scope="col">{t("operations.type")}</th><th scope="col">{t("operations.targets")}</th><th scope="col">{t("operations.result")}</th><th scope="col"><span className="sr-only">{t("operations.review")}</span></th></tr></thead><tbody>{matching.slice(offset, offset + 10).map(operation => <tr key={operation.id}>
        <td data-label={t("operations.time")}><time dateTime={new Date(operation.created / 1e6).toISOString()}>{new Date(operation.created / 1e6).toLocaleString(locale)}</time></td>
        <td data-label={t("operations.type")}>{t(`operations.${operation.kind}`)}</td>
        <td data-label={t("operations.targets")}><code id={`operation-path-${operation.id}`}>{operation.items.find(item => (state === "all" || item.state === state) && `${item.path} ${item.output} ${item.error || ""}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))?.path || operation.items[0]?.path}</code><small>{t("operations.total", { count: operation.items.length })}</small></td>
        <td data-label={t("operations.result")}><div className="operation-results">{(["applied", "failed", "undone", "interrupted", "prepared"] as const).map(result => { const count = operation.items.filter(item => item.state === result).length; return count > 0 && <span key={result} data-state={result}>{t(`operations.state.${result}`)} {count}</span>; })}</div></td>
        <td><Button id={`operation-${operation.id}`} aria-describedby={`operation-path-${operation.id}`} size="sm" variant="ghost" disabled={busy || undo.running} aria-expanded={review?.operation.id === operation.id} aria-controls={review?.operation.id === operation.id ? "operation-detail" : undefined} onClick={() => preview(operation)}>{t("operations.review")}</Button></td>
      </tr>)}</tbody></table></div>
      {matching.length > 10 && <div className="pagination"><span>{t("library.pagination", { start: offset + 1, end: Math.min(offset + 10, matching.length), total: matching.length })}</span><Button size="icon" variant="ghost" disabled={busy || offset === 0} aria-label={t("operations.previous")} onClick={() => setPage(Math.max(0, page - 1))}><ChevronLeft size={16} /></Button><Button size="icon" variant="ghost" disabled={busy || offset + 10 >= matching.length} aria-label={t("operations.next")} onClick={() => setPage(Math.floor(offset / 10) + 1)}><ChevronRight size={16} /></Button></div>}
    </> : <div className="content-maintenance-empty"><p>{t(operations.data?.length ? "operations.noMatch" : "operations.empty")}</p><Button size="sm" variant="outline" disabled={busy} onClick={() => { setQuery(""); setKind("all"); setState("all"); operations.retry(); }}>{t("operations.refresh")}</Button></div>}
    {review && <InlineDetails id="operation-detail" title={t("operations.preview")} close={() => { setReview(undefined); setSnapshot(undefined); }} returnFocusID={`operation-${review.operation.id}`} busy={busy} actions={<InfoHint>{t("operations.conflictHint")}</InfoHint>}>
      {snapshot && <section ref={comparison} className="operation-comparison preview-reveal" tabIndex={-1} aria-label={t("operations.compare")}><code>{snapshot.path}</code><div className="operation-snapshots">{(["before", "after"] as const).map(side => <div key={side}><strong>{t(`operations.${side}`)}</strong>{snapshot[side].image ? <img className="history-image" src={`data:image/jpeg;base64,${snapshot[side].image}`} alt={t(`operations.${side}`)} /> : <textarea readOnly rows={5} aria-label={t(`operations.${side}`)} value={snapshot[side].text || t("operations.absent")} />}</div>)}</div></section>}
      <ul className="review-issues">{review.operation.items.map((item, index) => <li key={index}><label className="check-field"><input type="checkbox" checked={review.items.includes(index)} disabled={busy || item.state !== "applied" || !item.undoable} onChange={event => setReview(old => old && ({ ...old, items: event.target.checked ? [...old.items, index] : old.items.filter(value => value !== index) }))} /><strong>{item.path}</strong> · {t(`operations.state.${item.state}`)}</label>{item.output && <code className="source-check-path">{item.output}</code>}{item.error && <p className="repair-warning">{errorMessage(item.error)}</p>}{item.before && <p>{t("operations.before")}: {JSON.stringify(item.before)}</p>}{item.after && <p>{t("operations.after")}: {JSON.stringify(item.after)}</p>}{review.operation.kind !== "tags" && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void compare(index)}>{t("operations.compare")}</Button>}{!item.undoable && <p>{t("operations.manual")}</p>}</li>)}</ul>
      <Button size="sm" disabled={busy || undo.running || !review.items.length} onClick={() => void restore()}>{t("operations.undo", { count: review.items.length })}</Button>
    </InlineDetails>}
    <ToolStatus kind="undo" report={undo.data} retry={undo.retry} notify={notify} />
  </section>;
}

type Space = { path: string; free_bytes: number; total_bytes: number; empty: boolean; error?: string };
type Health = { source: string; output: string; config: string; online: boolean; checked_at?: number; message: string; tracks: number; outputs: number; output_bytes: number; staging_bytes: number; staging_limit_bytes: number };
type Migration = { source: string; output: Space; source_bytes: number; output_bytes: number; paths_applied: boolean };

export function StorageHealth({ sources, selected, notify }: { sources: Source[]; selected: Source[]; notify: Notice }) {
  const { t, number, date, errorMessage } = useI18n();
  const health = useResource<Health>("/maintenance/storage/health", 10000);
  const verification = useTool("storage");
  const [spaces, setSpaces] = useState<Record<string, Space | string>>();
  const [source, setSource] = useState("");
  const [output, setOutput] = useState("");
  const [plan, setPlan] = useState<Migration>();
  const migrationPreview = useRevealPreview<HTMLDivElement>(plan);
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
  return <section aria-label={t("storage.title")}><InfoHint>{t("storage.hint")}</InfoHint><ResourceWarning error={health.error || verification.error} retry={() => { health.retry(); verification.retry(); }} />{health.data && <div className="maintenance-report"><p role="status">{health.data.online ? t("storage.online") : errorMessage(health.data.message)}</p>{health.data.checked_at ? <p>{t("storage.lastChecked", { time: date(health.data.checked_at) })}</p> : null}<dl><dt>{t("storage.source")}</dt><dd><code>{health.data.source}</code></dd><dt>{t("storage.output")}</dt><dd><code>{health.data.output}</code></dd><dt>{t("storage.config")}</dt><dd><code>{health.data.config}</code></dd><dt>{t("storage.indexed")}</dt><dd>{health.data.tracks} / {health.data.outputs} · {number(health.data.output_bytes)} B</dd><dt>{t("storage.staging")}</dt><dd>{number(health.data.staging_bytes)} / {number(health.data.staging_limit_bytes)} B</dd></dl></div>}<div className="actions"><Button size="sm" variant="outline" disabled={busy} onClick={() => void action("check")}>{t("storage.check")}</Button><Button size="sm" variant="outline" disabled={busy || verification.running || !outputs.length || outputs.length > 1000} onClick={() => void action("verify")}>{t("storage.verify", { count: outputs.length })}</Button></div>{spaces && <ul>{Object.entries(spaces).map(([key, value]) => <li key={key}><strong>{t(key === "ownership_error" ? "storage.ownership" : key === "source" ? "storage.source" : key === "output" ? "storage.output" : key === "config" ? "storage.config" : "storage.staging")}</strong>: {typeof value === "string" ? errorMessage(value) : value.error ? errorMessage(value.error) : "free_bytes" in value ? t("storage.space", { free: number(value.free_bytes), total: number(value.total_bytes) }) : t("storage.online")}</li>)}</ul>}<ToolStatus kind="storage" report={verification.data} retry={verification.retry} notify={notify} />
    <details className="migration-guide"><summary>{t("migration.title")}</summary><div className="comparison-body"><InfoHint>{t("migration.hint")}</InfoHint><label className="field"><span className="field-label">{t("migration.source")}</span><input value={source} disabled={busy} onChange={event => { setSource(event.target.value); setPlan(undefined); }} /></label><label className="field"><span className="field-label">{t("migration.output")}</span><input value={output} disabled={busy} onChange={event => { setOutput(event.target.value); setPlan(undefined); }} /></label><Button size="sm" variant="outline" disabled={busy || !source || !output} onClick={() => void action("migration")}>{t("migration.preview")}</Button>{plan && <div ref={migrationPreview} className="maintenance-report preview-reveal"><p>{t("migration.checked")}</p><code>{plan.source}</code><p><code>{plan.output.path}</code></p><p>{t("migration.size", { source: number(plan.source_bytes), output: number(plan.output_bytes), free: number(plan.output.free_bytes) })}</p><p>{plan.output.empty ? t("migration.empty") : t("migration.owned")}</p><ol>{(["pause", "backup", "copy", "mount", "verify", "resume"] as const).map(step => <li key={step}>{t(`migration.${step}`)}</li>)}</ol><p>{t("migration.restore")}</p><a href="/settings?section=library">{t("migration.settings")}</a></div>}</div></details>
  </section>;
}
