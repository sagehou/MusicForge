import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronRight, FileMusic, Search, Square, X } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { LyricsProvider, Source } from "../types";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";

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

export function LyricsSearchButton({ ids, all = false, refresh = false, disabled = false, label, notify, onStarted }: {
  ids?: number[]; all?: boolean; refresh?: boolean; disabled?: boolean; label: MessageKey; notify: Notice; onStarted?: () => void;
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
  return <Button type="button" variant="outline" size="sm" disabled={disabled || busy || !all && !ids?.length} onClick={() => void search()}><Search size={13} />{t(label)}</Button>;
}

export function LyricsMaintenance({ sources, notify }: { sources: Source[]; notify: Notice }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
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
  return <details className="library-comparison lyrics-panel" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary><FileMusic size={18} /><span>{t("lyrics.title")}</span><ChevronRight size={16} /></summary>
    {open && <>
      <section className="lyrics-library-summary" aria-label={t("lyrics.libraryStatus")}>
        <strong>{t("lyrics.coverage", { done: inventory.checked, total: inventory.total })}</strong>
        <p>{t("lyrics.inventory", { found: inventory.counts.ready, missing: inventory.counts.not_found, instrumental: inventory.counts.instrumental, review: inventory.counts.needs_review, failed: inventory.counts.failed, unchecked: inventory.counts.unchecked })}</p>
        <small>{t("lyrics.inventoryHint")}</small>
      </section>
      <LyricsControls notify={notify} />
    </>}
  </details>;
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
  return <div className="lyrics-body">
    <p>{t("lyrics.hint")}</p>
    <p>{t("lyrics.privacy")}</p>
    <p>{t("lyrics.stopHint")}</p>
    <label className="check-field"><input type="checkbox" checked={options.auto_search} disabled={busy} onChange={event => void automatic(event.target.checked)} /><span>{t("lyrics.automatic")}</span></label>
    <div className="actions">
      <LyricsSearchButton all label="lyrics.searchAll" notify={notify} disabled={busy || running} onStarted={resource.retry} />
      {running && <Button variant="outline" size="sm" disabled={busy || report.stopping} onClick={() => void stop()}><Square size={13} />{t(report.stopping ? "lyrics.stopping" : "lyrics.stop")}</Button>}
    </div>
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {report.state !== "idle" && <div role="status" aria-label={t("lyrics.batchProgress")} aria-live="polite">
      <strong>{t(`lyrics.report.${report.state}`)}</strong>
      <p>{t("lyrics.progress", { done: report.processed, total: report.total, found: report.found, missing: report.not_found, failed: report.failed, review: report.needs_review ?? 0 })}</p>
      {report.current && <code>{report.current}</code>}
      {report.error && <p className="repair-warning">{errorMessage(report.error)}</p>}
    </div>}
  </div>;
}

export function LyricsTrack({ source, notify, refresh }: { source: Source; notify: Notice; refresh: () => void }) {
  const { t, errorMessage } = useI18n();
  const [picker, setPicker] = useState(false);
  if (!source.output_present || !source.output) return null;
  return <details className="lyrics-track">
    <summary>{t("lyrics.title")} · {t(`lyrics.state.${source.lyrics?.state || "unchecked"}`)}</summary>
    {source.lyrics?.path && <small>{source.lyrics.path}</small>}
    {source.lyrics?.provider && <small>{t(`lyrics.provider.${source.lyrics.provider}`)}</small>}
    {source.lyrics?.error && <p>{errorMessage(source.lyrics.error)}</p>}
    {source.lyrics?.attempts && <details><summary>{t("lyrics.attempts")}</summary>{source.lyrics.attempts.map(attempt => <p key={attempt.provider}>{t(`lyrics.provider.${attempt.provider}`)} · {t(`lyrics.state.${attempt.state}`)}{attempt.error && <> · {errorMessage(attempt.error)}</>}</p>)}</details>}
    <LyricsSearchButton ids={[source.id]} refresh label="lyrics.searchAgain" notify={notify} />
    <Button type="button" variant="outline" size="sm" onClick={() => setPicker(true)}>{t("lyrics.choose")}</Button>
    {picker && <LyricsPicker key={`${source.id}:${source.output}`} source={source} notify={notify} close={() => setPicker(false)} saved={refresh} />}
  </details>;
}

const onlineProviders = ["lrclib", "qq", "kugou", "netease"] as const;
type OnlineProvider = Exclude<LyricsProvider, "source">;
type Candidate = { index: number; provider: OnlineProvider; title: string; artist: string; album: string; duration: number; high_confidence: boolean };
type CandidateGroup = { provider: OnlineProvider; token?: string; candidates: Candidate[]; loading: boolean; error?: string };
type CandidateText = { original: string; translation?: string; romanization?: string };
type CandidateChoice = { token: string; candidate: Candidate };

function LyricsPicker({ source, notify, close, saved }: { source: Source; notify: Notice; close: () => void; saved: () => void }) {
  const { t, errorMessage } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
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
  const base = `/library/${source.id}/lyrics`;
  useEffect(() => {
    dialog.current?.showModal();
    return () => { searchController.current?.abort(); previewController.current?.abort(); };
  }, []);
  async function search() {
    searchController.current?.abort(); previewController.current?.abort();
    const controller = new AbortController(); searchController.current = controller;
    setSelected(undefined); setPreview(undefined); setPreviewError(""); setPreviewing(false);
    setGroups(onlineProviders.map(provider => ({ provider, candidates: [], loading: true })));
    await Promise.allSettled(onlineProviders.map(async provider => {
      try {
        const result = await api<{ token: string; candidates: Candidate[] }>(`${base}/candidates`, "POST", { provider, query }, controller.signal);
        if (!controller.signal.aborted) setGroups(old => old.map(group => group.provider === provider ? { provider, ...result, loading: false } : group));
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
    setSelected(choice); setPreview(undefined); setPreviewing(true); setPreviewError(""); setTranslation(false); setRomanization(false);
    try {
      const result = await api<CandidateText>(`${base}/preview`, "POST", { token: choice.token, index: choice.candidate.index }, controller.signal);
      if (!controller.signal.aborted) setPreview(result);
    } catch (err) { if (!controller.signal.aborted) setPreviewError((err as Error).message); }
    finally { if (!controller.signal.aborted) setPreviewing(false); }
  }
  async function save() {
    if (!selected || !preview) return;
    setSaving(true);
    try {
      await api(base, "PUT", { token: selected.token, index: selected.candidate.index, translation, romanization });
      notify("lyrics.saved"); saved(); dialog.current?.close();
    } catch (err) { setPreviewError((err as Error).message); }
    finally { setSaving(false); }
  }
  const loading = groups.some(group => group.loading);
  return createPortal(<dialog className="lyrics-picker" ref={dialog} aria-labelledby={`lyrics-picker-title-${source.id}`} onClose={close} onCancel={event => { if (saving) event.preventDefault(); }}>
    <div className="lyrics-picker-heading"><h2 id={`lyrics-picker-title-${source.id}`}>{t("lyrics.choose")}</h2><Button type="button" variant="ghost" size="icon" aria-label={t("lyrics.close")} disabled={saving} onClick={() => dialog.current?.close()}><X size={18} /></Button></div>
    <p>{source.title} · {source.artist}</p><p>{t("lyrics.chooseHint")}</p>
    <form className="lyrics-query" onSubmit={event => { event.preventDefault(); void search(); }}>
      <label><span>{t("lyrics.query")}</span><input autoFocus maxLength={500} value={query} disabled={saving} onChange={event => setQuery(event.target.value)} /></label>
      <Button type="submit" variant="outline" disabled={saving || loading || !query.trim()}><Search size={15} />{t("lyrics.findCandidates")}</Button>
      {loading && <Button type="button" variant="ghost" onClick={cancelSearch}>{t("lyrics.cancelSearch")}</Button>}
    </form>
    <div className="lyrics-candidate-groups" aria-live="polite">{groups.map(group => <fieldset key={group.provider} disabled={saving}>
      <legend>{t(`lyrics.name.${group.provider}`)}</legend>
      {group.loading && <p role="status">{t("app.loading")}</p>}
      {group.error && <p role="alert">{errorMessage(group.error)}</p>}
      {!group.loading && !group.error && group.candidates.length === 0 && <p>{t("lyrics.noCandidates")}</p>}
      {group.candidates.map(candidate => <label className="lyrics-candidate" key={candidate.index}>
        <input type="radio" name={`lyrics-candidate-${source.id}`} aria-label={t("lyrics.previewCandidate", { title: candidate.title, artist: candidate.artist, provider: t(`lyrics.name.${candidate.provider}`) })} checked={!!selected && selected.token === group.token && selected.candidate.index === candidate.index} onChange={() => group.token && void choose({ token: group.token, candidate })} />
        <span><strong dir="auto">{candidate.title}</strong><span dir="auto">{candidate.artist} · {candidate.album || "—"} · {candidate.duration > 0 ? `${Math.floor(candidate.duration / 60)}:${String(Math.floor(candidate.duration % 60)).padStart(2, "0")}` : "—"}</span><small>{t(candidate.high_confidence ? "lyrics.matchHigh" : "lyrics.matchReview")}</small></span>
      </label>)}
    </fieldset>)}</div>
    {previewing && <p role="status">{t("lyrics.loadingPreview")}</p>}
    {previewError && <p className="repair-warning" role="alert">{errorMessage(previewError)}</p>}
    {preview && <section className="lyrics-preview" aria-label={t("lyrics.preview")}>
      <h3>{t("lyrics.preview")}</h3><pre dir="auto">{preview.original}</pre>
      {preview.translation && <><label className="check-field"><input type="checkbox" checked={translation} disabled={saving} onChange={event => setTranslation(event.target.checked)} /><span>{t("lyrics.includeTranslation")}</span></label>{translation && <pre dir="auto">{preview.translation}</pre>}</>}
      {preview.romanization && <><label className="check-field"><input type="checkbox" checked={romanization} disabled={saving} onChange={event => setRomanization(event.target.checked)} /><span>{t("lyrics.includeRomanization")}</span></label>{romanization && <pre dir="auto">{preview.romanization}</pre>}</>}
      <p>{t("lyrics.saveHint")}</p><Button type="button" disabled={saving} onClick={() => void save()}>{t("lyrics.saveCandidate")}</Button>
    </section>}
  </dialog>, document.body);
}
