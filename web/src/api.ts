let csrf = "";
export function setCSRF(value: string) { csrf = value; }
export async function api<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method, credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  }).catch((err: unknown) => {
    if (err instanceof DOMException && err.name === "TimeoutError") throw new Error("Request timed out; please retry");
    throw err;
  });
  if (response.status === 401 && !path.startsWith("/auth/")) window.dispatchEvent(new Event("session-expired"));
  let value: unknown;
  try { value = await response.json(); } catch { throw new Error(`Server returned an unreadable response (HTTP ${response.status}); please retry`); }
  if (!response.ok) {
    const message = value !== null && typeof value === "object" && "error" in value && typeof value.error === "string" ? value.error : `Server returned HTTP ${response.status}; please retry`;
    throw new Error(message);
  }
  return value as T;
}
