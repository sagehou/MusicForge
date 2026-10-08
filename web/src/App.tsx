import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Activity, ArrowDownToLine, ArrowRight, Check, ChevronLeft, ChevronRight, Clock3, Disc3, FolderOpen, HardDrive, LayoutDashboard, ListMusic, Loader2, LogOut, Music2, Pause, Play, RefreshCw, Search, Settings2, ShieldCheck, Square, Trash2, Waves } from "lucide-react";
import { Button } from "./components/ui/button";
import { api, setCSRF } from "./api";
import type { Activity as JobActivity, Dashboard, Encoding, Job, Me, Settings, Source, TaskItem } from "./types";
import { LanguageSelector, useI18n, type MessageKey } from "./i18n";

type Notice = (message: MessageKey | Error, error?: boolean) => void;

function useResource<T>(path: string, poll: boolean | number = false) {
  const [result, setResult] = useState<{ path: string; data: T }>();
  const [failure, setFailure] = useState<{ path: string; message: string }>();
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let alive = true;
    let loading = false;
    const load = async () => {
      if (loading) return;
      loading = true;
      try {
        const data = await api<T>(path);
        if (alive) { setResult({ path, data }); setFailure(undefined); }
      } catch (err) {
        if (alive) setFailure({ path, message: (err as Error).message });
      } finally { loading = false; }
    };
    void load();
    const timer = poll ? setInterval(() => void load(), typeof poll === "number" ? poll : 5000) : undefined;
    return () => { alive = false; clearInterval(timer); };
  }, [path, poll, revision]);
  return { data: result?.path === path ? result.data : undefined, error: failure?.path === path ? failure.message : "", retry: () => setRevision(value => value + 1) };
}
function ResourceWarning({ error, retry }: { error: string; retry: () => void }) {
  const { t, errorMessage } = useI18n();
  return error ? <div className="banner amber" role="alert"><Activity size={20} /><div><strong>{t("app.stale")}</strong><p>{errorMessage(error)}</p></div><Button variant="outline" size="sm" onClick={retry}>{t("app.retry")}</Button></div> : null;
}
function Badge({ status }: { status: string }) { const { status: label } = useI18n(); return <span className={`badge badge-${status}`}><span />{label(status)}</span>; }
function Empty({ title, children }: { title: string; children?: ReactNode }) { return <div className="empty"><Disc3 size={36} strokeWidth={1.2} /><h3>{title}</h3><p>{children}</p></div>; }
function Field({ label, hint, children, action }: { label: string; hint?: string; children: ReactNode; action?: ReactNode }) { return <div className="field"><div className={action ? "input-action" : undefined}><label className="field-label"><span>{label}</span>{children}</label>{action}</div>{hint && <small>{hint}</small>}</div>; }
function Loading({ error, retry }: { error?: string; retry?: () => void }) { const { t, errorMessage } = useI18n(); return <div className="empty">{error ? <><p role="alert">{errorMessage(error)}</p>{retry && <Button variant="outline" onClick={retry}>{t("app.retry")}</Button>}</> : <Loader2 className="animate-spin" role="status" aria-label={t("app.loading")} />}</div>; }

export function App() {
  const { t, errorMessage } = useI18n();
  const [me, setMe] = useState<Me>();
  const [authError, setAuthError] = useState("");
  const [page, setPage] = useState(location.pathname);
  const [toast, setToast] = useState<{ message: MessageKey | Error; error: boolean }>();
  const notify: Notice = (message, error = false) => setToast({ message, error });
  const loadMe = () => api<Me>("/auth/me").then(value => { setCSRF(value.csrf || ""); setMe(value); setAuthError(""); }).catch(err => setAuthError(err.message));
  useEffect(() => {
    void loadMe();
    const pop = () => setPage(location.pathname);
    const expired = () => { setMe(undefined); void loadMe(); };
    window.addEventListener("popstate", pop); window.addEventListener("session-expired", expired);
    return () => { window.removeEventListener("popstate", pop); window.removeEventListener("session-expired", expired); };
  }, []);
  useEffect(() => { if (toast) { const timer = setTimeout(() => setToast(undefined), 6000); return () => clearTimeout(timer); } }, [toast]);
  useEffect(() => { document.getElementById("main-content")?.focus(); }, [page, me?.authenticated]);
  function navigate(path: string) { history.pushState(null, "", path); setPage(path); window.scrollTo(0, 0); }
  if (!me) return <div className="auth-layout"><LanguageSelector /><Loading error={authError} />{authError && <Button onClick={() => void loadMe()}>{t("app.reconnect")}</Button>}</div>;
  if (!me.authenticated) return <Auth me={me} done={loadMe} />;
  const nav = [{ path: "/", label: t("app.overview"), icon: LayoutDashboard }, { path: "/library", label: t("app.library"), icon: ListMusic }, { path: "/jobs", label: t("app.jobs"), icon: Activity }, { path: "/settings", label: t("app.settings"), icon: Settings2 }];
  const current = nav.find(item => item.path === page) || nav[0];
  return <div className="app-shell"><a className="skip-link" href="#main-content">{t("app.skipContent")}</a>
    <aside className="sidebar">
      <a className="brand" href="/" onClick={e => { e.preventDefault(); navigate("/"); }}><span className="brand-icon"><Waves /></span><span>MusicForge<small>{t("app.tagline")}</small></span></a>
      <span className="nav-caption">{t("app.workspace")}</span>
      <nav aria-label={t("app.navigation")}>{nav.map(item => <a key={item.path} href={item.path} aria-label={item.label} title={item.label} aria-current={current.path === item.path ? "page" : undefined} className={current.path === item.path ? "active" : ""} onClick={e => { e.preventDefault(); navigate(item.path); }}><item.icon size={19} />{item.label}{current.path === item.path && <span className="nav-dot" />}</a>)}</nav>
      <div className="sidebar-bottom"><div className="single-host"><HardDrive size={16} /><span>{t("app.singleHost")}<small>{me.version}</small></span></div><div className="account"><span className="avatar">{me.username?.slice(0, 1).toUpperCase()}</span><span>{me.username}<small>{me.method === "oidc" ? t("app.oidcAdmin") : t("app.localAdmin")}</small></span><Button variant="ghost" size="icon" aria-label={t("app.logout")} onClick={() => void api("/auth/logout", "POST", {}).then(loadMe).catch(err => notify(err as Error, true))}><LogOut size={17} /></Button></div></div>
    </aside>
    <main className="main" id="main-content" tabIndex={-1}><header className="topbar"><span>{t("app.workspace")}<ChevronRight size={14} /> <strong>{current.label}</strong></span><div className="topbar-actions"><LanguageSelector /><Button className="mobile-logout" variant="ghost" size="icon" aria-label={t("app.logout")} onClick={() => void api("/auth/logout", "POST", {}).then(loadMe).catch(err => notify(err as Error, true))}><LogOut size={17} /></Button><span className="topbar-note"><ShieldCheck size={15} />{t("app.session")}</span></div></header>
      <div className="page-content">{page === "/library" ? <Library notify={notify} /> : page === "/jobs" ? <Jobs notify={notify} /> : page === "/settings" ? <SettingsPage me={me} notify={notify} /> : <Overview notify={notify} navigate={navigate} />}</div>
    </main>
    {toast && <div className={`toast ${toast.error ? "toast-error" : ""}`} role={toast.error ? "alert" : "status"}>{toast.error ? <Activity size={17} /> : <Check size={17} />}{toast.message instanceof Error ? errorMessage(toast.message.message) : t(toast.message)}<button aria-label={t("app.dismiss")} onClick={() => setToast(undefined)}>×</button></div>}
  </div>;
}

function Auth({ me, done }: { me: Me; done: () => Promise<void> }) {
  const { t, errorMessage } = useI18n();
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const setup = !me.initialized;
  async function submit(e: FormEvent<HTMLFormElement>) { e.preventDefault(); setBusy(true); setError(""); const form = new FormData(e.currentTarget); try { await api(`/auth/${setup ? "setup" : "login"}`, "POST", { username: form.get("username"), password: form.get("password"), ...(setup ? { code: form.get("code") } : {}) }); await done(); } catch (err) { setError((err as Error).message); } finally { setBusy(false); } }
  return <div className="auth-layout"><LanguageSelector /><div className="auth-art" aria-hidden="true">{Array.from({ length: 29 }, (_, i) => <span key={i} style={{ height: `${20 + Math.sin(i * 1.7) ** 2 * 130}px` }} />)}</div><div className="auth-card"><div className="auth-logo"><Waves size={30} /> MusicForge</div><span className="eyebrow">{t("auth.eyebrow")}</span><h1>{setup ? t("auth.setupTitle") : t("auth.loginTitle")}</h1><p>{setup ? t("auth.setupDescription") : t("auth.loginDescription")}</p>
    <form onSubmit={submit}>{setup && <Field label={t("auth.code")}><input name="code" required autoComplete="off" placeholder={t("auth.codePlaceholder")} /></Field>}<Field label={t("auth.username")}><input name="username" required autoComplete="username" maxLength={100} /></Field><Field label={t("auth.password")} hint={setup ? t("auth.passwordHint") : undefined}><input name="password" type="password" required autoComplete={setup ? "new-password" : "current-password"} /></Field>{error && <div className="error-box" role="alert">{errorMessage(error)}</div>}<Button className="w-full" disabled={busy}>{busy ? <Loader2 size={16} className="animate-spin" /> : <ArrowRight size={16} />}{setup ? t("auth.create") : t("auth.login")}</Button></form>{!setup && me.oidc && <Button className="mt-3 w-full" variant="outline" asChild><a href="/api/auth/oidc/login"><ShieldCheck size={17} />{t("auth.oidc")}</a></Button>}<small className="auth-footer">{t("auth.footer")}</small></div></div>;
}

function Overview({ notify, navigate }: { notify: Notice; navigate: (path: string) => void }) {
  const { t, kind, date, number, errorMessage, locale } = useI18n();
  const { data, error, retry: retryResource } = useResource<Dashboard>("/dashboard", true);
  const jobs = useResource<{ jobs: Job[] }>("/jobs?limit=5", true);
  const config = useResource<{ settings: Settings }>("/settings");
  const [busy, setBusy] = useState(false);
  async function scan() { setBusy(true); try { await api("/library/scan", "POST", {}); notify("notice.scan"); } catch (err) { notify(err as Error, true); } finally { setBusy(false); } }
  if (!data) return <Loading error={error} retry={retryResource} />;
  const percent = data.source_count ? Math.round(data.ready_count / data.source_count * 100) : 0;
  const metrics = [{ label: t("overview.sources"), value: data.source_count, icon: Disc3, note: t("overview.masters") }, { label: t("overview.outputs"), value: data.output_count, icon: Music2, note: t("overview.outputLibrary", { codec: data.codec.toUpperCase() }) }, { label: t("overview.complete"), value: new Intl.NumberFormat(locale, { style: "percent" }).format(percent / 100), icon: Check, note: t("overview.readyCount", { count: data.ready_count }) }, { label: t("overview.attention"), value: data.expired_count + data.rebuild_count + data.failed_count, icon: Clock3, note: t("overview.attentionCounts", { rebuild: data.rebuild_count, expired: data.expired_count, failed: data.failed_count }) }];
  return <><ResourceWarning error={error} retry={retryResource} /><div className="page-heading"><div><span className="eyebrow">{t("overview.eyebrow")}</span><h1>{t("overview.title")}</h1><p>{t("overview.description")}</p></div><Button onClick={() => void scan()} disabled={busy || !data.enabled}><RefreshCw size={16} className={busy ? "animate-spin" : ""} />{t("overview.scan")}</Button></div>
    {!data.online && <div className="banner"><HardDrive size={20} /><div><strong>{data.enabled ? t("overview.offline") : t("overview.configure")}</strong><p>{errorMessage(data.storage_message)}</p></div><Button variant="outline" size="sm" onClick={() => navigate("/settings")}>{t("overview.openSettings")}</Button></div>}
    {data.rebuild_count > 0 && <div className="banner"><RefreshCw size={20} /><div><strong>{t("overview.rebuildCount", { count: data.rebuild_count })}</strong><p>{t("overview.keepPlayable")}</p></div><Button variant="outline" size="sm" onClick={() => navigate("/library")}>{t("overview.viewLibrary")}</Button></div>}
    <div className="metrics">{metrics.map(metric => <div className="metric" key={metric.label}><div><span>{metric.label}</span><metric.icon size={19} /></div><strong>{typeof metric.value === "number" ? number(metric.value) : metric.value}</strong><small>{metric.note}</small></div>)}</div>
    <div className="panel pipeline"><div className="panel-heading"><div><h2>{t("overview.pipeline")}</h2><p>{t("overview.pipelineDescription")}</p></div><span className={`online ${data.online ? "is-online" : ""}`}><span />{data.online ? t("overview.online") : t("overview.waiting")}</span></div><div className="pipeline-flow"><div><Disc3 /><small>{t("overview.sourceEyebrow")}</small><h3>{t("overview.flac")}</h3><code>{config.data?.settings.source || "—"}</code></div><ArrowRight className="flow-arrow" /><div className="pipeline-forge"><Waves /><small>{t("overview.buildEyebrow")}</small><h3>MusicForge</h3><span>ffmpeg · {data.codec.toUpperCase()}</span></div><ArrowRight className="flow-arrow" /><div><ArrowDownToLine /><small>{t("overview.outputEyebrow")}</small><h3>{t("overview.streaming")}</h3><code>{config.data?.settings.output || "—"}</code></div></div><div className="build-progress" role="progressbar" aria-label={t("overview.complete")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><span>{t("overview.currentProfile")}</span><span>{number(data.ready_count)} / {number(data.source_count)}</span><div><span style={{ width: `${percent}%` }} /></div></div></div>
    <div className="panel"><div className="panel-heading"><div><h2>{t("overview.recent")}</h2><p>{t("overview.background")}</p></div><Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>{t("overview.allJobs")}<ArrowRight size={14} /></Button></div>{jobs.data?.jobs.length ? <div className="job-list">{jobs.data.jobs.map(job => <div className="job-row" key={job.id}><span className={`job-symbol ${job.state}`}><Activity size={18} /></span><div><strong>{kind(job.kind)}</strong><small>{t("overview.job", { id: String(job.id), date: date(job.updated) })}</small></div><Badge status={job.state} /></div>)}</div> : <Empty title={t("overview.noJobs")}>{t("overview.noJobsHint")}</Empty>}</div>
  </>;
}

function Library({ notify }: { notify: Notice }) {
  const { t, status: statusLabel } = useI18n();
  const { data, error, retry: retryResource } = useResource<Source[]>("/library", true);
  const [query, setQuery] = useState(""); const [filter, setFilter] = useState("all");
  const [artistKey, setArtistKey] = useState<string>(); const [albumKey, setAlbumKey] = useState<string>();
  const [selected, setSelected] = useState<Set<number>>(new Set()); const [busy, setBusy] = useState(false); const [offset, setOffset] = useState(0);
  async function action(path: string, body: unknown, message: MessageKey) {
    setBusy(true);
    try { await api(path, "POST", body); notify(message); setSelected(new Set()); retryResource(); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  if (!data) return <Loading error={error} retry={retryResource} />;
  function identity(source: Source) {
    const parts = source.path.split("/");
    const artist = source.metadata.albumartist || source.metadata.album_artist || source.artist || (parts.length > 2 ? parts[0] : t("library.unknownArtist"));
    return { artistKey: parts.length > 2 ? parts[0] : `tag:${artist}`, artist, albumKey: parts.length > 2 ? parts.slice(0, 2).join("/") : parts.slice(0, -1).join("/"), album: source.album || parts[parts.length - 2] || t("library.unknownAlbum") };
  }
  const artist = data.find(source => identity(source).artistKey === artistKey);
  const album = data.find(source => identity(source).artistKey === artistKey && identity(source).albumKey === albumKey);
  const matches = data.filter(source => (filter === "all" || source.status === filter) && `${source.artist} ${source.metadata.albumartist || ""} ${source.album} ${source.title} ${source.path}`.toLowerCase().includes(query.toLowerCase()));
  const rows = matches.filter(source => (!artistKey || identity(source).artistKey === artistKey) && (albumKey === undefined || identity(source).albumKey === albumKey));
  const groupMap = new Map<string, { key: string; name: string; path: string; tracks: Source[] }>();
  if (albumKey === undefined) for (const source of rows) {
    const meta = identity(source); const key = artistKey ? meta.albumKey : meta.artistKey;
    let group = groupMap.get(key);
    if (!group) { group = { key, name: artistKey ? meta.album : meta.artist, path: key.startsWith("tag:") ? "" : key, tracks: [] }; groupMap.set(key, group); }
    group.tracks.push(source);
  }
  const groups = [...groupMap.values()].sort((a, b) => a.name.localeCompare(b.name));
  const count = albumKey === undefined ? groups.length : rows.length;
  const safeOffset = Math.min(offset, Math.max(0, Math.ceil(count / 50) - 1) * 50);
  const shown = rows.slice(safeOffset, safeOffset + 50);
  const selectedExpired = data.filter(source => selected.has(source.id) && !source.present && source.output).length;
  const expired = data.filter(source => !source.present && source.output).length;
  function toggle(ids: number[], checked: boolean) { setSelected(old => { const value = new Set(old); ids.forEach(id => checked ? value.add(id) : value.delete(id)); return value; }); }
  function deleteSelected(all: boolean) { if (window.confirm(all ? t("library.deleteAllConfirm", { count: expired }) : t("library.deleteSelectedConfirm"))) void action("/library/delete", { all, ids: [...selected] }, "notice.delete"); }
  function navigate(nextArtist?: string, nextAlbum?: string) { setArtistKey(nextArtist); setAlbumKey(nextAlbum); setOffset(0); }
  return <>
    <ResourceWarning error={error} retry={retryResource} />
    <div className="page-heading"><div><span className="eyebrow">{t("library.eyebrow")}</span><h1>{t("app.library")}</h1><p>{t("library.description", { count: data.length })}</p></div><div className="actions"><Button variant="outline" disabled={busy} onClick={() => void action("/library/scan", { verify: true }, "notice.verify")}><ShieldCheck size={16} />{t("library.verify")}</Button><Button disabled={busy} onClick={() => void action("/library/scan", {}, "notice.scan")}><RefreshCw size={16} />{t("library.scan")}</Button></div></div>
    {expired > 0 && <div className="banner amber"><Clock3 size={20} /><div><strong>{t("library.expiredCount", { count: expired })}</strong><p>{t("library.expiredHint")}</p></div><Button variant="destructive" size="sm" disabled={busy} onClick={() => deleteSelected(true)}>{t("library.deleteExpired")}</Button></div>}
    <nav className="library-breadcrumbs" aria-label={t("library.hierarchy")}><button onClick={() => navigate()} aria-current={!artistKey ? "page" : undefined}>{t("library.artists")}</button>{artistKey && <><ChevronRight size={14} /><button onClick={() => navigate(artistKey)} aria-current={albumKey === undefined ? "page" : undefined}>{artist ? identity(artist).artist : artistKey}</button></>}{albumKey !== undefined && <><ChevronRight size={14} /><span aria-current="page">{album ? identity(album).album : albumKey}</span></>}</nav>
    <div className="panel">
      <div className="table-toolbar"><div className="search"><Search size={17} /><input aria-label={t("library.search")} placeholder={t("library.searchPlaceholder")} value={query} onChange={event => { setQuery(event.target.value); setOffset(0); }} /></div><select aria-label={t("library.filter")} value={filter} onChange={event => { setFilter(event.target.value); setOffset(0); }}><option value="all">{t("library.allStatuses")}</option>{["ready", "needs_rebuild", "expired", "missing", "changed", "failed"].map(state => <option value={state} key={state}>{statusLabel(state)}</option>)}</select><Button variant="outline" size="sm" disabled={busy} onClick={() => void action("/library/rebuild", { all: true }, "notice.rebuild")}><Play size={14} />{t("library.startRebuild")}</Button></div>
      {selected.size > 0 && <div className="selection-bar"><span>{t("library.selected", { count: selected.size })}</span><Button size="sm" variant="outline" disabled={busy} onClick={() => void action("/library/rebuild", { ids: [...selected] }, "notice.selectedBuild")}>{t("library.buildSelected")}</Button><Button size="sm" variant="destructive" disabled={busy || selectedExpired === 0} onClick={() => deleteSelected(false)}><Trash2 size={13} />{t("library.deleteSelected")}</Button><Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>{t("library.clearSelection")}</Button></div>}
      {count > 0 ? <>
        {albumKey === undefined ? <div className="library-groups">{groups.slice(safeOffset, safeOffset + 50).map(group => {
          const ready = group.tracks.filter(source => source.status === "ready").length;
          const statuses = [...new Set(group.tracks.map(source => source.status))];
          return <div className="library-group" key={group.key}><input type="checkbox" aria-label={t(artistKey ? "library.selectAlbum" : "library.selectArtist", { name: group.name })} checked={group.tracks.every(source => selected.has(source.id))} onChange={event => toggle(group.tracks.map(source => source.id), event.target.checked)} /><button className="library-open" aria-label={t(artistKey ? "library.openAlbum" : "library.openArtist", { name: group.name })} onClick={() => artistKey ? navigate(artistKey, group.key) : navigate(group.key)}>{artistKey ? <Disc3 size={26} /> : <Music2 size={26} />}<span><strong>{group.name}</strong><small>{t("library.groupCounts", { ready, total: group.tracks.length })}</small>{group.path && <small>{group.path}</small>}</span><ChevronRight size={18} /></button><div className="library-group-status">{statuses.map(state => <Badge key={state} status={state} />)}</div></div>;
        })}</div> : <div className="table-scroll"><table><thead><tr><th><input type="checkbox" aria-label={t("library.selectPage")} checked={shown.length > 0 && shown.every(source => selected.has(source.id))} onChange={event => toggle(shown.map(source => source.id), event.target.checked)} /></th><th>{t("library.trackFile")}</th><th>{t("library.artistAlbum")}</th><th>{t("library.source")}</th><th>{t("library.artifact")}</th></tr></thead><tbody>{shown.map(source => <tr key={source.id} className={selected.has(source.id) ? "selected" : ""}><td><input type="checkbox" aria-label={t("library.selectTrack", { title: source.title || source.path })} checked={selected.has(source.id)} onChange={event => toggle([source.id], event.target.checked)} /></td><td><div className="track-cell"><span className="track-number">{source.disc > 1 ? `${source.disc}.${source.track}` : source.track || <Music2 size={15} />}</span><div><strong>{source.title || source.path.split("/").pop()}</strong><small title={source.path}>{source.path}</small></div></div></td><td><strong>{source.artist || t("library.unknownArtist")}</strong><small>{source.album || t("library.unknownAlbum")}</small></td><td><span className={source.present ? "source-present" : "source-missing"}>{source.present ? t("library.sourceOnline") : t("library.sourceDeleted")}</span></td><td><Badge status={source.status} />{source.output_present && source.status !== "ready" && <small>{t("library.stillPlayable")}</small>}{source.error && <details><summary>{t("library.errorDetails")}</summary><pre>{source.error}</pre></details>}</td></tr>)}</tbody></table></div>}
        <div className="pagination"><span>{albumKey === undefined ? t("library.groupPagination", { start: safeOffset + 1, end: Math.min(safeOffset + 50, count), total: count }) : t("library.pagination", { start: safeOffset + 1, end: Math.min(safeOffset + 50, count), total: count })}</span><Button variant="ghost" size="icon" aria-label={t("library.previous")} disabled={safeOffset === 0} onClick={() => setOffset(Math.max(0, safeOffset - 50))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("library.next")} disabled={safeOffset + 50 >= count} onClick={() => setOffset(safeOffset + 50)}><ChevronRight size={16} /></Button></div>
      </> : <Empty title={data.length ? t("library.noMatch") : t("library.empty")}>{data.length ? t("library.noMatchHint") : t("library.emptyHint")}</Empty>}
    </div>
  </>;
}

function ActivityView({ activity }: { activity: JobActivity }) {
  const { t, kind } = useI18n();
  const phase = ["discover", "scan", "artwork", "indexed", "convert", "validate", "move"].includes(activity.phase) ? t(`phase.${activity.phase}` as MessageKey) : kind(activity.phase);
  return <div className="task-activity"><span>{phase}</span><strong>{activity.title || activity.path || "—"}</strong>{(activity.artist || activity.album) && <small>{[activity.artist, activity.album].filter(Boolean).join(" · ")}</small>}{activity.path && <small className="task-path">{activity.path}</small>}{activity.total > 0 && <small>{t("jobs.files", { done: activity.processed, total: activity.total })}</small>}{activity.phase === "discover" && <small>{t("jobs.discovered", { count: activity.processed })}</small>}{activity.phase === "convert" && <small>{t("jobs.songPercent", { percent: Math.round(activity.percent) })}</small>}</div>;
}

function TaskDetails({ job }: { job: Job }) {
  const { t, kind, date, errorMessage } = useI18n();
  const [offset, setOffset] = useState(0); const [onlyFailed, setOnlyFailed] = useState(false);
  const { data, error, retry } = useResource<{ items: TaskItem[]; total: number }>(`/jobs/${job.id}/items?offset=${offset}&state=${onlyFailed ? "failed" : "all"}`, 2000);
  const profile = (value: Encoding) => `${value.codec.toUpperCase()} · ${value.mode.toUpperCase()} · ${value.codec === "mp3" && value.mode === "vbr" ? `V${value.quality}` : `${value.bitrate} kbps`}`;
  return <div className="task-details">
    <dl className="task-facts"><div><dt>{t("jobs.scope")}</dt><dd>{job.kind === "scan" ? job.scope.length ? job.scope.join(", ") : t("jobs.fullLibrary") : t("jobs.selectedTracks")}</dd></div><div><dt>{t("jobs.created")}</dt><dd>{date(job.created)}</dd></div><div><dt>{t("jobs.updated")}</dt><dd>{date(job.updated)}</dd></div>{job.profile && <div><dt>{t("jobs.profile")}</dt><dd>{profile(job.profile)}</dd></div>}{job.args.verify && <div><dt>{t("library.verify")}</dt><dd>{t("jobs.hashVerify")}</dd></div>}</dl>
    {job.scan && <div className="task-scan-result">{t("jobs.scanSummary", { done: job.scan.processed, total: job.scan.total })}</div>}
    <p>{t("jobs.logs")}</p><div className="task-outcome">{job.log ? errorMessage(job.log) : t(job.state === "success" ? "jobs.completed" : job.state === "paused" ? "jobs.pausedHint" : job.state === "stopped" ? "jobs.stoppedHint" : "jobs.waiting")}</div>
    <div className="task-result-heading"><h3>{t("jobs.trackResults")}</h3><label className="check-field"><input type="checkbox" checked={onlyFailed} onChange={event => { setOnlyFailed(event.target.checked); setOffset(0); }} />{t("jobs.onlyFailed")}</label></div>
    <ResourceWarning error={error} retry={retry} />
    {!data ? <Loading error={error} retry={retry} /> : <>
      <div className="task-results">{data.items.map(item => <div className="task-result" key={item.id}><div><strong>{item.activity.title || item.activity.path || kind(item.kind)}</strong>{(item.activity.artist || item.activity.album) && <small>{[item.activity.artist, item.activity.album].filter(Boolean).join(" · ")}</small>}{item.activity.path && <small className="task-path">{item.activity.path}</small>}{item.output && <small>{t("jobs.output", { path: item.output })}</small>}{item.profile && <small>{profile(item.profile)}</small>}<small>{item.log === "Stopped by administrator" ? t("jobs.cancelled") : t("jobs.itemAttempts", { count: item.attempts })}</small></div><Badge status={job.state === "stopped" && item.log === "Stopped by administrator" ? "stopped" : job.state === "paused" && item.state === "pending" ? "paused" : item.state} />{item.log && <details className="task-diagnostic"><summary>{t("jobs.diagnostics")}</summary><pre>{errorMessage(item.log)}</pre></details>}</div>)}</div>
      {data.total === 0 && <p>{t("jobs.noResults")}</p>}
      <div className="pagination"><span>{t("jobs.itemsPage", { start: data.total ? offset + 1 : 0, end: Math.min(offset + 50, data.total), total: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("jobs.previousResults")} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("jobs.nextResults")} disabled={offset + 50 >= data.total} onClick={() => setOffset(offset + 50)}><ChevronRight size={16} /></Button></div>
    </>}
  </div>;
}

function Jobs({ notify }: { notify: Notice }) {
  const { t, status, kind, date } = useI18n();
  const [offset, setOffset] = useState(0); const [filter, setFilter] = useState("all"); const [expanded, setExpanded] = useState<number>(); const [selected, setSelected] = useState<Set<number>>(new Set()); const [busy, setBusy] = useState(false);
  const { data, error, retry: retryResource } = useResource<{ jobs: Job[]; total: number }>(`/jobs?offset=${offset}&limit=100&state=${filter}`, 2000);
  async function control(action: "pause" | "resume" | "stop" | "delete" | "retry", ids?: number[]) {
    if (action === "stop" && !window.confirm(t("jobs.stopConfirm")) || action === "delete" && !window.confirm(t("jobs.deleteConfirm"))) return;
    setBusy(true);
    try { await api("/jobs/control", "POST", { action, ...(ids ? { ids } : { all: true }) }); notify(`notice.task${action[0].toUpperCase()}${action.slice(1)}` as MessageKey); setSelected(new Set()); retryResource(); }
    catch (err) { notify(err as Error, true); } finally { setBusy(false); }
  }
  if (!data) return <Loading error={error} retry={retryResource} />;
  const chosen = data.jobs.filter(job => selected.has(job.id));
  return <>
    <ResourceWarning error={error} retry={retryResource} />
    <div className="page-heading"><div><span className="eyebrow">{t("jobs.eyebrow")}</span><h1>{t("jobs.title")}</h1><p>{t("jobs.description")}</p></div><Button variant="outline" disabled={busy} onClick={() => void control("retry")}><RefreshCw size={16} />{t("jobs.retryAll")}</Button></div>
    <div className="panel"><div className="table-toolbar"><div className="filter-tabs">{["all", "pending", "running", "paused", "success", "failed", "stopped"].map(state => <button key={state} className={filter === state ? "active" : ""} aria-pressed={filter === state} onClick={() => { setFilter(state); setOffset(0); setSelected(new Set()); setExpanded(undefined); }}>{state === "all" ? t("jobs.all") : status(state)}</button>)}</div></div>
      {selected.size > 0 && <div className="selection-bar"><span>{t("library.selected", { count: selected.size })}</span><Button size="sm" disabled={busy || !chosen.every(job => ["pending", "running"].includes(job.state))} onClick={() => void control("pause", [...selected])}>{t("jobs.pause")}</Button><Button size="sm" disabled={busy || !chosen.every(job => job.state === "paused")} onClick={() => void control("resume", [...selected])}>{t("jobs.resume")}</Button><Button size="sm" variant="outline" disabled={busy || !chosen.every(job => (["pending", "running", "paused"].includes(job.state) || job.state === "failed" && !job.can_delete))} onClick={() => void control("stop", [...selected])}>{t("jobs.stop")}</Button><Button size="sm" variant="outline" disabled={busy || !chosen.every(job => ["failed", "stopped"].includes(job.state))} onClick={() => void control("retry", [...selected])}>{t("jobs.retrySelected", { count: selected.size })}</Button><Button size="sm" variant="destructive" disabled={busy || !chosen.every(job => job.can_delete)} onClick={() => void control("delete", [...selected])}>{t("jobs.delete")}</Button></div>}
      {data.jobs.length ? <div className="task-list">{data.jobs.map(job => {
        const scanActive = job.current.some(activity => ["discover", "scan", "artwork"].includes(activity.phase));
        const percent = Math.round(Math.max(0, Math.min(1, scanActive && job.scan?.total ? job.scan.processed / job.scan.total : job.progress)) * 100);
        return <div className="task" key={job.id} data-task-id={job.id}><div className="task-main"><input type="checkbox" aria-label={t("jobs.select", { id: String(job.id) })} checked={selected.has(job.id)} onChange={event => setSelected(old => { const value = new Set(old); event.target.checked ? value.add(job.id) : value.delete(job.id); return value; })} /><span className={`job-symbol ${job.state}`}>{job.state === "running" ? <Loader2 className="animate-spin" size={18} /> : <Activity size={18} />}</span><div className="task-description"><strong>{kind(job.kind)} <span>#{job.id}</span></strong><small>{date(job.updated)}</small></div><Badge status={job.state} /><div className="task-actions"><Button variant="ghost" size="sm" onClick={() => setExpanded(expanded === job.id ? undefined : job.id)}>{expanded === job.id ? t("jobs.collapse") : t("jobs.details")}</Button>{["pending", "running"].includes(job.state) && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("pause", [job.id])}><Pause size={14} />{t("jobs.pause")}</Button>}{job.state === "paused" && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("resume", [job.id])}><Play size={14} />{t("jobs.resume")}</Button>}{(["pending", "running", "paused"].includes(job.state) || job.state === "failed" && !job.can_delete) && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("stop", [job.id])}><Square size={14} />{t("jobs.stop")}</Button>}{["failed", "stopped"].includes(job.state) && <Button variant="outline" size="sm" disabled={busy} onClick={() => void control("retry", [job.id])}>{t("jobs.retry")}</Button>}<Button variant="ghost" size="sm" disabled={busy || !job.can_delete} onClick={() => void control("delete", [job.id])}><Trash2 size={14} />{t("jobs.delete")}</Button></div></div>
          <div className="task-progress"><div className="task-progress-label"><span>{job.counts.total ? t("jobs.queueProgress", { done: job.counts.done, total: job.counts.total, pending: job.counts.pending, running: job.counts.running, failed: job.counts.failed }) : job.scan?.total ? t("jobs.files", { done: job.scan.processed, total: job.scan.total }) : t("jobs.waiting")}</span><span>{percent}%</span></div><div className="task-meter" role="progressbar" aria-label={t("jobs.progress", { id: String(job.id) })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><span style={{ width: `${percent}%` }} /></div>{job.current.map((activity, index) => <ActivityView key={index} activity={activity} />)}{job.state === "paused" && <p>{t("jobs.pausedHint")}</p>}{job.state === "stopped" && <p>{t("jobs.stoppedHint")}</p>}</div>
          {expanded === job.id && <TaskDetails job={job} />}
        </div>;
      })}</div> : <Empty title={t("jobs.empty")}>{t("jobs.emptyHint")}</Empty>}
      <div className="pagination"><span>{t("jobs.total", { count: data.total })}</span><Button variant="ghost" size="icon" aria-label={t("jobs.previous")} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 100))}><ChevronLeft size={16} /></Button><Button variant="ghost" size="icon" aria-label={t("jobs.next")} disabled={offset + 100 >= data.total} onClick={() => setOffset(offset + 100)}><ChevronRight size={16} /></Button></div>
    </div>
  </>;
}

type SettingsResponse = { settings: Settings; configured: { webhook: boolean; nav_password: boolean; oidc_secret: boolean }; public_url: string; allowed_origins: string[] };
function SettingsPage({ me, notify }: { me: Me; notify: Notice }) {
  const { t } = useI18n();
  const { data, error, retry: retryResource } = useResource<SettingsResponse>("/settings");
  const [settings, setSettings] = useState<Settings>(); const [saved, setSaved] = useState<Settings>(); const [configured, setConfigured] = useState<SettingsResponse["configured"]>(); const [busy, setBusy] = useState(false); const [webhook, setWebhook] = useState(""); const [clearWebhook, setClearWebhook] = useState(false); const [unbind, setUnbind] = useState(false); const [clearNavPassword, setClearNavPassword] = useState(false); const [clearOIDCSecret, setClearOIDCSecret] = useState(false);
  useEffect(() => { if (data) { setSettings(data.settings); setSaved(data.settings); setConfigured(data.configured); } }, [data]);
  if (!data || !settings) return <Loading error={error} retry={retryResource} />;
  const s = settings;
  const primaryOrigin = data.public_url !== "" && location.origin === data.public_url;
  const dirty = JSON.stringify(settings) !== JSON.stringify(saved) || webhook !== "" || clearWebhook || unbind || clearNavPassword || clearOIDCSecret;
  function update<K extends keyof Settings>(key: K, value: Settings[K]) { setSettings(old => old && ({ ...old, [key]: value })); }
  function encoding<K extends keyof Settings["encoding"]>(key: K, value: Settings["encoding"][K]) { setSettings(old => old && ({ ...old, encoding: { ...old.encoding, [key]: value } })); }
  async function save(e: FormEvent) { e.preventDefault(); setBusy(true); try { const result = await api<{ encoding_changed: boolean }>("/settings", "PUT", { ...settings, webhook_secret: webhook, clear_webhook: clearWebhook, unbind_oidc: unbind, clear_nav_password: clearNavPassword, clear_oidc_secret: clearOIDCSecret }); const fresh = await api<SettingsResponse>("/settings"); setSettings(fresh.settings); setSaved(fresh.settings); setConfigured(fresh.configured); setWebhook(""); setClearWebhook(false); setUnbind(false); setClearNavPassword(false); setClearOIDCSecret(false); notify(result.encoding_changed ? "notice.profileChanged" : "notice.saved"); } catch (err) { notify(err as Error, true); } finally { setBusy(false); } }
  async function bind() { try { const result = await api<{ url: string }>("/auth/oidc/bind", "POST", {}); location.href = result.url; } catch (err) { notify(err as Error, true); } }
  return <><ResourceWarning error={error} retry={retryResource} /><div className="page-heading"><div><span className="eyebrow">{t("settings.eyebrow")}</span><h1>{t("app.settings")}</h1><p>{t("settings.description")}</p></div></div><form onSubmit={e => void save(e)} className="settings-form">
    <section className="panel settings-panel"><div className="settings-title"><FolderOpen /><div><h2>{t("settings.paths")}</h2><p>{t("settings.pathsHint")}</p></div></div><div className="form-grid"><Field label={t("settings.source")} hint={t("settings.sourceHint")}><input value={s.source} onChange={e => update("source", e.target.value)} required /></Field><Field label={t("settings.output")} hint={t("settings.outputHint")}><input value={s.output} onChange={e => update("output", e.target.value)} required /></Field></div><label className="check-field"><input type="checkbox" checked={s.enabled} onChange={e => update("enabled", e.target.checked)} /><span>{t("settings.enabled")}</span></label><small>{t("settings.mountHint")}</small></section>
    <section className="panel settings-panel"><div className="settings-title"><Waves /><div><h2>{t("settings.encoding")}</h2><p>{t("settings.encodingHint")}</p></div><Button type="button" variant="ghost" size="sm" onClick={() => setSettings(old => old && ({ ...old, encoding: { ...old.encoding, mode: "vbr", bitrate: 192, quality: 2 } }))}>{t("settings.restore")}</Button></div><div className="form-grid"><Field label={t("settings.codec")}><select value={s.encoding.codec} onChange={e => encoding("codec", e.target.value as "mp3" | "opus")}><option value="opus">{t("settings.opus")}</option><option value="mp3">{t("settings.mp3")}</option></select></Field><Field label={t("settings.mode")} hint={t("settings.modeHint")}><select value={s.encoding.mode} onChange={e => encoding("mode", e.target.value as "vbr" | "cbr")}><option value="vbr">{t("settings.vbr")}</option><option value="cbr">{t("settings.cbr")}</option></select></Field>{s.encoding.codec === "mp3" && s.encoding.mode === "vbr" ? <Field label={t("settings.quality")} hint={t("settings.qualityHint")}><select value={s.encoding.quality} onChange={e => encoding("quality", Number(e.target.value))}>{Array.from({ length: 10 }, (_, quality) => <option key={quality} value={quality}>V{quality}{quality === 2 ? t("settings.recommended") : ""}</option>)}</select></Field> : <Field label={s.encoding.mode === "vbr" ? t("settings.targetBitrate") : t("settings.fixedBitrate")} hint={t("settings.bitrateHint")}>{s.encoding.codec === "mp3" ? <select value={s.encoding.bitrate} onChange={e => encoding("bitrate", Number(e.target.value))}>{[32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320].map(rate => <option value={rate} key={rate}>{rate} kbps</option>)}</select> : <input type="number" min={32} max={512} value={s.encoding.bitrate} onChange={e => encoding("bitrate", Number(e.target.value))} />}</Field>}<Field label={t("settings.concurrency")} hint={t("settings.concurrencyHint")}><input type="number" min={1} max={16} value={s.concurrency} onChange={e => update("concurrency", Number(e.target.value))} /></Field><Field label={t("settings.interval")}><input type="number" min={1} max={10080} value={s.scan_minutes} onChange={e => update("scan_minutes", Number(e.target.value))} /></Field></div><div className="info-note"><Check size={15} />{t("settings.encodingNote")}</div></section>
    <section className="panel settings-panel"><div className="settings-title"><RefreshCw /><div><h2>{t("settings.integrations")}</h2><p>{t("settings.integrationsHint")}</p></div></div><div className="form-grid"><Field label={t("settings.lidarrPrefix")} hint={t("settings.lidarrPrefixHint")}><input value={s.lidarr_prefix} onChange={e => update("lidarr_prefix", e.target.value)} placeholder="/music/flac" /></Field><Field label={t("settings.webhookSecret")} hint={configured?.webhook ? t("settings.secretConfigured") : t("settings.webhookHint")} action={<Button type="button" size="sm" variant="outline" onClick={() => { const bytes = crypto.getRandomValues(new Uint8Array(24)); const secret = Array.from(bytes, b => b.toString(16).padStart(2, "0")).join(""); setWebhook(secret); void navigator.clipboard?.writeText(secret).then(() => notify("notice.secretCopied")).catch(() => notify("notice.copyFailed", true)); }}>{t("settings.generate")}</Button>}><input type="password" value={webhook} onChange={e => setWebhook(e.target.value)} autoComplete="new-password" /></Field></div><label className="check-field"><input type="checkbox" checked={clearWebhook} onChange={e => setClearWebhook(e.target.checked)} />{t("settings.disableWebhook")}</label><div className="endpoint"><code>POST /api/webhook/lidarr</code><span>{t("settings.webhookCredentials")}</span></div><div className="form-grid mt-6"><Field label={t("settings.navUrl")}><input type="url" value={s.nav_url} onChange={e => { update("nav_url", e.target.value); if (e.target.value) setClearNavPassword(false); }} placeholder="http://navidrome:4533" /></Field><Field label={t("settings.navLibrary")}><input type="number" min={1} value={s.nav_library} onChange={e => update("nav_library", Number(e.target.value))} /></Field><Field label={t("settings.navUser")}><input value={s.nav_user} onChange={e => update("nav_user", e.target.value)} autoComplete="off" /></Field><Field label={t("settings.navPassword")} hint={configured?.nav_password ? t("settings.keepSecret") : undefined}><input type="password" disabled={clearNavPassword} value={s.nav_password || ""} onChange={e => update("nav_password", e.target.value)} autoComplete="new-password" /></Field></div><label className="check-field"><input type="checkbox" checked={clearNavPassword} disabled={!configured?.nav_password || s.nav_url !== ""} onChange={e => setClearNavPassword(e.target.checked)} />{t("settings.clearNavPassword")}</label><small>{t("settings.clearNavPasswordHint")}</small><Button type="button" variant="outline" size="sm" onClick={() => void api("/navidrome/refresh", "POST", {}).then(() => notify("notice.refresh")).catch(err => notify(err as Error, true))}>{t("settings.refresh")}</Button><small>{t("settings.navHint")}</small></section>
    <section className="panel settings-panel"><div className="settings-title"><ShieldCheck /><div><h2>{t("settings.oidc")}</h2><p>{t("settings.oidcHint")}</p></div></div><div className="form-grid"><Field label={t("settings.issuer")}><input type="url" value={s.oidc_issuer} onChange={e => { update("oidc_issuer", e.target.value); if (e.target.value) setClearOIDCSecret(false); }} placeholder="https://auth.example.com/application/o/musicforge/" /></Field><Field label={t("settings.clientId")}><input value={s.oidc_client_id} onChange={e => update("oidc_client_id", e.target.value)} /></Field><Field label={t("settings.clientSecret")} hint={configured?.oidc_secret ? t("settings.keepSecret") : undefined}><input type="password" disabled={clearOIDCSecret} value={s.oidc_secret || ""} onChange={e => update("oidc_secret", e.target.value)} autoComplete="new-password" /></Field><Field label={t("settings.callback")}><input readOnly value={data.public_url ? `${data.public_url}/api/auth/oidc/callback` : t("settings.publicUrlFirst")} /></Field><Field label={t("settings.allowedOrigins")} hint={t("settings.originsHint")}><textarea readOnly rows={3} value={data.allowed_origins.join("\n")} /></Field></div><label className="check-field"><input type="checkbox" checked={clearOIDCSecret} disabled={me.method !== "local" || !configured?.oidc_secret || s.oidc_issuer !== ""} onChange={e => setClearOIDCSecret(e.target.checked)} />{t("settings.clearOIDCSecret")}</label><small>{t("settings.clearOIDCSecretHint")}</small>{s.bound_subject && <div className="info-note"><Check size={16} />{t("settings.bound", { identity: s.bound_username || s.bound_email || s.bound_subject })}</div>}<div className="actions"><Button type="button" variant="outline" disabled={me.method !== "local" || !primaryOrigin} onClick={() => void bind()}>{t("settings.bind")}</Button><label className="check-field"><input type="checkbox" checked={unbind} disabled={me.method !== "local"} onChange={e => setUnbind(e.target.checked)} />{t("settings.unbind")}</label></div><small>{t("settings.bindHint")}</small>{data.public_url && !primaryOrigin && <div className="info-note"><span>{t("settings.primaryBindHint")} <a href={`${data.public_url}/settings`}>{t("settings.openPrimary")}</a></span></div>}</section>
    <div className="settings-save"><span>{dirty ? t("settings.unsaved") : t("settings.savedIn")}</span><Button disabled={busy || !dirty}>{busy ? <Loader2 size={16} className="animate-spin" /> : <Check size={16} />}{t("settings.save")}</Button></div>
  </form><PasswordForm notify={notify} disabled={me.method !== "local"} /></>;
}

function PasswordForm({ notify, disabled }: { notify: Notice; disabled: boolean }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) { e.preventDefault(); setBusy(true); const element = e.currentTarget; const form = new FormData(element); try { await api("/auth/password", "POST", { current: form.get("current"), password: form.get("password") }); const me = await api<Me>("/auth/me"); setCSRF(me.csrf || ""); notify("notice.password"); element.reset(); } catch (err) { notify(err as Error, true); } finally { setBusy(false); } }
  return <form className="panel settings-panel mt-6" onSubmit={e => void submit(e)}><div className="settings-title"><ShieldCheck /><div><h2>{t("password.title")}</h2><p>{t("password.description")}</p></div></div><div className="form-grid"><Field label={t("password.current")}><input name="current" type="password" required disabled={disabled || busy} autoComplete="current-password" /></Field><Field label={t("password.new")} hint={t("auth.passwordHint")}><input name="password" type="password" required disabled={disabled || busy} autoComplete="new-password" /></Field></div><Button variant="outline" disabled={disabled || busy}>{t("password.update")}</Button></form>;
}
