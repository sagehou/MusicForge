import { Fragment, useState } from "react";
import { ChevronLeft, ChevronRight, ImagePlus, PencilLine, Search } from "lucide-react";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import { ArtworkEditor } from "./artwork-editor";
import { InlineDetails } from "./inline-details";
import { LyricsEditor } from "./lyrics-editor";
import { LyricsPicker, lyricsMatchState } from "./lyrics";
import { Button } from "./ui/button";
import type { Notice } from "./resource";
import { InfoHint } from "./info-hint";

export function ContentMaintenance({ kind, sources, notify, refresh, onBusyChange, busy, openEditor }: {
  kind: "lyrics" | "covers"; sources: Source[]; notify: Notice; refresh: () => void;
  onBusyChange: (busy: boolean) => void; busy: boolean; openEditor: () => void;
}) {
  const { t, number } = useI18n();
  const [query, setQuery] = useState("");
  const [all, setAll] = useState(false);
  const [page, setPage] = useState(0);
  const [editor, setEditor] = useState<{ source: Source; action: "search" | "edit" | "cover"; trigger: string }>();
  const albums = new Map<string, Source>();
  if (kind === "covers") for (const source of sources) {
    if (!source.present && !source.output_present) continue;
    const directory = source.path.split("/").slice(0, -1).join("/");
    const current = albums.get(directory);
    if (!current || !current.artwork_url && source.artwork_url) albums.set(directory, source);
  }
  const available = kind === "lyrics" ? sources.filter(source => source.output_present && source.output) : [...albums.values()];
  const missing = (source: Source) => kind === "lyrics" ? !["ready", "instrumental"].includes(lyricsMatchState(source) || "unchecked") : !source.artwork_url;
  const matching = available.filter(source => `${source.title} ${source.artist} ${source.album} ${source.path}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()) && (all || missing(source) || editor?.source.id === source.id));
  const offset = Math.min(page * 20, Math.max(0, Math.floor((matching.length - 1) / 20) * 20));
  const shown = matching.slice(offset, offset + 20);
  function open(source: Source, action: "search" | "edit" | "cover", trigger: string) {
    if (busy) return;
    openEditor(); setEditor({ source, action, trigger });
  }
  function close() {
    if (busy) return;
    const trigger = editor?.trigger;
    setEditor(undefined);
    requestAnimationFrame(() => {
      const target = trigger ? document.getElementById(trigger) : null;
      (target?.getClientRects().length ? target : document.getElementById(`maintenance-${kind}-heading`))?.focus({ preventScroll: true });
    });
  }
  return <section className="content-maintenance" aria-labelledby={`maintenance-${kind}-heading`}>
    <div className="section-heading"><h3 id={`maintenance-${kind}-heading`} tabIndex={-1}>{t(kind === "lyrics" ? "workbench.lyricsList" : "workbench.coversList")}</h3><InfoHint>{t(kind === "lyrics" ? "workbench.lyricsHint" : "workbench.coversHint")}</InfoHint></div>
    <div className="content-maintenance-filters">
      <label className="field"><span>{t(kind === "lyrics" ? "workbench.findTrack" : "workbench.findAlbum")}</span><input type="search" value={query} disabled={busy || !!editor} onChange={event => { setQuery(event.target.value); setPage(0); }} /></label>
      <div className="segmented" role="group" aria-label={t("workbench.listScope")}><button type="button" aria-pressed={!all} disabled={busy || !!editor} onClick={() => { setAll(false); setPage(0); }}>{t("workbench.pending")} <span>{number(available.filter(missing).length)}</span></button><button type="button" aria-pressed={all} disabled={busy || !!editor} onClick={() => { setAll(true); setPage(0); }}>{t("workbench.showAll")} <span>{number(available.length)}</span></button></div>
    </div>
    {!matching.length ? <div className="content-maintenance-empty"><p>{t(!available.length ? kind === "lyrics" ? "workbench.noLyricsTracks" : "workbench.noAlbums" : query ? "library.noMatch" : "workbench.nothingPending")}</p>{available.length ? <Button variant="outline" size="sm" onClick={() => { setQuery(""); setAll(true); }}>{t("workbench.showAll")}</Button> : <Button asChild variant="outline" size="sm"><a href="/library">{t("app.browse")}</a></Button>}</div> : <ul className="content-maintenance-list">{shown.map(source => {
      const searchID = `maintenance-search-${source.id}`, editID = `maintenance-edit-${source.id}`, coverID = `maintenance-cover-${source.id}`;
      const editing = editor?.source.id === source.id;
      return <li key={source.id}><div className="content-maintenance-row">
        {kind === "covers" && <span className="content-maintenance-cover">{source.artwork_url ? <img src={source.artwork_url} width={56} height={56} alt="" loading="lazy" /> : <ImagePlus size={24} aria-hidden="true" />}</span>}
        <div className="content-maintenance-identity"><strong dir="auto">{(kind === "lyrics" ? source.title : source.album) || source.path}</strong><small dir="auto">{source.artist || t("library.unknownArtist")}{kind === "lyrics" && source.album && ` · ${source.album}`}</small>{kind === "covers" && <code>{source.path.split("/").slice(0, -1).join("/") || "."}</code>}</div>
        <span className="content-maintenance-state">{kind === "lyrics" ? t(`lyrics.state.${lyricsMatchState(source) || "unchecked"}`) : t(source.artwork_url ? "workbench.coverReady" : "workbench.coverMissing")}</span>
        <div className="actions">{kind === "lyrics" ? <>
          <Button id={searchID} size="sm" variant="outline" disabled={busy} aria-expanded={editing && editor.action === "search"} aria-controls={editing && editor.action === "search" ? `maintenance-picker-${source.id}` : undefined} onClick={() => open(source, "search", searchID)}><Search size={14} />{t("workbench.searchLyrics")}</Button>
          <Button id={editID} size="sm" variant="outline" disabled={busy} aria-expanded={editing && editor.action === "edit"} aria-controls={editing && editor.action === "edit" ? `maintenance-lyrics-editor-${source.id}` : undefined} onClick={() => open(source, "edit", editID)}><PencilLine size={14} />{t(source.lyrics?.state === "ready" ? "lyricsEdit.open" : "workbench.addLyrics")}</Button>
        </> : <Button id={coverID} size="sm" variant="outline" disabled={busy} aria-expanded={editing} aria-controls={editing ? `maintenance-artwork-${source.id}` : undefined} onClick={() => open(source, "cover", coverID)}><ImagePlus size={14} />{t(source.artwork_url ? "workbench.changeCover" : "workbench.addCover")}</Button>}</div>
      </div>{editing && editor && <Fragment key={`${source.id}:${editor.action}`}>
        {editor.action === "search" && <LyricsPicker panelID={`maintenance-picker-${source.id}`} returnFocusID={editor.trigger} source={editor.source} notify={notify} close={close} saved={refresh} onBusyChange={onBusyChange} />}
        {editor.action === "edit" && <InlineDetails id={`maintenance-lyrics-editor-${source.id}`} title={t("workbench.manualLyrics")} subtitle={source.title || source.path} returnFocusID={editor.trigger} close={close} busy={busy} actions={<InfoHint>{t("lyricsEdit.hint")}</InfoHint>}><LyricsEditor embedded id={source.id} position={0} audition={() => {}} canAudition={false} preview={() => {}} onBusyChange={onBusyChange} close={close} saved={() => { refresh(); notify("lyrics.saved"); close(); }} /></InlineDetails>}
        {editor.action === "cover" && <ArtworkEditor panelID={`maintenance-artwork-${source.id}`} source={editor.source} saved={refresh} close={close} returnFocusID={editor.trigger} onBusyChange={onBusyChange} />}
      </Fragment>}</li>;
    })}</ul>}
    {matching.length > 20 && <div className="pagination"><span>{t("library.pagination", { start: offset + 1, end: Math.min(offset + 20, matching.length), total: matching.length })}</span><Button size="icon" variant="ghost" disabled={busy || !!editor || offset === 0} aria-label={t(kind === "lyrics" ? "workbench.previousLyrics" : "workbench.previousCovers")} onClick={() => setPage(Math.max(0, page - 1))}><ChevronLeft size={16} /></Button><Button size="icon" variant="ghost" disabled={busy || !!editor || offset + 20 >= matching.length} aria-label={t(kind === "lyrics" ? "workbench.nextLyrics" : "workbench.nextCovers")} onClick={() => setPage(Math.floor(offset / 20) + 1)}><ChevronRight size={16} /></Button></div>}
  </section>;
}
