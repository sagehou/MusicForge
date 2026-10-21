import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, FileMusic, Info, Search, Square } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { LyricsProvider, Source } from "../types";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";
import { DetailPanel } from "./detail-panel";

type LyricsResponse = {
  options: { auto_search: boolean };
  report: { state: "idle" | "running" | "complete" | "stopped"; total: number; processed: number; found: number; not_found: number; failed: number; needs_review?: number; current?: string; error?: string; stopping: boolean };
};

export const lyricsMatchStates = ["ready", "unchecked", "not_found", "instrumental", "needs_review", "failed"] as const;
export type LyricsMatchState = typeof lyricsMatchStates[number];

export function lyricsMatchState(source: Source): LyricsMatchState | undefined {
  if (!source.output || !source.output_present) return undefined;
  return source.lyrics?.state || "unchecked";
}

export function LyricsSearchButton({ ids, all = false, refresh = false, disabled = false, compact = false, iconOnly = false, label, notify, onStarted }: {
  ids?: number[]; all?: boolean; refresh?: boolean; disabled?: boolean; compact?: boolean; iconOnly?: boolean; label: MessageKey; notify: Notice; onStarted?: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  async function search() {
    setBusy(true);
    try {
      await api("/lyrics/search", "POST", { ...(all ? { all: true } : { ids }), refresh });
      notify("lyrics.started"); onStarted?.();
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <Button type="button" variant="outline" size="sm" className={compact ? "lyrics-action" : undefined} aria-label={t(label)} disabled={disabled || busy || !all && !ids?.length} onClick={() => void search()}><Search size={13} />{!iconOnly && t(compact ? "lyrics.searchShort" : label)}</Button>;
}

export function LyricsMaintenance({ sources, notify }: { sources: Source[]; notify: Notice }) {
  const { t } = useI18n();
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
    <details className="lyrics-settings"><summary><FileMusic size={18} /><span>{t("lyrics.title")}</span><small className="maintenance-count" aria-label={t("lyrics.coverage", { done: inventory.checked, total: inventory.total })}>{inventory.checked} / {inventory.total}</small><ChevronRight size={16} /></summary>
      <section className="lyrics-library-summary" aria-label={t("lyrics.libraryStatus")}>
        <strong>{t("lyrics.coverage", { done: inventory.checked, total: inventory.total })}</strong>
        <p>{t("lyrics.inventory", { found: inventory.counts.ready, missing: inventory.counts.not_found, instrumental: inventory.counts.instrumental, review: inventory.counts.needs_review, failed: inventory.counts.failed, unchecked: inventory.counts.unchecked })}</p>
        <small>{t("lyrics.inventoryHint")}</small>
      </section>
    </details>
    <LyricsControls notify={notify} />
  </section>;
}

function LyricsControls({ notify }: { notify: Notice }) {
  const { t, errorMessage } = useI18n();
  const resource = useResource<LyricsResponse>("/lyrics", 2000);
  const [busy, setBusy] = useState(false);
  if (!resource.data) return <Loading error={resource.error} retry={resource.retry} />;
  const { options, report } = resource.data;
  const running = report.state === "running";
  async function automatic(enabled: boolean) {
    resource.update(data => ({ ...data, options: { auto_search: enabled } }));
    setBusy(true);
    try { await api("/lyrics/options", "PUT", { auto_search: enabled }); resource.retry(); }
    catch (err) { resource.update(data => ({ ...data, options })); notify(err as Error, true); } finally { setBusy(false); }
  }
  async function stop() {
    setBusy(true);
    try { await api("/lyrics/search", "DELETE"); resource.update(data => ({ ...data, options: { auto_search: false } })); resource.retry(); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  return <div className="lyrics-body" data-running={running}>
    <div className="actions lyrics-quick-actions">
      <LyricsSearchButton all compact iconOnly label="lyrics.searchAll" notify={notify} disabled={busy || running} onStarted={resource.retry} />
      {running && <Button className="lyrics-action" variant="outline" size="sm" aria-label={t(report.stopping ? "lyrics.stopping" : "lyrics.stop")} disabled={busy || report.stopping} onClick={() => void stop()}><Square size={13} /></Button>}
    </div>
    <details className="lyrics-options"><summary>{t("lyrics.options")}</summary>
      <p>{t("lyrics.hint")}</p>
      <p>{t("lyrics.privacy")}</p>
      <p>{t("lyrics.stopHint")}</p>
      <label className="check-field"><input type="checkbox" checked={options.auto_search} disabled={busy} onChange={event => void automatic(event.target.checked)} /><span>{t("lyrics.automatic")}</span></label>
    </details>
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {report.state !== "idle" && <div role="status" aria-label={t("lyrics.batchProgress")} aria-live="polite">
      <strong>{t(`lyrics.report.${report.state}`)}</strong>
      <p>{t("lyrics.progress", { done: report.processed, total: report.total, found: report.found, missing: report.not_found, failed: report.failed, review: report.needs_review ?? 0 })}</p>
      {report.current && <code>{report.current}</code>}
      {report.error && <p className="repair-warning">{errorMessage(report.error)}</p>}
    </div>}
  </div>;
}

export function LyricsTrack({ source, notify, refresh, onChoose }: { source: Source; notify: Notice; refresh: () => void; onChoose?: () => void }) {
  const { t, errorMessage } = useI18n();
  const [picker, setPicker] = useState(false);
  if (!source.output_present || !source.output) return null;
  return <div className="lyrics-track" data-state={source.lyrics?.state || "unchecked"}>
    <span className="lyrics-state">{t(`lyrics.state.${source.lyrics?.state || "unchecked"}`)}</span>
    <div className="lyrics-actions">
      <LyricsSearchButton ids={[source.id]} refresh compact label="lyrics.searchAgain" notify={notify} />
      <Button type="button" variant="outline" size="sm" className="lyrics-action" aria-label={t("lyrics.choose")} onClick={() => onChoose ? onChoose() : setPicker(true)}><FileMusic size={13} />{t("lyrics.chooseShort")}</Button>
    {source.lyrics && <details className="lyrics-details"><summary aria-label={t("lyrics.details")} title={t("lyrics.details")}><Info size={14} /></summary><div className="lyrics-status-popover">
    {source.lyrics?.path && <small>{source.lyrics.path}</small>}
    {source.lyrics?.provider && <small>{t(`lyrics.provider.${source.lyrics.provider}`)}</small>}
    {source.lyrics?.error && <p>{errorMessage(source.lyrics.error)}</p>}
    {source.lyrics?.attempts && <details><summary>{t("lyrics.attempts")}</summary>{source.lyrics.attempts.map(attempt => <p key={attempt.provider}>{t(`lyrics.provider.${attempt.provider}`)} · {t(`lyrics.state.${attempt.state}`)}{attempt.error && <> · {errorMessage(attempt.error)}</>}</p>)}</details>}
    </div></details>}
    </div>
    {picker && <LyricsPicker key={`${source.id}:${source.output}`} source={source} notify={notify} close={() => setPicker(false)} saved={refresh} />}
  </div>;
}

const onlineProviders = ["lrclib", "qq", "kugou", "netease"] as const;
type OnlineProvider = Exclude<LyricsProvider, "source">;
type Candidate = { index: number; provider: OnlineProvider; title: string; artist: string; album: string; duration: number; high_confidence: boolean; match_score: number; duration_delta?: number; script: string; translation_script?: string };
type CandidateGroup = { provider: OnlineProvider; token?: string; candidates: Candidate[]; cached?: boolean; searched?: number; loading: boolean; error?: string };
type CandidateText = { original: string; translation?: string; romanization?: string; script?: string; translation_script?: string };
type CandidateChoice = { token: string; candidate: Candidate };

function candidateDuration(value: number) {
  const seconds = Math.round(value);
  return seconds > 0 ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}` : "—";
}

export function LyricsPicker({ source, notify, close, saved, previous, next }: { source: Source; notify: Notice; close: () => void; saved: () => void; previous?: () => void; next?: () => void }) {
  const { t, errorMessage, locale } = useI18n();
  const searchController = useRef<AbortController | null>(null);
  const previewController = useRef<AbortController | null>(null);
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
    searchController.current?.abort();
    setGroups(old => old.map(group => ({ ...group, loading: false })));
  }
  async function choose(choice: CandidateChoice) {
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
  }
  async function save(advance = false) {
    if (!selected || !preview) return;
    setSaving(true);
    try {
      await api(base, "PUT", { token: selected.token, index: selected.candidate.index, translation, romanization });
      notify("lyrics.saved"); saved(); setSavedSelection(true);
      if (advance && next) next(); else if (!previous && !next) close();
    } catch (err) { setPreviewError((err as Error).message); }
    finally { setSaving(false); }
  }
  const loading = groups.some(group => group.loading);
  const recommended = groups.flatMap(group => group.candidates.filter(candidate => candidate.high_confidence).map(candidate => ({ token: group.token, candidate })))
    .sort((a, b) => Number(b.candidate.script === "simplified") - Number(a.candidate.script === "simplified") || Number(b.candidate.translation_script === "simplified") - Number(a.candidate.translation_script === "simplified") || b.candidate.match_score - a.candidate.match_score)[0];
  const choices = groups.flatMap(group => group.candidates.map(candidate => ({ token: group.token, candidate })))
    .sort((a, b) => Number(b.candidate.high_confidence) - Number(a.candidate.high_confidence) || Number(b.candidate.script === "simplified") - Number(a.candidate.script === "simplified") || Number(b.candidate.translation_script === "simplified") - Number(a.candidate.translation_script === "simplified") || (b.candidate.match_score || 0) - (a.candidate.match_score || 0) || onlineProviders.indexOf(a.candidate.provider) - onlineProviders.indexOf(b.candidate.provider));
  return <DetailPanel className="lyrics-picker" title={t("lyrics.choose")} subtitle={`${source.title} · ${source.artist}${source.duration ? ` · ${t("lyrics.targetDuration", { seconds: source.duration })}` : ""}`} close={close} closeLabel={t("lyrics.close")} busy={saving} actions={<><Button type="button" variant="ghost" size="icon" aria-label={t("library.previousTrack")} disabled={saving || !previous} onClick={previous}><ChevronLeft size={17} /></Button><Button type="button" variant="ghost" size="icon" aria-label={t("library.nextTrack")} disabled={saving || !next} onClick={next}><ChevronRight size={17} /></Button></>}>
    <details className="lyrics-guidance"><summary>{t("lyrics.recommendationInfo")}</summary><p>{t("lyrics.recommendationHint")}</p><p>{t("lyrics.cacheHint")}</p></details>
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
  </DetailPanel>;
}
