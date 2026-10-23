import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, ChevronLeft, ChevronRight, FileMusic, Info, Search, Settings2, Square } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { LyricsProvider, Source } from "../types";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";
import { InlineDetails } from "./inline-details";

type LyricsResponse = {
  options: { auto_search: boolean; min_score: number };
  report: { state: "idle" | "running" | "complete" | "stopped"; total: number; processed: number; found: number; not_found: number; failed: number; needs_review?: number; current?: string; error?: string; stopping: boolean; mode?: "fill" | "replace" | "inspect"; replaced?: number; unchanged?: number; skipped?: number };
};

export const lyricsMatchStates = ["ready", "unchecked", "not_found", "instrumental", "needs_review", "failed"] as const;
export type LyricsMatchState = typeof lyricsMatchStates[number];

export function lyricsMatchState(source: Source): LyricsMatchState | undefined {
  if (!source.output || !source.output_present) return undefined;
  return source.lyrics?.state || "unchecked";
}

export function LyricsSearchButton({ ids, all = false, refresh = false, replaceTraditional = false, inspectOnly = false, disabled = false, compact = false, iconOnly = false, label, notify, onStarted }: {
  ids?: number[]; all?: boolean; refresh?: boolean; replaceTraditional?: boolean; inspectOnly?: boolean; disabled?: boolean; compact?: boolean; iconOnly?: boolean; label: MessageKey; notify: Notice; onStarted?: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  async function search() {
    setBusy(true);
    try {
      const result = await api<{ selected: number; skipped?: number }>("/lyrics/search", "POST", { ...(all ? { all: true } : { ids }), refresh, ...(replaceTraditional ? { replace_traditional: true } : {}), ...(inspectOnly ? { inspect_only: true } : {}) });
      notify(result.selected === 0 ? "lyrics.nothingPending" : "lyrics.started"); onStarted?.();
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <Button type="button" variant="outline" size="sm" className={compact ? "lyrics-action" : undefined} aria-label={t(label)} title={t(label)} disabled={disabled || busy || !all && !ids?.length} onClick={() => void search()}><Search size={13} />{!iconOnly && t(compact ? "lyrics.searchShort" : label)}</Button>;
}

export function LyricsMaintenance({ sources, scope, notify }: { sources: Source[]; scope?: "album" | "artist"; notify: Notice }) {
  const { t } = useI18n();
  const coverage = scope === "album" ? "lyrics.albumCoverage" : scope === "artist" ? "lyrics.artistCoverage" : "lyrics.coverage";
  const inventory = useMemo(() => {
    const counts: Record<LyricsMatchState, number> = { ready: 0, unchecked: 0, not_found: 0, instrumental: 0, needs_review: 0, failed: 0 };
    let total = 0;
    for (const source of sources) {
      const state = lyricsMatchState(source);
      if (!state) continue;
      counts[state]++;
      total++;
    }
    return { counts, total, checked: total - counts.unchecked };
  }, [sources]);
  return <section className="library-comparison lyrics-panel">
    <details className="lyrics-settings"><summary><FileMusic size={18} /><span>{t("lyrics.title")}</span><small className="maintenance-count" aria-label={t(coverage, { done: inventory.checked, total: inventory.total })}>{inventory.checked} / {inventory.total}</small><ChevronRight size={16} /></summary>
      <section className="lyrics-library-summary" aria-label={t(scope === "album" ? "lyrics.albumStatus" : scope === "artist" ? "lyrics.artistStatus" : "lyrics.libraryStatus")}>
        <strong>{t(coverage, { done: inventory.checked, total: inventory.total })}</strong>
        <p>{t("lyrics.inventory", { found: inventory.counts.ready, missing: inventory.counts.not_found, instrumental: inventory.counts.instrumental, review: inventory.counts.needs_review, failed: inventory.counts.failed, unchecked: inventory.counts.unchecked })}</p>
        <small>{t("lyrics.inventoryHint")}</small>
      </section>
    </details>
    <LyricsControls notify={notify} ids={scope ? sources.filter(source => lyricsMatchState(source)).map(source => source.id) : undefined} label={scope === "album" ? "lyrics.searchAlbum" : scope === "artist" ? "lyrics.searchArtist" : "lyrics.searchAll"} />
  </section>;
}

function LyricsControls({ notify, settings = false, ids, label = "lyrics.searchAll" }: { notify: Notice; settings?: boolean; ids?: number[]; label?: MessageKey }) {
  const { t, errorMessage } = useI18n();
  const resource = useResource<LyricsResponse>("/lyrics", 2000);
  const [busy, setBusy] = useState(false);
  if (!resource.data) return <Loading error={resource.error} retry={resource.retry} />;
  const { report } = resource.data;
  const running = report.state === "running";
  async function stop() {
    setBusy(true);
    try { await api("/lyrics/search", "DELETE"); resource.retry(); window.dispatchEvent(new Event("lyrics-options-changed")); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  return <div className="lyrics-body" data-running={running}>
    <div className="actions lyrics-quick-actions">
      <LyricsSearchButton all={ids === undefined} ids={ids} compact={!settings && ids === undefined} iconOnly={!settings && ids === undefined} label={label} notify={notify} disabled={busy || running} onStarted={resource.retry} />
      {settings && <LyricsSearchButton all inspectOnly label="lyrics.inspect" notify={notify} disabled={busy || running} onStarted={resource.retry} />}
      {running && ids === undefined && <Button className="lyrics-action" variant="outline" size="sm" aria-label={t(report.stopping ? "lyrics.stopping" : "lyrics.stop")} disabled={busy || report.stopping} onClick={() => void stop()}><Square size={13} /></Button>}
      {!settings && <a className="lyrics-settings-link" href="/settings?section=lyrics" aria-label={t("lyrics.settings")} title={t("lyrics.settings")}><Settings2 size={14} /></a>}
    </div>
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {running && ids !== undefined && <a className="lyrics-global-progress" href="/settings?section=lyrics">{t("lyrics.globalProgress")}</a>}
    {ids === undefined && report.state !== "idle" && <div role="status" aria-label={t("lyrics.batchProgress")} aria-live="polite">
      <strong>{t(report.total === 0 && report.skipped ? "lyrics.nothingPending" : report.mode === "inspect" ? `lyrics.inspectReport.${report.state}` : report.mode === "replace" ? `lyrics.replaceReport.${report.state}` : `lyrics.report.${report.state}`)}</strong>
      {report.total > 0 && <p>{report.mode === "replace" ? t("lyrics.replaceProgress", { done: report.processed, total: report.total, replaced: report.replaced ?? 0, unchanged: report.unchanged ?? 0, failed: report.failed }) : report.mode === "inspect" ? t("lyrics.inspectProgress", { done: report.processed, total: report.total, found: report.found, failed: report.failed }) : t("lyrics.progress", { done: report.processed, total: report.total, found: report.found, missing: report.not_found, failed: report.failed, review: report.needs_review ?? 0 })}</p>}
      {(report.skipped ?? 0) > 0 && <small>{t("lyrics.skipped", { count: report.skipped ?? 0 })}</small>}
      {report.current && <code>{report.current}</code>}
      {report.error && <p className="repair-warning">{errorMessage(report.error)}</p>}
    </div>}
  </div>;
}

export function LyricsSettings({ notify, notification }: { notify: Notice; notification: ReactNode }) {
  const { t } = useI18n();
  const resource = useResource<LyricsResponse>("/lyrics");
  const [draft, setDraft] = useState<LyricsResponse["options"]>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const refresh = () => { setDraft(old => old && { ...old, auto_search: false }); resource.retry(); };
    window.addEventListener("lyrics-options-changed", refresh);
    return () => window.removeEventListener("lyrics-options-changed", refresh);
  }, [resource.retry]);
  if (!resource.data) return <Loading error={resource.error} retry={resource.retry} />;
  const saved = { ...resource.data.options, min_score: resource.data.options.min_score ?? 96 };
  const options = draft ?? saved;
  const dirty = options.auto_search !== saved.auto_search || options.min_score !== saved.min_score;
  return <div className="lyrics-settings-page">
    <ResourceWarning error={resource.error} retry={resource.retry} />
    <form id="lyrics-options-form" className="settings-form" onSubmit={event => {
      event.preventDefault(); setBusy(true);
      void api<LyricsResponse["options"]>("/lyrics/options", "PUT", options).then(result => { resource.update(data => ({ ...data, options: result })); setDraft(undefined); notify("notice.saved"); }).catch(err => notify(err as Error, true)).finally(() => setBusy(false));
    }}>
      <section className="panel settings-panel"><div className="settings-title"><FileMusic /><div><h2>{t("lyrics.settings")}</h2><p>{t("lyrics.hint")}</p></div></div>
        <label className="check-field switch-field"><input type="checkbox" role="switch" checked={options.auto_search} disabled={busy} onChange={event => setDraft({ ...options, auto_search: event.target.checked })} /><span>{t("lyrics.automatic")}</span></label>
        <div className="lyrics-threshold"><label className="field field-label"><span>{t("lyrics.threshold")}</span><input type="number" min={0} max={100} step={1} required value={options.min_score} disabled={busy} onChange={event => setDraft({ ...options, min_score: event.target.valueAsNumber })} /></label><div className="actions" role="group" aria-label={t("lyrics.presets")}>{([100, 96, 90] as const).map(score => <Button key={score} type="button" variant="outline" size="sm" aria-pressed={options.min_score === score} disabled={busy} onClick={() => setDraft({ ...options, min_score: score })}>{t(`lyrics.preset.${score}`)}</Button>)}</div></div>
        <small>{t("lyrics.thresholdHint")}</small><small>{t("lyrics.privacy")}</small><small>{t("lyrics.stopHint")}</small>
      </section>

      <div className="settings-save" data-dirty={dirty}>{notification}<span>{t(dirty ? "settings.unsaved" : "settings.savedIn")}</span><Button type="button" variant="ghost" disabled={busy || !dirty} onClick={() => setDraft(undefined)}>{t("settings.discard")}</Button><Button type="submit" form="lyrics-options-form" disabled={busy || !dirty}><Check size={16} />{t("settings.save")}</Button></div>
    </form>
    <section className="panel settings-panel lyrics-batch-settings"><div className="settings-title"><Search /><div><h2>{t("lyrics.batchTitle")}</h2><p>{t("lyrics.batchHint")}</p></div></div><LyricsControls notify={notify} settings /><small>{t("lyrics.inspectHint")}</small><div className="actions"><Button asChild variant="outline" size="sm"><a href="/library?view=tracks&lyrics_script=traditional">{t("lyrics.filterTraditional")}</a></Button></div><small>{t("lyrics.replaceHint")}</small></section>

  </div>;
}

export function LyricsTrack({ source, notify, onChoose, expanded = false, disabled = false }: { source: Source; notify: Notice; onChoose: () => void; expanded?: boolean; disabled?: boolean }) {
  const { t, errorMessage } = useI18n();
  if (!source.output_present || !source.output) return null;
  return <div className="lyrics-track" data-state={source.lyrics?.state || "unchecked"}>
    <span className="lyrics-state">{t(`lyrics.state.${source.lyrics?.state || "unchecked"}`)}{source.lyrics_script && <small className="lyrics-script"> · {t(`lyrics.script.${source.lyrics_script}`)}</small>}</span>
    <div className="lyrics-actions">
      <LyricsSearchButton ids={[source.id]} refresh compact iconOnly disabled={disabled} label="lyrics.searchAgain" notify={notify} />
      <Button type="button" variant="outline" size="sm" className="lyrics-action" id={`track-lyrics-trigger-${source.id}`} aria-label={t("lyrics.choose")} title={t("lyrics.choose")} aria-expanded={expanded} aria-controls={expanded ? `track-lyrics-${source.id}` : undefined} disabled={disabled} onClick={onChoose}><FileMusic size={15} /></Button>
    {source.lyrics && <details className="lyrics-details"><summary aria-label={t("lyrics.details")} title={t("lyrics.details")}><Info size={14} /></summary><div className="lyrics-status-popover">
    {source.lyrics?.path && <small>{source.lyrics.path}</small>}
    {source.lyrics?.provider && <small>{t(`lyrics.provider.${source.lyrics.provider}`)}</small>}
    {source.lyrics?.error && <p>{errorMessage(source.lyrics.error)}</p>}
    {source.lyrics?.attempts && <details><summary>{t("lyrics.attempts")}</summary>{source.lyrics.attempts.map(attempt => <p key={attempt.provider}>{t(`lyrics.provider.${attempt.provider}`)} · {t(`lyrics.state.${attempt.state}`)}{attempt.error && <> · {errorMessage(attempt.error)}</>}</p>)}</details>}
    </div></details>}
    </div>
  </div>;
}

const onlineProviders = ["lrclib", "qq", "kugou", "netease"] as const;
type OnlineProvider = Exclude<LyricsProvider, "source">;
type Candidate = { index: number; provider: OnlineProvider; title: string; artist: string; album: string; duration: number; high_confidence: boolean; identity_match?: boolean; match_score: number; duration_delta?: number; script: string; translation_script?: string };
type CandidateGroup = { provider: OnlineProvider; token?: string; candidates: Candidate[]; cached?: boolean; searched?: number; loading: boolean; error?: string };
type CandidateText = { original: string; translation?: string; romanization?: string; script?: string; translation_script?: string };
type CandidateChoice = { token: string; candidate: Candidate };

function candidateDuration(value: number) {
  const seconds = Math.round(value);
  return seconds > 0 ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}` : "—";
}

export function LyricsPicker({ source, notify, close, saved, previous, next, onBusyChange }: { source: Source; notify: Notice; close: () => void; saved: () => void; previous?: () => void; next?: () => void; onBusyChange: (busy: boolean) => void }) {
  const { t, errorMessage, locale } = useI18n();
  const options = useResource<LyricsResponse>("/lyrics");
  const searchController = useRef<AbortController | null>(null);
  const previewController = useRef<AbortController | null>(null);
  const autoSelect = useRef(true);
  const [query, setQuery] = useState(`${source.title} ${source.artist}`.trim());
  const [groups, setGroups] = useState<CandidateGroup[]>([]);
  const [selected, setSelected] = useState<CandidateChoice>();
  const [preview, setPreview] = useState<CandidateText>();
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [translation, setTranslation] = useState(false);
  const [romanization, setRomanization] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedSelection, setSavedSelection] = useState(false);
  const [providerFilter, setProviderFilter] = useState("all");
  const [restoring, setRestoring] = useState(true);
  const [restoreError, setRestoreError] = useState("");
  const base = `/library/${source.id}/lyrics`;
  useEffect(() => {
    const controller = new AbortController();
    void api<{ query: string; groups: CandidateGroup[] }>(`${base}/candidates`, "GET", undefined, controller.signal).then(result => {
      if (!controller.signal.aborted) { setQuery(result.query); setGroups(result.groups.map(group => ({ ...group, loading: false }))); }
    }).catch((err: Error) => { if (!controller.signal.aborted) setRestoreError(err.message); })
      .finally(() => { if (!controller.signal.aborted) setRestoring(false); });
    return () => { controller.abort(); searchController.current?.abort(); previewController.current?.abort(); };
  }, []);
  async function search(refresh = false) {
    searchController.current?.abort(); previewController.current?.abort();
    autoSelect.current = true;
    const controller = new AbortController(); searchController.current = controller;
    setSelected(undefined); setPreview(undefined); setPreviewError(""); setPreviewing(false);
    setRestoreError("");
    setGroups(onlineProviders.map(provider => ({ provider, candidates: [], loading: true })));
    await Promise.allSettled(onlineProviders.map(async provider => {
      try {
        const result = await api<CandidateGroup>(`${base}/candidates`, "POST", { provider, query, refresh }, controller.signal);
        if (!controller.signal.aborted) setGroups(old => old.map(group => group.provider === provider ? { ...result, provider, loading: false } : group));
      } catch (err) {
        if (!controller.signal.aborted) setGroups(old => old.map(group => group.provider === provider ? { provider, candidates: [], loading: false, error: (err as Error).message } : group));
      }
    }));
  }
  function cancelSearch() {
    autoSelect.current = false;
    searchController.current?.abort();
    setGroups(old => old.map(group => ({ ...group, loading: false })));
  }
  const choose = useCallback(async (choice: CandidateChoice) => {
    previewController.current?.abort();
    const controller = new AbortController(); previewController.current = controller;
    setSavedSelection(false); setSelected(choice); setPreview(undefined); setPreviewing(true); setPreviewError(""); setTranslation(false); setRomanization(false);
    try {
      const result = await api<CandidateText>(`${base}/preview`, "POST", { token: choice.token, index: choice.candidate.index }, controller.signal);
      if (!controller.signal.aborted) {
        setPreview(result);
        setGroups(old => old.map(group => ({ ...group, candidates: group.candidates.map(candidate => group.token === choice.token && candidate.index === choice.candidate.index ? { ...candidate, script: result.script || "unknown", translation_script: result.translation_script } : candidate) })));
      }
    } catch (err) { if (!controller.signal.aborted) setPreviewError((err as Error).message); }
    finally { if (!controller.signal.aborted) setPreviewing(false); }
  }, [base]);
  async function save(advance = false) {
    if (!selected || !preview) return;
    setSaving(true); onBusyChange(true);
    try {
      await api(base, "PUT", { token: selected.token, index: selected.candidate.index, translation, romanization });
      notify("lyrics.saved"); saved(); setSavedSelection(true);
      if (advance && next) next(); else if (!previous && !next) {
        close();
        requestAnimationFrame(() => (document.getElementById(`track-lyrics-trigger-${source.id}`) || document.getElementById("library-title"))?.focus({ preventScroll: true }));
      }
    } catch (err) { setPreviewError((err as Error).message); }
    finally { setSaving(false); onBusyChange(false); }
  }
  const loading = groups.some(group => group.loading);
  const choices = useMemo(() => groups.flatMap(group => group.candidates.map(candidate => ({ token: group.token, candidate })))
    .sort((a, b) => Number(b.candidate.high_confidence) - Number(a.candidate.high_confidence) || Number(b.candidate.script === "simplified") - Number(a.candidate.script === "simplified") || Number(b.candidate.translation_script === "simplified") - Number(a.candidate.translation_script === "simplified") || (b.candidate.match_score || 0) - (a.candidate.match_score || 0) || onlineProviders.indexOf(a.candidate.provider) - onlineProviders.indexOf(b.candidate.provider)), [groups]);
  const minScore = options.data?.options.min_score ?? 96;
  const recommended = options.data && choices.find(({ token, candidate }) => token && (candidate.identity_match ?? candidate.high_confidence) && candidate.duration > 0 && (candidate.duration_delta !== undefined ? candidate.duration_delta <= 10 : candidate.high_confidence) && candidate.match_score >= minScore);
  useEffect(() => {
    if (autoSelect.current && !restoring && !loading && !selected && recommended?.token) void choose({ token: recommended.token, candidate: recommended.candidate });
  }, [restoring, loading, selected, recommended, choose]);
  return <InlineDetails id={`track-lyrics-${source.id}`} returnFocusID={`track-lyrics-trigger-${source.id}`} className="lyrics-picker" title={t("lyrics.choose")} subtitle={`${source.title} · ${source.artist}${source.duration ? ` · ${t("lyrics.targetDuration", { seconds: source.duration })}` : ""}`} close={close} closeLabel={t("lyrics.close")} busy={saving} actions={<><Button type="button" variant="ghost" size="icon" aria-label={t("library.previousTrack")} disabled={saving || !previous} onClick={previous}><ChevronLeft size={17} /></Button><Button type="button" variant="ghost" size="icon" aria-label={t("library.nextTrack")} disabled={saving || !next} onClick={next}><ChevronRight size={17} /></Button></>}>
    <details className="lyrics-guidance"><summary>{t("lyrics.recommendationInfo")}</summary><p>{t("lyrics.recommendationHint")}</p><p>{t("lyrics.cacheHint")}</p></details>
    <ResourceWarning error={options.error} retry={options.retry} />
    {restoring && <p role="status">{t("lyrics.restoring")}</p>}
    {restoreError && <p role="alert">{errorMessage(restoreError)}</p>}
    <form className="lyrics-query" onSubmit={event => { event.preventDefault(); void search(); }}>
      <label><span>{t("lyrics.query")}</span><input autoFocus maxLength={500} value={query} disabled={saving || restoring} onChange={event => setQuery(event.target.value)} /></label>
      <Button type="submit" variant="outline" disabled={saving || restoring || loading || !query.trim()}><Search size={15} />{t("lyrics.findCandidates")}</Button>
      <Button type="button" variant="ghost" disabled={saving || restoring || loading || !query.trim()} onClick={() => void search(true)}>{t("lyrics.forceSearch")}</Button>
      {loading && <Button type="button" variant="ghost" onClick={cancelSearch}>{t("lyrics.cancelSearch")}</Button>}
    </form>
    <div className="lyrics-provider-status" aria-label={t("lyrics.providerStatus")}>{groups.map(group => <fieldset key={group.provider} disabled={saving}><legend>{t(`lyrics.name.${group.provider}`)}{group.provider === "lrclib" && <> <span className="provider-priority">{t("lyrics.preferredSource")}</span></>}</legend>{group.cached && <small>{t("lyrics.cached")}{group.searched ? <> · {new Date(group.searched * 1000).toLocaleString(locale)}</> : null}</small>}{group.loading && <p role="status">{t("app.loading")}</p>}{group.error && <p role="alert">{errorMessage(group.error)}</p>}{!group.loading && !group.error && group.candidates.length === 0 && <p>{t("lyrics.noCandidates")}</p>}</fieldset>)}</div>
    <div className="lyrics-picker-results"><div className="lyrics-candidate-groups" aria-live="polite"><label className="candidate-source-filter"><span>{t("lyrics.sourceFilter")}</span><select value={providerFilter} onChange={event => setProviderFilter(event.target.value)}><option value="all">{t("lyrics.allCandidates")}</option>{onlineProviders.map(provider => <option key={provider} value={provider}>{t(`lyrics.name.${provider}`)}</option>)}</select></label>{choices.filter(choice => providerFilter === "all" || choice.candidate.provider === providerFilter).map(({ token, candidate }) => <label className="lyrics-candidate" key={`${token}:${candidate.index}`} data-recommended={recommended?.token === token && recommended?.candidate.index === candidate.index} data-selected={selected?.token === token && selected?.candidate.index === candidate.index}>
      <input type="radio" name={`lyrics-candidate-${source.id}`} aria-label={t("lyrics.previewCandidate", { title: candidate.title, artist: candidate.artist, provider: t(`lyrics.name.${candidate.provider}`) })} disabled={saving || !token} checked={!!selected && selected.token === token && selected.candidate.index === candidate.index} onChange={() => token && void choose({ token, candidate })} />
      <span><strong dir="auto">{candidate.title}{recommended?.token === token && recommended?.candidate.index === candidate.index && <em className="lyrics-recommended">{t("lyrics.recommended")}</em>}</strong><span dir="auto">{candidate.artist} · {candidate.album || "—"} · {candidateDuration(candidate.duration)}</span><span className="candidate-source">{t(`lyrics.name.${candidate.provider}`)}</span><small className="candidate-confidence" data-high={candidate.high_confidence}>{t(candidate.high_confidence ? "lyrics.matchHigh" : "lyrics.matchReview")}{candidate.match_score !== undefined && <> · {t("lyrics.matchScore", { score: candidate.match_score })}</>}{candidate.duration_delta !== undefined && <> · {t("lyrics.durationDelta", { seconds: Math.round(candidate.duration_delta * 100) / 100 })}</>}</small>{candidate.script === "simplified" && <small className="script-chip">{t("lyrics.simplified")}</small>}{candidate.script === "traditional" && <small className="script-chip">{t("lyrics.traditional")}</small>}{candidate.translation_script === "simplified" && <small className="script-chip">{t("lyrics.simplifiedTranslation")}</small>}</span>
    </label>)}</div>
    <div className="lyrics-preview-pane">
    {previewing && <p role="status">{t("lyrics.loadingPreview")}</p>}
    {previewError && <p className="repair-warning" role="alert">{errorMessage(previewError)}</p>}
    {preview && <section className="lyrics-preview" aria-label={t("lyrics.preview")}>
      <h3>{t("lyrics.preview")}</h3>{preview.script === "simplified" && <p>{t("lyrics.simplified")}</p>}{preview.script === "traditional" && <p>{t("lyrics.traditional")}</p>}<pre dir="auto">{preview.original}</pre>
      {preview.translation && <><label className="check-field"><input type="checkbox" checked={translation} disabled={saving} onChange={event => setTranslation(event.target.checked)} /><span>{t("lyrics.includeTranslation")}</span></label>{translation && <pre dir="auto">{preview.translation}</pre>}</>}
      {preview.romanization && <><label className="check-field"><input type="checkbox" checked={romanization} disabled={saving} onChange={event => setRomanization(event.target.checked)} /><span>{t("lyrics.includeRomanization")}</span></label>{romanization && <pre dir="auto">{preview.romanization}</pre>}</>}
      <div className="lyrics-save">{savedSelection && <p className="saved-feedback" role="status">{t("lyrics.savedNext")}</p>}<p>{t("lyrics.saveHint")}</p><Button type="button" disabled={saving || savedSelection} onClick={() => void save()}>{t("lyrics.saveCandidate")}</Button>{next && <Button type="button" variant="outline" disabled={saving} onClick={() => savedSelection ? next() : void save(true)}>{t(savedSelection ? "lyrics.next" : "lyrics.saveNext")}<ChevronRight size={15} /></Button>}</div>
    </section>}
    {!preview && !previewing && <div className="lyrics-preview-empty"><FileMusic size={24} /><h3>{t("lyrics.preview")}</h3><p>{t("lyrics.selectToPreview")}</p></div>}
    </div></div>
  </InlineDetails>;
}
