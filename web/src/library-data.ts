import type { Source } from "./types";

export type LibraryGroup = {
  artistKey: string; albumKey: string; artist: string; album: string; key: string; name: string;
  ids?: number[]; count: number; albums: number; rank: number; ready: number; damaged: boolean;
  covers: { artwork_url: string }[]; duration: number; duration_known: boolean;
  audio: (Source["source_audio"] | null)[]; audio_count: number; playable: number;
  output_formats: string[]; output_size: number; output_known: boolean; lyrics_ready: number;
  discs: number[]; formats: string[];
};
export type LibraryCounts = { tracks: number; artists: number; albums: number; expired: number; removable: number; present: number; deleted: number; outputs: number; failed: number };
export type LyricsInventory = { counts: Record<"ready" | "unchecked" | "not_found" | "instrumental" | "needs_review" | "failed", number>; total: number; checked: number };
export type LibraryPage = {
  revision: string; rows: Source[]; groups: LibraryGroup[]; total: number; matched: number; offset: number;
  counts: LibraryCounts; scope: LibraryGroup; lyrics: LyricsInventory; attention: number;
  active?: Source; context_source?: Source; previous?: number; next?: number;
};

export function mergeLibraryPage(previous: LibraryPage, patch: LibraryPage & { base: string; row_order: number[]; group_order: string[] }): LibraryPage {
  const rows = new Map([...previous.rows, ...patch.rows].map(source => [source.id, source]));
  const groups = new Map([...previous.groups, ...patch.groups].map(group => [group.key, group]));
  if (patch.row_order.some(id => !rows.has(id)) || patch.group_order.some(key => !groups.has(key))) throw new Error("Library changed while refreshing; please retry");
  const { base: _base, row_order: rowOrder, group_order: groupOrder, ...page } = patch;
  return { ...page, rows: rowOrder.map(id => rows.get(id)!), groups: groupOrder.map(key => groups.get(key)!) };
}
