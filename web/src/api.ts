import type { Me } from "./types";

let csrf = "";
let sessionRequest: Promise<Me> | undefined;
let signedOut = false;
export function setCSRF(value: string) { csrf = value; signedOut = false; }

export function loadSession(): Promise<Me> {
  if (!sessionRequest) {
    sessionRequest = api<Me>("/auth/me").then(me => {
      csrf = me.csrf || "";
      signedOut = !me.authenticated;
      window.dispatchEvent(new CustomEvent("session-updated", { detail: me }));
      return me;
    }).finally(() => { sessionRequest = undefined; });
  }
  return sessionRequest;
}

export async function api<T>(path: string, method = "GET", body?: unknown, signal?: AbortSignal): Promise<T> {
  const publicAuth = ["/auth/me", "/auth/setup", "/auth/login"].includes(path);
  if (!publicAuth && sessionRequest) await sessionRequest;
  if (!publicAuth && signedOut) throw new Error("login required");
  const send = () => fetch(`/api${path}`, {
    method, credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000),
  }).catch((err: unknown) => {
    if (err instanceof DOMException && err.name === "TimeoutError") throw new Error("Request timed out; please retry");
    if (err instanceof TypeError) throw new Error("Cannot reach MusicForge; check your connection and retry");
    throw err;
  });
  let response = await send();
  let value: unknown;
  const read = async () => {
    try { value = await response.json(); }
    catch { throw new Error(`Server returned an unreadable response (HTTP ${response.status}); please retry\nRequest: ${method} /api${path}`); }
  };
  await read();
  const message = () => value !== null && typeof value === "object" && "error" in value && typeof value.error === "string" ? value.error : `Server returned HTTP ${response.status}; please retry`;
  if (!publicAuth && (response.status === 401 && message() === "login required" ||
    response.status === 403 && message() === "invalid request origin or CSRF token")) {
    const me = await loadSession();
    // Only reads may be replayed. A rejected write must be explicitly retried.
    if (me.authenticated && method === "GET" && response.status === 401) {
      response = await send();
      await read();
    } else if (me.authenticated) {
      throw new Error("Session changed; please retry this action");
    }
  }
  if (!response.ok) {
    if (response.status === 401 && !publicAuth && !signedOut) window.dispatchEvent(new Event("session-expired"));
    const raw = message();
    const detail = `\nRequest: ${method} /api${path}${response.headers.get("X-Request-ID") ? `\nRequest ID: ${response.headers.get("X-Request-ID")}` : ""}`;
    if (/^internal (?:server )?error$/i.test(raw.trim())) throw new Error(`Server returned HTTP ${response.status}; please retry${detail}\n${raw}`);
    if (response.status >= 500) throw new Error(raw + detail);
    throw new Error(raw);
  }
  return value as T;
}
