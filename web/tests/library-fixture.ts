import { createHash } from "node:crypto";
import type { BrowserContext, Page, Request, Route } from "@playwright/test";
import type { Source } from "../src/types";
import type { LibraryGroup, LibraryPage } from "../src/library-data";
import { libraryQuality } from "../src/library-quality";

const priority = ["failed", "possibly_damaged", "expired", "missing", "changed", "needs_rebuild", "unverified", "ready"];
const rank = (source: Source) => priority.indexOf(source.status) < 0 ? priority.length : priority.indexOf(source.status);
function identity(source: Source, locale: string) {
  const parts = source.path.split("/");
  const artist = source.metadata.albumartist || source.metadata.album_artist || source.artist || (parts.length > 2 ? parts[0] : locale === "zh-CN" ? "未知艺人" : "Unknown artist");
  const album = source.album || (parts.length > 1 ? parts.at(-2)! : locale === "zh-CN" ? "未知专辑" : "Unknown album");
  return { artist, album, artistKey: parts.length > 2 ? parts[0] : `tag:${artist}`, albumKey: parts.slice(0, parts.length > 2 ? 2 : -1).join("/") };
}
function lyricState(source: Source) {
  return source.output && source.output_present ? source.lyrics?.instrumental && source.lyrics.state === "ready" ? "instrumental" : source.lyrics?.state || "unchecked" : "";
}
function group(sources: Source[], artists: boolean, locale: string): LibraryGroup {
  const first = sources[0] ? identity(sources[0], locale) : { artist: "", album: "", artistKey: "", albumKey: "" };
  const playable = sources.filter(source => source.output && source.output_present);
  const audio = [...new Map(sources.map(source => [JSON.stringify(source.source_audio || null), source.source_audio || null])).values()];
  const covers = [...new Map(sources.filter(source => source.artwork_url).map(source => [source.artwork_url!.split("?v=")[1] || source.artwork_url, { artwork_url: source.artwork_url! }])).values()].slice(0, 4);
  const extension = (path: string) => path.split(".").at(-1)!.toUpperCase();
  return {
    ...first, key: artists ? first.artistKey : `${first.artistKey}\0${first.albumKey}`, name: artists ? first.artist : first.album,
    ids: sources.map(source => source.id), count: sources.length, albums: new Set(sources.map(source => identity(source, locale).albumKey)).size,
    rank: sources.reduce((value, source) => Math.min(value, rank(source)), priority.length), ready: sources.filter(source => source.status === "ready").length,
    damaged: sources.some(source => source.possibly_damaged), covers, duration: sources.reduce((sum, source) => sum + (source.duration || 0), 0), duration_known: sources.every(source => (source.duration || 0) > 0),
    audio: audio.slice(0, 3), audio_count: audio.length, playable: playable.length, output_formats: [...new Set(playable.map(source => extension(source.output)))],
    output_size: playable.reduce((sum, source) => sum + (source.output_size || 0), 0), output_known: playable.every(source => source.output_size !== undefined),
    lyrics_ready: playable.filter(source => source.lyrics?.state === "ready").length, discs: [...new Set(sources.map(source => source.disc || 1))], formats: [...new Set(sources.map(source => extension(source.path)))],
  };
}

// Existing browser fixtures keep their songs and mutation behavior. Only this
// test adapter turns their responses into the paged API contract.
export function libraryFixture(sources: Source[], request: Pick<Request, "url" | "method" | "postDataJSON">): Source[] | LibraryPage | { ids: number[] } {
  const url = new URL(request.url());
  const body = request.method() === "POST" ? request.postDataJSON() : {};
  const q = url.pathname.endsWith("/selection") ? new URLSearchParams(body.query || "") : url.searchParams;
  const locale = q.get("locale") || "en";
  const compare = new Intl.Collator(locale, { numeric: true, sensitivity: "base" }).compare;
  const anchor = Number(q.get("anchor"));
  const scoped = sources.filter(source => {
    const keys = identity(source, locale);
    return (!q.get("artist") || keys.artistKey === q.get("artist")) && (!q.has("album") || keys.albumKey === q.get("album"));
  });
  if (url.pathname.endsWith("/scope")) return scoped;
  const filtered = scoped.filter(source => {
    if (source.id === anchor) return true;
    const keys = identity(source, locale);
    const filters = ["status", "lyrics", "lyrics_script"].every(key => {
      const value = q.get(key);
      if (!value || value === "all") return true;
      if (key === "status") return value === "possibly_damaged" ? source.possibly_damaged : source.status === value;
      if (key === "lyrics") return lyricState(source) === value;
      return source.output_present && !!source.output && (source.lyrics_script || "unidentified") === value;
    });
    return filters && `${keys.artist} ${source.artist} ${keys.album} ${source.title} ${source.path}`.toLowerCase().includes((q.get("q") || "").trim().toLowerCase());
  }).sort((a, b) => q.has("album") ? (a.disc || 1) - (b.disc || 1) || a.track - b.track || compare(a.path, b.path) :
    (q.get("sort") === "attention" ? rank(a) - rank(b) : 0) || compare(a.title || a.path, b.title || b.path) || compare(a.path, b.path));
  if (url.pathname.endsWith("/selection")) {
    let selected = body.ids ? (body.ids as number[]).flatMap(id => sources.find(source => source.id === id) || []) : filtered;
    if (body.playable) selected = selected.filter(source => source.output && source.output_present);
    return body.ids_only ? { ids: selected.map(source => source.id) } : selected;
  }
  if (url.pathname === "/api/library") return sources;
  const artists = q.get("view") === "artists" && !q.get("artist");
  const tracks = q.get("view") === "tracks" || q.has("album");
  const grouped = new Map<string, Source[]>();
  for (const source of filtered) {
    const keys = identity(source, locale), key = artists ? keys.artistKey : `${keys.artistKey}\0${keys.albumKey}`;
    grouped.set(key, [...(grouped.get(key) || []), source]);
  }
  const groups = tracks ? [] : [...grouped.values()].map(sources => group(sources, artists, locale)).sort((a, b) => (q.get("sort") === "attention" ? a.rank - b.rank : 0) || compare(a.name, b.name) || compare(a.artist, b.artist) || compare(a.key, b.key));
  const total = tracks ? filtered.length : groups.length;
  const position = filtered.findIndex(source => source.id === anchor);
  const offset = tracks && position >= 0 ? Math.floor(position / 50) * 50 : Math.min(Number(q.get("offset")) || 0, Math.max(0, Math.floor((total - 1) / 50) * 50));
  const states = { ready: 0, unchecked: 0, not_found: 0, instrumental: 0, needs_review: 0, failed: 0 };
  for (const source of scoped) { const state = lyricState(source); if (state) states[state]++; }
  const lyricTotal = Object.values(states).reduce((sum, count) => sum + count, 0);
  return {
    revision: createHash("sha256").update(JSON.stringify(sources)).digest("hex"), rows: tracks ? filtered.slice(offset, offset + 50) : [], groups: groups.slice(offset, offset + 50), total, matched: filtered.length, offset,
    counts: { tracks: sources.length, artists: new Set(sources.map(source => identity(source, locale).artistKey)).size, albums: new Set(sources.map(source => { const keys = identity(source, locale); return `${keys.artistKey}\0${keys.albumKey}`; })).size,
      expired: sources.filter(source => !source.present && source.output).length, removable: sources.filter(source => !source.present && !source.output && !source.output_present).length,
      present: sources.filter(source => source.present).length, deleted: sources.filter(source => !source.present).length, outputs: sources.filter(source => source.output_present).length, failed: sources.filter(source => source.present && source.error).length },
    scope: group(scoped, !!q.get("artist") && !q.has("album"), locale), lyrics: { counts: states, total: lyricTotal, checked: lyricTotal - states.unchecked },
    attention: new Set(Object.values(libraryQuality(scoped)).flat().map(source => source.id)).size,
    active: sources.find(source => source.id === anchor), context_source: scoped[0], previous: position > 0 ? filtered[position - 1].id : undefined, next: position >= 0 ? filtered[position + 1]?.id : undefined,
  };
}

export async function routeLibrary(target: Page | BrowserContext, handler: (route: Route) => unknown) {
  await target.route(/\/api\/library(?:\/(?:browse|scope|selection)(?:\?.*)?)?$/, route => handler(new Proxy(route, {
    get(target, property) {
      if (property === "fulfill") return (options: Parameters<Route["fulfill"]>[0]) => target.fulfill({ ...options, ...(Array.isArray(options?.json) ? { json: libraryFixture(options.json, target.request()) } : {}) });
      const value = Reflect.get(target, property);
      return typeof value === "function" ? value.bind(target) : value;
    },
  })));
}
