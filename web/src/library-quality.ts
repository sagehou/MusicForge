import type { Source } from "./types";

export const qualityKinds = ["metadata", "duration", "numbering", "duplicates", "failed", "damaged", "lyrics", "covers"] as const;
export type QualityKind = typeof qualityKinds[number];

export function sourceDiscKey(source: Source) {
  return `${source.path.split("/").slice(0, -1).join("/")}\0${source.disc || 1}`;
}

export function numberingIssues(group: Source[]) {
  const tracks = group.map(source => source.track).filter(track => track > 0);
  const counts = new Map<number, number>();
  for (const track of tracks) counts.set(track, (counts.get(track) || 0) + 1);
  const numbered = [...counts.keys()].sort((a, b) => a - b);
  const missing: string[] = [];
  let previous = 0;
  for (const track of numbered) {
    if (track > previous + 1) missing.push(track === previous + 2 ? String(previous + 1) : `${previous + 1}–${track - 1}`);
    previous = track;
  }
  return { missing, repeated: numbered.filter(track => counts.get(track)! > 1), untagged: group.length - tracks.length };
}

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
    const key = sourceDiscKey(source);
    const disc = discs.get(key) || [];
    disc.push(source); discs.set(key, disc);
  }
  for (const group of hashes.values()) if (group.length > 1) report.duplicates.push(...group);
  for (const group of discs.values()) {
    const issues = numberingIssues(group);
    if (issues.untagged || issues.repeated.length || issues.missing.length) report.numbering.push(...group);
  }
  return report;
}
