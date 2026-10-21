import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { Activity, ArrowRight, Check, ChevronLeft, ChevronRight, Clock3, FolderOpen, HardDrive, LayoutDashboard, ListMusic, Loader2, LogOut, PanelLeftClose, PanelLeftOpen, Pause, Play, RefreshCw, Settings2, ShieldCheck, Square, Trash2, Waves, X } from "lucide-react";
import { Button } from "./components/ui/button";
import { api, loadSession, setCSRF } from "./api";
import type { Activity as JobActivity, Encoding, Job, Me, Settings, TaskItem } from "./types";
import { LanguageSelector, useI18n, type MessageKey, type Params } from "./i18n";
import { Badge, Empty, Loading, ResourceWarning, useResource, type Notice } from "./components/resource";
import { ThemeSelector } from "./components/theme-selector";
import { Library } from "./components/library";
import { BatchRepairButton } from "./components/repair";
import { RepairTaskDetails } from "./components/repair-tasks";
import { Overview } from "./components/overview";
import { DetailPanel, DetailPanelSuspended } from "./components/detail-panel";
import { taskPercent } from "./lib/utils";

function Field({ label, hint, children, action }: { label: string; hint?: string; children: ReactNode; action?: ReactNode }) { return <div className="field"><div className={action ? "input-action" : undefined}><label className="field-label"><span>{label}</span>{children}</label>{action}</div>{hint && <small>{hint}</small>}</div>; }

export function App() {
  const { t, errorMessage } = useI18n();
  const [me, setMe] = useState<Me>();
  const lastMe = useRef<Me | undefined>(undefined);
  const [locked, setLocked] = useState<Me>();
  const wasLocked = useRef(false);
  const [authError, setAuthError] = useState("");
  const authRequest = useRef<Promise<void> | undefined>(undefined);
  const [page, setPage] = useState(location.pathname);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem("musicforge.sidebar") === "collapsed"; } catch { return false; }
  });
  function toggleSidebar() {
    const next = !sidebarCollapsed;
    setSidebarCollapsed(next);
    try { localStorage.setItem("musicforge.sidebar", next ? "collapsed" : "expanded"); } catch { /* Available without browser storage. */ }
  }
  const [toast, setToast] = useState<{ message: MessageKey | Error; error: boolean; params?: Params }>();
  const notify: Notice = (message, error = false, params) => setToast({ message, error, params });
  const acceptSession = (value: Me) => {
    if (!value.authenticated && lastMe.current?.authenticated) {
      setLocked(value); setToast(undefined);
    } else {
      lastMe.current = value; setMe(value); setLocked(undefined);
    }
    setAuthError("");
  };
  const loadMe = () => {
    if (!authRequest.current) authRequest.current = loadSession().then(acceptSession).catch(err => setAuthError(err.message)).finally(() => { authRequest.current = undefined; });
    return authRequest.current;
  };
  async function logout() {
    try {
      await api("/auth/logout", "POST", {});
      lastMe.current = undefined; setLocked(undefined);
      await loadMe();
    } catch (err) { notify(err as Error, true); }
  }
  useEffect(() => {
    void loadMe();
    const pop = () => setPage(location.pathname);
    const expired = () => { void loadMe(); };
    const updated = (event: Event) => acceptSession((event as CustomEvent<Me>).detail);
    const visible = () => { if (document.visibilityState === "visible") void loadMe(); };
    const restored = (event: PageTransitionEvent) => { if (event.persisted) visible(); };
    const timer = setInterval(visible, 30 * 60 * 1000);
    window.addEventListener("popstate", pop); window.addEventListener("session-expired", expired);
    window.addEventListener("session-updated", updated); window.addEventListener("pageshow", restored);
    document.addEventListener("visibilitychange", visible);
    return () => {
      clearInterval(timer);
      window.removeEventListener("popstate", pop); window.removeEventListener("session-expired", expired);
      window.removeEventListener("session-updated", updated); window.removeEventListener("pageshow", restored);
      document.removeEventListener("visibilitychange", visible);
    };
  }, []);
  useEffect(() => { if (toast && !toast.error) { const timer = setTimeout(() => setToast(undefined), 6000); return () => clearTimeout(timer); } }, [toast]);
  useEffect(() => { document.getElementById("main-content")?.focus(); }, [page, me?.authenticated]);
  useEffect(() => {
    if (wasLocked.current && !locked) document.getElementById("main-content")?.focus();
    wasLocked.current = Boolean(locked);
  }, [locked]);
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      document.querySelectorAll<HTMLDetailsElement>(".preferences[open], .page-tools[open]").forEach(menu => {
        if (!menu.contains(event.target as Node)) menu.open = false;
      });
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, []);
  function navigate(path: string) { history.pushState(null, "", path); setPage(location.pathname); window.dispatchEvent(new Event("route-changed")); window.scrollTo(0, 0); }
  if (!me) return <div className="auth-layout"><div className="appearance-controls"><LanguageSelector /><ThemeSelector /></div><Loading error={authError} />{authError && <Button onClick={() => void loadMe()}>{t("app.reconnect")}</Button>}</div>;
  if (!me.authenticated) return <Auth me={me} done={loadMe} recoveryError={authError} />;
  const nav = [{ path: "/", label: t("app.overview"), icon: LayoutDashboard }, { path: "/library", label: t("app.library"), icon: ListMusic }, { path: "/jobs", label: t("app.jobs"), icon: Activity }, { path: "/settings", label: t("app.settings"), icon: Settings2 }];
  const current = nav.find(item => item.path === page) || nav[0];
  return <DetailPanelSuspended.Provider value={Boolean(locked)}><div className="app-shell" data-sidebar-collapsed={sidebarCollapsed} inert={locked ? true : undefined} aria-hidden={locked ? true : undefined}><a className="skip-link" href="#main-content">{t("app.skipContent")}</a>
    <aside className="sidebar">
      <div className="brand-row"><a className="brand" href="/" aria-label="MusicForge" title="MusicForge" onClick={e => { e.preventDefault(); navigate("/"); }}><span className="brand-icon"><Waves /></span><span>MusicForge</span></a><Button className="sidebar-toggle" variant="ghost" size="icon" aria-label={t(sidebarCollapsed ? "app.expandSidebar" : "app.collapseSidebar")} title={t(sidebarCollapsed ? "app.expandSidebar" : "app.collapseSidebar")} aria-expanded={!sidebarCollapsed} aria-controls="main-navigation" onClick={toggleSidebar}>{sidebarCollapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}</Button></div>
      <nav id="main-navigation" aria-label={t("app.navigation")}>{nav.map(item => <a key={item.path} href={item.path} aria-label={item.label} title={item.label} aria-current={current.path === item.path ? "page" : undefined} className={current.path === item.path ? "active" : ""} onClick={e => { e.preventDefault(); navigate(item.path); }}><item.icon size={19} /><span className="nav-label">{item.label}</span>{current.path === item.path && <span className="nav-dot" />}</a>)}</nav>
      <div className="sidebar-bottom"><div className="single-host"><HardDrive size={16} /><span>MusicForge<small>{me.version}</small></span></div><details className="preferences" onKeyDown={event => { if (event.key === "Escape") { event.currentTarget.open = false; event.currentTarget.querySelector("summary")?.focus(); } }}><summary aria-label={t("app.preferences")} title={t("app.preferences")}><span className="avatar">{me.username?.slice(0, 1).toUpperCase()}</span><span className="account-name">{me.username}<small>{t("app.preferences")}</small></span><Settings2 size={17} /></summary><div className="preferences-popover"><strong>{me.username}</strong><small>{me.method === "oidc" ? t("app.oidcAdmin") : t("app.localAdmin")}</small><LanguageSelector /><ThemeSelector /><Button variant="ghost" onClick={() => void logout()}><LogOut size={16} />{t("app.logout")}</Button></div></details></div>
    </aside>
    <main className="main" id="main-content" tabIndex={-1}><header className="mobile-header"><span><Waves size={18} />MusicForge</span></header>
      <div className="page-content"><ResourceWarning error={authError} retry={() => void loadMe()} />{page === "/library" ? <Library notify={notify} /> : page === "/jobs" ? <Jobs notify={notify} /> : page === "/settings" ? <SettingsPage me={me} notify={notify} /> : <Overview notify={notify} navigate={navigate} />}</div>
    </main>
    {toast && <div className={`toast ${toast.error ? "toast-error" : ""}`} role={toast.error ? "alert" : "status"}>{toast.error ? <Activity size={17} /> : <Check size={17} />}<span>{toast.message instanceof Error ? errorMessage(toast.message.message) : t(toast.message, toast.params)}</span><button aria-label={t("app.dismiss")} title={t("app.dismiss")} onClick={() => setToast(undefined)}><X size={17} /></button></div>}
  </div>{locked && <div className="session-dialog" role="dialog" aria-modal="true" aria-label={t("auth.expired")} onKeyDown={event => {
    if (event.key !== "Tab") return;
    const nodes = event.currentTarget.querySelectorAll<HTMLElement>('input,select,button,a[href]');
    const focusable = [...nodes].filter(node => !node.hasAttribute("disabled"));
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }}><Auth me={locked} done={loadMe} username={me.username} recoveryError={authError} expired /></div>}</DetailPanelSuspended.Provider>;
}

function Auth({ me, done, username, recoveryError = "", expired = false }: { me: Me; done: () => Promise<void>; username?: string; recoveryError?: string; expired?: boolean }) {
  const { t, errorMessage } = useI18n();
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const setup = !me.initialized;
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => { if (expired) formRef.current?.querySelector<HTMLInputElement>('input[name="password"]')?.focus(); }, [expired]);
  async function submit(e: FormEvent<HTMLFormElement>) { e.preventDefault(); setBusy(true); setError(""); const form = new FormData(e.currentTarget); try { await api(`/auth/${setup ? "setup" : "login"}`, "POST", { username: form.get("username"), password: form.get("password"), ...(setup ? { code: form.get("code") } : {}) }); await done(); } catch (err) { setError((err as Error).message); } finally { setBusy(false); } }
  return <div className="auth-layout"><div className="appearance-controls"><LanguageSelector /><ThemeSelector /></div><div className="auth-card"><div className="auth-logo"><Waves size={30} /> MusicForge</div><h1>{setup ? t("auth.setupTitle") : t("auth.loginTitle")}</h1><p>{expired ? t("auth.expiredHint") : setup ? t("auth.setupDescription") : t("auth.loginDescription")}</p>
    <form ref={formRef} onSubmit={submit}>{setup && <Field label={t("auth.code")}><input name="code" required autoComplete="off" placeholder={t("auth.codePlaceholder")} /></Field>}<Field label={t("auth.username")}><input name="username" defaultValue={username} required autoComplete="username" maxLength={100} /></Field><Field label={t("auth.password")} hint={setup ? t("auth.passwordHint") : undefined}><input name="password" type="password" required autoComplete={setup ? "new-password" : "current-password"} /></Field>{(error || recoveryError) && <div className="error-box" role="alert">{errorMessage(error || recoveryError)}</div>}<Button className="w-full" disabled={busy}>{busy ? <Loader2 size={16} className="animate-spin" /> : <ArrowRight size={16} />}{setup ? t("auth.create") : t("auth.login")}</Button></form>{!setup && me.oidc && <Button className="mt-3 w-full" variant="outline" asChild><a href="/api/auth/oidc/login"><ShieldCheck size={17} />{t("auth.oidc")}</a></Button>}<small className="auth-footer">{t("auth.footer")}</small></div></div>;
}

function ActivityView({ activity }: { activity: JobActivity }) {
  const { t, kind } = useI18n();
  const phase = ["storage", "discover", "scan", "read", "staging_wait", "probe", "artwork", "indexed", "convert", "validate", "move", "repair_wait", "publish"].includes(activity.phase) ? t(`phase.${activity.phase}` as MessageKey) : kind(activity.phase);
  return <div className="task-activity"><span>{phase}</span><strong>{activity.title || activity.path || "—"}</strong>{(activity.artist || activity.album) && <small>{[activity.artist, activity.album].filter(Boolean).join(" · ")}</small>}{activity.path && <small className="task-path">{activity.path}</small>}{activity.total > 0 && <small>{t("jobs.files", { done: activity.processed, total: activity.total })}</small>}{activity.phase === "discover" && <small>{t("jobs.discovered", { count: activity.processed })}</small>}{activity.phase === "read" && activity.read_total_bytes !== undefined && activity.read_total_bytes > 0 && <small>{t("jobs.readBytes", { read: ((activity.read_bytes || 0) / 1048576).toFixed(1), total: (activity.read_total_bytes / 1048576).toFixed(1) })}</small>}{activity.phase === "convert" && <small>{t("jobs.songPercent", { percent: Math.round(activity.percent) })}</small>}</div>;
}

function TaskDetails({ job, notify }: { job: Job; notify: Notice }) {
  const { t, kind, date, errorMessage } = useI18n();
  const [offset, setOffset] = useState(0); const [onlyFailed, setOnlyFailed] = useState(false);
  const { data, error, retry } = useResource<{ items: TaskItem[]; total: number }>(`/jobs/${job.id}/items?offset=${offset}&state=${onlyFailed ? "failed" : "all"}`, 2000);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const targets = (data?.items || []).flatMap(item => item.repair_target ? [item.repair_target] : []);
  const chosen = targets.filter(source => selected.has(source.id));
  const profile = (value: Encoding) => `${value.codec.toUpperCase()} · ${value.mode.toUpperCase()} · ${value.codec === "mp3" && value.mode === "vbr" ? `V${value.quality}` : `${value.bitrate} kbps`}`;
  return <div className="task-details">
    <dl className="task-facts"><div><dt>{t("jobs.scope")}</dt><dd>{job.kind === "scan" ? job.scope.length ? job.scope.join(", ") : t("jobs.fullLibrary") : ["convert", "move"].includes(job.kind) ? t("jobs.selectedTracks") : kind(job.kind)}</dd></div><div><dt>{t("jobs.created")}</dt><dd>{date(job.created)}</dd></div><div><dt>{t("jobs.updated")}</dt><dd>{date(job.updated)}</dd></div>{job.profile && <div><dt>{t("jobs.profile")}</dt><dd>{profile(job.profile)}</dd></div>}{job.args.verify && <div><dt>{t("library.verify")}</dt><dd>{t("jobs.hashVerify")}</dd></div>}</dl>
    {job.scan && <div className="task-scan-result">{t("jobs.scanSummary", { done: job.scan.processed, total: job.scan.total })}</div>}
    <p>{t("jobs.logs")}</p><div className="task-outcome">{job.log ? errorMessage(job.log) : t(job.state === "success" ? "jobs.completed" : job.state === "paused" ? "jobs.pausedHint" : job.state === "stopped" ? "jobs.stoppedHint" : job.state === "failed" ? "jobs.failedHint" : job.state === "running" ? "jobs.processing" : "jobs.waiting")}</div>
    <div className="task-result-heading"><h3>{t("jobs.trackResults")}</h3><label className="check-field"><input type="checkbox" checked={onlyFailed} onChange={event => { setOnlyFailed(event.target.checked); setOffset(0); setSelected(new Set()); }} />{t("jobs.onlyFailed")}</label></div>
    <ResourceWarning error={error} retry={retry} />
    {!data ? <Loading error={error} retry={retry} /> : <>
      {targets.length > 0 && <div className="selection-bar"><label className="check-field"><input type="checkbox" checked={targets.every(source => selected.has(source.id))} onChange={event => setSelected(event.target.checked ? new Set(targets.map(source => source.id)) : new Set())} />{t("repair.selectPage")}</label><BatchRepairButton sources={chosen} notify={notify} refresh={() => { setSelected(new Set()); retry(); }} /></div>}
      <div className="task-results">{data.items.map(item => <div className="task-result" key={item.id}>{item.repair_target && <input type="checkbox" aria-label={t("library.selectTrack", { title: item.activity.title || item.activity.path })} checked={selected.has(item.repair_target.id)} onChange={event => setSelected(old => { const next = new Set(old); event.target.checked ? next.add(item.repair_target!.id) : next.delete(item.repair_target!.id); return next; })} />}<div><strong>{item.activity.title || item.activity.path || kind(item.kind)}</strong>{(item.activity.artist || item.activity.album) && <small>{[item.activity.artist, item.activity.album].filter(Boolean).join(" · ")}</small>}{item.activity.path && <small className="task-path">{item.activity.path}</small>}{item.output && <small>{t("jobs.output", { path: item.output })}</small>}{item.profile && <small>{profile(item.profile)}</small>}<small>{item.log === "Stopped by administrator" ? t("jobs.cancelled") : t("jobs.itemAttempts", { count: item.attempts })}</small></div><Badge status={job.state === "stopped" && item.log === "Stopped by administrator" ? "stopped" : job.state === "paused" && item.state === "pending" ? "paused" : item.state} />{item.failure && <p className="error-box task-error-summary">{t(`jobs.failure.${item.failure}`)}</p>}{item.log && !["Paused by administrator", "Stopped by administrator"].includes(item.log) && <details className="task-diagnostic"><summary>{t("jobs.diagnostics")}</summary><pre>{errorMessage(item.log)}</pre></details>}</div>)}</div>
      {data.total === 0 && <p>{t("jobs.noResults")}</p>}
      <div className="pagination"><span>{t("jobs.itemsPage", { start: data.total ? offset + 1 : 0, end: Math.min(offset + 50, data.total), total: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("jobs.previousResults")} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("jobs.nextResults")} disabled={offset + 50 >= data.total} onClick={() => setOffset(offset + 50)}><ChevronRight size={16} /></Button></div>
    </>}
  </div>;
}

function Jobs({ notify }: { notify: Notice }) {
  const { t, status, kind, date, errorMessage } = useI18n();
  const [offset, setOffset] = useState(0); const [filter, setFilter] = useState("all"); const [expanded, setExpanded] = useState<number | string>(); const [selected, setSelected] = useState<Set<number | string>>(new Set()); const [busy, setBusy] = useState(false); const [allSelected, setAllSelected] = useState(false);
  const { data, error, retry: retryResource } = useResource<{ jobs: Job[]; total: number }>(`/jobs?offset=${offset}&limit=100&state=${filter}&include_repairs=true`, 2000);
  type Action = "pause" | "resume" | "stop" | "delete" | "retry";
  const taskKey = (job: Job) => job.repair_id || job.id;
  function eligible(job: Job, action: Action) {
    if (job.repair_id) return action === "delete" ? job.can_delete : action === "stop" && ["pending", "running"].includes(job.state) && !job.stopping;
    if (action === "pause") return ["pending", "running"].includes(job.state);
    if (action === "resume") return job.state === "paused";
    if (action === "stop") return ["pending", "running", "paused"].includes(job.state) || job.state === "failed" && !job.can_delete;
    if (action === "retry") return ["failed", "stopped"].includes(job.state);
    return job.can_delete;
  }
  function clearSelection() { setSelected(new Set()); setAllSelected(false); }
  function paginate(next: number) { setOffset(next); setExpanded(undefined); if (!allSelected) setSelected(new Set()); }
  async function control(action: Action, ids?: (number | string)[], matching = false) {
    if (action === "stop" && !window.confirm(t("jobs.stopConfirm")) || action === "delete" && !window.confirm(t("jobs.deleteConfirm"))) return;
    setBusy(true);
    try {
      const result = await api<{ changed: number }>("/jobs/control", "POST", { action, ...(ids ? { ids: ids.filter(id => typeof id === "number"), ...(ids.some(id => typeof id === "string") ? { repair_ids: ids.filter(id => typeof id === "string") } : {}) } : { all: true, ...(matching ? { state: filter, ...(["stop", "delete"].includes(action) ? { include_repairs: true } : {}) } : {}) }) });
      notify(result.changed ? `notice.task${action[0].toUpperCase()}${action.slice(1)}` as MessageKey : "notice.noTasksChanged", false, { count: result.changed });
      clearSelection(); retryResource();
    }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  const chosen = (data?.jobs || []).filter(job => selected.has(taskKey(job)));
  const selectedCount = allSelected ? (data?.total || 0) : chosen.length;
  const pageChecked = !!data?.jobs.length && (allSelected || chosen.length === data.jobs.length);
  function canBulk(action: Action) {
    if (!allSelected) return chosen.some(job => eligible(job, action));
    const states = { pause: ["all", "pending", "running"], resume: ["all", "paused"], stop: ["all", "pending", "running", "paused", "failed"], retry: ["all", "failed", "stopped"], delete: ["all", "success", "failed", "stopped"] };
    return selectedCount > 0 && states[action].includes(filter);
  }
  function bulk(action: Action) { void control(action, allSelected ? undefined : chosen.filter(job => eligible(job, action)).map(taskKey), allSelected); }
  return <>
    {data && <ResourceWarning error={error} retry={retryResource} />}
    <div className="page-heading"><div><h1>{t("jobs.title")}</h1><p>{t("jobs.description")}</p></div><Button variant="outline" disabled={busy} onClick={() => void control("retry")}><RefreshCw size={16} />{t("jobs.retryAll")}</Button></div>
    {!data ? <Loading error={error} retry={retryResource} /> : <div className="panel jobs-panel"><div className="table-toolbar"><div className="filter-tabs">{["all", "pending", "running", "paused", "success", "failed", "stopped"].map(state => <button key={state} className={filter === state ? "active" : ""} aria-pressed={filter === state} onClick={() => { setFilter(state); setOffset(0); clearSelection(); setExpanded(undefined); }}>{state === "all" ? t("jobs.all") : status(state)}</button>)}</div></div>
      {data.jobs.length > 0 && <div className="selection-bar"><label className="check-field"><input type="checkbox" checked={pageChecked} ref={node => { if (node) node.indeterminate = !pageChecked && chosen.length > 0; }} disabled={busy} onChange={event => { setAllSelected(false); setSelected(event.target.checked ? new Set(data.jobs.map(taskKey)) : new Set()); }} /><span>{t("jobs.selectPage")}</span></label><Button variant="ghost" size="sm" disabled={busy || allSelected} onClick={() => { setAllSelected(true); setSelected(new Set()); }}>{t("jobs.selectMatching", { count: data.total })}</Button>{selectedCount > 0 && <Button variant="ghost" size="sm" disabled={busy} onClick={clearSelection}>{t("library.clearSelection")}</Button>}</div>}
      {selectedCount > 0 && <div className="selection-bar"><span>{t(allSelected ? "jobs.allSelected" : "library.selected", { count: selectedCount })}</span><Button size="sm" disabled={busy || !canBulk("pause")} onClick={() => bulk("pause")}>{t("jobs.pause")}</Button><Button size="sm" disabled={busy || !canBulk("resume")} onClick={() => bulk("resume")}>{t("jobs.resume")}</Button><Button size="sm" variant="outline" disabled={busy || !canBulk("stop")} onClick={() => bulk("stop")}>{t("jobs.stop")}</Button><Button size="sm" variant="outline" disabled={busy || !canBulk("retry")} onClick={() => bulk("retry")}>{t("jobs.retry")}</Button><Button size="sm" variant="destructive" disabled={busy || !canBulk("delete")} onClick={() => bulk("delete")}>{t("jobs.delete")}</Button><small className="selection-hint">{t("jobs.bulkHint")}</small></div>}
      {data.jobs.length ? <div className="task-list">{data.jobs.map(job => {
        const percent = taskPercent(job);
        const key = taskKey(job);
        return <div className="task" key={key} data-state={job.state} data-task-id={job.repair_id ? undefined : job.id} data-repair-task-id={job.repair_id}><div className="task-main"><input type="checkbox" aria-label={t("jobs.select", { id: job.repair_id?.slice(0, 8) || String(job.id) })} checked={allSelected || selected.has(key)} disabled={busy} onChange={event => { setAllSelected(false); setSelected(old => { const value = allSelected ? new Set(data.jobs.map(taskKey)) : new Set(old); event.target.checked ? value.add(key) : value.delete(key); return value; }); }} /><span className={`job-symbol ${job.state}`}>{job.state === "running" ? <Loader2 className="animate-spin" size={18} /> : <Activity size={18} />}</span><div className="task-description"><strong>{kind(job.kind)} <span>#{job.repair_id?.slice(0, 8) || job.id}</span></strong><small>{date(job.updated)}</small></div><Badge status={job.state} /><div className="task-actions"><Button variant="ghost" size="sm" aria-expanded={expanded === key} onClick={() => setExpanded(expanded === key ? undefined : key)}>{expanded === key ? t("jobs.collapse") : t("jobs.details")}</Button>{eligible(job, "pause") && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("pause", [job.id])}><Pause size={14} />{t("jobs.pause")}</Button>}{eligible(job, "resume") && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("resume", [job.id])}><Play size={14} />{t("jobs.resume")}</Button>}{(eligible(job, "stop") || job.stopping) && <Button variant="outline" size="sm" disabled={busy || job.stopping} onClick={() => void control("stop", [key])}><Square size={14} />{t(job.stopping ? "repairTasks.stopping" : "jobs.stop")}</Button>}{eligible(job, "retry") && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("retry", [job.id])}>{t("jobs.retry")}</Button>}<Button className="task-delete" variant="ghost" size="sm" aria-label={t("jobs.delete")} disabled={busy || !job.can_delete} onClick={() => void control("delete", [key])}><Trash2 size={14} /><span className="task-delete-label">{t("jobs.delete")}</span></Button></div></div>
          <div className="task-progress"><div className="task-progress-label"><span>{job.counts.total ? t("jobs.queueProgress", { done: job.counts.done, total: job.counts.total, pending: job.counts.pending, running: job.counts.running, failed: job.counts.failed }) : job.scan?.total ? t("jobs.files", { done: job.scan.processed, total: job.scan.total }) : t(job.state === "success" ? "jobs.completed" : job.state === "failed" ? "jobs.failedHint" : "jobs.waiting")}{job.counts.cancelled > 0 && ` · ${t("jobs.cancelledCount", { count: job.counts.cancelled })}`}</span><span className="task-percent">{percent}%</span></div><div className="task-meter" role="progressbar" aria-label={t("jobs.progress", { id: job.repair_id?.slice(0, 8) || String(job.id) })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><span style={{ width: `${percent}%` }} /></div>{job.repair_id && job.counts.done > 0 && <p className="repair-warning">{t("repairTasks.imported")}</p>}{job.current.map((activity, index) => <ActivityView key={index} activity={activity} />)}{job.state === "paused" && <p>{t("jobs.pausedHint")}</p>}{job.state === "stopped" && <p>{t("jobs.stoppedHint")}</p>}</div>
          {job.state === "failed" && <p className="task-failure">{errorMessage(job.log || t("jobs.failedHint"))}</p>}
          {job.waiting_reason && <div className="task-waiting" role="status"><Clock3 size={17} /><div>{errorMessage(job.waiting_reason)}{job.retry_at && <small>{t("jobs.waitingUntil", { date: date(job.retry_at) })}</small>}</div></div>}
          {expanded === key && <DetailPanel title={kind(job.kind)} subtitle={`#${job.repair_id?.slice(0, 8) || job.id}`} close={() => setExpanded(undefined)} className="task-inspector">{job.repair_id ? <RepairTaskDetails job={job} /> : <TaskDetails job={job} notify={notify} />}</DetailPanel>}
        </div>;
      })}</div> : <Empty title={t("jobs.empty")}>{t("jobs.emptyHint")}</Empty>}
      <div className="pagination"><span>{t("jobs.total", { count: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("jobs.previous")} disabled={offset === 0} onClick={() => paginate(Math.max(0, offset - 100))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("jobs.next")} disabled={offset + 100 >= data.total} onClick={() => paginate(offset + 100)}><ChevronRight size={16} /></Button></div>
    </div>}
  </>;
}

type SettingsResponse = { settings: Settings; configured: { webhook: boolean; nav_password: boolean; oidc_secret: boolean }; lidarr?: { url: string; key_configured: boolean }; public_url: string; allowed_origins: string[] };
function SettingsPage({ me, notify }: { me: Me; notify: Notice }) {
  const { t } = useI18n();
  const { data, error, retry: retryResource } = useResource<SettingsResponse>("/settings");
  const [section, setSection] = useState(() => { const value = new URLSearchParams(location.search).get("section") || "library"; return ["library", "encoding", "integrations", "security"].includes(value) ? value : "library"; });
  const sections = [
    { id: "library", label: t("settings.tabLibrary"), icon: FolderOpen },
    { id: "encoding", label: t("settings.tabEncoding"), icon: Waves },
    { id: "integrations", label: t("settings.tabIntegrations"), icon: RefreshCw },
    { id: "security", label: t("settings.tabSecurity"), icon: ShieldCheck },
  ];
  const [settings, setSettings] = useState<Settings>(); const [saved, setSaved] = useState<Settings>(); const [configured, setConfigured] = useState<SettingsResponse["configured"]>(); const [busy, setBusy] = useState(false); const [refreshing, setRefreshing] = useState(false); const [webhook, setWebhook] = useState(""); const [clearWebhook, setClearWebhook] = useState(false); const [unbind, setUnbind] = useState(false); const [clearNavPassword, setClearNavPassword] = useState(false); const [clearOIDCSecret, setClearOIDCSecret] = useState(false);
  const [lidarrURL, setLidarrURL] = useState<string>();
  const [lidarrKey, setLidarrKey] = useState("");
  const [savedLidarr, setSavedLidarr] = useState<SettingsResponse["lidarr"]>();
  useEffect(() => { if (data) { setLidarrURL(old => old ?? data.lidarr?.url ?? ""); setSavedLidarr(old => old ?? data.lidarr ?? { url: "", key_configured: false }); } }, [data]);
  useEffect(() => { if (data) { setSettings(old => old ?? data.settings); setSaved(old => old ?? data.settings); setConfigured(data.configured); } }, [data]);
  if (!data || !settings) return <Loading error={error} retry={retryResource} />;
  const s = settings;
  const primaryOrigin = data.public_url !== "" && location.origin === data.public_url;
  const normalizedLidarrURL = (lidarrURL || "").trim().replace(/\/+$/, "");
  const lidarrDirty = normalizedLidarrURL !== (savedLidarr?.url || "") || lidarrKey !== "";
  const dirty = lidarrDirty || JSON.stringify(settings) !== JSON.stringify(saved) || webhook !== "" || clearWebhook || unbind || clearNavPassword || clearOIDCSecret;
  function update<K extends keyof Settings>(key: K, value: Settings[K]) { setSettings(old => old && ({ ...old, [key]: value })); }
  function encoding<K extends keyof Settings["encoding"]>(key: K, value: Settings["encoding"][K]) { setSettings(old => old && ({ ...old, encoding: { ...old.encoding, [key]: value } })); }
  async function save(e: FormEvent) { e.preventDefault(); setBusy(true); try { const result = await api<{ encoding_changed: boolean }>("/settings", "PUT", { ...settings, webhook_secret: webhook, clear_webhook: clearWebhook, unbind_oidc: unbind, clear_nav_password: clearNavPassword, clear_oidc_secret: clearOIDCSecret, ...(lidarrDirty ? { lidarr_connection: { url: lidarrURL || "", api_key: lidarrKey } } : {}) }); const fresh = await api<SettingsResponse>("/settings"); setSettings(fresh.settings); setSaved(fresh.settings); setConfigured(fresh.configured); setLidarrURL(fresh.lidarr?.url || ""); setSavedLidarr(fresh.lidarr || { url: "", key_configured: false }); setLidarrKey(""); setWebhook(""); setClearWebhook(false); setUnbind(false); setClearNavPassword(false); setClearOIDCSecret(false); notify(result.encoding_changed ? "notice.profileChanged" : "notice.saved"); } catch (err) { notify(err as Error, true); } finally { setBusy(false); } }
  async function bind() { try { const result = await api<{ url: string }>("/auth/oidc/bind", "POST", {}); location.href = result.url; } catch (err) { notify(err as Error, true); } }
  async function refresh() { setRefreshing(true); try { await api("/navidrome/refresh", "POST", {}); notify("notice.refresh"); } catch (err) { notify(err as Error, true); } finally { setRefreshing(false); } }
  return <><ResourceWarning error={error} retry={retryResource} /><div className="page-heading"><div><h1>{t("app.settings")}</h1><p>{t("settings.description")}</p></div></div><div className="settings-layout">
    <nav className="settings-nav" role="tablist" aria-label={t("settings.sections")} onKeyDown={event => {
      const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
      const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : ["ArrowRight", "ArrowDown"].includes(event.key) ? (current + 1) % buttons.length : ["ArrowLeft", "ArrowUp"].includes(event.key) ? (current + buttons.length - 1) % buttons.length : -1;
      if (next >= 0) { event.preventDefault(); buttons[next].focus(); buttons[next].click(); }
    }}>{sections.map(item => <button key={item.id} id={`settings-tab-${item.id}`} role="tab" aria-selected={section === item.id} aria-controls={`settings-${item.id}`} tabIndex={section === item.id ? 0 : -1} onClick={() => setSection(item.id)}><item.icon size={17} /><span>{item.label}</span><ChevronRight size={14} /></button>)}</nav>
    <div className="settings-content" role="tabpanel" id={`settings-${section}`} aria-labelledby={`settings-tab-${section}`}>
    <form noValidate onSubmit={event => {
      event.preventDefault();
      const invalid = event.currentTarget.querySelector<HTMLInputElement | HTMLSelectElement>(":invalid");
      if (invalid) {
        const target = invalid.closest<HTMLElement>("[data-settings-section]")?.dataset.settingsSection;
        if (target) flushSync(() => setSection(target));
        invalid.reportValidity(); invalid.focus(); return;
      }
      void save(event);
    }} className="settings-form">
    <section className="panel settings-panel" data-settings-section="library" hidden={section !== "library"}><div className="settings-title"><FolderOpen /><div><h2>{t("settings.paths")}</h2><p>{t("settings.pathsHint")}</p></div></div><div className="form-grid"><Field label={t("settings.source")} hint={t("settings.sourceHint")}><input value={s.source} onChange={e => update("source", e.target.value)} required /></Field><Field label={t("settings.output")} hint={t("settings.outputHint")}><input value={s.output} onChange={e => update("output", e.target.value)} required /></Field></div><label className="check-field switch-field"><input type="checkbox" role="switch" checked={s.enabled} onChange={e => update("enabled", e.target.checked)} /><span>{t("settings.enabled")}</span></label><small>{t("settings.mountHint")}</small></section>
    <section className="panel settings-panel" data-settings-section="encoding" hidden={section !== "encoding"}><div className="settings-title"><Waves /><div><h2>{t("settings.encoding")}</h2><p>{t("settings.encodingHint")}</p></div><Button type="button" variant="ghost" size="sm" onClick={() => setSettings(old => old && ({ ...old, encoding: { ...old.encoding, mode: "vbr", bitrate: 192, quality: 2 } }))}>{t("settings.restore")}</Button></div><div className="form-grid"><Field label={t("settings.codec")}><select value={s.encoding.codec} onChange={e => encoding("codec", e.target.value as "mp3" | "opus")}><option value="opus">{t("settings.opus")}</option><option value="mp3">{t("settings.mp3")}</option></select></Field><Field label={t("settings.mode")} hint={t("settings.modeHint")}><select value={s.encoding.mode} onChange={e => encoding("mode", e.target.value as "vbr" | "cbr")}><option value="vbr">{t("settings.vbr")}</option><option value="cbr">{t("settings.cbr")}</option></select></Field>{s.encoding.codec === "mp3" && s.encoding.mode === "vbr" ? <Field label={t("settings.quality")} hint={t("settings.qualityHint")}><select value={s.encoding.quality} onChange={e => encoding("quality", Number(e.target.value))}>{Array.from({ length: 10 }, (_, quality) => <option key={quality} value={quality}>V{quality}{quality === 2 ? t("settings.recommended") : ""}</option>)}</select></Field> : <Field label={s.encoding.mode === "vbr" ? t("settings.targetBitrate") : t("settings.fixedBitrate")} hint={t("settings.bitrateHint")}>{s.encoding.codec === "mp3" ? <select value={s.encoding.bitrate} onChange={e => encoding("bitrate", Number(e.target.value))}>{[32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320].map(rate => <option value={rate} key={rate}>{rate} kbps</option>)}</select> : <input type="number" min={32} max={512} value={s.encoding.bitrate} onChange={e => encoding("bitrate", Number(e.target.value))} />}</Field>}<Field label={t("settings.concurrency")} hint={t("settings.concurrencyHint")}><input type="number" min={1} max={16} value={s.concurrency} onChange={e => update("concurrency", Number(e.target.value))} /></Field><Field label={t("settings.interval")}><input type="number" min={1} max={10080} value={s.scan_minutes} onChange={e => update("scan_minutes", Number(e.target.value))} /></Field></div><div className="info-note"><Check size={15} />{t("settings.encodingNote")}</div></section>
    <section className="panel settings-panel" data-settings-section="integrations" hidden={section !== "integrations"}><div className="settings-title"><RefreshCw /><div><h2>{t("settings.integrations")}</h2><p>{t("settings.integrationsHint")}</p></div></div><div className="settings-subheading"><h3>Lidarr</h3><p>{t("settings.lidarrSectionHint")}</p></div><div className="form-grid"><Field label={t("compare.url")}><input type="url" value={lidarrURL || ""} onChange={event => setLidarrURL(event.target.value)} placeholder="http://lidarr:8686" /></Field><Field label={t("compare.key")} hint={savedLidarr?.key_configured ? t("settings.keepSecret") : undefined}><input type="password" value={lidarrKey} onChange={event => setLidarrKey(event.target.value)} autoComplete="new-password" required={normalizedLidarrURL !== "" && (normalizedLidarrURL !== savedLidarr?.url || !savedLidarr?.key_configured) && lidarrKey.trim() === ""} /></Field></div><small>{t("compare.connectionHint")}</small><div className="form-grid"><Field label={t("settings.lidarrPrefix")} hint={t("settings.lidarrPrefixHint")}><input value={s.lidarr_prefix} onChange={e => update("lidarr_prefix", e.target.value)} placeholder="/music/flac" /></Field><Field label={t("settings.webhookSecret")} hint={configured?.webhook ? t("settings.secretConfigured") : t("settings.webhookHint")} action={<Button type="button" size="sm" variant="outline" onClick={() => { const bytes = crypto.getRandomValues(new Uint8Array(24)); const secret = Array.from(bytes, b => b.toString(16).padStart(2, "0")).join(""); setWebhook(secret); void navigator.clipboard?.writeText(secret).then(() => notify("notice.secretCopied")).catch(() => notify("notice.copyFailed", true)); }}>{t("settings.generate")}</Button>}><input type="password" value={webhook} onChange={e => setWebhook(e.target.value)} autoComplete="new-password" /></Field></div><label className="check-field"><input type="checkbox" checked={clearWebhook} onChange={e => setClearWebhook(e.target.checked)} />{t("settings.disableWebhook")}</label><div className="endpoint"><code>POST /api/webhook/lidarr</code><span>{t("settings.webhookCredentials")}</span></div><div className="settings-subheading"><h3>Navidrome</h3><p>{t("settings.navSectionHint")}</p></div><div className="form-grid"><Field label={t("settings.navUrl")}><input type="url" value={s.nav_url} onChange={e => { update("nav_url", e.target.value); if (e.target.value) setClearNavPassword(false); }} placeholder="http://navidrome:4533" /></Field><Field label={t("settings.navLibrary")}><input type="number" min={1} value={s.nav_library} onChange={e => update("nav_library", Number(e.target.value))} /></Field><Field label={t("settings.navUser")}><input value={s.nav_user} onChange={e => update("nav_user", e.target.value)} autoComplete="off" /></Field><Field label={t("settings.navPassword")} hint={configured?.nav_password ? t("settings.keepSecret") : undefined}><input type="password" disabled={clearNavPassword} value={s.nav_password || ""} onChange={e => update("nav_password", e.target.value)} autoComplete="new-password" /></Field></div><label className="check-field"><input type="checkbox" checked={clearNavPassword} disabled={!configured?.nav_password || s.nav_url !== ""} onChange={e => setClearNavPassword(e.target.checked)} />{t("settings.clearNavPassword")}</label><small>{t("settings.clearNavPasswordHint")}</small><Button type="button" variant="outline" size="sm" disabled={busy || refreshing || !saved?.enabled || !saved.nav_url} onClick={() => void refresh()}><RefreshCw size={14} className={refreshing ? "animate-spin" : ""} />{t("settings.refresh")}</Button><small>{t("settings.navHint")}</small></section>
    <section className="panel settings-panel" data-settings-section="security" hidden={section !== "security"}><div className="settings-title"><ShieldCheck /><div><h2>{t("settings.oidc")}</h2><p>{t("settings.oidcHint")}</p></div></div><div className="form-grid"><Field label={t("settings.issuer")}><input type="url" value={s.oidc_issuer} onChange={e => { update("oidc_issuer", e.target.value); if (e.target.value) setClearOIDCSecret(false); }} placeholder="https://auth.example.com/application/o/musicforge/" /></Field><Field label={t("settings.clientId")}><input value={s.oidc_client_id} onChange={e => update("oidc_client_id", e.target.value)} /></Field><Field label={t("settings.clientSecret")} hint={configured?.oidc_secret ? t("settings.keepSecret") : undefined}><input type="password" disabled={clearOIDCSecret} value={s.oidc_secret || ""} onChange={e => update("oidc_secret", e.target.value)} autoComplete="new-password" /></Field><Field label={t("settings.callback")}><input readOnly value={data.public_url ? `${data.public_url}/api/auth/oidc/callback` : t("settings.publicUrlFirst")} /></Field><Field label={t("settings.allowedOrigins")} hint={t("settings.originsHint")}><textarea readOnly rows={3} value={data.allowed_origins.join("\n")} /></Field></div><label className="check-field"><input type="checkbox" checked={clearOIDCSecret} disabled={me.method !== "local" || !configured?.oidc_secret || s.oidc_issuer !== ""} onChange={e => setClearOIDCSecret(e.target.checked)} />{t("settings.clearOIDCSecret")}</label><small>{t("settings.clearOIDCSecretHint")}</small>{s.bound_subject && <div className="info-note"><Check size={16} />{t("settings.bound", { identity: s.bound_username || s.bound_email || s.bound_subject })}</div>}<div className="actions"><Button type="button" variant="outline" disabled={me.method !== "local" || !primaryOrigin} onClick={() => void bind()}>{t("settings.bind")}</Button><label className="check-field"><input type="checkbox" checked={unbind} disabled={me.method !== "local"} onChange={e => setUnbind(e.target.checked)} />{t("settings.unbind")}</label></div><small>{t("settings.bindHint")}</small>{data.public_url && !primaryOrigin && <div className="info-note"><span>{t("settings.primaryBindHint")} <a href={`${data.public_url}/settings`}>{t("settings.openPrimary")}</a></span></div>}</section>
    <div className="settings-save" data-dirty={dirty}><span>{dirty ? t("settings.unsaved") : t("settings.savedIn")}</span><Button type="button" variant="ghost" disabled={busy || !dirty} onClick={() => { setSettings(saved); setLidarrURL(savedLidarr?.url || ""); setLidarrKey(""); setWebhook(""); setClearWebhook(false); setUnbind(false); setClearNavPassword(false); setClearOIDCSecret(false); }}>{t("settings.discard")}</Button><Button disabled={busy || !dirty}>{busy ? <Loader2 size={16} className="animate-spin" /> : <Check size={16} />}{t("settings.save")}</Button></div>
  </form><div hidden={section !== "security"}><PasswordForm notify={notify} disabled={me.method !== "local"} /></div></div></div></>;
}

function PasswordForm({ notify, disabled }: { notify: Notice; disabled: boolean }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) { e.preventDefault(); setBusy(true); const element = e.currentTarget; const form = new FormData(element); try { await api("/auth/password", "POST", { current: form.get("current"), password: form.get("password") }); const me = await api<Me>("/auth/me"); setCSRF(me.csrf || ""); notify("notice.password"); element.reset(); } catch (err) { notify(err as Error, true); } finally { setBusy(false); } }
  return <form className="panel settings-panel mt-6" onSubmit={e => void submit(e)}><div className="settings-title"><ShieldCheck /><div><h2>{t("password.title")}</h2><p>{t("password.description")}</p></div></div><div className="form-grid"><Field label={t("password.current")}><input name="current" type="password" required disabled={disabled || busy} autoComplete="current-password" /></Field><Field label={t("password.new")} hint={t("auth.passwordHint")}><input name="password" type="password" required disabled={disabled || busy} autoComplete="new-password" /></Field></div><Button variant="outline" disabled={disabled || busy}>{t("password.update")}</Button></form>;
}
