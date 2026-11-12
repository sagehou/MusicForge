import { useEffect, useState } from "react";
import { ClipboardList, Loader2, Play, RefreshCw, Square, X } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Encoding, LoudnessReport, Source } from "../types";
import { Button } from "./ui/button";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { MaintenanceReview, QualityReview } from "./maintenance-review";
import { InfoHint } from "./info-hint";

export function storageSize(bytes: number, locale: string) {
  const unit = Math.min(4, Math.floor(Math.log2(Math.max(bytes, 1)) / 10));
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(bytes / 1024 ** unit)} ${["B", "KiB", "MiB", "GiB", "TiB"][unit]}`;
}

type Selection = { all: true } | { ids: number[] };
type Plan = { encoding: Encoding; tracks: number; skipped: number; unverified: number; input_bytes: number; estimated_output_bytes: number; current_output_bytes: number; largest_source_bytes: number; staging_limit_bytes: number; unknown_duration: number; reasons: Record<string, number> };

export function BuildPreview({ selection, notify, currentScope = false }: { selection: Selection; notify: Notice; currentScope?: boolean }) {
  const { t, locale, number, status } = useI18n();
  const [plan, setPlan] = useState<Plan>();
  const [scope, setScope] = useState<Selection>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  async function preview() {
    setBusy(true); setError(undefined);
    try { const result = await api<Plan>("/library/plan", "POST", selection); setPlan(result); setScope(selection); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  async function start() {
    setBusy(true); setError(undefined);
    try { await api("/library/rebuild", "POST", scope); setPlan(undefined); notify("notice.rebuild"); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  return <div className="build-preview">
    <Button size="sm" variant="outline" disabled={busy || "ids" in selection && !selection.ids.length} onClick={() => void preview()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <ClipboardList size={14} />}{t(currentScope ? "buildPlan.currentScope" : "all" in selection ? "buildPlan.all" : "buildPlan.selected")}</Button>
    {error && <p role="alert">{error.message}</p>}
    {plan && <section className="maintenance-report" aria-label={t("buildPlan.title")}>
      <div className="section-heading"><h3>{t("buildPlan.title")}</h3><Button size="icon" variant="ghost" disabled={busy} aria-label={t("app.dismiss")} onClick={() => setPlan(undefined)}><X size={16} /></Button></div>
      <p>{t("buildPlan.scope", { count: number(plan.tracks), skipped: number(plan.skipped) })}</p>
      <dl className="comparison-metrics">{([ ["input", plan.input_bytes], ["output", plan.estimated_output_bytes], ["current", plan.current_output_bytes], ["staging", plan.staging_limit_bytes] ] as const).map(([key, value]) => <div key={key}><dt>{t(`buildPlan.${key}`)}</dt><dd>{storageSize(value, locale)}</dd></div>)}</dl>
      <ul>{Object.entries(plan.reasons).map(([reason, count]) => <li key={reason}>{status(reason)} · {number(count)}</li>)}</ul>
      <InfoHint>{t("buildPlan.estimate")}</InfoHint>
      {plan.unverified > 0 && <p>{t("buildPlan.unverified", { count: number(plan.unverified) })}</p>}
      {plan.unknown_duration > 0 && <p>{t("buildPlan.unknown", { count: number(plan.unknown_duration) })}</p>}
      {plan.largest_source_bytes > plan.staging_limit_bytes && <p className="repair-warning">{t("buildPlan.stagingWarning")}</p>}
      <Button size="sm" disabled={busy || !plan.tracks} onClick={() => void start()}><Play size={14} />{t("buildPlan.start")}</Button>
    </section>}
  </div>;
}

export function MaintenanceWorkbench({ scopeQuery, attention, selected, select, notify, comparisonAvailable }: { scopeQuery: string; attention: number; selected: Source[]; select: (ids: number[]) => void; notify: Notice; comparisonAvailable: boolean }) {
  const { t, number } = useI18n();
  const [open, setOpen] = useState(() => location.hash === "#maintenance-workbench");
  return <details className="maintenance-workbench" id="maintenance-workbench" open={open} onToggle={event => setOpen(event.currentTarget.open)} onKeyDown={event => { if (event.key === "Escape" && event.currentTarget.open && !event.defaultPrevented && (event.target as Element).closest("details[open]") === event.currentTarget) { event.preventDefault(); setOpen(false); event.currentTarget.querySelector("summary")?.focus(); } }}>
    <summary><ClipboardList size={18} /><span>{t("maintenance.title")}</span><small className="quality-count">{number(attention)}</small></summary>
    {open && <WorkbenchContent scopeQuery={scopeQuery} selected={selected} select={select} notify={notify} comparisonAvailable={comparisonAvailable} />}
  </details>;
}

function WorkbenchContent({ scopeQuery, selected, select, notify, comparisonAvailable }: { scopeQuery: string; selected: Source[]; select: (ids: number[]) => void; notify: Notice; comparisonAvailable: boolean }) {
  const { t } = useI18n();
  const resource = useResource<Source[]>(`/library/scope?${scopeQuery}`, true);
  function reveal(selector: string) {
    const element = document.querySelector<HTMLDetailsElement>(selector);
    if (element) { element.open = true; element.scrollIntoView({ block: "nearest", behavior: "smooth" }); element.querySelector("summary")?.focus(); }
  }
  if (!resource.data) return <Loading error={resource.error} retry={resource.retry} />;
  const sources = resource.data;
  return <div className="comparison-body">
    <ResourceWarning error={resource.error} retry={resource.retry} />
    <InfoHint>{t("maintenance.hint")}</InfoHint>
    <p><a href="/jobs#maintenance-progress">{t("progress.open")}</a></p>
    <div className="maintenance-actions"><BuildPreview selection={{ ids: sources.map(source => source.id) }} currentScope notify={notify} />{selected.length > 0 && <BuildPreview selection={{ ids: selected.map(source => source.id) }} notify={notify} />}<Button size="sm" variant="ghost" onClick={() => reveal(".lyrics-panel > details")}>{t("lyrics.title")}</Button>{comparisonAvailable && <Button size="sm" variant="ghost" onClick={() => reveal("#library-comparison")}>{t("compare.heading")}</Button>}<a href="/settings?section=encoding">{t("maintenance.scheduleLink")}</a></div>
    <QualityReview sources={sources} selected={selected} select={select} notify={notify} />
    <LoudnessMaintenance selected={selected} sources={sources} notify={notify} />
    <MaintenanceReview sources={sources} selected={selected} select={select} notify={notify} />
  </div>;
}

function LoudnessMaintenance({ selected, sources, notify }: { selected: Source[]; sources: Source[]; notify: Notice }) {
  const { t, number, errorMessage } = useI18n();
  const [poll, setPoll] = useState(false);
  const report = useResource<LoudnessReport>("/maintenance/loudness", poll);
  const [busy, setBusy] = useState(false);
  const running = report.data?.state === "running";
  useEffect(() => { setPoll(running); }, [running]);
  async function action(stop: boolean, chosen: Source[]) {
    setBusy(true);
    try { await api("/maintenance/loudness", stop ? "DELETE" : "POST", stop ? {} : { ids: chosen.map(source => source.id) }); report.retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  const ready = sources.filter(source => source.output_present && source.output_gain);
  return <section className="loudness-maintenance" aria-label={t("gain.title")}><h3>{t("gain.title")}</h3><InfoHint>{t("gain.hint")}</InfoHint><p>{t("gain.coverage", { done: number(ready.length), total: number(sources.filter(source => source.output_present).length) })}</p><ResourceWarning error={report.error} retry={report.retry} /><div className="actions"><Button size="sm" variant="outline" disabled={busy || running || !sources.some(source => source.output_present)} onClick={() => void action(false, sources)}><RefreshCw size={14} />{t("gain.fill")}</Button>{selected.length > 0 && <Button size="sm" variant="outline" disabled={busy || running || !selected.some(source => source.output_present)} onClick={() => void action(false, selected)}>{t("gain.selected")}</Button>}{running && <Button size="sm" variant="outline" disabled={busy} onClick={() => void action(true, [])}><Square size={14} />{t("gain.stop")}</Button>}</div>{report.data && report.data.state !== "idle" && <div role="status"><p>{t(`gain.state.${report.data.state}`)} · {t("gain.progress", { done: number(report.data.processed), total: number(report.data.total), updated: number(report.data.updated), skipped: number(report.data.skipped), failed: number(report.data.failed) })}</p>{report.data.current && <code>{report.data.current}</code>}{report.data.error && <p role="alert">{errorMessage(report.data.error)}</p>}</div>}</section>;
}
