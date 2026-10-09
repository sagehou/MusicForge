import { test, expect } from "@playwright/test";

test("remote read progress is bilingual and proxy errors retain a usable task page", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", version: "test", csrf: "test" } }));
  const activity = { phase: "read", path: "Artist/Album/36.flac", artist: "Remote Artist", album: "Remote Album", title: "Track 36", processed: 35, total: 40, percent: 0, read_bytes: 12 * 1048576, read_total_bytes: 40 * 1048576 };
  const job = { id: 1, kind: "scan", state: "running", attempts: 0, progress: 0.875, log: "", created: 1800000000, updated: 1800000000, args: {}, counts: { total: 35, done: 0, failed: 0, pending: 35, running: 0, cancelled: 0 }, scan: activity, current: [activity], scope: [], can_delete: false };
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [job], total: 1 } }));
  await page.goto("/jobs");
  await expect(page.locator(".task-activity")).toContainText("Read 12.0 / 40.0 MiB");
  await expect(page.locator(".task-activity")).toContainText("Track 36");
  await expect(page.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "88");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator(".task-activity")).toContainText("已读取 12.0 / 40.0 MiB");
  await expect(page.locator(".task-activity")).toContainText("读取源文件并校验哈希");
  await page.unroute("**/api/jobs?*");
  await page.route("**/api/jobs?*", route => route.fulfill({ status: 502, contentType: "text/html", body: "<html>internal error</html>" }));
  await expect(page.getByRole("alert")).toContainText("服务器返回 HTTP 502，请重试");
  await expect(page.locator(".task-activity")).toContainText("Track 36");
  await expect(page.getByRole("button", { name: "暂停", exact: true })).toBeEnabled();
});


test("completed tag indexing does not show 100 percent while a source is being staged", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const activity = { phase: "read", path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Still downloading", processed: 0, total: 0, percent: 0, read_bytes: 1048576, read_total_bytes: 40 * 1048576 };
  const job = { id: 1, kind: "scan", state: "running", attempts: 0, progress: 0.05, log: "", created: 1800000000, updated: 1800000000, args: {}, counts: { total: 40, done: 0, failed: 0, pending: 39, running: 1, cancelled: 0 }, scan: { ...activity, phase: "indexed", processed: 40, total: 40, percent: 100 }, current: [activity], scope: [], can_delete: false };
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [job], total: 1 } }));
  await page.goto("/jobs");
  await expect(page.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "5");
  await expect(page.locator(".task-activity")).toContainText("Still downloading");
});
