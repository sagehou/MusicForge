import { useEffect, useMemo, useRef, useState } from "react";
import { Loader2, Pause, Play, RotateCcw } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { activeLyric, audioTime, lyricsTimeline } from "../lyrics-timeline";
import type { Source } from "../types";
import { InlineDetails } from "./inline-details";
import { Button } from "./ui/button";

type Audition = { audio_url: string; format: string; size: number; lyrics: string; lyrics_state: "ready" | "missing" | "unavailable" };

export function ArtifactAudition({ source, close }: { source: Source; close: () => void }) {
  const { t } = useI18n();
  const [attempt, setAttempt] = useState(0);
  return <InlineDetails id={`audition-${source.id}`} title={t("audition.title")} subtitle={[source.title || source.output.split("/").pop(), source.artist].filter(Boolean).join(" · ")} close={close} returnFocusID={`audition-trigger-${source.id}`} className="artifact-audition">
    <LoadAudition key={attempt} source={source} reload={() => setAttempt(value => value + 1)} />
  </InlineDetails>;
}

function LoadAudition({ source, reload }: { source: Source; reload: () => void }) {
  const { t, errorMessage } = useI18n();
  const [data, setData] = useState<Audition>();
  const [error, setError] = useState<Error>();
  useEffect(() => {
    const controller = new AbortController();
    void api<Audition>(`/library/${source.id}/audition`, "GET", undefined, controller.signal).then(value => {
      if (!controller.signal.aborted) setData(value);
    }).catch((err: Error) => { if (!controller.signal.aborted) setError(err); });
    return () => controller.abort();
  }, [source.id]);
  if (error) return <div className="audition-error" role="alert"><p>{errorMessage(error.message)}</p><Button size="sm" variant="outline" onClick={reload}><RotateCcw size={14} />{t("audition.reload")}</Button></div>;
  if (!data) return <p className="audition-loading" role="status"><Loader2 size={16} className="animate-spin" />{t("audition.loading")}</p>;
  return <AudioPreview data={data} source={source} reload={reload} />;
}

function AudioPreview({ data, source, reload }: { data: Audition; source: Source; reload: () => void }) {
  const { t } = useI18n();
  const audio = useRef<HTMLAudioElement>(null);
  const lyrics = useRef<HTMLDivElement>(null);
  const mounted = useRef(true);
  const [playing, setPlaying] = useState(false);
  const [loading, setLoading] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  const [follow, setFollow] = useState(true);
  const [failed, setFailed] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const cues = useMemo(() => lyricsTimeline(data.lyrics), [data.lyrics]);
  const current = activeLyric(cues, position);
  useEffect(() => {
    mounted.current = true;
    const element = audio.current;
    const frame = requestAnimationFrame(() => element?.closest(".artifact-audition")?.scrollIntoView({ block: "nearest" }));
    const stop = () => element?.pause();
    const session = (event: Event) => { if (!(event as CustomEvent<{ authenticated: boolean }>).detail.authenticated) stop(); };
    window.addEventListener("session-updated", session);
    window.addEventListener("session-expired", stop);
    return () => {
      mounted.current = false;
      cancelAnimationFrame(frame);
      window.removeEventListener("session-updated", session);
      window.removeEventListener("session-expired", stop);
      element?.pause();
      element?.removeAttribute("src");
      element?.load();
    };
  }, []);
  useEffect(() => {
    const container = lyrics.current;
    const line = container?.querySelector<HTMLElement>('[aria-current="true"]');
    if (follow && container && line) {
      container.scrollTo({ top: line.offsetTop - Math.max(0, (container.clientHeight - line.clientHeight) / 2), behavior: matchMedia("(prefers-reduced-motion: reduce)").matches || !playing ? "auto" : "smooth" });
    }
  }, [current, follow, playing]);
  async function toggle() {
    const element = audio.current;
    if (!element) return;
    if (!element.paused) { element.pause(); return; }
    setBlocked(false);
    setFollow(true);
    try { await element.play(); }
    catch (err) {
      if (!mounted.current || err instanceof DOMException && err.name === "AbortError") return;
      setLoading(false);
      if (err instanceof DOMException && err.name === "NotAllowedError") setBlocked(true);
      else setFailed(true);
    }
  }
  function updateDuration() {
    const seconds = audio.current?.duration;
    setDuration(seconds && Number.isFinite(seconds) && seconds > 0 ? seconds : 0);
  }
  function seekTo(time: number) {
    const element = audio.current;
    if (!element || !duration || failed) return;
    const seconds = Math.min(Math.max(time, 0), duration);
    element.currentTime = seconds;
    setPosition(seconds);
    setFollow(true);
  }
  return <div className="audition-body">
    <audio ref={audio} src={data.audio_url} preload="metadata" playsInline onLoadedMetadata={updateDuration} onDurationChange={updateDuration} onTimeUpdate={() => setPosition(audio.current?.currentTime || 0)} onPlay={() => setPlaying(true)} onPlaying={() => { setPlaying(true); setLoading(false); }} onPause={() => { setPlaying(false); setLoading(false); }} onWaiting={() => setLoading(true)} onCanPlay={() => setLoading(false)} onSeeked={() => { setPosition(audio.current?.currentTime || 0); setLoading(false); }} onEnded={() => { setPlaying(false); setLoading(false); }} onError={() => { audio.current?.pause(); setFailed(true); setPlaying(false); setLoading(false); }} />
    <div className="audition-playback">
      <div className="audition-controls" role="group" aria-label={t("audition.controls")}>
        <Button size="icon" aria-label={t(playing ? "audition.pause" : "audition.play")} title={t(playing ? "audition.pause" : "audition.play")} disabled={failed} onClick={() => void toggle()}>{loading ? <Loader2 size={19} className="animate-spin" /> : playing ? <Pause size={19} /> : <Play size={19} />}</Button>
        <span className="audition-time">{audioTime(position)}</span>
        <input type="range" min={0} max={duration || 1} step={0.01} value={Math.min(position, duration || 0)} disabled={!duration || failed} aria-label={t("audition.seek")} aria-valuetext={`${audioTime(position)} / ${audioTime(duration)}`} onChange={event => seekTo(Number(event.target.value))} />
        <span className="audition-time">{duration ? audioTime(duration) : "—"}</span><span className="audition-format">{data.format.slice(1).toUpperCase()}</span>
      </div>
      {source.possibly_damaged && <p className="audition-warning">{t("audition.damaged")}</p>}
      {!source.possibly_damaged && !["ready", "expired"].includes(source.status) && <p className="audition-note">{t("audition.existing")}</p>}
      {failed ? <div className="audition-error" role="alert"><p>{t("audition.failed")}</p><Button size="sm" variant="outline" onClick={reload}><RotateCcw size={14} />{t("audition.reload")}</Button></div> : blocked && <p role="status" className="audition-note">{t("audition.blocked")}</p>}
    </div>
    <div className="audition-transcript">
      <div className="audition-lyrics-heading"><span>{t("audition.outputLyrics")}{cues.length > 0 && <small>{t("audition.seekHint")}</small>}</span>{cues.length > 0 && <Button size="sm" variant="ghost" aria-pressed={follow} onClick={() => setFollow(value => !value)}>{t("audition.follow")}</Button>}</div>
      <div className="audition-lyrics" ref={lyrics} role="region" aria-label={t("audition.outputLyrics")} tabIndex={0}>
        {cues.length ? cues.map((cue, index) => <button type="button" key={cue.time} className="audition-line" aria-current={current === index ? "true" : undefined} disabled={!duration || failed || cue.time > duration} aria-label={t("audition.seekLyric", { time: audioTime(cue.time), text: cue.text || "♪" })} onClick={() => seekTo(cue.time)}><time>{audioTime(cue.time)}</time><span dir="auto">{cue.text || "♪"}</span></button>) : <p className="audition-empty">{t(data.lyrics_state === "unavailable" ? "audition.lyricsUnavailable" : "audition.noLyrics")}</p>}
      </div>
    </div>
  </div>;
}
