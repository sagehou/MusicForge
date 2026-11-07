import { useState } from "react";
import {
  ArrowRight, AudioLines, Check, CircleAlert, CircleSlash,
  CircleX, Clock3, Disc3, FileMusic, Music2, RefreshCw
} from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { PageHeading } from "./page-heading";
import { taskPercent } from "../lib/utils";
import { lyricsMatchState } from "./lyrics";
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
  const storageUnchecked = data.enabled && data.storage_checked_at === 0;
  const percent = data.source_count ? Math.max(0, Math.min(100, Math.round(data.ready_count / data.source_count * 100))) : 0;
  const percentLabel = new Intl.NumberFormat(locale, { style: "percent" }).format(percent / 100);
  const encoding = config.data?.settings.encoding;
  const profile = encoding ? `${encoding.mode.toUpperCase()} · ${encoding.codec === "mp3" && encoding.mode === "vbr" ? `V${encoding.quality}` : `${number(encoding.bitrate)} kbps`}` : "—";
  const playable = library.data?.filter(source => source.output_present && source.output);
  const lyricReady = playable?.filter(source => lyricsMatchState(source) === "ready").length;
  const lyricIssues = playable ? (["unchecked", "not_found", "instrumental", "needs_review", "failed"] as const).map(state => ({ state, count: playable.filter(source => lyricsMatchState(source) === state).length })).filter(item => item.count > 0) : undefined;
  const active = jobs.data?.jobs.filter(job => job.state === "running").flatMap(job => job.current.map(activity => ({ job, activity }))).slice(0, 3) || [];
  const attentionCount = data.expired_count + data.rebuild_count + data.failed_count;
  const attention = [
    { state: "failed", count: data.failed_count },
    { state: "needs_rebuild", count: data.rebuild_count },
    { state: "expired", count: data.expired_count },
  ];
  const metrics: { label: string; value: number; icon: typeof Disc3; path: string; tone: string; track?: string }[] = [
    { label: t("overview.sources"), value: data.source_count, icon: Disc3, path: "/library?view=tracks", tone: "sources" },
    { label: t("overview.outputs"), value: data.output_count, icon: Music2, path: "/library?view=tracks", tone: "outputs" },
    { label: t("overview.profileReady"), value: data.ready_count, icon: Check, path: "/library?view=tracks&status=ready", tone: "ready", track: number(data.source_count) },
    { label: t("overview.attention"), value: attentionCount, icon: Clock3, path: "/library?view=tracks&sort=attention", tone: attentionCount > 0 ? "attention" : "neutral" },
  ];
  const stateGlyph = { unchecked: Clock3, not_found: CircleSlash, instrumental: Music2, needs_review: CircleAlert, failed: CircleX } as const;
  return <div className="overview-page">
    <ResourceWarning error={error} retry={retry} />
    <PageHeading>
      <div>
        <h1>{t("app.overview")}</h1>
      </div>
      <div className="actions hero-actions">
        <Button variant="outline" onClick={() => navigate("/library")}>
          {t("overview.viewLibrary")}
          <ArrowRight size={15} />
        </Button>
        <Button variant="outline" onClick={() => void scan()} disabled={busy || !data.enabled}>
          <RefreshCw size={15} className={busy ? "animate-spin" : ""} />
          {t("overview.scan")}
        </Button>
      </div>
    </PageHeading>
    <div className="overview-summary">
      <div className="metrics overview-metrics">
        {metrics.map(metric => <button className="metric" data-tone={metric.tone} key={metric.label} onClick={() => navigate(metric.path)}>
          <div className="metric-header">
            <span className="metric-icon"><metric.icon size={13} /></span>
            <span className="metric-label">{metric.label}</span>
          </div>
          <div className="metric-body">
            <strong>{number(metric.value)}</strong>
            {metric.track ? <small>{`/ ${metric.track}`}</small> : null}
          </div>
        </button>)}
      </div>
      <section className="readiness-panel">
        <div className="completion-value">
          <span className="readiness-eyebrow">{t("overview.complete")}</span>
          <div className="completion-main-stat">
            <strong>{percentLabel}</strong>
            {active.length > 0 ? (
              <Badge status="running" />
            ) : (
              <span className={`readiness-status-badge ${percent === 100 ? "is-ready" : ""}`}>
                <span className="readiness-status-dot" />
                {percent === 100 ? t("overview.allReady") : t("overview.inProgress")}
              </span>
            )}
          </div>
        </div>
        <div className="hero-completion sr-only" role="progressbar" aria-label={t("overview.complete")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} />
        <div className="audio-pipeline-visual">
          <Disc3 size={18} className="pipeline-glyph" aria-hidden="true" />
          <div className="pipeline-flow">
            <span className="flow-line" />
            <span className="flow-badge">{`${data.codec.toUpperCase()} · ${profile}`}</span>
            <span className="flow-line" />
          </div>
          <AudioLines size={18} className="pipeline-glyph is-ready" aria-hidden="true" />
        </div>
        {active.length > 0 && (
          <div className="active-tracks">{active.map(({ job, activity }, index) => {
            const source = library.data?.find(source => source.path === activity.path);
            return <div className="active-track" key={`${job.repair_id || job.id}:${activity.path}:${index}`}>
              <span className="active-track-artwork" aria-hidden="true">{source?.artwork_url ? <img src={source.artwork_url} alt="" loading="lazy" /> : <Music2 size={16} />}</span>
              <div className="active-track-main">
                <div className="active-track-title">{activity.title || activity.path || kind(job.kind)}<span className="audio-eq-wave" aria-hidden="true"><i /><i /><i /><i /></span></div>
                <small>{[activity.artist, activity.album].filter(Boolean).join(" · ") || kind(job.kind)}</small>
              </div>
              <strong className="active-track-percent">{number(Math.round(activity.phase === "convert" ? activity.percent : taskPercent(job)))}%</strong>
            </div>;
          })}</div>
        )}
      </section>
    </div>
    <div className="overview-workspace">
      <section className="panel recent-panel">
        <div className="section-heading">
          <h2>{t("overview.recent")}</h2>
          <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>{t("overview.allJobs")}<ArrowRight size={14} /></Button>
        </div>
        <ResourceWarning error={jobs.error} retry={jobs.retry} />
        {!jobs.data ? <Loading error={jobs.error} retry={jobs.retry} /> : jobs.data.jobs.length ? <div className="job-list">{[...jobs.data.jobs].sort((a, b) => Number(b.state === "running") - Number(a.state === "running")).map(job => <button className="job-row" data-state={job.state} key={job.repair_id || job.id} onClick={() => navigate("/jobs")}>
          <div>
            <strong>{kind(job.kind)} #{job.repair_id?.slice(0, 8) || job.id}</strong>
            <small>{job.current[0]?.title || job.current[0]?.path || date(job.updated)}{job.counts.total > 0 && ` · ${number(job.counts.done)} / ${number(job.counts.total)}`}</small>
          </div>
          <Badge status={job.state} />
          <ArrowRight className="recent-arrow" size={14} />
        </button>)}</div> : <Empty title={t("overview.noJobs")}><Button variant="outline" onClick={() => navigate("/library")}>{t("overview.viewLibrary")}</Button></Empty>}
      </section>
      <aside className="overview-side">
        <section className="panel lyrics-summary">
          <div className="section-heading">
            <h2><FileMusic size={16} />{t("lyrics.title")}</h2>
            <Button variant="ghost" size="sm" onClick={() => navigate("/settings?section=lyrics")}>{t("lyrics.settings")}<ArrowRight size={13} /></Button>
          </div>
          <ResourceWarning error={library.error} retry={library.retry} />
          <div className="lyrics-overview">
            <div className="lyrics-stat-row">
              <span className="lyrics-stat-label">{t("overview.lyricCoverage")}</span>
              <strong className="lyrics-stat-val">{playable ? `${number(lyricReady || 0)} / ${number(playable.length)}` : "—"}</strong>
            </div>
            {lyricIssues && lyricIssues.length > 0 && <div className="lyrics-breakdown">{lyricIssues.map(item => {
              const Glyph = stateGlyph[item.state];
              return <button className="lyrics-state-row" key={item.state} onClick={() => navigate(`/library?view=tracks&lyrics=${item.state}`)}>
                <Glyph size={14} data-tone={item.state} aria-hidden="true" />
                <span className="lyrics-state-label">{t(`lyrics.state.${item.state}`)}</span>
                <strong>{number(item.count)}</strong>
                <ArrowRight size={13} className="lyrics-arrow" aria-hidden="true" />
              </button>;
            })}</div>}
          </div>
        </section>
        <section className="panel storage-summary">
          <div className="section-heading">
            <h2>{t("overview.storage")}</h2>
            <span className={`online ${data.online ? "is-online" : ""}`}><span />{data.online ? t("overview.online") : storageUnchecked ? t("overview.unchecked") : t("overview.waiting")}</span>
          </div>
          <ResourceWarning error={config.error} retry={config.retry} />
          <dl className="storage-paths">
            <div>
              <dt>{t("overview.flac")}</dt>
              <dd><code>{config.data?.settings.source || "—"}</code></dd>
            </div>
            <div>
              <dt>{t("overview.streaming")}</dt>
              <dd><code>{config.data?.settings.output || "—"}</code></dd>
            </div>
            <div>
              <dt>{t("storage.lastCheck")}</dt>
              <dd><code>{data.storage_checked_at ? date(data.storage_checked_at) : "—"}</code></dd>
            </div>
          </dl>
          <div className="storage-status" data-online={data.online} role="status" aria-live="polite">{!data.online && <>
            <div className="storage-status-header">
              <strong>{t(storageUnchecked ? "overview.unchecked" : data.enabled ? "overview.offline" : "overview.configure")}</strong>
              <Button variant="ghost" size="sm" onClick={() => navigate("/settings")}>{t("overview.openSettings")}<ArrowRight size={13} /></Button>
            </div>
            <p>{errorMessage(data.storage_message)}</p>
          </>}</div>
        </section>
        <section className="panel overview-attention">
          <div className="section-heading">
            <h2>{t("overview.attention")}</h2>
            {attentionCount === 0 && <span className="attention-clean-badge"><Check size={12} /> {t("overview.allGood")}</span>}
          </div>
          <div className="attention-list">
            {attention.map(item => <button key={item.state} onClick={() => navigate(`/library?view=tracks&status=${item.state}`)}>
              <Badge status={item.state} />
              <strong>{number(item.count)}</strong>
              <ArrowRight size={14} />
            </button>)}
          </div>
        </section>
      </aside>
    </div>
  </div>;
}
