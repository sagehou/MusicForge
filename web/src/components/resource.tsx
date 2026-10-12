import { useEffect, useState, type ReactNode } from "react";
import { Activity, Disc3, Loader2, RefreshCw } from "lucide-react";
import { api } from "../api";
import { useI18n, type MessageKey, type Params } from "../i18n";
import { Button } from "./ui/button";

export type Notice = (message: MessageKey | Error, error?: boolean, params?: Params) => void;

export function useResource<T>(path: string, poll: boolean | number = false) {
  const [result, setResult] = useState<{ path: string; data: T }>();
  const [failure, setFailure] = useState<{ path: string; message: string }>();
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let alive = true;
    let loading = false;
    let loaded = false;
    let failures = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    const interval = typeof poll === "number" ? poll : 5000;
    const load = async () => {
      if (loading || !alive || document.visibilityState === "hidden") return;
      clearTimeout(timer);
      loading = true;
      try {
        const data = await api<T>(path, "GET", undefined, controller.signal);
        if (alive) { loaded = true; setResult({ path, data }); setFailure(undefined); failures = 0; }
      } catch (err) {
        if (alive && !controller.signal.aborted) {
          failures++;
          setFailure({ path, message: (err as Error).message });
        }
      } finally {
        loading = false;
        if (alive && poll) timer = setTimeout(() => void load(), Math.min(30000, interval * 2 ** Math.min(failures, 3)));
      }
    };
    const visible = () => { if (document.visibilityState === "visible" && (poll || !loaded)) void load(); };
    const session = (event: Event) => { if ((event as CustomEvent<{ authenticated: boolean }>).detail.authenticated && (poll || !loaded)) void load(); };
    void load();
    document.addEventListener("visibilitychange", visible);
    window.addEventListener("session-updated", session);
    return () => { alive = false; clearTimeout(timer); controller.abort(); document.removeEventListener("visibilitychange", visible); window.removeEventListener("session-updated", session); };
  }, [path, poll, revision]);
  return { data: result?.path === path ? result.data : undefined, error: failure?.path === path ? failure.message : "", retry: () => setRevision(value => value + 1) };
}

export function ResourceWarning({ error, retry }: { error: string; retry: () => void }) {
  const { t, errorMessage } = useI18n();
  return error ? <div className="banner amber" role="alert"><Activity size={20} /><div><strong>{t("app.stale")}</strong><p>{errorMessage(error)}</p></div><Button variant="outline" size="sm" onClick={retry}><RefreshCw size={14} />{t("app.retry")}</Button></div> : null;
}
export function Badge({ status }: { status: string }) {
  const { status: label } = useI18n();
  return <span className={`badge badge-${status}`}><span />{label(status)}</span>;
}
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return <div className="empty"><Disc3 size={36} strokeWidth={1.2} /><h3>{title}</h3><p>{children}</p></div>;
}
export function Loading({ error, retry }: { error?: string; retry?: () => void }) {
  const { t, errorMessage } = useI18n();
  return <div className="empty">{error ? <><p role="alert">{errorMessage(error)}</p>{retry && <Button variant="outline" onClick={retry}><RefreshCw size={16} />{t("app.retry")}</Button>}</> : <Loader2 className="animate-spin" role="status" aria-label={t("app.loading")} />}</div>;
}
