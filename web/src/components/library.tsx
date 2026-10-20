import { useDeferredValue, useEffect, useMemo, useRef, useState, Fragment, memo } from "react";
import { flushSync } from "react-dom";
import { ArrowLeft, ChevronLeft, ChevronRight, Clock3, Disc3, Grid2X2, List, Music2, RefreshCw, Search, ShieldCheck, SlidersHorizontal, Trash2, X } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { Source } from "../types";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";
import { BatchRepairButton, RepairControls, RepairDiagnostics } from "./repair";
import { LibraryComparison } from "./library-comparison";
import { LyricsMaintenance, LyricsSearchButton, LyricsTrack } from "./lyrics";

type Scope = { view: "artists" | "albums"; artist?: string; album?: string };
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
  return { view: params.get("view") === "albums" ? "albums" : "artists", artist: params.get("artist") ?? undefined, album: params.get("album") ?? undefined };
}
function duration(seconds: number) {
  if (!seconds) return "--";
  const value = Math.round(seconds);
  return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, "0")}`;
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
  const [filter, setFilter] = useState("all");
  const [layout, setLayout] = useState<"grid" | "list">(() => preference("layout") === "list" ? "list" : "grid");
  const [density, setDensity] = useState<Density>(() => {
    const saved = preference("density");
    return saved === "compact" || saved === "large" ? saved : "standard";
  });
  const transition = useRef<ViewTransition | null>(null);
  const navigation = useRef(0);
  const [compact, setCompact] = useState(() => matchMedia("(max-width: 800px)").matches);
  const [phone, setPhone] = useState(() => matchMedia("(max-width: 600px)").matches);
  const trackColumns = phone ? 3 : compact ? 4 : 5;
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [sort, setSort] = useState("name");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState(false);
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const back = () => { navigation.current++; transition.current?.skipTransition(); setScope(readScope()); setOffset(0); setOptionsOpen(false); };
    window.addEventListener("popstate", back);
    window.addEventListener("route-changed", back);
    return () => { window.removeEventListener("popstate", back); window.removeEventListener("route-changed", back); };
  }, []);
  useEffect(() => () => transition.current?.skipTransition(), []);
  useEffect(() => {
    const media = matchMedia("(max-width: 800px)");
    const small = matchMedia("(max-width: 600px)");
    const change = () => { setCompact(media.matches); setPhone(small.matches); setOptionsOpen(false); };
    media.addEventListener("change", change);
    small.addEventListener("change", change);
    return () => { media.removeEventListener("change", change); small.removeEventListener("change", change); };
  }, []);
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
      if (search && !`${identity.artist} ${source.artist} ${identity.album} ${source.title} ${source.path}`.toLocaleLowerCase(locale).includes(search)) continue;
      rows.push(source);
      if (isAlbum) continue;
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
    return { rows, groups };
  }, [indexed, scope.artist, scope.album, isAlbum, showingArtists, filter, search, sort, collator, locale]);
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
  function toggle(ids: number[], checked: boolean) {
    setSelected(old => { const next = new Set(old); ids.forEach(id => checked ? next.add(id) : next.delete(id)); return next; });
  }
  if (!data) return error ? <Loading error={error} retry={retry} /> : <div className="library-loading" role="status" aria-label={t("app.loading")}>
    <div className="page-heading"><div><h1>{t("app.library")}</h1><p>{t("library.loadingHint")}</p></div></div>
    <div className="skeleton-toolbar" aria-hidden="true" />
    <div className="library-groups" aria-hidden="true">{Array.from({ length: 10 }, (_, index) => <div className="skeleton-card" key={index}><div /><span /><span /></div>)}</div>
  </div>;
  const count = isAlbum ? rows.length : groups.length;
  const safeOffset = Math.min(offset, Math.max(0, Math.ceil(count / 50) - 1) * 50);
  const shown = rows.slice(safeOffset, safeOffset + 50);
  const visibleGroups = groups.slice(safeOffset, safeOffset + 50);
  const pageTracks = isAlbum ? shown : visibleGroups.flatMap(group => group.tracks);
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
  return <>
    <ResourceWarning error={error} retry={retry} />
    <div className="page-heading"><div><h1 id="library-title" tabIndex={-1}>{t("app.library")}</h1><p>{t("library.description", { count: data.length })}</p></div>
      <div className="actions"><Button variant="outline" disabled={busy} onClick={() => void action("/library/scan", { verify: true }, "notice.verify")}><ShieldCheck size={16} />{t("library.verify")}</Button><Button disabled={busy} onClick={() => void action("/library/scan", {}, "notice.scan")}><RefreshCw size={16} className={busy ? "animate-spin" : ""} />{t("library.scan")}</Button></div>
    </div>
    <div className="library-maintenance">
      {!scope.artist && !isAlbum && <LibraryComparison sources={data} notify={notify} />}
      <LyricsMaintenance notify={notify} />
    </div>
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
      {!scope.artist && <div className="library-navigation"><div className="segmented" role="group" aria-label={t("library.browse")}>
        <button aria-pressed={scope.view === "artists"} onClick={() => navigate({ view: "artists" })}>{t("library.artists")}<span>{number(totals.artists)}</span></button>
        <button aria-pressed={scope.view === "albums"} onClick={() => navigate({ view: "albums" })}>{t("library.albums")}<span>{number(totals.albums)}</span></button>
      </div></div>}
      <div className="table-toolbar library-toolbar">
        <div className="search"><Search size={17} aria-hidden="true" /><input type="search" aria-label={t("library.search")} placeholder={t("library.searchPlaceholder")} value={query} onChange={event => { setQuery(event.target.value); setOffset(0); }} />{query && <button aria-label={t("library.clearSearch")} title={t("library.clearSearch")} onClick={() => { setQuery(""); setOffset(0); }}><X size={16} /></button>}</div>
        <details className="library-options" data-filtered={filter !== "all"} open={!compact || optionsOpen} onKeyDown={event => { if (event.key === "Escape") { setOptionsOpen(false); event.currentTarget.querySelector("summary")?.focus(); } }}>
          <summary aria-label={t("library.options")} title={filter === "all" ? t("library.options") : `${t("library.options")}: ${statusLabel(filter)}`} onClick={event => { if (compact) { event.preventDefault(); setOptionsOpen(open => !open); } }}><SlidersHorizontal size={17} /></summary>
          <div className="library-options-body">
            <select aria-label={t("library.filter")} value={filter} onChange={event => { setFilter(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="all">{t("library.allStatuses")}</option>{priority.slice().reverse().map(state => <option value={state} key={state}>{statusLabel(state)}</option>)}</select>
            {!isAlbum && <select aria-label={t("library.sort")} value={sort} onChange={event => { setSort(event.target.value); setOffset(0); setOptionsOpen(false); }}><option value="name">{t("library.sortName")}</option><option value="attention">{t("library.sortAttention")}</option></select>}
            {!isAlbum && !compact && layout === "grid" && <select aria-label={t("library.coverSize")} value={density} onChange={event => { const value = event.target.value as Density; setDensity(value); savePreference("density", value); setOptionsOpen(false); }}>
              <option value="compact">{t("library.coverCompact")}</option><option value="standard">{t("library.coverStandard")}</option><option value="large">{t("library.coverLarge")}</option>
            </select>}
          </div>
        </details>
        {!isAlbum && <div className="segmented layout-control" role="group" aria-label={t("library.layout")}>
          <button aria-label={t("library.grid")} title={t("library.grid")} aria-pressed={layout === "grid"} onClick={() => { setLayout("grid"); savePreference("layout", "grid"); }}><Grid2X2 size={17} /></button>
          <button aria-label={t("library.list")} title={t("library.list")} aria-pressed={layout === "list"} onClick={() => { setLayout("list"); savePreference("layout", "list"); }}><List size={17} /></button>
        </div>}
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void action("/library/rebuild", { all: true }, "notice.rebuild")}><RefreshCw size={14} />{t("library.startRebuild")}</Button>
      </div>
      {count > 0 && <div className="library-selection"><label className="check-field"><input type="checkbox" aria-label={t("library.selectPage")} checked={pageTracks.length > 0 && pageTracks.every(source => selected.has(source.id))} ref={node => { if (node) node.indeterminate = pageTracks.some(source => selected.has(source.id)) && !pageTracks.every(source => selected.has(source.id)); }} disabled={busy} onChange={event => toggle(pageTracks.map(source => source.id), event.target.checked)} /><span>{t("library.selectPage")}</span></label><span>{t(isAlbum ? "library.trackCount" : showingArtists ? "library.artistCount" : "library.albumCount", { count })}</span></div>}
      {chosen.length > 0 && <div className="selection-bar"><span>{t("library.selected", { count: chosen.length })}</span>{rows.length > pageTracks.length && <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => setSelected(new Set(rows.map(source => source.id)))}>{t("library.selectMatching", { count: rows.length })}</Button>}<Button size="sm" variant="outline" disabled={busy} onClick={() => void action("/library/rebuild", { ids: [...selected] }, "notice.selectedBuild")}><RefreshCw size={14} />{t("library.buildSelected")}</Button><LyricsSearchButton ids={chosen.filter(source => source.output && source.output_present).map(source => source.id)} label="lyrics.searchSelected" notify={notify} disabled={busy} /><BatchRepairButton sources={chosen} notify={notify} disabled={busy} refresh={() => { setSelected(new Set()); retry(); }} /><Button size="sm" variant="destructive" disabled={busy || selectedExpired === 0} onClick={() => deleteSelected(false)}><Trash2 size={14} />{t("library.deleteSelected")}</Button>{selectedRemovable.length > 0 && <Button size="sm" variant="outline" disabled={busy} onClick={() => void forget(selectedRemovable.map(source => source.id))}><Trash2 size={14} />{t("library.forgetSelected", { count: selectedRemovable.length })}</Button>}<Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}><X size={14} />{t("library.clearSelection")}</Button></div>}
      {count > 0 ? <>
        {!isAlbum ? <div className={`library-groups library-${layout}`} data-density={density}>{visibleGroups.map(group => {
          const ready = group.tracks.filter(source => source.status === "ready").length;
          const state = priority[group.rank] || "ready";
          const open = (origin: HTMLElement) => showingArtists ? navigate({ view: scope.view, artist: group.artistKey }) : navigate({ view: scope.view, artist: group.artistKey, album: group.albumKey }, origin);
          return <article className="library-group" key={group.key} data-selected={group.tracks.some(source => selected.has(source.id))}>
            <button className="library-cover-open" aria-label={t(showingArtists ? "library.openArtist" : "library.openAlbum", { name: group.name })} onClick={event => open(event.currentTarget)}><Cover tracks={group.tracks} name={group.name} artist={showingArtists} /></button>
            <div className="library-group-body"><input type="checkbox" aria-label={t(showingArtists ? "library.selectArtist" : "library.selectAlbum", { name: group.name })} checked={group.tracks.every(source => selected.has(source.id))} ref={node => { if (node) node.indeterminate = group.tracks.some(source => selected.has(source.id)) && !group.tracks.every(source => selected.has(source.id)); }} disabled={busy} onChange={event => toggle(group.tracks.map(source => source.id), event.target.checked)} />
              <div><button className="library-open" title={group.name} onClick={event => open(event.currentTarget)}><strong>{group.name}</strong></button>
                <small>{showingArtists ? t("library.albumCount", { count: group.albums.size }) : group.artist}</small>
                <small>{t("library.groupCounts", { ready, total: group.tracks.length })}</small>
                {state !== "possibly_damaged" && group.tracks.some(source => source.possibly_damaged) && <small className="repair-warning">{t("repair.damaged")}</small>}
              </div>
              <Badge status={state} />
            </div>
          </article>;
        })}</div> : <div className="table-scroll"><table className="track-table"><thead><tr><th aria-label={t("library.selectedTracks")} /><th>{t("library.trackFile")}</th><th className="track-artist">{t("library.artistAlbum")}</th><th className="track-duration"><Clock3 size={15} aria-label={t("library.duration")} /></th><th>{t("library.artifact")}</th></tr></thead><tbody>{shown.map((source, index) => {
          const disc = source.disc || 1;
          const heading = discs.size > 1 && (index === 0 || disc !== (shown[index - 1].disc || 1));
          return <Fragment key={source.id}>{heading && <tr className="disc-heading"><th colSpan={trackColumns} scope="rowgroup"><Disc3 size={15} />{t("library.disc", { number: disc })}</th></tr>}<tr className={selected.has(source.id) ? "selected" : ""}>
            <td><input type="checkbox" aria-label={t("library.selectTrack", { title: source.title || source.path })} checked={selected.has(source.id)} disabled={busy} onChange={event => toggle([source.id], event.target.checked)} /></td>
            <td><div className="track-cell"><span className="track-number">{source.track || <Music2 size={15} />}</span><div><strong>{source.title || source.path.split("/").pop()}</strong><small className={source.present ? "source-present" : "source-missing"}>{source.present ? t("library.sourceOnline", { format: source.path.split(".").pop()?.toUpperCase() || "--" }) : t("library.sourceDeleted")}</small>
              <details className="track-file"><summary>{t("library.fileDetails")}</summary><dl><dt>{t("library.source")}</dt><dd>{source.path}</dd>{source.output && <><dt>{t("library.artifact")}</dt><dd>{source.output}</dd></>}</dl></details>
            </div></div></td>
            <td className="track-artist"><strong>{source.artist || t("library.unknownArtist")}</strong><small>{source.album || t("library.unknownAlbum")}</small></td>
            <td className="track-duration">{duration(source.duration || 0)}</td>
            <td><Badge status={source.status} />{source.output_present && source.status !== "ready" && source.status !== "possibly_damaged" && <small>{t("library.stillPlayable")}</small>}<RepairControls source={source} notify={notify} refresh={retry} /><LyricsTrack source={source} notify={notify} refresh={retry} />{!source.present && !source.output && !source.output_present && <Button className="forget-record" size="sm" variant="ghost" disabled={busy} onClick={() => void forget([source.id])}><Trash2 size={13} />{t("library.forgetRecord")}</Button>}{source.error && <details><summary>{t("library.errorDetails")}</summary><pre>{source.error}</pre></details>}</td>
          </tr>{(source.possibly_damaged || source.repair?.diagnostics) && <tr className="repair-detail-row"><td colSpan={trackColumns}><RepairDiagnostics source={source} /></td></tr>}</Fragment>;
        })}</tbody></table></div>}
        <div className="pagination"><span>{t(isAlbum ? "library.pagination" : "library.groupPagination", { start: safeOffset + 1, end: Math.min(safeOffset + 50, count), total: count })}</span>
          <Button variant="ghost" size="icon" aria-label={t("library.previous")} title={t("library.previous")} disabled={safeOffset === 0} onClick={() => setOffset(Math.max(0, safeOffset - 50))}><ChevronLeft size={16} /></Button>
          <Button variant="ghost" size="icon" aria-label={t("library.next")} title={t("library.next")} disabled={safeOffset + 50 >= count} onClick={() => setOffset(safeOffset + 50)}><ChevronRight size={16} /></Button>
        </div>
      </> : <Empty title={data.length ? t("library.noMatch") : t("library.empty")}>{data.length ? t("library.noMatchHint") : t("library.emptyHint")}</Empty>}
    </section>
  </>;
}
