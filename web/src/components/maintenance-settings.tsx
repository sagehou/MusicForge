import { useState } from "react";
import { Loader2, Send } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { MaintenanceOptions } from "../types";
import type { Notice } from "./resource";
import { Button } from "./ui/button";

export const defaultMaintenance: MaintenanceOptions = { version: 1, schedule_enabled: false, start: "22:00", end: "07:00", timezone: "UTC", day_concurrency: 0, prioritize_new: false, notify_url: "", notify_mode: "webhook", notify_completion: false };
type Props = { value: MaintenanceOptions; change: (value: MaintenanceOptions) => void };

export function MaintenanceSchedule({ value, change }: Props) {
  const { t } = useI18n();
  function update<K extends keyof MaintenanceOptions>(key: K, next: MaintenanceOptions[K]) { change({ ...value, [key]: next }); }
  return <><div className="settings-subheading"><h3>{t("schedule.title")}</h3><p>{t("schedule.hint")}</p></div>
    <label className="check-field"><input type="checkbox" checked={value.prioritize_new} onChange={event => update("prioritize_new", event.target.checked)} />{t("schedule.priority")}</label>
    <label className="check-field"><input type="checkbox" checked={value.schedule_enabled} onChange={event => update("schedule_enabled", event.target.checked)} />{t("schedule.enabled")}</label>
    <div className="form-grid"><label className="field"><span className="field-label">{t("schedule.start")}</span><input type="time" required disabled={!value.schedule_enabled} value={value.start} onChange={event => update("start", event.target.value)} /></label><label className="field"><span className="field-label">{t("schedule.end")}</span><input type="time" required disabled={!value.schedule_enabled} value={value.end} onChange={event => update("end", event.target.value)} /></label><label className="field"><span className="field-label">{t("schedule.timezone")}</span><input required disabled={!value.schedule_enabled} value={value.timezone} placeholder="Asia/Shanghai" onChange={event => update("timezone", event.target.value.trim())} /></label><label className="field"><span className="field-label">{t("schedule.day")}</span><input type="number" min={0} max={16} required disabled={!value.schedule_enabled} value={value.day_concurrency} onChange={event => update("day_concurrency", Number(event.target.value))} /></label></div>
  </>;
}

export function NotificationSettings({ value, change, tokenConfigured, clearToken, setClearToken, dirty, notify }: Props & { tokenConfigured: boolean; clearToken: boolean; setClearToken: (value: boolean) => void; dirty: boolean; notify: Notice }) {
  const { t } = useI18n();
  const [testing, setTesting] = useState(false);
  async function test() {
    setTesting(true);
    try { await api("/maintenance/notify-test", "POST", {}); notify("notifications.sent"); }
    catch (err) { notify(err as Error, true); }
    finally { setTesting(false); }
  }
  return <><div className="settings-subheading"><h3>{t("notifications.title")}</h3><p>{t("notifications.hint")}</p></div><div className="form-grid"><label className="field"><span className="field-label">{t("notifications.url")}</span><input type="url" value={value.notify_url} placeholder="https://ntfy.example.com/musicforge" onChange={event => change({ ...value, notify_url: event.target.value, notify_token: "" })} /></label><label className="field"><span className="field-label">{t("notifications.mode")}</span><select value={value.notify_mode} onChange={event => change({ ...value, notify_mode: event.target.value as MaintenanceOptions["notify_mode"] })}><option value="webhook">Webhook JSON</option><option value="ntfy">ntfy</option></select></label><label className="field"><span className="field-label">{t("notifications.token")}</span><input type="password" aria-label={t("notifications.token")} autoComplete="new-password" disabled={clearToken} value={value.notify_token || ""} onChange={event => change({ ...value, notify_token: event.target.value })} />{tokenConfigured && <small>{t("settings.keepSecret")}</small>}</label></div><label className="check-field"><input type="checkbox" checked={clearToken} disabled={!tokenConfigured} onChange={event => setClearToken(event.target.checked)} />{t("notifications.clear")}</label><label className="check-field"><input type="checkbox" checked={value.notify_completion} onChange={event => change({ ...value, notify_completion: event.target.checked })} />{t("notifications.completion")}</label><div className="actions"><Button type="button" variant="outline" size="sm" disabled={testing || dirty || !value.notify_url} onClick={() => void test()}>{testing ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}{t("notifications.test")}</Button><small>{t("notifications.savedOnly")}</small></div></>;
}
