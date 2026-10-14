import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useI18n } from "../i18n";
import type { Job, RepairTask } from "../types";
import { Badge, Loading, ResourceWarning, useResource } from "./resource";
import { Button } from "./ui/button";

export function RepairTaskDetails({ job }: { job: Job }) {
  const { t, date, errorMessage } = useI18n();
  const [offset, setOffset] = useState(0);
  const [onlyFailed, setOnlyFailed] = useState(false);
  const { data, error, retry } = useResource<{ tasks: RepairTask[]; total: number }>(`/repair-tasks?group=${job.repair_id}&state=${onlyFailed ? "failed" : "all"}&limit=50&offset=${offset}`, 2000);
  function libraryURL(path: string) {
    const parts = path.split("/");
    return parts.length > 2 ? `/library?${new URLSearchParams({ view: "artists", artist: parts[0], album: parts.slice(0, 2).join("/") })}` : "/library";
  }
  return <div className="task-details">
    <dl className="task-facts"><div><dt>{t("jobs.scope")}</dt><dd>{t("jobs.selectedTracks")}</dd></div><div><dt>{t("jobs.created")}</dt><dd>{date(job.created)}</dd></div><div><dt>{t("jobs.updated")}</dt><dd>{date(job.updated)}</dd></div></dl>
    <p>{t("repairTasks.description")}</p>
    <div className="task-result-heading"><h3>{t("jobs.trackResults")}</h3><label className="check-field"><input type="checkbox" checked={onlyFailed} onChange={event => { setOnlyFailed(event.target.checked); setOffset(0); }} />{t("jobs.onlyFailed")}</label></div>
    <ResourceWarning error={error} retry={retry} />
    {!data ? <Loading error={error} retry={retry} /> : <>
      <div className="task-results">{data.tasks.map(task => <div className="task-result" key={task.id}>
        <div><strong>{task.activity.title || task.activity.path}</strong><small>{[task.activity.artist, task.activity.album].filter(Boolean).join(" · ")}</small><small className="task-path">{task.activity.path}</small>{task.output && <small>{t("jobs.output", { path: task.output })}</small>}</div>
        <Badge status={task.state === "running" && task.activity.phase === "repair_wait" ? "pending" : task.state} />
        {task.state === "success" && <p className="repair-warning">{t("repairTasks.imported")}</p>}
        {task.log && <details className="task-diagnostic"><summary>{t("jobs.diagnostics")}</summary><pre>{errorMessage(task.log)}</pre></details>}
        <Button variant="outline" size="sm" asChild><a href={libraryURL(task.activity.path)}>{t("repairTasks.library")}</a></Button>
      </div>)}</div>
      {data.total === 0 && <p>{t("jobs.noResults")}</p>}
      <div className="pagination"><span>{t("jobs.itemsPage", { start: data.total ? offset + 1 : 0, end: Math.min(offset + 50, data.total), total: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("jobs.previousResults")} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("jobs.nextResults")} disabled={offset + 50 >= data.total} onClick={() => setOffset(offset + 50)}><ChevronRight size={16} /></Button></div>
    </>}
  </div>;
}
