import { useState } from "react";
import { Clock3, RefreshCw, Square } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { LoudnessReport, LyricsProvider } from "../types";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";

const labels = { lyrics: "lyrics.title", loudness: "gain.title", tags: "tags.title", fingerprints: "fingerprint.title", storage: "progress.storage", undo: "operations.title" } as const;
type MaintenanceKind = keyof typeof labels;
type Report = LoudnessReport & { kind: MaintenanceKind; stopping?: boolean; waiting_reason?: "schedule" | "capacity" | "disabled" };
type Progress = { reports: Report[]; cooldowns: { provider: LyricsProvider; retry_at: number }[] };

export function MaintenanceProgress({ notify }: { notify: Notice }) {
  const { t, number, date, errorMessage } = useI18n();
  const resource = useResource<Progress>("/maintenance/progress", true);
  const [busy, setBusy] = useState<MaintenanceKind>();
  async function stop(kind: MaintenanceKind) {
    setBusy(kind);
    try { await api(kind === "lyrics" ? "/lyrics/search" : `/maintenance/${kind}`, "DELETE"); resource.retry(); }
    catch (err) { notify(err as Error, true); }
    finally { setBusy(undefined); }
  }
  return <section className="panel maintenance-progress" id="maintenance-progress" aria-label={t("progress.title")}>
    <div className="section-heading"><h2>{t("progress.title")}</h2><Button size="icon" variant="ghost" aria-label={t("progress.refresh")} onClick={resource.retry}><RefreshCw size={15} /></Button></div>
    <p>{t("progress.hint")}</p>
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {!resource.data ? <Loading error={resource.error} retry={resource.retry} /> : <>
      {!resource.data.reports.length && <p>{t("progress.empty")}</p>}
      <div className="maintenance-progress-list">{[...resource.data.reports].sort((a, b) => Number(b.state === "running") - Number(a.state === "running")).map(report => {
        const label = t(labels[report.kind]);
        const percent = report.total > 0 ? Math.min(100, Math.max(0, Math.round(report.processed / report.total * 100))) : 0;
        return <div className="maintenance-progress-row" key={report.kind} data-state={report.state}>
          <div className="section-heading"><h3>{label}</h3><span>{t(report.stopping ? "lyrics.stopping" : `gain.state.${report.state}`)}</span></div>
          <p>{t("progress.counts", { done: number(report.processed), total: number(report.total), failed: number(report.failed), skipped: number(report.skipped) })}</p>
          {report.current && <code>{report.current}</code>}
          {report.state === "running" && <div className="task-meter" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><span style={{ width: `${percent}%` }} /></div>}
          {report.waiting_reason && report.state === "running" && <p className="task-waiting" role="status"><Clock3 size={15} />{t(`progress.wait.${report.waiting_reason}`)}</p>}
          {report.error && <details><summary>{t("jobs.diagnostics")}</summary><p>{errorMessage(report.error)}</p></details>}
          {report.state === "running" && <div className="actions"><Button size="sm" variant="outline" disabled={busy !== undefined || report.stopping} onClick={() => void stop(report.kind)}><Square size={13} />{t(report.kind === "lyrics" ? "lyrics.stop" : "tools.stop")}</Button>{report.kind === "lyrics" && <small>{t("lyrics.stopHint")}</small>}</div>}
        </div>;
      })}</div>
      {!!resource.data.cooldowns.length && <details className="provider-cooldowns"><summary>{t("progress.cooldowns")}</summary><p>{t("progress.cooldownHint")}</p>{resource.data.cooldowns.map(item => <p key={item.provider}>{t(`lyrics.name.${item.provider}`)} · {t("jobs.waitingUntil", { date: date(item.retry_at) })}</p>)}</details>}
    </>}
  </section>;
}

type FailureReason = "permission" | "space" | "storage" | "rate_limit" | "network" | "read" | "decode" | "encode" | "validate" | "other";
type FailureGroup = { reason: FailureReason; jobs: number; retryable_jobs: number; lyrics: number; lyric_ids: number[]; samples: { kind: "jobs" | "lyrics"; id: number; path?: string; error: string }[] };

export function MaintenanceFailures({ notify, refresh }: { notify: Notice; refresh: () => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return <details className="panel maintenance-failures" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{t("failures.title")}</summary>
    {open && <FailureGroups notify={notify} refresh={refresh} />}
  </details>;
}

function FailureGroups({ notify, refresh }: { notify: Notice; refresh: () => void }) {
  const { t, errorMessage } = useI18n();
  const resource = useResource<FailureGroup[]>("/maintenance/failures", true);
  const [busy, setBusy] = useState(false);
  async function retry(group: FailureGroup, kind: "jobs" | "lyrics") {
    setBusy(true);
    try {
      if (kind === "jobs") {
        const result = await api<{ changed: number }>("/jobs/control", "POST", { action: "retry", all: true, failure: group.reason });
        notify(result.changed ? "notice.taskRetry" : "notice.noTasksChanged", false, { count: result.changed });
      } else {
        const result = await api<{ selected: number }>("/lyrics/search", "POST", { ids: group.lyric_ids });
        notify(result.selected ? "lyrics.started" : "lyrics.nothingPending");
      }
      resource.retry(); refresh();
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  const destinations: Record<FailureReason, { href: string; label: MessageKey }> = {
    permission: { href: "/settings?section=library", label: "overview.openSettings" },
    space: { href: "/library#maintenance-workbench", label: "failures.openTools" },
    storage: { href: "/settings?section=library", label: "overview.openSettings" },
    rate_limit: { href: "#maintenance-progress", label: "failures.openProgress" },
    network: { href: "/settings?section=integrations", label: "overview.openSettings" },
    read: { href: "/library?view=tracks&status=failed", label: "overview.viewLibrary" },
    decode: { href: "/library?view=tracks&status=failed", label: "overview.viewLibrary" },
    encode: { href: "/settings?section=encoding", label: "overview.openSettings" },
    validate: { href: "/library?view=tracks&status=failed", label: "overview.viewLibrary" },
    other: { href: "/library?view=tracks&lyrics=failed", label: "overview.viewLibrary" },
  };
  return <div className="comparison-body">
    <p>{t("failures.hint")}</p>
    <ResourceWarning error={resource.error} retry={resource.retry} />
    {!resource.data ? <Loading error={resource.error} retry={resource.retry} /> : !resource.data.length ? <p>{t("failures.empty")}</p> : resource.data.map(group => <section className="maintenance-report" key={group.reason} aria-label={t(`failures.reason.${group.reason}`)}>
      <h3>{t(`failures.reason.${group.reason}`)}</h3>
      <p>{t("failures.counts", { jobs: group.jobs, lyrics: group.lyrics })}</p>
      <p>{t(`failures.help.${group.reason}`)}</p>
      <div className="actions"><a href={destinations[group.reason].href}>{t(destinations[group.reason].label)}</a>
        {group.jobs > 0 && <Button size="sm" variant="outline" disabled={busy || !group.retryable_jobs} onClick={() => void retry(group, "jobs")}><RefreshCw size={14} />{t("failures.retryJobs", { count: group.retryable_jobs })}</Button>}
        {group.lyric_ids.length > 0 && <Button size="sm" variant="outline" disabled={busy} onClick={() => void retry(group, "lyrics")}><RefreshCw size={14} />{t("failures.retryLyrics", { count: group.lyric_ids.length })}</Button>}
      </div>
      {group.retryable_jobs < group.jobs && <p>{t("failures.held")}</p>}
      {group.lyrics > group.lyric_ids.length && <p>{t("failures.lyricsLimit")}</p>}
      <details><summary>{t("failures.samples")}</summary>{group.samples.map(sample => <div key={`${sample.kind}-${sample.id}`}><strong>{sample.path || `#${sample.id}`}</strong><p>{errorMessage(sample.error)}</p></div>)}</details>
    </section>)}
  </div>;
}
