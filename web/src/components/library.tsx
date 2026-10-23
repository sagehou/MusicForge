import { useDeferredValue, useEffect, useMemo, useRef, useState, Fragment, memo } from "react";
import { flushSync } from "react-dom";
import { ArrowLeft, ChevronLeft, ChevronRight, Clock3, Copy, Disc3, Ellipsis, Grid2X2, List, Music2, RefreshCw, Search, ShieldCheck, SlidersHorizontal, Trash2, X } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { Source } from "../types";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";
import { BatchRepairButton, RepairControls, RepairDiagnostics } from "./repair";
import { LibraryComparison } from "./library-comparison";
import { DetailPanel } from "./detail-panel";
import { PageHeading } from "./page-heading";
import { LyricsMaintenance, LyricsPicker, LyricsSearchButton, LyricsTrack, lyricsMatchState, lyricsMatchStates, type LyricsMatchState } from "./lyrics";

type Scope = { view: "artists" | "albums" | "tracks"; artist?: string; album?: string };
type Identity = { artistKey: string; albumKey: string; artist: string; album: string };
type Group = Identity & { key: string; name: string; tracks: Source[]; albums: Set<string>; rank: number };
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
function TrackFacts({ source }: { source: Source }) {
  const { t, locale, number } = useI18n();
  return <dl className="track-facts">
    <div className="track-fact-duration"><dt>{t("library.duration")}</dt><dd>{duration(source.duration || 0)}</dd></div>
    <div className="track-fact-audio"><dt>{t("library.sourceAudio")}</dt><dd><AudioSpec audio={source.source_audio} /></dd><dd className="track-bitrate" title={!source.source_bitrate ? t("library.bitratePending") : undefined}><span>{t("library.sourceBitrate")} </span>{source.source_bitrate ? `${number(Math.round(source.source_bitrate / 1000))} kbps` : "—"}</dd></div>
    <div className="track-fact-sizes"><dt>{t("library.sourceSize")}</dt><dd>{fileSize(source.size, locale)}</dd>{source.output_present && source.output_size !== undefined && <><dt>{t("library.outputSize")}</dt><dd className="track-output-size">{fileSize(source.output_size, locale)}</dd></>}</div>
  </dl>;
}
function GroupFacts({ tracks }: { tracks: Source[] }) {
  const { t, locale, number } = useI18n();
  const audio = [...new Map(tracks.map(source => [JSON.stringify([source.source_audio?.codec, source.source_audio?.bit_depth, source.source_audio?.sample_rate, source.source_audio?.channels]), source.source_audio])).values()];
  const playable = tracks.filter(source => source.output_present && source.output);
  const formats = [...new Set(playable.map(source => source.output.split(".").pop()?.toUpperCase()))];
  return <dl className="group-facts">
    <div><dt>{t("library.duration")}</dt><dd>{t("library.trackCount", { count: tracks.length })}<small>{tracks.every(source => source.duration) ? duration(tracks.reduce((sum, source) => sum + (source.duration || 0), 0)) : "—"}</small></dd></div>
    <div><dt>{t("library.sourceAudio")}</dt><dd>{audio.slice(0, 3).map((value, index) => <span className="group-audio-spec" key={index}><AudioSpec audio={value} /></span>)}{audio.length > 3 && <small>+{number(audio.length - 3)}</small>}</dd></div>
    <div><dt>{t("library.outputColumn")}</dt><dd>{formats.join(" / ") || "—"}<small>{playable.length && playable.every(source => source.output_size !== undefined) ? fileSize(playable.reduce((sum, source) => sum + source.output_size!, 0), locale) : "—"}</small></dd></div>
    <div><dt>{t("lyrics.title")}</dt><dd>{playable.length ? `${number(playable.filter(source => source.lyrics?.state === "ready").length)} / ${number(playable.length)}` : "—"}</dd></div>
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
    <dt>{t("library.sourceSHA256")}</dt><dd className="hash-value"><code>{source.hash || t("library.hashPending")}</code>{source.hash && <Button type="button" variant="ghost" size="icon" aria-label={t("library.copyHash")} onClick={() => void copyHash()}><Copy size={15} /></Button>}</dd>
    <dt>{t("library.source")}</dt><dd><code>{source.path}</code></dd>
    {source.output && <><dt>{t("library.artifact")}</dt><dd><code>{source.output}</code></dd></>}
  </dl>{source.error && <div className="error-box" role="alert"><strong>{t("library.errorDetails")}</strong><pre>{source.error}</pre></div>}{Object.keys(source.metadata).length > 0 && <details><summary>{t("library.metadata")}</summary><dl>{Object.entries(source.metadata).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => <Fragment key={key}><dt>{key}</dt><dd dir="auto">{value}</dd></Fragment>)}</dl></details>}<RepairDiagnostics source={source} /></section>;
}
function CoverImage({ url, failed }: { url: string; failed: () => void }) {
  const [ready, setReady] = useState(false);
  return <img src={url} alt="" loading="lazy" decoding="async" className={ready ? "cover-loaded" : ""} onLoad={() => setReady(true)} onError={failed} />;
}
const Cover = memo(function Cover({ tracks, name, artist = false, transition = false }: { tracks: Source[]; name: string; artist?: boolean; transition?: boolean }) {
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
  const { data, error, retry } = useResource<Source[]>("/library", true);
  const [scope, setScope] = useState<Scope>(readScope);
  const [query, setQuery] = useState("");
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
  const trackColumns = 7;
  const [detailID, setDetailID] = useState<number>();
  const [lyricsID, setLyricsID] = useState<number>();
  const lyricsOrder = useRef<number[]>([]);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [sort, setSort] = useState(() => new URLSearchParams(location.search).get("sort") === "attention" ? "attention" : "name");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState(false);
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const back = () => { navigation.current++; transition.current?.skipTransition(); setScope(readScope()); const params = new URLSearchParams(location.search); setFilter(params.get("status") || "all"); setLyricsFilter((params.get("lyrics") || "all") as LyricsMatchState | "all"); setScriptFilter(readScriptFilter()); setSort(params.get("sort") === "attention" ? "attention" : "name"); setOffset(0); setDetailID(undefined); setLyricsID(undefined); setOptionsOpen(false); };
    window.addEventListener("popstate", back);
    window.addEventListener("route-changed", back);
    return () => { window.removeEventListener("popstate", back); window.removeEventListener("route-changed", back); };
  }, []);
  useEffect(() => () => transition.current?.skipTransition(), []);
  useEffect(() => {
    const frame = requestAnimationFrame(() => document.getElementById("library-title")?.focus());
    return () => cancelAnimationFrame(frame);
  }, [scope.view, scope.artist, scope.album]);

  const indexed = useMemo(() => (data || []).map(source => {
    const parts = source.path.split("/");
    const artist = source.metadata.albumartist || source.metadata.album_artist || source.artist || (parts.length > 2 ? parts[0] : t("library.unknownArtist"));
    const identity: Identity = {
      artistKey: parts.length > 2 ? parts[0] : `tag:${artist}`,
      albumKey: parts.length > 2 ? parts.slice(0, 2).join("/") : parts.slice(0, -1).join("/"),
      artist, album: source.album || parts[parts.length - 2] || t("library.unknownAlbum"),
    };
    return { source, identity };
  }), [data, locale]);
  const isAlbum = scope.album !== undefined;
  const showingTracks = isAlbum || scope.view === "tracks";
  const showingArtists = !scope.artist && scope.view === "artists" && !isAlbum;
  const artist = indexed.find(item => item.identity.artistKey === scope.artist)?.identity;
  const albumTracks = useMemo(() => indexed.filter(item => item.identity.artistKey === scope.artist && item.identity.albumKey === scope.album).map(item => item.source), [indexed, scope.artist, scope.album]);
  const album = indexed.find(item => item.identity.artistKey === scope.artist && item.identity.albumKey === scope.album)?.identity;
  const collator = useMemo(() => new Intl.Collator(locale, { numeric: true, sensitivity: "base" }), [locale]);
  const { rows, groups } = useMemo(() => {
    const rows: Source[] = [];
    const map = new Map<string, Group>();
    for (const item of indexed) {
      const { source, identity } = item;
      if (scope.artist && identity.artistKey !== scope.artist || isAlbum && identity.albumKey !== scope.album) continue;
      if (filter !== "all" && (filter === "possibly_damaged" ? !source.possibly_damaged : source.status !== filter)) continue;
      if (lyricsFilter !== "all" && lyricsMatchState(source) !== lyricsFilter) continue;
      if (scriptFilter !== "all" && (!source.output || !source.output_present || (source.lyrics_script || "unidentified") !== scriptFilter)) continue;
      if (search && !`${identity.artist} ${source.artist} ${identity.album} ${source.title} ${source.path}`.toLocaleLowerCase(locale).includes(search)) continue;
      rows.push(source);
      if (showingTracks) continue;
      const key = showingArtists ? identity.artistKey : `${identity.artistKey}\0${identity.albumKey}`;
      let group = map.get(key);
      if (!group) {
        group = { ...identity, key, name: showingArtists ? identity.artist : identity.album, tracks: [], albums: new Set(), rank: priority.length };
        map.set(key, group);
      }
      group.tracks.push(source);
      group.albums.add(identity.albumKey);
      const rank = priority.indexOf(source.status);
      if (rank >= 0) group.rank = Math.min(group.rank, rank);
    }
    const groups = [...map.values()].sort((a, b) => {
      if (sort === "attention") {
        const difference = a.rank - b.rank;
        if (difference) return difference;
      }
      return collator.compare(a.name, b.name) || collator.compare(a.artist, b.artist);
    });
    if (isAlbum) rows.sort((a, b) => (a.disc || 1) - (b.disc || 1) || a.track - b.track || collator.compare(a.path, b.path));
    else if (showingTracks) rows.sort((a, b) => {
      if (sort === "attention") {
        const rank = (source: Source) => { const index = priority.indexOf(source.status); return index < 0 ? priority.length : index; };
        const difference = rank(a) - rank(b);
        if (difference) return difference;
      }
      return collator.compare(a.title || a.path, b.title || b.path) || collator.compare(a.path, b.path);
    });
    return { rows, groups };
  }, [indexed, scope.artist, scope.album, isAlbum, showingTracks, showingArtists, filter, lyricsFilter, scriptFilter, search, sort, collator, locale]);
  const totals = useMemo(() => ({
    artists: new Set(indexed.map(item => item.identity.artistKey)).size,
    albums: new Set(indexed.map(item => `${item.identity.artistKey}\0${item.identity.albumKey}`)).size,
  }), [indexed]);

  async function action(path: string, body: unknown, message: MessageKey) {
    setBusy(true);
    try { await api(path, "POST", body); notify(message); setSelected(new Set()); retry(); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  function navigate(next: Scope, origin?: HTMLElement) {
    const params = new URLSearchParams({ view: next.view });
    if (next.artist !== undefined) params.set("artist", next.artist);
    if (next.album !== undefined) params.set("album", next.album);
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
    const query = params.toString();
    history.replaceState(null, "", `/library${query ? `?${query}` : ""}`);
  }, [filter, lyricsFilter, scriptFilter, sort]);
  function toggle(ids: number[], checked: boolean) {
    setSelected(old => { const next = new Set(old); ids.forEach(id => checked ? next.add(id) : next.delete(id)); return next; });
  }
  if (!data) return error ? <><PageHeading><div><h1>{t("app.library")}</h1></div></PageHeading><Loading error={error} retry={retry} /></> : <div className="library-loading" role="status" aria-label={t("app.loading")}>
    <PageHeading><div><h1>{t("app.library")}</h1><p>{t("library.loadingHint")}</p></div></PageHeading>
    <div className="skeleton-toolbar" aria-hidden="true" />
    <div className="library-groups" aria-hidden="true">{Array.from({ length: 10 }, (_, index) => <div className="skeleton-card" key={index}><div /><span /><span /></div>)}</div>
  </div>;
  const count = showingTracks ? rows.length : groups.length;
  const safeOffset = Math.min(offset, Math.max(0, Math.ceil(count / 50) - 1) * 50);
  const shown = rows.slice(safeOffset, safeOffset + 50);
  const visibleGroups = groups.slice(safeOffset, safeOffset + 50);
  const pageTracks = showingTracks ? shown : visibleGroups.flatMap(group => group.tracks);
  const chosen = data.filter(source => selected.has(source.id));
  const selectedExpired = chosen.filter(source => !source.present && source.output).length;
  const expired = data.filter(source => !source.present && source.output).length;
  const removable = data.filter(source => !source.present && !source.output && !source.output_present);
  const selectedRemovable = chosen.filter(source => !source.present && !source.output && !source.output_present);
  const albumDuration = albumTracks.reduce((total, source) => total + (source.duration || 0), 0);
  const discs = new Set(albumTracks.map(source => source.disc || 1));
  function deleteSelected(all: boolean) {
    if (window.confirm(all ? t("library.deleteAllConfirm", { count: expired }) : t("library.deleteSelectedConfirm"))) {
      void action("/library/delete", { all, ids: [...selected] }, "notice.delete");
    }
  }
  async function forget(ids: number[], all = false) {
    if (!window.confirm(t("library.forgetConfirm", { count: all ? removable.length : ids.length }))) return;
    setBusy(true);
    try {
      const result = await api<{ removed: number; skipped: number }>("/library/forget", "POST", all ? { all: true } : { ids });
      notify("notice.forgotten", false, { count: result.removed, skipped: result.skipped });
      setSelected(new Set()); retry();
    } catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  const detailSource = data.find(source => source.id === detailID);
  const lyricsSource = data.find(source => source.id === lyricsID);
  function adjacent(id: number, direction: number, lyrics = false) {
    if (!lyrics) {
      const index = rows.findIndex(source => source.id === id);
      return index < 0 ? undefined : rows[index + direction];
    }
    const index = lyricsOrder.current.indexOf(id);
    if (index < 0) return;
    for (let i = index + direction; i >= 0 && i < lyricsOrder.current.length; i += direction) {
      const source = data?.find(source => source.id === lyricsOrder.current[i]);
      if (source?.output_present && source.output) return source;
    }
  }
  function openLyrics(id: number) {
    lyricsOrder.current = rows.filter(source => source.output_present && source.output).map(source => source.id);
    setLyricsID(id);
  }
  function step(source: Source | undefined, lyrics = false) {
    if (!source) return;
    if (lyrics) setLyricsID(source.id); else setDetailID(source.id);
    const index = rows.findIndex(row => row.id === source.id);
    if (showingTracks && index >= 0) setOffset(Math.floor(index / 50) * 50);
  }
  return <>
    <ResourceWarning error={error} retry={retry} />
    <PageHeading><div><h1 id="library-title" tabIndex={-1}>{t("app.library")}</h1><p>{t("library.description", { count: data.length })}</p></div>
      <div className="actions"><details className="page-tools"><summary aria-label={t("library.more")} title={t("library.more")}><Ellipsis size={19} /></summary><div className="tools-popover"><Button variant="ghost" disabled={busy} onClick={() => void action("/library/scan", { verify: true }, "notice.verify")}><ShieldCheck size={16} />{t("library.verify")}</Button><Button variant="ghost" disabled={busy} onClick={() => void action("/library/rebuild", { all: true }, "notice.rebuild")}><RefreshCw size={14} />{t("library.startRebuild")}</Button></div></details><Button disabled={busy} onClick={() => void action("/library/scan", {}, "notice.scan")}><RefreshCw size={16} className={busy ? "animate-spin" : ""} />{t("library.scan")}</Button></div>
    </PageHeading>
    {expired > 0 && <div className="banner amber"><Clock3 size={20} /><div><strong>{t("library.expiredCount", { count: expired })}</strong><p>{t("library.expiredHint")}</p></div><Button variant="destructive" size="sm" disabled={busy} onClick={() => deleteSelected(true)}><Trash2 size={14} />{t("library.deleteExpired")}</Button></div>}
    {removable.length > 0 && <div className="library-cleanup"><Trash2 size={17} /><div><strong>{t("library.removableCount", { count: removable.length })}</strong><small>{t("library.forgetHint")}</small></div><Button variant="outline" size="sm" disabled={busy} onClick={() => void forget([], true)}>{t("library.forgetAll")}</Button></div>}
    {(scope.artist || isAlbum) && <nav className="library-breadcrumbs" aria-label={t("library.hierarchy")}>
      <button onClick={() => navigate({ view: scope.view })} aria-current={!scope.artist ? "page" : undefined}>{t(scope.view === "artists" ? "library.artists" : "library.albums")}</button>
      {scope.artist && <><ChevronRight size={14} /><button onClick={() => navigate({ view: scope.view, artist: scope.artist })} aria-current={!isAlbum ? "page" : undefined}>{artist?.artist || scope.artist}</button></>}
      {isAlbum && <><ChevronRight size={14} /><span aria-current="page">{album?.album || scope.album}</span></>}
    </nav>}
    {isAlbum && <section className="album-summary">
      <Cover tracks={albumTracks} name={album?.album || t("library.unknownAlbum")} transition />
      <div><Button variant="ghost" size="sm" onClick={() => navigate({ view: scope.view, artist: scope.artist })}><ArrowLeft size={14} />{t("library.backToAlbums")}</Button>
        <h2>{album?.album || t("library.unknownAlbum")}</h2><p>{album?.artist || t("library.unknownArtist")}</p>
        <div className="album-facts"><span>{t("library.trackCount", { count: albumTracks.length })}</span>{discs.size > 1 && <span>{t("library.discCount", { count: discs.size })}</span>}{albumDuration > 0 && <span><Clock3 size={14} />{duration(albumDuration)}</span>}
          {[...new Set(albumTracks.map(source => source.path.split(".").pop()?.toUpperCase()))].map(format => <span key={format}>{format}</span>)}
        </div>
      </div>
    </section>}
    <section className="library-surface">
      <div className="library-toolbar-box">
      <div className="library-navigation">{!scope.artist && <div className="segmented" role="group" aria-label={t("library.browse")}>
        <button aria-pressed={scope.view === "albums"} onClick={() => navigate({ view: "albums" })}>{t("library.albums")}<span>{number(totals.albums)}</span></button>
        <button aria-pressed={scope.view === "tracks"} onClick={() => navigate({ view: "tracks" })}>{t("library.allTracks")}<span>{number(data.length)}</span></button>
        <button aria-pressed={scope.view === "artists"} onClick={() => navigate({ view: "artists" })}>{t("library.artists")}<span>{number(totals.artists)}</span></button>
      </div>}
      {count > 0 && <div className="library-selection"><label className="check-field"><input type="checkbox" aria-label={t("library.selectPage")} checked={pageTracks.length > 0 && pageTracks.every(source => selected.has(source.id))} ref={node => { if (node) node.indeterminate = pageTracks.some(source => selected.has(source.id)) && !pageTracks.every(source => selected.has(source.id)); }} disabled={busy} onChange={event => toggle(pageTracks.map(source => source.id), event.target.checked)} /><span>{t("library.selectPage")}</span></label><span>{t(showingTracks ? "library.trackCount" : showingArtists ? "library.artistCount" : "library.albumCount", { count })}</span></div>}
      </div>
      <div className="table-toolbar library-toolbar">
        {!showingTracks && layout === "grid" && <div className="density-control"><span>{t("library.coverSize")}</span><div className="segmented" role="group" aria-label={t("library.coverSize")}>
          {(["compact", "standard", "large"] as const).map(value => <button key={value} aria-pressed={density === value} onClick={() => { setDensity(value); savePreference("density", value); }}>{t(value === "compact" ? "library.coverCompact" : value === "standard" ? "library.coverStandard" : "library.coverLarge")}</button>)}
        </div></div>}
        <div className="library-display-actions">

        <details className="library-options" data-filtered={filter !== "all" || lyricsFilter !== "all" || scriptFilter !== "all"} open={optionsOpen} onChange={event => { if (event.target instanceof HTMLSelectElement) event.currentTarget.querySelector("summary")?.focus(); }} onKeyDown={event => { if (event.key === "Escape") { setOptionsOpen(false); event.currentTarget.querySelector("summary")?.focus(); } }}>
          <summary aria-label={t("library.options")} title={[t("library.options"), filter !== "all" ? statusLabel(filter) : "", lyricsFilter !== "all" ? t(`lyrics.state.${lyricsFilter}`) : "", scriptFilter !== "all" ? t(`lyrics.script.${scriptFilter}`) : ""].filter(Boolean).join(" · ")} onClick={event => { event.preventDefault(); setOptionsOpen(open => !open); }}><SlidersHorizontal size={17} /></summary>
          <div className="library-options-body">
            <select aria-label={t("library.filter")} value={filter} onChange={event => { setFilter(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="all">{t("library.allStatuses")}</option>{priority.slice().reverse().map(state => <option value={state} key={state}>{statusLabel(state)}</option>)}</select>
            <select aria-label={t("lyrics.filter")} value={lyricsFilter} onChange={event => { setLyricsFilter(event.target.value as LyricsMatchState | "all"); setOffset(0); setOptionsOpen(false); }}><option value="all">{t("lyrics.allStates")}</option>{lyricsMatchStates.map(state => <option value={state} key={state}>{t(`lyrics.state.${state}`)}</option>)}</select>
            <select aria-label={t("lyrics.scriptFilter")} value={scriptFilter} onChange={event => { setScriptFilter(event.target.value as ScriptFilter); setOffset(0); setOptionsOpen(false); }}>{scriptFilters.map(script => <option value={script} key={script}>{t(`lyrics.script.${script}`)}</option>)}</select>
            {!isAlbum && <select aria-label={t("library.sort")} value={sort} onChange={event => { setSort(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="name">{t("library.sortName")}</option><option value="attention">{t("library.sortAttention")}</option></select>}

          </div>
        </details>
        {!showingTracks && <div className="segmented layout-control" role="group" aria-label={t("library.layout")}>
          <button aria-label={t("library.grid")} title={t("library.grid")} aria-pressed={layout === "grid"} onClick={() => { setLayout("grid"); savePreference("layout", "grid"); }}><Grid2X2 size={17} /></button>
          <button aria-label={t("library.list")} title={t("library.list")} aria-pressed={layout === "list"} onClick={() => { setLayout("list"); savePreference("layout", "list"); }}><List size={17} /></button>
        </div>}
        </div>
      </div>
        <div className="search"><Search size={17} aria-hidden="true" /><input type="search" aria-label={t("library.search")} placeholder={t("library.searchPlaceholder")} value={query} onChange={event => { setQuery(event.target.value); setOffset(0); }} />{query && <button aria-label={t("library.clearSearch")} title={t("library.clearSearch")} onClick={() => { setQuery(""); setOffset(0); }}><X size={16} /></button>}</div>
      {(filter !== "all" || lyricsFilter !== "all" || scriptFilter !== "all") && <div className="active-filters" role="group" aria-label={t("library.activeFilters")}>
        {filter !== "all" && <button aria-label={t("library.removeFilter", { name: statusLabel(filter) })} onClick={() => { setFilter("all"); setOffset(0); }}><span>{t("library.filter")} · {statusLabel(filter)}</span><X size={12} /></button>}
        {lyricsFilter !== "all" && <button aria-label={t("library.removeFilter", { name: t(`lyrics.state.${lyricsFilter}`) })} onClick={() => { setLyricsFilter("all"); setOffset(0); }}><span>{t("lyrics.filter")} · {t(`lyrics.state.${lyricsFilter}`)}</span><X size={12} /></button>}
        {scriptFilter !== "all" && <button aria-label={t("library.removeFilter", { name: t(`lyrics.script.${scriptFilter}`) })} onClick={() => { setScriptFilter("all"); setOffset(0); }}><span>{t("lyrics.scriptFilter")} · {t(`lyrics.script.${scriptFilter}`)}</span><X size={12} /></button>}
        <button className="clear-filters" onClick={() => { setFilter("all"); setLyricsFilter("all"); setScriptFilter("all"); setOffset(0); }}>{t("library.clearFilters")}</button>
      </div>}

      </div>
    <div className="library-maintenance">
      {!scope.artist && !isAlbum && <LibraryComparison sources={data} notify={notify} />}
      <LyricsMaintenance sources={data} notify={notify} />
    </div>
      {chosen.length > 0 && <div className="selection-bar"><span>{t("library.selected", { count: chosen.length })}</span>{rows.length > pageTracks.length && <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => setSelected(new Set(rows.map(source => source.id)))}>{t("library.selectMatching", { count: rows.length })}</Button>}<Button size="sm" variant="outline" disabled={busy} onClick={() => void action("/library/rebuild", { ids: [...selected] }, "notice.selectedBuild")}><RefreshCw size={14} />{t("library.buildSelected")}</Button><LyricsSearchButton ids={chosen.filter(source => source.output && source.output_present).map(source => source.id)} label="lyrics.searchSelected" notify={notify} disabled={busy} />{chosen.some(source => source.lyrics_script === "traditional" && source.output_present) && <LyricsSearchButton ids={chosen.filter(source => source.lyrics_script === "traditional" && source.output_present).map(source => source.id)} replaceTraditional label="lyrics.replaceSelected" notify={notify} disabled={busy} />}<BatchRepairButton sources={chosen} notify={notify} disabled={busy} refresh={() => { setSelected(new Set()); retry(); }} /><Button size="sm" variant="destructive" disabled={busy || selectedExpired === 0} onClick={() => deleteSelected(false)}><Trash2 size={14} />{t("library.deleteSelected")}</Button>{selectedRemovable.length > 0 && <Button size="sm" variant="outline" disabled={busy} onClick={() => void forget(selectedRemovable.map(source => source.id))}><Trash2 size={14} />{t("library.forgetSelected", { count: selectedRemovable.length })}</Button>}<Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}><X size={14} />{t("library.clearSelection")}</Button></div>}
      {count > 0 ? <>
        {!showingTracks ? <div className={`library-groups library-${layout}`} data-density={density}>{layout === "list" && <div className="library-list-header" aria-hidden="true"><span>#</span><span>{t("library.coverColumn")}</span><span>{t(showingArtists ? "library.artists" : "library.albumArtistColumn")}</span><span>{t("library.tracksDurationColumn")}</span><span>{t("library.sourceAudio")}</span><span>{t("library.outputColumn")}</span><span>{t("lyrics.title")}</span><span>{t("library.statusColumn")}</span></div>}{visibleGroups.map(group => {
          const ready = group.tracks.filter(source => source.status === "ready").length;
          const state = priority[group.rank] || "ready";
          const open = (origin: HTMLElement) => showingArtists ? navigate({ view: scope.view, artist: group.artistKey }) : navigate({ view: scope.view, artist: group.artistKey, album: group.albumKey }, origin);
          return <article className="library-group" key={group.key} data-selected={group.tracks.some(source => selected.has(source.id))}>
            <button className="library-cover-open" aria-label={t(showingArtists ? "library.openArtist" : "library.openAlbum", { name: group.name })} onClick={event => open(event.currentTarget)}><Cover tracks={group.tracks} name={group.name} artist={showingArtists} /></button>
            <div className="library-group-body"><input type="checkbox" aria-label={t(showingArtists ? "library.selectArtist" : "library.selectAlbum", { name: group.name })} checked={group.tracks.every(source => selected.has(source.id))} ref={node => { if (node) node.indeterminate = group.tracks.some(source => selected.has(source.id)) && !group.tracks.every(source => selected.has(source.id)); }} disabled={busy} onChange={event => toggle(group.tracks.map(source => source.id), event.target.checked)} />
              <div className="library-group-title"><button className="library-open" title={group.name} onClick={event => open(event.currentTarget)}><strong>{group.name}</strong></button>
                <small>{showingArtists ? t("library.albumCount", { count: group.albums.size }) : group.artist}</small>
                <small className="group-counts">{t("library.groupCounts", { ready, total: group.tracks.length })}</small>
                {state !== "possibly_damaged" && group.tracks.some(source => source.possibly_damaged) && <small className="repair-warning">{t("repair.damaged")}</small>}
              </div>
              {layout === "list" && <GroupFacts tracks={group.tracks} />}
              <Badge status={state} />
            </div>
          </article>;
        })}</div> : <div className="table-scroll"><table className="track-table"><thead><tr><th aria-label={t("library.selectedTracks")} /><th className="track-title-column">{t("library.trackFile")}</th><th className="track-duration-column">{t("library.duration")}</th><th className="track-audio-column">{t("library.audioColumn")}</th><th className="track-size-column">{t("library.sizesColumn")}</th><th className="track-status-column">{t("library.artifact")}</th><th className="track-lyrics-column">{t("lyrics.title")}</th></tr></thead><tbody>{shown.map((source, index) => {
          const disc = source.disc || 1;
          const heading = isAlbum && discs.size > 1 && (index === 0 || disc !== (shown[index - 1].disc || 1));
          return <Fragment key={source.id}>{heading && <tr className="disc-heading"><th colSpan={trackColumns} scope="rowgroup"><Disc3 size={15} />{t("library.disc", { number: disc })}</th></tr>}<tr className={selected.has(source.id) ? "selected" : ""}>
            <td><input type="checkbox" aria-label={t("library.selectTrack", { title: source.title || source.path })} checked={selected.has(source.id)} disabled={busy} onChange={event => toggle([source.id], event.target.checked)} /></td>
            <td className="track-title"><div className="track-cell"><span className="track-number">{source.track || <Music2 size={15} />}</span><div><button className="track-open" aria-label={t("library.openDetails", { title: source.title || source.path })} onClick={() => setDetailID(source.id)}><strong dir="auto">{source.title || source.path.split("/").pop()}</strong></button><small className="track-byline" dir="auto">{source.artist || t("library.unknownArtist")} · {source.album || t("library.unknownAlbum")}</small>{!source.present && <small className="source-missing">{t("library.sourceDeleted")}</small>}</div></div></td>
            <td className="track-facts-cell" colSpan={3}><TrackFacts source={source} /></td>
            <td className="track-output"><Badge status={source.status} />{source.output_present && source.status !== "ready" && source.status !== "possibly_damaged" && <small>{t("library.stillPlayable")}</small>}<RepairControls source={source} notify={notify} refresh={retry} />{!source.present && !source.output && !source.output_present && <Button className="forget-record" size="sm" variant="ghost" disabled={busy} onClick={() => void forget([source.id])}><Trash2 size={13} />{t("library.forgetRecord")}</Button>}{source.error && <Button variant="ghost" size="sm" onClick={() => setDetailID(source.id)}>{t("library.errorDetails")}</Button>}</td>
            <td className="track-lyrics"><LyricsTrack source={source} notify={notify} refresh={retry} onChoose={() => openLyrics(source.id)} /></td>
          </tr></Fragment>;
        })}</tbody></table></div>}
        <div className="pagination"><span>{t(showingTracks ? "library.pagination" : "library.groupPagination", { start: safeOffset + 1, end: Math.min(safeOffset + 50, count), total: count })}</span>
          <Button variant="ghost" size="icon" aria-label={t("library.previous")} title={t("library.previous")} disabled={safeOffset === 0} onClick={() => setOffset(Math.max(0, safeOffset - 50))}><ChevronLeft size={16} /></Button>
          <Button variant="ghost" size="icon" aria-label={t("library.next")} title={t("library.next")} disabled={safeOffset + 50 >= count} onClick={() => setOffset(safeOffset + 50)}><ChevronRight size={16} /></Button>
        </div>
      </> : <Empty title={data.length ? t("library.noMatch") : t("library.empty")}>{data.length ? t("library.noMatchHint") : t("library.emptyHint")}</Empty>}
    </section>
    {detailSource && <DetailPanel title={t("library.fileDetails")} subtitle={detailSource.title || detailSource.path} close={() => setDetailID(undefined)} actions={<><Button variant="ghost" size="icon" aria-label={t("library.previousTrack")} disabled={!adjacent(detailSource.id, -1)} onClick={() => step(adjacent(detailSource.id, -1))}><ChevronLeft size={17} /></Button><Button variant="ghost" size="icon" aria-label={t("library.nextTrack")} disabled={!adjacent(detailSource.id, 1)} onClick={() => step(adjacent(detailSource.id, 1))}><ChevronRight size={17} /></Button></>}><TrackDetails source={detailSource} notify={notify} /></DetailPanel>}
    {lyricsSource && <LyricsPicker key={`${lyricsSource.id}:${lyricsSource.output}`} source={lyricsSource} notify={notify} close={() => setLyricsID(undefined)} saved={retry} previous={adjacent(lyricsSource.id, -1, true) ? () => step(adjacent(lyricsSource.id, -1, true), true) : undefined} next={adjacent(lyricsSource.id, 1, true) ? () => step(adjacent(lyricsSource.id, 1, true), true) : undefined} />}
  </>;
}
