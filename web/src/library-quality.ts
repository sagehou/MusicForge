import type { Source } from "./types";

export const qualityKinds = ["metadata", "duration", "numbering", "duplicates", "failed", "damaged", "lyrics", "covers"] as const;
export type QualityKind = typeof qualityKinds[number];

// This is an index report. It never opens source or output files.
export function libraryQuality(sources: Source[]): Record<QualityKind, Source[]> {
  const report: Record<QualityKind, Source[]> = { metadata: [], duration: [], numbering: [], duplicates: [], failed: [], damaged: [], lyrics: [], covers: [] };
  const hashes = new Map<string, Source[]>();
  const discs = new Map<string, Source[]>();
  for (const source of sources) {
    if (!source.present && !source.output_present) continue;
    if (!source.artist?.trim() || !source.album?.trim() || !source.title?.trim()) report.metadata.push(source);
    if (!source.duration || source.duration < 10 || source.duration > 3600) report.duration.push(source);
    if (source.status === "failed" || source.error) report.failed.push(source);
    if (source.possibly_damaged) report.damaged.push(source);
    if (source.output_present && (!source.lyrics || source.lyrics.state !== "ready" && source.lyrics.state !== "instrumental")) report.lyrics.push(source);
    if (!source.artwork_url) report.covers.push(source);
    if (source.present && source.hash) {
      const matches = hashes.get(source.hash) || [];
      matches.push(source); hashes.set(source.hash, matches);
    }
    const parts = source.path.split("/");
    const key = `${parts.length >= 3 ? parts.slice(0, 2).join("/") : parts.slice(0, -1).join("/")}\0${source.disc || 1}`;
    const disc = discs.get(key) || [];
    disc.push(source); discs.set(key, disc);
  }
  for (const group of hashes.values()) if (group.length > 1) report.duplicates.push(...group);
  for (const group of discs.values()) {
    const tracks = group.map(source => source.track).filter(track => track > 0);
    const numbered = new Set(tracks);
    const max = tracks.reduce((value, track) => Math.max(value, track), 0);
    if (tracks.length !== group.length || numbered.size !== tracks.length || max !== numbered.size) report.numbering.push(...group);
  }
  return report;
}
