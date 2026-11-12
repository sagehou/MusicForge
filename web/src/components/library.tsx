import { useDeferredValue, useEffect, useRef, useState, Fragment, memo } from "react";
import { flushSync } from "react-dom";
import { ArrowLeft, ChevronLeft, ChevronRight, Clock3, Copy, Info, Disc3, Fingerprint, Grid2X2, ListMusic, Tags, List, Play, RefreshCw, Search, ShieldCheck, SlidersHorizontal, Trash2, X } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { Source } from "../types";
import type { LibraryGroup, LibraryPage } from "../library-data";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";
import { BatchRepairButton, RepairControls, RepairDiagnostics } from "./repair";
import { ArtworkEditor } from "./artwork-editor";
import { MaintenanceWorkbench, BuildPreview } from "./maintenance";
import { ArtifactAudition } from "./artifact-audition";
import { LibraryComparison } from "./library-comparison";
import { InlineDetails } from "./inline-details";
import { LibraryTools, type LibraryTool } from "./library-tools";
import { PageHeading } from "./page-heading";
import { SavedLibraryViews } from "./saved-library-views";
import { LyricsMaintenance, LyricsPicker, LyricsSearchButton, LyricsTrack, lyricsMatchStates, type LyricsMatchState } from "./lyrics";

type Scope = { view: "artists" | "albums" | "tracks"; artist?: string; album?: string };
type Density = "compact" | "standard" | "large";
const priority = ["failed", "possibly_damaged", "expired", "missing", "changed", "needs_rebuild", "unverified", "ready"];
function preference(key: string) {
  try { return localStorage.getItem(`musicforge.library.${key}`); } catch { return null; }
}
function savePreference(key: string, value: string) {
  try { localStorage.setItem(`musicforge.library.${key}`, value); } catch { /* Browsing remains available without storage. */ }
}
function readScope(): Scope {
  const params = new URLSearchParams(location.search);
  const view = params.get("view");
  return { view: view === "tracks" || view === "artists" ? view : "albums", artist: params.get("artist") ?? undefined, album: params.get("album") ?? undefined };
}
function lyricsPageTrack() {
  const value = Number(new URLSearchParams(location.search).get("lyrics_track"));
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
}
function duration(seconds: number) {
  if (!seconds) return "--";
  const value = Math.round(seconds);
  return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, "0")}`;
}
function fileSize(bytes: number, locale: string) {
  const unit = Math.min(4, Math.floor(Math.log2(Math.max(bytes, 1)) / 10));
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(bytes / 1024 ** unit)} ${["B", "KiB", "MiB", "GiB", "TiB"][unit]}`;
}
function AudioSpec({ audio }: { audio: Source["source_audio"] }) {
  const { t, number } = useI18n();
  const resolution = [audio?.bit_depth ? `${number(audio.bit_depth)} bit` : "", audio?.sample_rate ? `${number(audio.sample_rate / 1000)} kHz` : ""].filter(Boolean).join(" / ");
  return <>{[audio?.codec?.toUpperCase(), resolution, audio?.channels ? t("library.channels", { count: number(audio.channels) }) : ""].filter(Boolean).join(" · ") || "—"}</>;
}
function TrackFacts({ source, compact = false }: { source: Source; compact?: boolean }) {
  const { t, locale, number } = useI18n();
  return <dl className={`track-facts ${compact ? "track-facts-compact" : ""}`}>
    <div className="track-fact-duration"><dt>{t("library.duration")}</dt><dd>{duration(source.duration || 0)}</dd></div>
    <div className="track-fact-audio"><dt>{t("library.sourceAudio")}</dt><dd className="track-spec-chip"><AudioSpec audio={source.source_audio} /></dd><dd className="track-bitrate" title={!source.source_bitrate ? t("library.bitratePending") : undefined}><span>{t("library.sourceBitrate")} </span>{source.source_bitrate ? `${number(Math.round(source.source_bitrate / 1000))} kbps` : "—"}</dd></div>
    <div className="track-fact-sizes"><dt title={t("library.sourceSize")}>{t("library.sourceSizeShort")}</dt><dd>{fileSize(source.size, locale)}</dd>{source.output_present && source.output_size !== undefined && <><dt title={t("library.outputSize")}>{t("library.outputSizeShort")}</dt><dd className="track-output-size">{fileSize(source.output_size, locale)}</dd></>}</div>
  </dl>;
}
function GroupFacts({ group }: { group: LibraryGroup }) {
  const { t, locale, number } = useI18n();
  return <dl className="group-facts">
    <div><dt>{t("library.duration")}</dt><dd>{t("library.trackCount", { count: group.count })}<small>{group.duration_known ? duration(group.duration) : "—"}</small></dd></div>
    <div><dt>{t("library.sourceAudio")}</dt><dd>{group.audio.map((value, index) => <span className="group-audio-spec" key={index}><AudioSpec audio={value ?? undefined} /></span>)}{group.audio_count > 3 && <small>+{number(group.audio_count - 3)}</small>}</dd></div>
    <div><dt>{t("library.outputColumn")}</dt><dd>{group.output_formats.join(" / ") || "—"}<small>{group.playable && group.output_known ? fileSize(group.output_size, locale) : "—"}</small></dd></div>
    <div><dt>{t("lyrics.title")}</dt><dd>{group.playable ? `${number(group.lyrics_ready)} / ${number(group.playable)}` : "—"}</dd></div>
  </dl>;
}
function TrackDetails({ source, notify }: { source: Source; notify: Notice }) {
  const { t, locale, number } = useI18n();
  async function copyHash() {
    try { await navigator.clipboard.writeText(source.hash || ""); notify("notice.hashCopied"); }
    catch { notify("notice.copyFailed", true); }
  }
  return <section className="track-file"><dl>
    <dt>{t("library.songTitle")}</dt><dd dir="auto">{source.title || "—"}</dd>
    <dt>{t("library.songArtist")}</dt><dd dir="auto">{source.artist || t("library.unknownArtist")}</dd>
    <dt>{t("library.songAlbum")}</dt><dd dir="auto">{source.album || t("library.unknownAlbum")}</dd>
    <dt>{t("library.duration")}</dt><dd>{source.duration ? t("library.exactDuration", { time: duration(source.duration), seconds: number(source.duration) }) : "—"}</dd>
    <dt>{t("library.trackDisc")}</dt><dd>{source.track || "—"} / {source.disc || "—"}</dd>
    <dt>{t("library.sourceAudio")}</dt><dd><AudioSpec audio={source.source_audio} /></dd>
    <dt>{t("library.sourceBitrate")}</dt><dd>{source.source_bitrate ? `${number(source.source_bitrate)} bps` : t("library.bitratePending")}</dd>
    <dt>{t("library.sourceSize")}</dt><dd>{fileSize(source.size, locale)}{source.size >= 1024 && <> ({t("library.bytes", { size: source.size })})</>}</dd>
    {source.output_present && source.output_size !== undefined && <><dt>{t("library.outputSize")}</dt><dd>{fileSize(source.output_size, locale)} ({t("library.bytes", { size: source.output_size })})</dd></>}
    {source.output_gain && <><dt>ReplayGain / R128</dt><dd>{source.output_gain.track} · {source.output_gain.album}</dd></>}
    <dt>{t("library.sourceSHA256")}</dt><dd className="hash-value"><code>{source.hash || t("library.hashPending")}</code>{source.hash && <Button type="button" variant="ghost" size="icon" aria-label={t("library.copyHash")} onClick={() => void copyHash()}><Copy size={15} /></Button>}</dd>
    <dt>{t("library.source")}</dt><dd><code>{source.path}</code></dd>
    {source.output && <><dt>{t("library.artifact")}</dt><dd><code>{source.output}</code></dd></>}
  </dl>{source.error && <div className="error-box" role="alert"><strong>{t("library.errorDetails")}</strong><pre>{source.error}</pre></div>}{Object.keys(source.metadata).length > 0 && <details><summary>{t("library.metadata")}</summary><dl>{Object.entries(source.metadata).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => <Fragment key={key}><dt>{key}</dt><dd dir="auto">{value}</dd></Fragment>)}</dl></details>}{source.output_tags && <details><summary>{t("tags.saved")}</summary><dl>{Object.entries(source.output_tags).map(([key, value]) => <Fragment key={key}><dt>{key}</dt><dd dir="auto">{value || "—"}</dd></Fragment>)}</dl></details>}<RepairDiagnostics source={source} /></section>;
}
function CoverImage({ url, failed }: { url: string; failed: () => void }) {
  const [ready, setReady] = useState(false);
  return <img src={url} alt="" loading="lazy" decoding="async" className={ready ? "cover-loaded" : ""} onLoad={() => setReady(true)} onError={failed} />;
}
const Cover = memo(function Cover({ tracks, name, artist = false, transition = false }: { tracks: Pick<Source, "artwork_url">[]; name: string; artist?: boolean; transition?: boolean }) {
  const { t } = useI18n();
  const [failed, setFailed] = useState<Set<string>>(new Set());
  const covers = new Map<string, string>();
  for (const track of tracks) {
    const url = track.artwork_url;
    if (url && !failed.has(url)) {
      const key = new URL(url, location.origin).searchParams.get("v") || url;
      if (!covers.has(key)) covers.set(key, url);
      if (covers.size === (artist ? 4 : 1)) break;
    }
  }
  const urls = [...covers.values()].slice(0, artist ? 4 : 1);
  return <span className={`cover ${artist && urls.length > 1 ? "cover-mosaic" : ""}`} style={transition ? { viewTransitionName: "library-artwork" } : undefined} role="img" aria-label={t(artist ? "library.artistArtwork" : "library.albumArtwork", { name })}>
    {urls.length ? urls.map(url => <CoverImage key={url} url={url} failed={() => setFailed(old => new Set([...old, url]))} />) :
      <span className="cover-fallback"><Disc3 size={40} strokeWidth={1} /><span>{name.slice(0, 1).toUpperCase()}</span></span>}
  </span>;
});

export function Library({ notify }: { notify: Notice }) {
  const { t, status: statusLabel, locale, number } = useI18n();
  const [scope, setScope] = useState<Scope>(readScope);
  const [query, setQuery] = useState(() => new URLSearchParams(location.search).get("q") || "");
  const search = useDeferredValue(query.trim().toLocaleLowerCase(locale));
  const [filter, setFilter] = useState(() => new URLSearchParams(location.search).get("status") || "all");
  const [lyricsFilter, setLyricsFilter] = useState<LyricsMatchState | "all">(() => { const value = new URLSearchParams(location.search).get("lyrics"); return lyricsMatchStates.includes(value as LyricsMatchState) ? value as LyricsMatchState : "all"; });
  const scriptFilters = ["all", "traditional", "simplified", "unknown", "unidentified"] as const;
  type ScriptFilter = typeof scriptFilters[number];
  function readScriptFilter(): ScriptFilter { const value = new URLSearchParams(location.search).get("lyrics_script"); return scriptFilters.includes(value as ScriptFilter) ? value as ScriptFilter : "all"; }
  const [scriptFilter, setScriptFilter] = useState<ScriptFilter>(readScriptFilter);
  const [layout, setLayout] = useState<"grid" | "list">(() => preference("layout") === "list" ? "list" : "grid");
  const [density, setDensity] = useState<Density>(() => {
    const saved = preference("density");
    return saved === "compact" || saved === "large" ? saved : "standard";
  });
  const transition = useRef<ViewTransition | null>(null);
  const navigation = useRef(0);
  const [detailID, setDetailID] = useState<number>();
  const [auditionID, setAuditionID] = useState<number>();
  const [auditionEditing, setAuditionEditing] = useState(false);
  const [coverID, setCoverID] = useState<number>();
  const [coverTrigger, setCoverTrigger] = useState("");
  const [tool, setTool] = useState<{ kind: LibraryTool; trigger: string; sources?: Source[] }>();
  const standalone = new URLSearchParams(location.search).has("lyrics_track");
  const [lyricsID, setLyricsID] = useState<number | undefined>(lyricsPageTrack);
  const [lyricsOrder, setLyricsOrder] = useState<number[]>([]);
  const [editorBusy, setEditorBusy] = useState(false);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [sort, setSort] = useState(() => new URLSearchParams(location.search).get("sort") === "attention" ? "attention" : "name");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [working, setBusy] = useState(false);
  const [selectionRows, setSelectionRows] = useState<Source[]>([]);
  const [extraSource, setExtraSource] = useState<Source>();
  const reviewRequest = useRef(0);
  const stepRequest = useRef(0);
  const [selectionPending, setSelectionPending] = useState(false);
  const standaloneOrderLoaded = useRef(false);
  const [offset, setOffset] = useState(0);
  const isAlbum = scope.album !== undefined;
  const showingTracks = isAlbum || scope.view === "tracks";
  const showingArtists = !scope.artist && scope.view === "artists" && !isAlbum;
  const trackColumns = isAlbum ? 4 : 7;
  const filters = new URLSearchParams({ view: scope.view, q: search, status: filter, lyrics: lyricsFilter, lyrics_script: scriptFilter, sort, locale });
  if (scope.artist !== undefined) filters.set("artist", scope.artist);
  if (scope.album !== undefined) filters.set("album", scope.album);
  const scopeParams = new URLSearchParams({ locale });
  if (scope.artist !== undefined) scopeParams.set("artist", scope.artist);
  if (scope.album !== undefined) scopeParams.set("album", scope.album);
  const request = new URLSearchParams(filters);
  request.set("offset", String(offset));
  const anchor = lyricsID ?? detailID ?? auditionID ?? coverID;
  if (anchor) request.set("anchor", String(anchor));
  const { data, error, retry, pending } = useResource<LibraryPage>(`/library/browse?${request}`, true, true);
  const busy = working || pending || selectionPending;
  const rows = data?.rows ?? [];
  const groups = data?.groups ?? [];
  const known = new Map([...selectionRows, ...(extraSource && pending && extraSource.id === anchor ? [extraSource] : []), ...(data?.context_source ? [data.context_source] : []), ...rows, ...(data?.active ? [data.active] : [])].map(source => [source.id, source]));
  const chosen = [...selected].flatMap(id => known.has(id) ? [known.get(id)!] : []);
  const selectedKey = [...selected].join(",");
  useEffect(() => {
    if (!selectedKey) { setSelectionRows([]); setSelectionPending(false); return; }
    setSelectionPending(true);
    const controller = new AbortController();
    void api<Source[]>("/library/selection", "POST", { ids: selectedKey.split(",").map(Number) }, controller.signal).then(sources => {
      if (!controller.signal.aborted) {
        setSelectionRows(sources);
        const found = new Set(sources.map(source => source.id));
        setSelected(old => old.size === found.size ? old : found);
        setSelectionPending(false);
      }
    }).catch(err => { if (!controller.signal.aborted) { setSelected(new Set()); setSelectionRows([]); setSelectionPending(false); notify(err as Error, true); } });
    return () => controller.abort();
  }, [selectedKey, data?.revision]);
  useEffect(() => {
    const back = () => { reviewRequest.current++; stepRequest.current++; standaloneOrderLoaded.current = false; navigation.current++; transition.current?.skipTransition(); setScope(readScope()); const params = new URLSearchParams(location.search); setFilter(params.get("status") || "all"); setLyricsFilter((params.get("lyrics") || "all") as LyricsMatchState | "all"); setScriptFilter(readScriptFilter()); setSort(params.get("sort") === "attention" ? "attention" : "name"); setQuery(params.get("q") || ""); setOffset(0); setDetailID(undefined); setAuditionID(undefined); setLyricsID(lyricsPageTrack()); setOptionsOpen(false); setLyricsOrder([]); setCoverID(undefined); setTool(undefined); };
    window.addEventListener("popstate", back);
    window.addEventListener("route-changed", back);
    return () => { window.removeEventListener("popstate", back); window.removeEventListener("route-changed", back); };
  }, []);
  useEffect(() => () => { reviewRequest.current++; stepRequest.current++; transition.current?.skipTransition(); }, []);
  useEffect(() => {
    if (auditionID && data && !pending && (!data.active?.output_present || !data.active.output)) setAuditionID(undefined);
  }, [data, auditionID, pending]);
  useEffect(() => {
    const frame = requestAnimationFrame(() => document.getElementById("library-title")?.focus());
    return () => cancelAnimationFrame(frame);
  }, [scope.view, scope.artist, scope.album]);

  useEffect(() => {
    if (standalone && data && !standaloneOrderLoaded.current) {
      standaloneOrderLoaded.current = true;
      void loadLyricsOrder();
    }
  }, [standalone, lyricsID, data?.revision]);

  async function loadLyricsOrder() {
    const version = ++reviewRequest.current;
    try {
      const result = await api<{ ids: number[] }>("/library/selection", "POST", { query: filters.toString(), ids_only: true, playable: true });
      if (reviewRequest.current === version) setLyricsOrder(result.ids);
    } catch (err) { if (reviewRequest.current === version) { standaloneOrderLoaded.current = false; notify(err as Error, true); } }
  }
  async function selectMatching() {
    setBusy(true);
    try {
      const sources = await api<Source[]>("/library/selection", "POST", { query: filters.toString() });
      setSelectionRows(sources); setSelected(new Set(sources.map(source => source.id)));
    } catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  async function action(path: string, body: unknown, message: MessageKey) {
    setBusy(true);
    try { await api(path, "POST", body); notify(message); setSelected(new Set()); retry(); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  function navigate(next: Scope, origin?: HTMLElement) {
    if (editorBusy) return;
    reviewRequest.current++; stepRequest.current++; setLyricsOrder([]);
    setDetailID(undefined); setLyricsID(undefined); setAuditionID(undefined); setCoverID(undefined); setTool(undefined);
    const params = new URLSearchParams({ view: next.view });
    if (next.artist !== undefined) params.set("artist", next.artist);
    if (next.album !== undefined) params.set("album", next.album);
    if (query.trim()) params.set("q", query.trim());
    if (filter !== "all") params.set("status", filter);
    if (lyricsFilter !== "all") params.set("lyrics", lyricsFilter);
    if (scriptFilter !== "all") params.set("lyrics_script", scriptFilter);
    if (sort !== "name") params.set("sort", sort);
    history.pushState(null, "", `/library?${params}`);
    transition.current?.skipTransition();
    const revision = ++navigation.current;
    const update = () => { if (navigation.current === revision) { setScope(next); setOffset(0); setOptionsOpen(false); window.scrollTo(0, 0); } };
    const cover = origin?.closest(".library-group")?.querySelector<HTMLElement>(".cover");
    if (!next.album || !cover || !document.startViewTransition || matchMedia("(prefers-reduced-motion: reduce)").matches) {
      update(); return;
    }
    cover.style.viewTransitionName = "library-artwork";
    const current = document.startViewTransition(() => flushSync(update));
    transition.current = current;
    void current.finished.catch(() => {}).finally(() => {
      cover.style.viewTransitionName = "";
      if (transition.current === current) transition.current = null;
    });
  }
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    for (const [key, value] of [["status", filter], ["lyrics", lyricsFilter], ["lyrics_script", scriptFilter], ["sort", sort]]) {
      if (value === "all" || key === "sort" && value === "name") params.delete(key); else params.set(key, value);
    }
    if (query.trim()) params.set("q", query.trim()); else params.delete("q");
    const search = params.toString();
    history.replaceState(null, "", `/library${search ? `?${search}` : ""}${location.hash}`);
  }, [filter, lyricsFilter, scriptFilter, sort, query]);
  function toggle(ids: number[], checked: boolean) {
    setSelected(old => { const next = new Set(old); ids.forEach(id => checked ? next.add(id) : next.delete(id)); return next; });
  }
  if (!data) return error ? <><PageHeading><div><h1>{t("app.library")}</h1></div></PageHeading><Loading error={error} retry={retry} /></> : <div className="library-loading" role="status" aria-label={t("app.loading")}>
    <PageHeading><div><h1>{t("app.library")}</h1><p>{t("library.loadingHint")}</p></div></PageHeading>
    <div className="skeleton-toolbar" aria-hidden="true" />
    <div className="library-groups" aria-hidden="true">{Array.from({ length: 10 }, (_, index) => <div className="skeleton-card" key={index}><div /><span /><span /></div>)}</div>
  </div>;
  const count = data.total;
  const safeOffset = data.offset;
  const shown = rows;
  const visibleGroups = groups;
  const pageIDs = showingTracks ? shown.map(source => source.id) : visibleGroups.flatMap(group => group.ids || []);
  const selectedExpired = chosen.filter(source => !source.present && source.output).length;
  const expired = data.counts.expired;
  const removable = data.counts.removable;
  const selectedRemovable = chosen.filter(source => !source.present && !source.output && !source.output_present);
  const album = data.scope;
  const artist = data.scope;
  const totals = data.counts;
  const albumDuration = data.scope.duration;
  const discs = data.scope.discs;
  function deleteSelected(all: boolean) {
    if (window.confirm(all ? t("library.deleteAllConfirm", { count: expired }) : t("library.deleteSelectedConfirm"))) {
      void action("/library/delete", { all, ids: [...selected] }, "notice.delete");
    }
  }
  async function forget(ids: number[], all = false) {
    if (!window.confirm(t("library.forgetConfirm", { count: all ? removable : ids.length }))) return;
    setBusy(true);
    try {
      const result = await api<{ removed: number; skipped: number }>("/library/forget", "POST", all ? { all: true } : { ids });
      notify("notice.forgotten", false, { count: result.removed, skipped: result.skipped });
      setSelected(new Set()); retry();
    } catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  function pickerHref(id?: number) {
    const params = new URLSearchParams(location.search);
    if (id) params.set("lyrics_track", String(id)); else params.delete("lyrics_track");
    if (query.trim()) params.set("q", query.trim()); else params.delete("q");
    return `/library?${params}`;
  }
  function clearDetails() {
    if (editorBusy) return;
    reviewRequest.current++; stepRequest.current++;
    if (standalone) { history.pushState(null, "", pickerHref()); window.dispatchEvent(new Event("route-changed")); return; }
    setOffset(safeOffset); setDetailID(undefined); setLyricsID(undefined); setAuditionID(undefined); setCoverID(undefined); setTool(undefined);
  }
  function openDetails(id: number) {
    if (editorBusy) return;
    reviewRequest.current++; stepRequest.current++;
    setCoverID(undefined); setTool(undefined); setLyricsID(undefined); setAuditionID(undefined); setDetailID(current => current === id ? undefined : id);
  }
  function openAudition(id: number, editLyrics = false) {
    if (editorBusy) return;
    reviewRequest.current++; stepRequest.current++;
    setCoverID(undefined); setTool(undefined); setDetailID(undefined); setLyricsID(undefined);
    setAuditionID(current => current === id && auditionEditing === editLyrics ? undefined : id);
    setAuditionEditing(editLyrics);
  }
  function openTool(kind: LibraryTool, trigger: string, sources?: Source[]) {
    if (editorBusy) return;
    clearDetails(); setTool({ kind, trigger, sources });
  }
  function openArtwork(id: number, trigger: string) {
    if (editorBusy) return;
    clearDetails(); setCoverID(id); setCoverTrigger(trigger);
  }
  function reviewTracks(ids: number[]) {
    if (editorBusy) return;
    clearDetails(); setSelected(new Set(ids)); navigate({ ...scope, view: "tracks" }); setFilter("all"); setLyricsFilter("all"); setScriptFilter("all"); setQuery(""); setOffset(0);
  }
  function changePage(next: number) { if (!editorBusy) { clearDetails(); setOffset(next); } }
  function adjacent(id: number, direction: number, lyrics = false) {
    if (pending) return undefined;
    if (!lyrics) return direction < 0 ? data?.previous : data?.next;
    const index = lyricsOrder.indexOf(id);
    return index < 0 ? undefined : lyricsOrder[index + direction];
  }
  function openLyrics(id: number) {
    if (editorBusy) return;
    setCoverID(undefined); setTool(undefined); setDetailID(undefined); setAuditionID(undefined);
    stepRequest.current++; reviewRequest.current++;
    setLyricsOrder([]); if (lyricsID !== id) void loadLyricsOrder();
    setLyricsID(current => current === id ? undefined : id);
  }
  async function step(id: number | undefined, lyrics = false) {
    if (!id || editorBusy) return;
    const version = ++stepRequest.current;
    try {
      let next: Source | undefined;
      const direction = lyricsOrder.indexOf(id) < lyricsOrder.indexOf(lyricsID ?? 0) ? -1 : 1;
      while (id) {
        const sources = await api<Source[]>("/library/selection", "POST", { ids: [id] });
        if (stepRequest.current !== version) return;
        next = sources[0];
        if (next && (!lyrics || next.output_present && next.output)) break;
        id = lyrics ? lyricsOrder[lyricsOrder.indexOf(id) + direction] : undefined;
      }
      if (!next || !id) { retry(); return; }
      setExtraSource(next);
      if (standalone) history.replaceState(null, "", pickerHref(next.id));
      if (lyrics) setLyricsID(next.id); else setDetailID(next.id);
    } catch (err) { if (stepRequest.current === version) notify(err as Error, true); }
  }
  if (standalone) {
    const source = lyricsID ? pending ? known.get(lyricsID) : data.active?.id === lyricsID ? data.active : undefined : undefined;
    return <div className="lyrics-page">
      <PageHeading><div><h1>{t("lyrics.choose")}</h1><p>{source && `${source.title} · ${source.artist}${source.duration ? ` · ${t("lyrics.targetDuration", { seconds: source.duration })}` : ""}`}</p></div><Button asChild variant="ghost" size="sm"><a href={editorBusy ? undefined : pickerHref()} aria-disabled={editorBusy || undefined}><ArrowLeft size={16} />{t("lyrics.returnToLibrary")}</a></Button></PageHeading>
      <ResourceWarning error={error} retry={retry} />
      {source?.output_present && source.output ? <LyricsPicker standalone key={`${source.id}:${source.output}`} onBusyChange={setEditorBusy} source={source} notify={notify} close={clearDetails} saved={retry} previous={adjacent(source.id, -1, true) ? () => step(adjacent(source.id, -1, true), true) : undefined} next={adjacent(source.id, 1, true) ? () => step(adjacent(source.id, 1, true), true) : undefined} /> : <Empty title={t("lyrics.trackUnavailable")}><Button asChild variant="outline"><a href={pickerHref()}>{t("lyrics.returnToLibrary")}</a></Button></Empty>}
    </div>;
  }
  return <>
    <ResourceWarning error={error} retry={retry} />
    <PageHeading><div><h1 id="library-title" tabIndex={-1}>{t("app.library")}</h1><p className="library-description">{t("library.description", { count: data.counts.tracks })}</p><h1 className="comparison-page-title">{t("compare.title")}</h1></div>
      <div className="actions"><Button className="comparison-close" variant="outline" size="icon" aria-label={t("compare.collapse")} title={t("compare.collapse")} onClick={() => {
        const panel = document.getElementById("library-comparison") as HTMLDetailsElement | null;
        if (panel) { panel.open = false; panel.querySelector("summary")?.focus(); }
      }} onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.currentTarget.click(); } }}><ArrowLeft size={18} /></Button><Button disabled={busy} onClick={() => void action("/library/scan", {}, "notice.scan")}><RefreshCw size={18} className={busy ? "animate-spin" : ""} />{t("library.scan")}</Button></div>
    </PageHeading>
    {expired > 0 && <div className="banner amber"><Clock3 size={20} /><div><strong>{t("library.expiredCount", { count: expired })}</strong><p>{t("library.expiredHint")}</p></div><Button variant="destructive" size="sm" disabled={busy} onClick={() => deleteSelected(true)}><Trash2 size={14} />{t("library.deleteExpired")}</Button></div>}
    {removable > 0 && <div className="library-cleanup"><Trash2 size={17} /><div><strong>{t("library.removableCount", { count: removable })}</strong><small>{t("library.forgetHint")}</small></div><Button variant="outline" size="sm" disabled={busy} onClick={() => void forget([], true)}>{t("library.forgetAll")}</Button></div>}
    {(scope.artist || isAlbum) && <nav className="library-breadcrumbs" aria-label={t("library.hierarchy")}>
      <button onClick={() => navigate({ view: scope.view })} aria-current={!scope.artist ? "page" : undefined}>{t(scope.view === "artists" ? "library.artists" : "library.albums")}</button>
      {scope.artist && <><ChevronRight size={14} /><button onClick={() => navigate({ view: scope.view, artist: scope.artist })} aria-current={!isAlbum ? "page" : undefined}>{artist?.artist || scope.artist}</button></>}
      {isAlbum && <><ChevronRight size={14} /><span aria-current="page">{album?.album || scope.album}</span></>}
    </nav>}
    {isAlbum && <section className="album-summary">
      <button id="album-artwork-trigger" className="album-cover-edit" disabled={editorBusy || !data.context_source} onClick={() => data.context_source && openArtwork(data.context_source.id, "album-artwork-trigger")} aria-label={t("coverEdit.title")}><Cover tracks={data.scope.covers} name={album?.album || t("library.unknownAlbum")} transition /><span>{t("coverEdit.title")}</span></button>
      <div><Button variant="ghost" size="sm" onClick={() => navigate({ view: scope.view, artist: scope.artist })}><ArrowLeft size={14} />{t("library.backToAlbums")}</Button>
        <h2>{album?.album || t("library.unknownAlbum")}</h2><p>{album?.artist || t("library.unknownArtist")}</p>
        <div className="album-facts"><span>{t("library.trackCount", { count: data.scope.count })}</span>{discs.length > 1 && <span>{t("library.discCount", { count: discs.length })}</span>}{albumDuration > 0 && <span><Clock3 size={14} />{duration(albumDuration)}</span>}
          {data.scope.formats.map(format => <span key={format}>{format}</span>)}
        </div>
      </div>
    </section>}
    <section className={`library-surface ${isAlbum ? "album-library" : ""}`} aria-busy={pending}>
      <div className="library-toolbar-box">
      <div className="library-navigation">{!scope.artist && <div className="segmented" role="group" aria-label={t("library.browse")}>
        <button aria-pressed={scope.view === "albums"} onClick={() => navigate({ view: "albums" })}>{t("library.albums")}<span className="count">{number(totals.albums)}</span></button>
        <button aria-pressed={scope.view === "tracks"} onClick={() => navigate({ view: "tracks" })}>{t("library.allTracks")}<span className="count">{number(data.counts.tracks)}</span></button>
        <button aria-pressed={scope.view === "artists"} onClick={() => navigate({ view: "artists" })}>{t("library.artists")}<span className="count">{number(totals.artists)}</span></button>
      </div>}

      </div>
      <SavedLibraryViews disabled={editorBusy || busy} search={new URLSearchParams({ view: scope.view, ...(scope.artist !== undefined ? { artist: scope.artist } : {}), ...(scope.album !== undefined ? { album: scope.album } : {}), q: query, status: filter, lyrics: lyricsFilter, lyrics_script: scriptFilter, sort }).toString()} apply={search => {
        if (editorBusy || busy) return;
        setSelected(new Set()); setCoverID(undefined);
        history.pushState(null, "", `/library?${search}`);
        window.dispatchEvent(new Event("route-changed"));
      }} />
      <div className="table-toolbar library-toolbar">
        <div className="library-display-actions">

        <details className="library-options" data-filtered={filter !== "all" || lyricsFilter !== "all" || scriptFilter !== "all"} open={optionsOpen} onChange={event => { if (event.target instanceof HTMLSelectElement) event.currentTarget.querySelector("summary")?.focus(); }} onKeyDown={event => { if (event.key === "Escape" && event.currentTarget.open) { event.preventDefault(); event.stopPropagation(); setOptionsOpen(false); event.currentTarget.querySelector("summary")?.focus(); } }}>
          <summary aria-label={t("library.options")} title={[t("library.options"), filter !== "all" ? statusLabel(filter) : "", lyricsFilter !== "all" ? t(`lyrics.state.${lyricsFilter}`) : "", scriptFilter !== "all" ? t(`lyrics.script.${scriptFilter}`) : ""].filter(Boolean).join(" · ")} onClick={event => { event.preventDefault(); setOptionsOpen(open => !open); }}><SlidersHorizontal size={17} /></summary>
          <div className="library-options-body">
        {!showingTracks && layout === "grid" && <div className="density-control"><span>{t("library.coverSize")}</span><div className="segmented" role="group" aria-label={t("library.coverSize")}>
          {(["compact", "standard", "large"] as const).map(value => <button key={value} aria-pressed={density === value} onClick={() => { setDensity(value); savePreference("density", value); }}>{t(value === "compact" ? "library.coverCompact" : value === "standard" ? "library.coverStandard" : "library.coverLarge")}</button>)}
        </div></div>}
        {!showingTracks && <div className="segmented layout-control" role="group" aria-label={t("library.layout")}>
          <button aria-label={t("library.grid")} title={t("library.grid")} aria-pressed={layout === "grid"} onClick={event => { setLayout("grid"); savePreference("layout", "grid"); setOptionsOpen(false); event.currentTarget.closest("details")?.querySelector("summary")?.focus(); }}><Grid2X2 size={17} /></button>
          <button aria-label={t("library.list")} title={t("library.list")} aria-pressed={layout === "list"} onClick={event => { setLayout("list"); savePreference("layout", "list"); setOptionsOpen(false); event.currentTarget.closest("details")?.querySelector("summary")?.focus(); }}><List size={17} /></button>
        </div>}
            <select disabled={editorBusy} aria-label={t("library.filter")} value={filter} onChange={event => { clearDetails(); setFilter(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="all">{t("library.allStatuses")}</option>{priority.slice().reverse().map(state => <option value={state} key={state}>{statusLabel(state)}</option>)}</select>
            <select disabled={editorBusy} aria-label={t("lyrics.filter")} value={lyricsFilter} onChange={event => { clearDetails(); setLyricsFilter(event.target.value as LyricsMatchState | "all"); setOffset(0); setOptionsOpen(false); }}><option value="all">{t("lyrics.allStates")}</option>{lyricsMatchStates.map(state => <option value={state} key={state}>{t(`lyrics.state.${state}`)}</option>)}</select>
            <select disabled={editorBusy} aria-label={t("lyrics.scriptFilter")} value={scriptFilter} onChange={event => { clearDetails(); setScriptFilter(event.target.value as ScriptFilter); setOffset(0); setOptionsOpen(false); }}>{scriptFilters.map(script => <option value={script} key={script}>{t(`lyrics.script.${script}`)}</option>)}</select>
            {!isAlbum && <select disabled={editorBusy} aria-label={t("library.sort")} value={sort} onChange={event => { clearDetails(); setSort(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="name">{t("library.sortName")}</option><option value="attention">{t("library.sortAttention")}</option></select>}

          </div>
        </details>
        </div>
      </div>
        <div className="search"><Search size={17} aria-hidden="true" /><input type="search" disabled={editorBusy} aria-label={t("library.search")} placeholder={t("library.searchPlaceholder")} value={query} onChange={event => { clearDetails(); setQuery(event.target.value); setOffset(0); }} />{query && <button disabled={editorBusy} aria-label={t("library.clearSearch")} title={t("library.clearSearch")} onClick={() => { clearDetails(); setQuery(""); setOffset(0); }}><X size={16} /></button>}</div>
      {(filter !== "all" || lyricsFilter !== "all" || scriptFilter !== "all") && <div className="active-filters" role="group" aria-label={t("library.activeFilters")}>
        {filter !== "all" && <button disabled={editorBusy} aria-label={t("library.removeFilter", { name: statusLabel(filter) })} onClick={() => { clearDetails(); setFilter("all"); setOffset(0); }}><span>{t("library.filter")} · {statusLabel(filter)}</span><X size={12} /></button>}
        {lyricsFilter !== "all" && <button disabled={editorBusy} aria-label={t("library.removeFilter", { name: t(`lyrics.state.${lyricsFilter}`) })} onClick={() => { clearDetails(); setLyricsFilter("all"); setOffset(0); }}><span>{t("lyrics.filter")} · {t(`lyrics.state.${lyricsFilter}`)}</span><X size={12} /></button>}
        {scriptFilter !== "all" && <button disabled={editorBusy} aria-label={t("library.removeFilter", { name: t(`lyrics.script.${scriptFilter}`) })} onClick={() => { clearDetails(); setScriptFilter("all"); setOffset(0); }}><span>{t("lyrics.scriptFilter")} · {t(`lyrics.script.${scriptFilter}`)}</span><X size={12} /></button>}
        <button disabled={editorBusy} className="clear-filters" onClick={() => { clearDetails(); setFilter("all"); setLyricsFilter("all"); setScriptFilter("all"); setOffset(0); }}>{t("library.clearFilters")}</button>
      </div>}

      </div>
    <div className="library-tool-actions" role="group" aria-label={t("library.tools")}>
      <Button id="playlist-tool-trigger" size="sm" variant="ghost" disabled={editorBusy} onClick={() => openTool("playlist", "playlist-tool-trigger")}><ListMusic size={15} />{t("playlist.title")}</Button>
      <Button id="fingerprints-tool-trigger" size="sm" variant="ghost" disabled={editorBusy} onClick={() => openTool("fingerprints", "fingerprints-tool-trigger")}><Fingerprint size={15} />{t("fingerprint.open")}</Button>
      <Button size="sm" variant="ghost" disabled={busy || editorBusy} onClick={() => void action("/library/scan", { verify: true }, "notice.verify")}><ShieldCheck size={15} />{t("library.verify")}</Button>
      <BuildPreview selection={{ all: true }} notify={notify} />
    </div>
    <div className="library-maintenance">
      <MaintenanceWorkbench comparisonAvailable={!scope.artist && !isAlbum} scopeQuery={scopeParams.toString()} attention={data.attention} selected={chosen} notify={notify} select={reviewTracks} />
      {!scope.artist && !isAlbum && <LibraryComparison counts={data.counts} notify={notify} />}
      <LyricsMaintenance inventory={data.lyrics} scopeQuery={scopeParams.toString()} scope={isAlbum ? "album" : scope.artist ? "artist" : undefined} notify={notify} />
    </div>
      {coverID && known.has(coverID) && <ArtworkEditor key={coverID} source={known.get(coverID)!} saved={retry} close={() => setCoverID(undefined)} returnFocusID={coverTrigger} onBusyChange={setEditorBusy} />}
      <div className="library-selection-toolbar">
      {count > 0 && <div className="library-selection"><label className="check-field"><input type="checkbox" aria-label={t("library.selectPage")} checked={pageIDs.length > 0 && pageIDs.every(id => selected.has(id))} ref={node => { if (node) node.indeterminate = pageIDs.some(id => selected.has(id)) && !pageIDs.every(id => selected.has(id)); }} disabled={busy} onChange={event => toggle(pageIDs, event.target.checked)} /><span>{t("library.selectPage")}</span></label>{data.matched > pageIDs.length && <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void selectMatching()}>{t("library.selectMatching", { count: data.matched })}</Button>}<span>{t(showingTracks ? "library.trackCount" : showingArtists ? "library.artistCount" : "library.albumCount", { count })}</span></div>}
      {selected.size > 0 && <div className="selection-bar" aria-busy={selectionPending}><span>{t("library.selected", { count: selected.size })}</span><Button id="tags-selected-trigger" size="sm" variant="outline" disabled={busy || editorBusy || !chosen.some(source => source.output_present)} onClick={() => openTool("tags", "tags-selected-trigger")}><Tags size={14} />{t("tags.open")}</Button><Button id="playlist-selected-trigger" size="sm" variant="outline" disabled={busy || editorBusy} onClick={() => openTool("playlist", "playlist-selected-trigger")}><ListMusic size={14} />{t("playlist.create")}</Button><BuildPreview selection={{ ids: [...selected] }} notify={notify} /><Button size="sm" variant="outline" disabled={busy} onClick={() => void action("/library/rebuild", { ids: [...selected] }, "notice.selectedBuild")}><RefreshCw size={14} />{t("library.buildSelected")}</Button><LyricsSearchButton ids={chosen.filter(source => source.output && source.output_present).map(source => source.id)} label="lyrics.searchSelected" notify={notify} disabled={busy} /><LyricsSearchButton ids={chosen.filter(source => source.output && source.output_present).map(source => source.id)} simplifyTraditional label="lyrics.simplifySelected" notify={notify} disabled={busy} />{chosen.some(source => source.lyrics_script === "traditional" && source.output_present) && <LyricsSearchButton ids={chosen.filter(source => source.lyrics_script === "traditional" && source.output_present).map(source => source.id)} replaceTraditional label="lyrics.replaceSelected" notify={notify} disabled={busy} />}<BatchRepairButton sources={chosen} notify={notify} disabled={busy} refresh={() => { setSelected(new Set()); retry(); }} /><Button size="sm" variant="destructive" disabled={busy || selectedExpired === 0} onClick={() => deleteSelected(false)}><Trash2 size={14} />{t("library.deleteSelected")}</Button>{selectedRemovable.length > 0 && <Button size="sm" variant="outline" disabled={busy} onClick={() => void forget(selectedRemovable.map(source => source.id))}><Trash2 size={14} />{t("library.forgetSelected", { count: selectedRemovable.length })}</Button>}<Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}><X size={14} />{t("library.clearSelection")}</Button></div>}
      </div>
      {tool && <LibraryTools key={tool.kind} tool={tool.kind} scopeQuery={scopeParams.toString()} selected={tool.sources ?? chosen} select={reviewTracks} notify={notify} close={() => setTool(undefined)} returnFocusID={tool.trigger} onBusyChange={setEditorBusy} />}
      {count > 0 ? <>
        {!showingTracks ? <div className={`library-groups library-${layout}`} data-density={density}>{layout === "list" && <div className="library-list-header" aria-hidden="true"><span>#</span><span>{t("library.coverColumn")}</span><span>{t(showingArtists ? "library.artists" : "library.albumArtistColumn")}</span><span>{t("library.tracksDurationColumn")}</span><span>{t("library.sourceAudio")}</span><span>{t("library.outputColumn")}</span><span>{t("lyrics.title")}</span><span>{t("library.statusColumn")}</span></div>}{visibleGroups.map(group => {
          const ready = group.ready;
          const groupIDs = group.ids || [];
          const state = priority[group.rank] || "ready";
          const open = (origin: HTMLElement) => showingArtists ? navigate({ view: scope.view, artist: group.artistKey }) : navigate({ view: scope.view, artist: group.artistKey, album: group.albumKey }, origin);
          return <article className="library-group" key={group.key} data-selected={groupIDs.some(id => selected.has(id))}>
            <button className="library-cover-open" aria-label={t(showingArtists ? "library.openArtist" : "library.openAlbum", { name: group.name })} title={`${group.name} · ${statusLabel(state)}`} onClick={event => open(event.currentTarget)}><Cover tracks={group.covers} name={group.name} artist={showingArtists} /></button>
            <div className="library-group-body"><input type="checkbox" aria-label={t(showingArtists ? "library.selectArtist" : "library.selectAlbum", { name: group.name })} checked={groupIDs.every(id => selected.has(id))} ref={node => { if (node) node.indeterminate = groupIDs.some(id => selected.has(id)) && !groupIDs.every(id => selected.has(id)); }} disabled={busy} onChange={event => toggle(groupIDs, event.target.checked)} />
              <div className="library-group-title"><button className="library-open" title={group.name} onClick={event => open(event.currentTarget)}><strong>{group.name}</strong></button>
                <small>{showingArtists ? t("library.albumCount", { count: group.albums }) : group.artist}</small>
                <small className="group-counts">{t("library.groupCounts", { ready, total: group.count })}</small>
                {state !== "possibly_damaged" && group.damaged && <small className="repair-warning">{t("repair.damaged")}</small>}
              </div>
              {layout === "list" && <GroupFacts group={group} />}
              <Badge status={state} compact={layout === "grid"} />
            </div>
          </article>;
        })}</div> : <div className={`table-scroll ${isAlbum ? "album-tracks" : ""}`}>{isAlbum && <div className="album-tracks-heading"><h3>{t("library.albumTracks")}</h3><span>{t("library.trackCount", { count: data.matched })}</span></div>}<table className={`track-table ${isAlbum ? "album-track-table" : ""}`}>{isAlbum && <colgroup><col className="album-select-column" /><col /><col className="album-status-column" /><col className="album-lyrics-column" /></colgroup>}<thead><tr><th aria-label={t("library.selectedTracks")} /><th className="track-title-column">{t("library.trackFile")}</th>{!isAlbum && <><th className="track-duration-column">{t("library.duration")}</th><th className="track-audio-column">{t("library.audioColumn")}</th><th className="track-size-column">{t("library.sizesColumn")}</th></>}<th className="track-status-column">{t("library.artifact")}</th><th className="track-lyrics-column">{t("lyrics.title")}</th></tr></thead><tbody>{shown.map((source, index) => {
          const disc = source.disc || 1;
          const heading = isAlbum && discs.length > 1 && (index === 0 || disc !== (shown[index - 1].disc || 1));
          return <Fragment key={source.id}>{heading && <tr className="disc-heading"><th colSpan={trackColumns} scope="rowgroup"><Disc3 size={15} />{t("library.disc", { number: disc })}</th></tr>}<tr className={`track-row ${selected.has(source.id) ? "selected" : ""}`} data-expanded={detailID === source.id || lyricsID === source.id || auditionID === source.id}>
            <td><input type="checkbox" aria-label={t("library.selectTrack", { title: source.title || source.path })} checked={selected.has(source.id)} disabled={busy} onChange={event => toggle([source.id], event.target.checked)} /></td>
            <td className="track-title"><div className="track-cell"><div><div className="track-title-line"><button id={`track-title-${source.id}`} className="track-open" title={source.title || source.path.split("/").pop() || source.path} aria-expanded={detailID === source.id} aria-controls={detailID === source.id ? `track-details-${source.id}` : undefined} disabled={editorBusy} aria-label={t("library.openDetails", { title: source.title || source.path })} onClick={() => openDetails(source.id)}><strong dir="auto">{source.track ? <span className="track-index">{String(source.track).padStart(2, "0")}. </span> : null}<span className="track-name">{source.title || source.path.split("/").pop()}</span></strong><ChevronRight className="track-expand-icon" size={14} aria-hidden="true" /></button>{source.output_present && source.output && <Button id={`audition-trigger-${source.id}`} className="track-audition-trigger" size="icon" variant="ghost" aria-label={t("audition.open", { title: source.title || source.output.split("/").pop() || "" })} title={t("audition.title")} aria-expanded={auditionID === source.id && !auditionEditing} aria-controls={auditionID === source.id && !auditionEditing ? `audition-${source.id}` : undefined} disabled={editorBusy} onClick={() => openAudition(source.id)}><Play size={18} /></Button>}{isAlbum && <span className="track-duration">{duration(source.duration || 0)}</span>}</div>{isAlbum ? <>{source.artist && source.artist !== album?.artist && <small className="track-byline" dir="auto" title={source.artist}>{source.artist}</small>}<TrackFacts source={source} compact /></> : <small className="track-byline" dir="auto" title={`${source.artist || t("library.unknownArtist")} · ${source.album || t("library.unknownAlbum")}`}>{source.artist || t("library.unknownArtist")} · {source.album || t("library.unknownAlbum")}</small>}{!source.present && <small className="source-missing">{t("library.sourceDeleted")}</small>}</div></div></td>
            {!isAlbum && <td className="track-facts-cell" colSpan={3}><TrackFacts source={source} /></td>}
            <td className="track-output"><Badge status={source.status} />{source.output_present && source.status !== "ready" && source.status !== "possibly_damaged" && <small>{t("library.stillPlayable")}</small>}<RepairControls source={source} notify={notify} refresh={retry} compact />{!source.present && !source.output && !source.output_present && <Button className="forget-record" size="icon" variant="ghost" aria-label={t("library.forgetRecord")} title={t("library.forgetRecord")} disabled={busy} onClick={() => void forget([source.id])}><Trash2 size={15} /></Button>}{source.error && <Button variant="ghost" size="icon" aria-label={t("library.errorDetails")} title={t("library.errorDetails")} disabled={editorBusy} onClick={() => openDetails(source.id)}><Info size={15} /></Button>}</td>
            <td className="track-lyrics"><LyricsTrack source={source} notify={notify} onChoose={() => openLyrics(source.id)} onEdit={() => openAudition(source.id, true)} expanded={lyricsID === source.id} editing={auditionID === source.id && auditionEditing} disabled={editorBusy} /></td>
          </tr>{(detailID === source.id || lyricsID === source.id || auditionID === source.id) && <tr className="track-expanded-row"><td colSpan={trackColumns}>
            {detailID === source.id && <InlineDetails className="track-details" id={`track-details-${source.id}`} returnFocusID={`track-title-${source.id}`} title={t("library.fileDetails")} subtitle={source.title || source.path} close={clearDetails} actions={<><Button variant="ghost" size="icon" aria-label={t("library.previousTrack")} title={t("library.previousTrack")} disabled={!adjacent(source.id, -1)} onClick={() => step(adjacent(source.id, -1))}><ChevronLeft size={17} /></Button><Button variant="ghost" size="icon" aria-label={t("library.nextTrack")} title={t("library.nextTrack")} disabled={!adjacent(source.id, 1)} onClick={() => step(adjacent(source.id, 1))}><ChevronRight size={17} /></Button></>}><div className="actions track-detail-actions"><Button size="sm" variant="outline" disabled={editorBusy || !source.output_present} onClick={() => openTool("tags", `track-title-${source.id}`, [source])}><Tags size={14} />{t("tags.open")}</Button><Button size="sm" variant="outline" disabled={editorBusy} onClick={() => openArtwork(source.id, `track-title-${source.id}`)}>{t("coverEdit.title")}</Button></div><TrackDetails source={source} notify={notify} /></InlineDetails>}
            {lyricsID === source.id && <LyricsPicker onBusyChange={setEditorBusy} key={`${source.id}:${source.output}`} source={source} notify={notify} close={clearDetails} saved={retry} previous={adjacent(source.id, -1, true) ? () => step(adjacent(source.id, -1, true), true) : undefined} next={adjacent(source.id, 1, true) ? () => step(adjacent(source.id, 1, true), true) : undefined} />}
            {auditionID === source.id && source.output_present && source.output && <ArtifactAudition key={`${source.id}:${source.output}:${auditionEditing}`} source={source} close={clearDetails} editLyrics={auditionEditing} onBusyChange={setEditorBusy} />}
          </td></tr>}</Fragment>;
        })}</tbody></table></div>}
        <div className="pagination"><span>{t(showingTracks ? "library.pagination" : "library.groupPagination", { start: safeOffset + 1, end: Math.min(safeOffset + 50, count), total: count })}</span>
          <Button variant="ghost" size="icon" aria-label={t("library.previous")} title={t("library.previous")} disabled={editorBusy || pending || safeOffset === 0} onClick={() => changePage(Math.max(0, safeOffset - 50))}><ChevronLeft size={16} /></Button>
          <Button variant="ghost" size="icon" aria-label={t("library.next")} title={t("library.next")} disabled={editorBusy || pending || safeOffset + 50 >= count} onClick={() => changePage(safeOffset + 50)}><ChevronRight size={16} /></Button>
        </div>
      </> : <Empty title={data.counts.tracks ? t("library.noMatch") : t("library.empty")}>{data.counts.tracks ? <Button variant="outline" onClick={() => { clearDetails(); setFilter("all"); setLyricsFilter("all"); setScriptFilter("all"); setQuery(""); setOffset(0); }}>{t("library.clearFilters")}</Button> : <Button asChild variant="outline"><a href="/settings?section=library">{t("overview.openSettings")}</a></Button>}</Empty>}
    </section>
  </>;
}
