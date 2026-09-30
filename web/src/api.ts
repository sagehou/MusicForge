let csrf = "";
export function setCSRF(value: string) { csrf = value; }
export async function api<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method, credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value = await response.json();
  if (!response.ok) { if (response.status === 401 && !path.startsWith("/auth/")) window.dispatchEvent(new Event("session-expired")); throw new Error(value.error || "请求失败"); }
  return value as T;
}
