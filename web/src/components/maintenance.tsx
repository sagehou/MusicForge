import { useEffect, useRef, useState, type ReactNode } from "react";
import { AudioLines, ClipboardList, FileMusic, FolderSync, HardDrive, History, Image, Loader2, Play, RefreshCw, ScanSearch, Square, X } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Encoding, LoudnessReport, Source } from "../types";
import { Button } from "./ui/button";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { OperationLog, QualityReview, StorageHealth } from "./maintenance-review";
import { InfoHint } from "./info-hint";
import { LyricsMaintenance } from "./lyrics";
import { ContentMaintenance } from "./content-maintenance";
import type { LyricsInventory } from "../library-data";

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

type WorkbenchView = "issues" | "lyrics" | "covers" | "loudness" | "suggestions" | "history" | "storage";
const workbenchViews: WorkbenchView[] = ["lyrics", "covers", "issues", "loudness", "suggestions", "history", "storage"];
const workbenchIcons = { lyrics: FileMusic, covers: Image, issues: ScanSearch, loudness: AudioLines, suggestions: FolderSync, history: History, storage: HardDrive };
function initialWorkbenchView(): WorkbenchView { return location.hash === "#maintenance-workbench" ? "issues" : "lyrics"; }

export function MaintenanceWorkbench({ active, scopeQuery, scope, inventory, selected, select, notify, busy, onBusyChange, openEditor }: {
  active: boolean; scopeQuery: string; scope?: "album" | "artist"; inventory: LyricsInventory;
  selected: Source[]; select: (ids: number[]) => void; notify: Notice; busy: boolean;
  onBusyChange: (busy: boolean) => void; openEditor: () => void;
}) {
  const { t } = useI18n();
  const workbench = useRef<HTMLElement>(null);
  const [visited, setVisited] = useState(active);
  const [view, setView] = useState<WorkbenchView>(initialWorkbenchView);
  const [views, setViews] = useState<Set<WorkbenchView>>(() => new Set([initialWorkbenchView()]));
  useEffect(() => { if (active) setVisited(true); }, [active]);
  useEffect(() => { workbench.current?.querySelectorAll("audio").forEach(audio => audio.pause()); }, [active, view]);
  function change(next: WorkbenchView) {
    if (busy) return;
    setView(next); setViews(old => new Set([...old, next]));
  }
  useEffect(() => {
    const reveal = () => { if (location.hash === "#lyrics-maintenance") change("lyrics"); };
    window.addEventListener("route-changed", reveal); window.addEventListener("popstate", reveal);
    return () => { window.removeEventListener("route-changed", reveal); window.removeEventListener("popstate", reveal); };
  }, [busy]);
  const contentScope = new URLSearchParams(scopeQuery);
  contentScope.delete("locale");
  return <section ref={workbench} className="maintenance-workbench" id="maintenance-workbench" aria-labelledby="workbench-heading">
    <div className="section-heading"><div className="actions"><h2 id="workbench-heading">{t("maintenance.title")}</h2><InfoHint label={`${t("maintenance.title")} · ${t("app.moreInfo")}`}>{t("maintenance.hint")}</InfoHint></div><Button asChild size="sm" variant="ghost"><a href="/jobs#maintenance-progress">{t("progress.open")}</a></Button></div>
    <div className="workbench-navigation" role="group" aria-label={t("workbench.navigation")}>{workbenchViews.map(item => { const Icon = workbenchIcons[item]; return <Button key={item} id={`workbench-${item}`} variant="ghost" size="sm" aria-pressed={view === item} aria-controls={`workbench-panel-${item}`} disabled={busy} onClick={() => change(item)}><Icon size={16} aria-hidden="true" />{t(`workbench.${item}`)}</Button>; })}</div>
    {visited && <WorkbenchContent key={contentScope.toString()} scopeQuery={scopeQuery} scope={scope} inventory={inventory} selected={selected} select={select} notify={notify} view={view} views={views} busy={busy} onBusyChange={onBusyChange} openEditor={openEditor} />}
  </section>;
}

function WorkbenchContent({ scopeQuery, scope, inventory, selected, select, notify, view, views, busy, onBusyChange, openEditor }: {
  scopeQuery: string; scope?: "album" | "artist"; inventory: LyricsInventory; selected: Source[]; select: (ids: number[]) => void;
  notify: Notice; view: WorkbenchView; views: Set<WorkbenchView>; busy: boolean;
  onBusyChange: (busy: boolean) => void; openEditor: () => void;
}) {
  const { t } = useI18n();
  const resource = useResource<Source[]>(`/library/scope?${scopeQuery}`, true, true);
  if (!resource.data) return <Loading error={resource.error} retry={resource.retry} />;
  const sources = resource.data;
  function panel(name: WorkbenchView, children: ReactNode) {
    return <div key={name} id={`workbench-panel-${name}`} className="workbench-panel" hidden={view !== name}>{views.has(name) && children}</div>;
  }
  return <div className="workbench-content">
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {panel("lyrics", <><LyricsMaintenance inventory={inventory} scopeQuery={scopeQuery} scope={scope} notify={notify} /><ContentMaintenance kind="lyrics" sources={sources} notify={notify} refresh={resource.retry} busy={busy} onBusyChange={onBusyChange} openEditor={openEditor} /></>)}
    {panel("covers", <ContentMaintenance kind="covers" sources={sources} notify={notify} refresh={resource.retry} busy={busy} onBusyChange={onBusyChange} openEditor={openEditor} />)}
    {panel("issues", <QualityReview sources={sources} select={select} notify={notify} onBusyChange={onBusyChange} />)}
    {panel("loudness", <LoudnessMaintenance selected={selected} sources={sources} notify={notify} />)}
    {panel("suggestions", <section className="conversion-maintenance" aria-label={t("workbench.suggestions")}><div className="section-heading"><h3>{t("workbench.suggestions")}</h3><InfoHint>{t("workbench.conversionHint")}</InfoHint></div><dl className="conversion-summary">{["missing", "changed", "needs_rebuild", "failed"].map(state => <div key={state}><dt>{t(state === "failed" ? "quality.failed" : state === "missing" ? "workbench.unbuilt" : state === "changed" ? "workbench.changed" : "workbench.outdated")}</dt><dd>{sources.filter(source => source.status === state).length}</dd></div>)}</dl><div className="maintenance-actions"><BuildPreview selection={{ ids: sources.map(source => source.id) }} currentScope notify={notify} /><a href="/settings?section=encoding">{t("maintenance.scheduleLink")}</a></div></section>)}
    {panel("history", <OperationLog notify={notify} onBusyChange={onBusyChange} />)}
    {panel("storage", <StorageHealth sources={sources} selected={selected} notify={notify} />)}
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
