import { test, expect } from "@playwright/test";

test("decode failures show a translated summary and retain full diagnostics", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [{
    id: 2086, kind: "scan", state: "failed", attempts: 3, progress: 0, created: 1800000000, updated: 1800000000, args: {},
    counts: { total: 1, done: 0, failed: 1, pending: 0, running: 0, cancelled: 0 }, current: [], scope: [], can_delete: true,
  }], total: 1 } }));
  await page.route("**/api/jobs/2086/items?*", route => route.fulfill({ json: { items: [{
    id: 2261, kind: "convert", state: "failed", attempts: 3, progress: 0, updated: 1800000000,
    activity: { phase: "convert", path: "Artist/Album/01.flac", title: "Failed track", artist: "Artist", album: "Album" },
    output: "Artist/Album/01.opus", failure: "decode",
    log: "Audio decoding failed\nSource SHA-256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n[dec:flac] Decoding error: Invalid data found when processing input\nreference FLAC decoder failed: MD5 signature mismatch",
  }], total: 1 } }));
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Details", exact: true }).click();
  await expect(page.locator(".task-error-summary")).toContainText("decoder compatibility");
  await expect(page.locator(".task-diagnostic pre")).not.toBeVisible();
  await page.locator(".task-diagnostic summary").click();
  await expect(page.locator(".task-diagnostic pre")).toContainText("MD5 signature mismatch");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator(".task-error-summary")).toContainText("尚不能认定源文件损坏");
  await expect(page.locator(".task-diagnostic pre")).toContainText("Source SHA-256:");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
