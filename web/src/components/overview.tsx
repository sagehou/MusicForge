import { useState } from "react";
import { Activity, ArrowRight, Check, Clock3, Disc3, FileMusic, Loader2, Music2, RefreshCw } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { PageHeading } from "./page-heading";
import { taskPercent } from "../lib/utils";
import type { Dashboard, Job, Settings, Source } from "../types";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";

export function Overview({ notify, navigate }: { notify: Notice; navigate: (path: string) => void }) {
  const { t, kind, date, number, errorMessage, locale } = useI18n();
  const { data, error, retry } = useResource<Dashboard>("/dashboard", true);
  const jobs = useResource<{ jobs: Job[] }>("/jobs?limit=5&include_repairs=true", true);
  const config = useResource<{ settings: Settings }>("/settings");
  const library = useResource<Source[]>("/library", true);
  const [busy, setBusy] = useState(false);
  async function scan() {
    setBusy(true);
    try { await api("/library/scan", "POST", {}); notify("notice.scan"); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  if (!data) return <><PageHeading><div><h1>{t("app.overview")}</h1></div></PageHeading><Loading error={error} retry={retry} /></>;
  const percent = data.source_count ? Math.max(0, Math.min(100, Math.round(data.ready_count / data.source_count * 100))) : 0;
  const percentLabel = new Intl.NumberFormat(locale, { style: "percent" }).format(percent / 100);
  const encoding = config.data?.settings.encoding;
  const profile = encoding ? `${encoding.mode.toUpperCase()} · ${encoding.codec === "mp3" && encoding.mode === "vbr" ? `V${encoding.quality}` : `${number(encoding.bitrate)} kbps`}` : "—";
  const playable = library.data?.filter(source => source.output_present && source.output);
  const lyricReady = playable?.filter(source => source.lyrics?.state === "ready").length;
  const review = playable?.filter(source => source.lyrics?.state === "needs_review").length;
  const active = jobs.data?.jobs.filter(job => job.state === "running").flatMap(job => job.current.map(activity => ({ job, activity }))).slice(0, 4) || [];
  const attention = [
    { state: "failed", count: data.failed_count },
    { state: "needs_rebuild", count: data.rebuild_count },
    { state: "expired", count: data.expired_count },
  ];
  const metrics = [
    { label: t("overview.sources"), value: data.source_count, icon: Disc3, path: "/library?view=tracks", note: t("overview.masters") },
    { label: t("overview.outputs"), value: data.output_count, icon: Music2, path: "/library?view=tracks", note: t("overview.outputLibrary", { codec: data.codec.toUpperCase() }) },
    { label: t("overview.profileReady"), value: data.ready_count, icon: Check, path: "/library?view=tracks&status=ready", note: t("overview.currentProfile") },
    { label: t("overview.attention"), value: data.expired_count + data.rebuild_count + data.failed_count, icon: Clock3, path: "/library?view=tracks&sort=attention", note: t("overview.attentionCounts", { rebuild: data.rebuild_count, expired: data.expired_count, failed: data.failed_count }) },
  ];
  return <div className="overview-page">
    <ResourceWarning error={error} retry={retry} />
    <PageHeading><div><h1>{t("app.overview")}</h1><p>{t("overview.description")}</p></div><div className="actions hero-actions"><Button variant="outline" onClick={() => navigate("/library")}>{t("overview.viewLibrary")}<ArrowRight size={15} /></Button><Button onClick={() => void scan()} disabled={busy || !data.enabled}><RefreshCw size={15} className={busy ? "animate-spin" : ""} />{t("overview.scan")}</Button></div></PageHeading>
    <div className="metrics overview-metrics">{metrics.map(metric => <button className="metric" key={metric.label} onClick={() => navigate(metric.path)}><div><span>{metric.label}</span><metric.icon size={17} /></div><strong>{number(metric.value)}</strong><small>{metric.note}</small></button>)}</div>
    <section className="readiness-panel">
      <div className="readiness-heading">
        <div className="completion-value"><span>{t("overview.complete")} <strong>{percentLabel}</strong></span>{active.length > 0 && <Badge status="running" />}</div>
        <button className="hero-profile" onClick={() => navigate("/settings?section=encoding")}><span>{data.codec.toUpperCase()} {profile}</span><span>{number(data.ready_count)} / {number(data.source_count)}</span><ArrowRight size={14} /></button>
      </div>
      <div className="hero-completion" role="progressbar" aria-label={t("overview.complete")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><div className="completion-meter" aria-hidden="true"><span style={{ width: `${percent}%` }} /></div></div>
      {active.length > 0 && <div className="active-tracks">{active.map(({ job, activity }, index) => {
        const source = library.data?.find(source => source.path === activity.path);
        return <div className="active-track" key={`${job.repair_id || job.id}:${activity.path}:${index}`}>
          <span className="active-track-artwork" aria-hidden="true">{source?.artwork_url ? <img src={source.artwork_url} alt="" loading="lazy" /> : <Music2 size={20} />}</span>
          <div className="active-track-title"><strong>{activity.title || activity.path || kind(job.kind)}<span className="audio-eq-wave" aria-hidden="true"><i /><i /><i /><i /></span></strong><small>{[activity.artist, activity.album].filter(Boolean).join(" · ") || kind(job.kind)}</small></div>
          <div className="active-track-specs">{source?.source_audio && <span>{source.source_audio.codec?.toUpperCase()}{source.source_audio.bit_depth ? ` ${source.source_audio.bit_depth}` : ""}{source.source_audio.sample_rate ? ` / ${source.source_audio.sample_rate / 1000} kHz` : ""}</span>}{job.profile && <span>{job.profile.codec.toUpperCase()} · {job.profile.codec === "mp3" && job.profile.mode === "vbr" ? `V${job.profile.quality}` : `${job.profile.bitrate} kbps`}</span>}</div><strong className="active-track-percent">{number(Math.round(activity.phase === "convert" ? activity.percent : taskPercent(job)))}%</strong>
        </div>;
      })}</div>}
    </section>
    <div className="overview-workspace">
      <section className="recent-panel"><div className="section-heading"><h2>{t("overview.recent")}</h2><Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>{t("overview.allJobs")}<ArrowRight size={14} /></Button></div>
        <ResourceWarning error={jobs.error} retry={jobs.retry} />
        {!jobs.data ? <Loading error={jobs.error} retry={jobs.retry} /> : jobs.data.jobs.length ? <div className="job-list">{[...jobs.data.jobs].sort((a, b) => Number(b.state === "running") - Number(a.state === "running")).map(job => <button className="job-row" data-state={job.state} key={job.repair_id || job.id} onClick={() => navigate("/jobs")}>
          <span className={`job-symbol ${job.state}`}>{job.state === "running" ? <Loader2 size={17} className="animate-spin" /> : job.state === "success" ? <Check size={17} /> : <Activity size={17} />}</span><div><strong>{kind(job.kind)} #{job.repair_id?.slice(0, 8) || job.id}</strong><small>{job.current[0]?.title || job.current[0]?.path || date(job.updated)}{job.counts.total > 0 && ` · ${number(job.counts.done)} / ${number(job.counts.total)}`}</small></div><Badge status={job.state} /><ArrowRight className="recent-arrow" size={15} />
        </button>)}</div> : <Empty title={t("overview.noJobs")}>{t("overview.noJobsHint")}</Empty>}
      </section>
      <aside className="overview-side">
        <section className="lyrics-summary"><div className="section-heading"><h2><FileMusic size={16} />{t("lyrics.title")}</h2></div><ResourceWarning error={library.error} retry={library.retry} /><button className="lyrics-overview" onClick={() => navigate("/library?view=tracks&lyrics=needs_review")}><span>{t("overview.lyricCoverage")}</span><strong>{playable ? `${number(lyricReady || 0)} / ${number(playable.length)}` : "—"}</strong><small>{t("overview.lyricReview", { count: review === undefined ? "—" : number(review) })}</small><ArrowRight size={15} /></button><Button variant="outline" size="sm" onClick={() => navigate("/settings?section=lyrics")}>{t("lyrics.settings")}</Button></section>
        <section className="storage-summary"><div className="section-heading"><h2>{t("overview.storage")}</h2><span className={`online ${data.online ? "is-online" : ""}`}><span />{data.online ? t("overview.online") : data.storage_checking ? t("overview.checkingShort") : t("overview.waiting")}</span></div><ResourceWarning error={config.error} retry={config.retry} /><dl><div><dt>{t("overview.flac")}</dt><dd><code>{config.data?.settings.source || "—"}</code></dd></div><div><dt>{t("overview.streaming")}</dt><dd><code>{config.data?.settings.output || "—"}</code></dd></div></dl><div className="storage-status" data-online={data.online} role="status" aria-live="polite">{!data.online && <><strong>{t(data.storage_checking ? "overview.checking" : data.enabled ? "overview.offline" : "overview.configure")}</strong><p>{errorMessage(data.storage_message)}</p><Button variant="ghost" size="sm" onClick={() => navigate("/settings")}>{t("overview.openSettings")}<ArrowRight size={13} /></Button></>}</div></section>
        <details className="overview-attention"><summary>{t("overview.attention")}</summary><div className="attention-list">{attention.map(item => <button key={item.state} onClick={() => navigate(`/library?view=tracks&status=${item.state}`)}><Badge status={item.state} /><strong>{number(item.count)}</strong><ArrowRight size={14} /></button>)}</div></details>
      </aside>
    </div>
  </div>;
}
