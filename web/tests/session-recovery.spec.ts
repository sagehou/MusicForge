import { test, expect } from "@playwright/test";

test("temporary auth failures preserve unsaved settings; confirmed expiry returns to login", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const me = { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" };
  let checks = 0;
  let fail = false;
  let expired = false;
  let release: (() => void) | undefined;
  await page.route("**/api/auth/me", async route => {
    checks++;
    if (fail) {
      await new Promise<void>(resolve => { release = resolve; });
      await route.fulfill({ status: 503, json: { error: "Authentication temporarily unavailable; please retry: database connection unavailable" } });
    } else await route.fulfill({ json: expired ? { ...me, authenticated: false } : me });
  });
  await page.route("**/api/settings", route => route.fulfill({ json: {
    settings: { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" },
    configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [],
  } }));
  await page.goto("/settings");
  const source = page.getByLabel("Source audio directory", { exact: true });
  await source.fill("/unsaved/source");
  fail = true;
  await page.evaluate(() => {
    window.dispatchEvent(new Event("session-expired"));
    window.dispatchEvent(new Event("session-expired"));
  });
  await expect.poll(() => checks).toBe(2);
  await expect(source).toHaveValue("/unsaved/source");
  expect(release).toBeDefined();
  release!();
  await expect(page.getByRole("alert")).toContainText("Authentication temporarily unavailable");
  await expect(source).toHaveValue("/unsaved/source");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.getByRole("alert")).toContainText("暂时无法验证登录状态");
  fail = false;
  await page.getByRole("button", { name: "重试", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByLabel("音频源目录", { exact: true })).toHaveValue("/unsaved/source");
  expired = true;
  await page.evaluate(() => window.dispatchEvent(new Event("session-expired")));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("欢迎回来");
});

test("HTML proxy 401 and generic 500 preserve the task page and identify the failed API", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  let checks = 0;
  let status = 200;
  await page.route("**/api/auth/me", route => {
    checks++;
    return route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "oidc", csrf: "test", oidc: true, version: "test" } });
  });
  await page.route("**/api/jobs?*", route => status === 401 ? route.fulfill({ status, contentType: "text/html", body: "<html>Unauthorized</html>" }) : status === 500 ? route.fulfill({ status, json: { error: "Internal Server Error" } }) : route.fulfill({ json: { jobs: [{
    id: 1, kind: "scan", state: "running", attempts: 0, progress: 0.5, created: 1800000000, updated: 1800000000, args: {},
    counts: { total: 2, done: 1, failed: 0, pending: 0, running: 1, cancelled: 0 }, scope: [], can_delete: false,
    current: [{ phase: "read", path: "Artist/Album/01.flac", title: "Still readable" }],
  }], total: 1 } }));
  await page.goto("/jobs");
  await expect(page.locator(".task-activity")).toContainText("Still readable");
  status = 401;
  await expect(page.getByRole("alert")).toContainText("HTTP 401");
  await expect(page.getByRole("alert")).toContainText("Request: GET /api/jobs");
  await expect(page.locator(".task-activity")).toContainText("Still readable");
  expect(checks).toBe(1);
  status = 500;
  await expect(page.getByRole("alert")).toContainText("HTTP 500");
  await expect(page.locator(".task-activity")).toContainText("Still readable");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.getByRole("alert")).toContainText("服务器返回 HTTP 500，请重试");
  await expect(page.getByRole("alert")).toContainText("/api/jobs");
  await expect(page.getByRole("button", { name: "暂停", exact: true })).toBeEnabled();
});
