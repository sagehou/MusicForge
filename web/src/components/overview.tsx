import { useState } from "react";
import { Activity, ArrowDownToLine, ArrowRight, Check, Clock3, Disc3, HardDrive, Loader2, Music2, RefreshCw, Waves } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { taskPercent } from "../lib/utils";
import type { Dashboard, Job, Settings } from "../types";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";

export function Overview({ notify, navigate }: { notify: Notice; navigate: (path: string) => void }) {
  const { t, kind, date, number, errorMessage, locale } = useI18n();
  const { data, error, retry } = useResource<Dashboard>("/dashboard", true);
  const jobs = useResource<{ jobs: Job[] }>("/jobs?limit=5&include_repairs=true", true);
  const config = useResource<{ settings: Settings }>("/settings");
  const [busy, setBusy] = useState(false);
  async function scan() {
    setBusy(true);
    try { await api("/library/scan", "POST", {}); notify("notice.scan"); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  if (!data) return <Loading error={error} retry={retry} />;
  const percent = data.source_count ? Math.max(0, Math.min(100, Math.round(data.ready_count / data.source_count * 100))) : 0;
  const percentLabel = new Intl.NumberFormat(locale, { style: "percent" }).format(percent / 100);
  const encoding = config.data?.settings.encoding;
  const profile = encoding ? `${encoding.mode.toUpperCase()} · ${encoding.codec === "mp3" && encoding.mode === "vbr" ? `V${encoding.quality}` : `${number(encoding.bitrate)} kbps`}` : "—";
  const metrics = [
    { label: t("overview.sources"), value: data.source_count, icon: Disc3, tone: "source", note: t("overview.masters") },
    { label: t("overview.outputs"), value: data.output_count, icon: Music2, tone: "output", note: t("overview.outputLibrary", { codec: data.codec.toUpperCase() }) },
    { label: t("overview.profileReady"), value: data.ready_count, icon: Check, tone: "ready", note: t("overview.currentProfile") },
    { label: t("overview.attention"), value: data.expired_count + data.rebuild_count + data.failed_count, icon: Clock3, tone: "attention", note: t("overview.attentionCounts", { rebuild: data.rebuild_count, expired: data.expired_count, failed: data.failed_count }) },
  ];
  return <div className="overview-page">
    <ResourceWarning error={error} retry={retry} />
    <section className="overview-hero">
      <div className="hero-copy">
        <div className="hero-kicker"><span><Waves size={16} />{t("overview.eyebrow")}</span><span className={`online ${data.online ? "is-online" : ""}`}><span />{data.online ? t("overview.online") : data.storage_checking ? t("overview.checkingShort") : t("overview.waiting")}</span></div>
        <h1>{t("overview.title")}</h1><p>{t("overview.description")}</p>
        <div className="hero-actions"><Button className="hero-primary" onClick={() => void scan()} disabled={busy || !data.enabled}><RefreshCw size={16} className={busy ? "animate-spin" : ""} />{t("overview.scan")}</Button><Button className="hero-secondary" variant="outline" onClick={() => navigate("/library")}>{t("overview.viewLibrary")}<ArrowRight size={16} /></Button></div>
        <div className="hero-profile"><span>{data.codec.toUpperCase()}</span><strong>{profile}</strong><span>{t("overview.buildEyebrow")}</span></div>
      </div>
      <div className="hero-visual">
        <div className="hero-completion" role="progressbar" aria-label={t("overview.complete")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
          <svg className="completion-orbit" viewBox="0 0 240 240" aria-hidden="true">
            <circle className="orbit-track" cx="120" cy="120" r="90" />
            <circle className="orbit-progress" cx="120" cy="120" r="90" transform="rotate(-90 120 120)" strokeDasharray={Math.PI * 180} strokeDashoffset={Math.PI * 180 * (1 - percent / 100)} />
          </svg>
          <div className="completion-value"><span>{t("overview.complete")}</span><strong>{percentLabel}</strong><small>{number(data.ready_count)} / {number(data.source_count)}</small></div>
        </div>
        <span className="hero-caption">{t("overview.currentProfile")}</span>
      </div>
    </section>
    {!data.online && <div className="banner"><HardDrive size={20} /><div><strong>{data.storage_checking ? t("overview.checking") : data.enabled ? t("overview.offline") : t("overview.configure")}</strong><p>{errorMessage(data.storage_message)}</p></div><Button variant="outline" size="sm" onClick={() => navigate("/settings")}>{t("overview.openSettings")}</Button></div>}
    {data.rebuild_count > 0 && <div className="banner"><RefreshCw size={20} /><div><strong>{t("overview.rebuildCount", { count: data.rebuild_count })}</strong><p>{t("overview.keepPlayable")}</p></div><Button variant="outline" size="sm" onClick={() => navigate("/library")}>{t("overview.viewLibrary")}</Button></div>}
    <div className="metrics overview-metrics">{metrics.map(metric => <div className="metric" data-tone={metric.tone} key={metric.tone}><div><span>{metric.label}</span><metric.icon size={18} /></div><strong>{number(metric.value)}</strong><small>{metric.note}</small></div>)}</div>
    <div className="overview-grid">
      <section className="panel forge-pipeline"><div className="panel-heading"><div><h2>{t("overview.pipeline")}</h2><p>{t("overview.pipelineDescription")}</p></div><Waves size={20} className="panel-mark" /></div>
        <div className="forge-stages">
          <div className="forge-stage"><span className="stage-icon"><Disc3 size={21} /></span><div><small>{t("overview.sourceEyebrow")}</small><h3>{t("overview.flac")}</h3><code>{config.data?.settings.source || "—"}</code></div><strong>{number(data.source_count)}</strong></div>
          <div className="forge-stage stage-build"><span className="stage-icon"><Waves size={21} /></span><div><small>{t("overview.buildEyebrow")}</small><h3>MusicForge <span>· ffmpeg</span></h3><span>{data.codec.toUpperCase()} · {profile}</span></div><Button variant="ghost" size="icon" aria-label={t("overview.openSettings")} onClick={() => navigate("/settings")}><ArrowRight size={17} /></Button></div>
          <div className="forge-stage"><span className="stage-icon"><ArrowDownToLine size={21} /></span><div><small>{t("overview.outputEyebrow")}</small><h3>{t("overview.streaming")}</h3><code>{config.data?.settings.output || "—"}</code></div><strong>{number(data.output_count)}</strong></div>
        </div>
        <div className="pipeline-foot"><Check size={15} /><span>{t("overview.readyCount", { count: data.ready_count })}</span></div>
      </section>
      <section className="panel recent-panel"><div className="panel-heading"><div><h2>{t("overview.recent")}</h2><p>{t("overview.background")}</p></div><Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>{t("overview.allJobs")}<ArrowRight size={14} /></Button></div>
        <ResourceWarning error={jobs.error} retry={jobs.retry} />
        {!jobs.data ? <Loading error={jobs.error} retry={jobs.retry} /> : jobs.data.jobs.length ? <div className="job-list">{jobs.data.jobs.map(job => <button className="job-row" data-state={job.state} key={job.repair_id || job.id} onClick={() => navigate("/jobs")}>
          <span className={`job-symbol ${job.state}`}>{job.state === "running" ? <Loader2 size={17} className="animate-spin" /> : <Activity size={17} />}</span><div><strong>{kind(job.kind)}</strong><small>{job.current[0]?.title || job.current[0]?.path || t("overview.job", { id: job.repair_id?.slice(0, 8) || String(job.id), date: date(job.updated) })}</small><div className="recent-meter" aria-hidden="true"><span style={{ width: `${taskPercent(job)}%` }} /></div></div><Badge status={job.state} /><ArrowRight className="recent-arrow" size={15} />
        </button>)}</div> : <Empty title={t("overview.noJobs")}>{t("overview.noJobsHint")}</Empty>}
      </section>
    </div>
  </div>;
}
