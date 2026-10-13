import { useState } from "react";
import { ChevronLeft, ChevronRight, Loader2, Square, Trash2, Wrench } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey } from "../i18n";
import type { Activity, Encoding } from "../types";
import { Badge, Loading, ResourceWarning, useResource, type Notice } from "./resource";
import { Button } from "./ui/button";

type RepairTask = {
  id: string; source_id: number; source_hash: string; state: "running" | "success" | "failed" | "stopped";
  activity: Activity; profile: Encoding; progress: number; created: number; updated: number; output: string; log: string; stopping: boolean;
};

export function RepairTasks({ notify }: { notify: Notice }) {
  const { t, date, errorMessage, status } = useI18n();
  const [filter, setFilter] = useState("all");
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState("");
  const { data, error, retry } = useResource<{ tasks: RepairTask[]; total: number }>(`/repair-tasks?state=${filter}&offset=${offset}`, 2000);
  async function control(task: RepairTask, action: "stop" | "delete") {
    if (!window.confirm(t(action === "stop" ? "jobs.stopConfirm" : "jobs.deleteConfirm"))) return;
    setBusy(task.id);
    try {
      await api(`/repair-tasks/${task.id}${action === "stop" ? "/stop" : ""}`, action === "stop" ? "POST" : "DELETE");
      retry(); notify(action === "stop" ? "repairTasks.stopping" : "repairTasks.deleted");
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(""); }
  }
  function libraryURL(path: string) {
    const parts = path.split("/");
    return parts.length > 2 ? `/library?${new URLSearchParams({ view: "artists", artist: parts[0], album: parts.slice(0, 2).join("/") })}` : "/library";
  }
  return <section className="panel repair-tasks" aria-labelledby="repair-tasks-title">
    <div className="panel-heading"><div><h2 id="repair-tasks-title">{t("repairTasks.title")}</h2><p>{t("repairTasks.description")}</p></div></div>
    <div className="table-toolbar"><div className="filter-tabs" role="group" aria-label={t("repairTasks.filter")}>{["all", "running", "success", "failed", "stopped"].map(state => <button key={state} className={filter === state ? "active" : ""} aria-pressed={filter === state} onClick={() => { setFilter(state); setOffset(0); }}>{state === "all" ? t("jobs.all") : status(state)}</button>)}</div></div>
    <ResourceWarning error={error} retry={retry} />
    {!data ? <Loading error={error} retry={retry} /> : <>
      {data.tasks.length ? <div className="task-list">{data.tasks.map(task => {
        const activity = task.activity;
        const percent = Math.round(Math.max(0, Math.min(1, task.progress)) * 100);
        const phase = ["storage", "read", "staging_wait", "convert", "validate", "publish"].includes(activity.phase) ? t(`phase.${activity.phase}` as MessageKey) : activity.phase;
        const profile = `${task.profile.codec.toUpperCase()} · ${task.profile.mode.toUpperCase()} · ${task.profile.codec === "mp3" && task.profile.mode === "vbr" ? `V${task.profile.quality}` : `${task.profile.bitrate} kbps`}`;
        return <article className="task" key={task.id} data-repair-task-id={task.id}>
          <div className="task-main"><span className={`job-symbol ${task.state}`}>{task.state === "running" ? <Loader2 size={18} className="animate-spin" /> : <Wrench size={18} />}</span><div className="task-description"><strong>{activity.title || activity.path}</strong><small>{[activity.artist, activity.album].filter(Boolean).join(" · ")}</small></div><Badge status={task.state} />
            <div className="task-actions">{task.state === "running" ? <Button variant="outline" size="sm" disabled={busy === task.id || task.stopping} onClick={() => void control(task, "stop")}><Square size={14} />{t(task.stopping ? "repairTasks.stopping" : "jobs.stop")}</Button> : <Button variant="ghost" size="sm" disabled={busy === task.id} onClick={() => void control(task, "delete")}><Trash2 size={14} />{t("jobs.delete")}</Button>}</div>
          </div>
          <div className="task-progress"><small className="task-path">{activity.path}</small><small>{profile}</small>
            {task.state === "running" && <><div className="task-progress-label"><span>{task.stopping ? t("repairTasks.stopping") : phase}</span><span>{percent}%</span></div><div className="task-meter" role="progressbar" aria-label={t("repairTasks.progress", { title: activity.title || activity.path })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><span style={{ width: `${percent}%` }} /></div>
              {activity.phase === "read" && (activity.read_total_bytes || 0) > 0 && <p>{t("jobs.readBytes", { read: ((activity.read_bytes || 0) / 1048576).toFixed(1), total: ((activity.read_total_bytes || 0) / 1048576).toFixed(1) })}</p>}
              {activity.phase === "convert" && <p>{t("jobs.songPercent", { percent: Math.round(activity.percent) })}</p>}
            </>}
            {task.state === "success" && <p className="repair-warning">{t("repairTasks.imported")}</p>}
            {task.output && <small className="task-path">{t("jobs.output", { path: task.output })}</small>}
            <details className="task-diagnostic"><summary>{t("jobs.details")}</summary><dl className="task-facts"><div><dt>{t("jobs.created")}</dt><dd>{date(task.created)}</dd></div><div><dt>{t("jobs.updated")}</dt><dd>{date(task.updated)}</dd></div></dl>{task.log && <pre>{errorMessage(task.log)}</pre>}<Button variant="outline" size="sm" asChild><a href={libraryURL(activity.path)}>{t("repairTasks.library")}</a></Button></details>
          </div>
        </article>;
      })}</div> : <p className="repair-tasks-empty">{t("repairTasks.empty")}</p>}
      <div className="pagination"><span>{t("jobs.total", { count: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("repairTasks.previous")} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 25))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("repairTasks.next")} disabled={offset + 25 >= data.total} onClick={() => setOffset(offset + 25)}><ChevronRight size={16} /></Button></div>
    </>}
  </section>;
}
