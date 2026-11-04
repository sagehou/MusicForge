export type LyricCue = { time: number; text: string };

export function shiftLyrics(text: string, delay: number): string {
  const offset = Number(text.match(/^\[offset:\s*([+-]?\d+)\s*\]/im)?.[1] || 0);
  if (!delay && !/^\[offset:/im.test(text)) return text;
  return text.replace(/^\[offset:\s*[+-]?\d+\s*\]\s*/gim, "").split(/\r?\n/).map(line => {
    const stamps = line.match(/^(?:\[\d{1,4}:[0-5]\d(?:[.:]\d{1,3})?\])+/)?.[0];
    if (!stamps) return line;
    return stamps.replace(/\[(\d+):([0-5]\d)(?:[.:](\d{1,3}))?\]/g, (_stamp, minutes: string, seconds: string, fraction: string | undefined) => {
      const millis = Math.max(0, Number(minutes) * 60000 + Number(seconds) * 1000 + Number((fraction || "").padEnd(3, "0")) - offset + delay);
      return `[${String(Math.floor(millis / 60000)).padStart(2, "0")}:${String(Math.floor(millis / 1000) % 60).padStart(2, "0")}.${String(millis % 1000).padStart(3, "0")}]`;
    }) + line.slice(stamps.length);
  }).join("\n");
}

// Output LRC may repeat timestamps or put translations on the same timestamp.
// A positive LRC offset advances the lyric relative to the audio (milliseconds).
export function lyricsTimeline(lrc: string): LyricCue[] {
  const rawOffset = Number(lrc.match(/^\[offset:\s*([+-]?\d+)\s*\]/im)?.[1] || 0);
  const offset = Number.isFinite(rawOffset) ? rawOffset / 1000 : 0;
  const groups = new Map<number, string[]>();
  for (const line of lrc.replace(/^\uFEFF/, "").split(/\r?\n/)) {
    const stamps = line.match(/^(?:\[\d{1,4}:[0-5]\d(?:[.:]\d{1,3})?\])+/)?.[0];
    if (!stamps) continue;
    const text = line.slice(stamps.length).trim();
    for (const match of stamps.matchAll(/\[(\d+):([0-5]\d)(?:[.:](\d{1,3}))?\]/g)) {
      const time = Math.max(0, Number(match[1]) * 60 + Number(match[2]) + Number(`0.${match[3] || 0}`) - offset);
      const lines = groups.get(time) || [];
      if (text && !lines.includes(text)) lines.push(text);
      groups.set(time, lines);
    }
  }
  return [...groups].sort(([a], [b]) => a - b).map(([time, lines]) => ({ time, text: lines.join("\n") }));
}

export function activeLyric(cues: LyricCue[], time: number): number {
  let low = 0, high = cues.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (cues[middle].time <= time) low = middle + 1; else high = middle;
  }
  return low - 1;
}

export function audioTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const whole = Math.floor(seconds);
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}
