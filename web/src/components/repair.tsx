import { useState } from "react";
import { AlertTriangle, ChevronRight, Loader2, Wrench, X } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import type { Notice } from "./resource";
import { Button } from "./ui/button";

export function RepairControls({ source, notify, refresh }: { source: Source; notify: Notice; refresh: () => void }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const repair = source.repair;
  if (!source.can_repair && !repair && !source.possibly_damaged) return null;
  if (source.possibly_damaged && source.status === "possibly_damaged" && repair?.state !== "running") return null;
  async function start() {
    if (!window.confirm(t("repair.confirm", { title: source.title || source.path }))) return;
    setBusy(true);
    try {
      await api(`/library/${source.id}/repair`, "POST", { hash: source.hash });
      notify("repair.started");
      refresh();
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  async function remove() {
    setBusy(true);
    try {
      await api(`/library/${source.id}/repair`, "DELETE");
      refresh();
    } catch (err) { notify(err as Error, true); }
    finally { setBusy(false); }
  }
  return <div className="repair-controls">
    {repair?.state === "running" ? <><span className="repair-progress" role="status"><Loader2 size={14} className="animate-spin" />{t("repair.running")}</span><Button variant="ghost" size="icon" disabled={busy} aria-label={t("repair.cancel")} title={t("repair.cancel")} onClick={() => void remove()}><X size={15} /></Button></> :
      source.possibly_damaged ? <>
        {source.status !== "possibly_damaged" && <span className="repair-warning"><AlertTriangle size={14} />{t("repair.damaged")}</span>}
      </> : <>
        {repair?.state === "failed" && <span className="repair-warning" role="status"><AlertTriangle size={14} />{t("repair.failed")}</span>}
        <Button variant="outline" size="sm" disabled={busy || !source.can_repair} onClick={() => void start()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Wrench size={14} />}{t("repair.start")}</Button>
      </>}
  </div>;
}

export function RepairDiagnostics({ source }: { source: Source }) {
  const { t, number } = useI18n();
  const repair = source.repair;
  return <div className="repair-details">
    {source.possibly_damaged && <small>{t("repair.imported")}</small>}
    {repair?.diagnostics && <details className="repair-diagnostic">
      <summary aria-label={`${t("repair.details")}: ${source.title || source.path}`}><ChevronRight size={14} aria-hidden="true" />{t("repair.details")}</summary>
      {source.possibly_damaged && <p>{t("repair.samples", { decoded: number(repair.decoded_samples || 0), declared: number(repair.declared_samples || 0) })}</p>}
      <pre>{repair.diagnostics}</pre>
    </details>}
  </div>;
}
