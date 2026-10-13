import { useEffect, useState, type FormEvent } from "react";
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import { Button } from "./ui/button";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";

type DifferenceKind = "not_indexed" | "expired" | "unsupported" | "not_in_lidarr" | "unmapped" | "failed" | "possibly_damaged" | "no_output";
type Difference = { path: string; kind: DifferenceKind; source_id?: number; error?: string; scan_dir?: string };
type Report = {
  state: "idle" | "running" | "failed" | "complete"; error?: string; started: number; finished: number; processed: number; total: number;
  source: string; prefix: string; lidarr_count: number; source_count: number; matched: number; only_lidarr: number; only_musicforge: number;
  unmapped: number; outside_scope: number; differences: Difference[];
};

export function LibraryComparison({ sources, notify }: { sources: Source[]; notify: Notice }) {
  const { t, number } = useI18n();
  const [open, setOpen] = useState(false);
  const present = sources.filter(source => source.present);
  return <details className="library-comparison" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{t("compare.title")}</summary>
    <p className="comparison-counts">{t("compare.localCounts", {
      sources: number(present.length), expired: number(sources.length - present.length),
      outputs: number(sources.filter(source => source.output_present).length), failed: number(present.filter(source => source.error).length),
    })}</p>
    <p>{t("compare.countHint")}</p>
    {open && <LidarrComparison notify={notify} />}
  </details>;
}

function LidarrComparison({ notify }: { notify: Notice }) {
  const { t, number, date } = useI18n();
  const connection = useResource<{ url: string; key_configured: boolean }>("/lidarr/connection");
  const report = useResource<Report>("/lidarr/check", true);
  const [url, setURL] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState("all");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => { if (connection.data) setURL(connection.data.url); }, [connection.data]);
  useEffect(() => { setSelected(new Set()); setPage(0); }, [report.data?.started]);
  const running = report.data?.state === "running";
  const dirty = url.replace(/\/+$/, "") !== connection.data?.url || key !== "";
  async function save(event: FormEvent) {
    event.preventDefault(); setBusy(true);
    try {
      await api("/lidarr/connection", "PUT", { url, api_key: key });
      setKey(""); connection.retry(); report.retry(); notify("compare.saved");
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  async function start() {
    setBusy(true);
    try { await api("/lidarr/check", "POST", {}); report.retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  async function scan() {
    const dirs = [...new Set((report.data?.differences || []).filter(row => selected.has(row.path) && row.scan_dir).map(row => row.scan_dir!))];
    if (!dirs.length) return;
    setBusy(true);
    try {
      await api("/library/scan", "POST", { dirs });
      setSelected(new Set()); notify("compare.scanQueued");
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  function toggle(paths: string[], checked: boolean) {
    setSelected(old => { const next = new Set(old); paths.forEach(path => checked ? next.add(path) : next.delete(path)); return next; });
  }
  if (!connection.data) return <Loading error={connection.error} retry={connection.retry} />;
  const data = report.data;
  const rows = (data?.differences || []).filter(row => (filter === "all" || row.kind === filter) && row.path.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  const offset = Math.min(page * 50, Math.max(0, Math.floor((rows.length - 1) / 50) * 50));
  const visible = rows.slice(offset, offset + 50);
  const selectable = visible.filter(row => row.scan_dir).map(row => row.path);
  return <div className="comparison-body">
    <ResourceWarning error={connection.error} retry={connection.retry} />
    <form onSubmit={event => void save(event)}>
      <div className="form-grid">
        <label className="field"><span>{t("compare.url")}</span><input type="url" value={url} onChange={event => setURL(event.target.value)} placeholder="http://lidarr:8686" disabled={busy || running} /></label>
        <label className="field"><span>{t("compare.key")}</span><input type="password" value={key} onChange={event => setKey(event.target.value)} autoComplete="new-password" disabled={busy || running} placeholder={connection.data.key_configured ? t("settings.keepSecret") : ""} /></label>
      </div>
      <p>{t("compare.connectionHint")}</p>
      <Button type="submit" variant="outline" size="sm" disabled={busy || running}>{t("compare.save")}</Button>
    </form>
    <p>{t("compare.scopeHint")}</p>
    <div className="actions"><Button disabled={busy || running || dirty || !connection.data.key_configured} onClick={() => void start()}><RefreshCw size={15} className={running ? "animate-spin" : ""} />{t("compare.start")}</Button>
      {running && <span role="status">{t("compare.progress", { done: number(data?.processed || 0), total: number(data?.total || 0) })}</span>}
    </div>
    <ResourceWarning error={report.error} retry={report.retry} />
    {data?.state === "failed" && <div className="error-box" role="alert"><strong>{t("compare.failed")}</strong><p>{data.error}</p></div>}
    {data?.state === "complete" && <>
      <p>{t("compare.snapshot", { time: date(data.finished) })}</p>
      <p className="comparison-paths"><code>{data.prefix || data.source}</code><span aria-hidden="true"> → </span><code>{data.source}</code></p>
      <dl className="comparison-metrics">{([
        ["lidarr", data.lidarr_count], ["sources", data.source_count], ["matched", data.matched],
        ["onlyLidarr", data.only_lidarr], ["onlyMusicForge", data.only_musicforge], ["unmapped", data.unmapped], ["outsideScope", data.outside_scope],
      ] as const).map(([label, value]) => <div key={label}><dt>{t(`compare.metric.${label}`)}</dt><dd>{number(value)}</dd></div>)}</dl>
      {data.outside_scope > 0 && <p className="repair-warning">{t("compare.outsideHint")}</p>}
      <p>{t("compare.snapshotHint")}</p>
      <div className="comparison-filters">
        <label className="field"><span>{t("compare.search")}</span><input value={query} onChange={event => { setQuery(event.target.value); setPage(0); }} /></label>
        <label className="field"><span>{t("compare.filter")}</span><select value={filter} onChange={event => { setFilter(event.target.value); setPage(0); }}><option value="all">{t("library.allStatuses")}</option>{(["not_indexed", "expired", "unsupported", "not_in_lidarr", "unmapped", "failed", "possibly_damaged", "no_output"] as const).map(kind => <option key={kind} value={kind}>{t(`compare.kind.${kind}`)}</option>)}</select></label>
      </div>
      <div className="actions"><label className="check-field"><input type="checkbox" checked={selectable.length > 0 && selectable.every(path => selected.has(path))} disabled={!selectable.length || busy} onChange={event => toggle(selectable, event.target.checked)} />{t("compare.selectPage")}</label>
        <Button variant="outline" disabled={busy || !selected.size} onClick={() => void scan()}>{t("compare.scan", { count: number(selected.size) })}</Button>
      </div>
      {rows.length ? <ul className="comparison-results">{visible.map(row => <li key={row.path}>
        <div className="comparison-result-heading">{row.scan_dir && <input type="checkbox" aria-label={t("compare.select", { path: row.path })} checked={selected.has(row.path)} disabled={busy} onChange={event => toggle([row.path], event.target.checked)} />}<strong>{t(`compare.kind.${row.kind}`)}</strong></div>
        <code>{row.path}</code><p>{t(`compare.help.${row.kind}`)}</p>
        {row.error && <details><summary>{t("library.errorDetails")}</summary><pre>{row.error}</pre></details>}
      </li>)}</ul> : <p>{t("compare.noDifferences")}</p>}
      {rows.length > 50 && <div className="pagination"><span>{t("library.pagination", { start: offset + 1, end: Math.min(offset + 50, rows.length), total: rows.length })}</span><Button variant="ghost" size="icon" aria-label={t("library.previous")} disabled={offset === 0} onClick={() => setPage(offset / 50 - 1)}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("library.next")} disabled={offset + 50 >= rows.length} onClick={() => setPage(offset / 50 + 1)}><ChevronRight size={16} /></Button></div>}
    </>}
  </div>;
}
