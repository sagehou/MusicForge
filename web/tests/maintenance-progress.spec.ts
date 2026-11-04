import { routeLibrary } from "./library-fixture";
import { test, expect, type Page } from "@playwright/test";
import { openLibraryOptions, selectLibraryPage, setPreference } from "./preferences";
import type { Source } from "../src/types";

const source: Source = { id: 1, path: "Artist/Album/01.flac", title: "Track one", artist: "Artist", album: "Album", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics: { state: "failed", output: "Artist/Album/01.opus", signature: "audio", checked: 5 } };
async function mock(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  await routeLibrary(page, route => route.fulfill({ json: [source] }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0 } } }));
}

test("saved library views preserve filters, update results and clear selections", async ({ page }) => {
  await mock(page);
  let sources = [source];
  await routeLibrary(page, route => route.fulfill({ json: sources }));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?view=tracks&artist=Artist&status=ready&lyrics=failed&sort=attention&q=Track");
  const views = page.locator(".saved-library-views");
  await views.locator(":scope > summary").click();
  await views.getByLabel("View name", { exact: true }).fill("Lyrics to retry");
  await views.getByRole("button", { name: "Save current view", exact: true }).click();
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem("musicforge.library.views") || "[]"));
  expect(saved).toEqual([{ name: "Lyrics to retry", search: "view=tracks&status=ready&lyrics=failed&sort=attention&artist=Artist&q=Track" }]);
  await views.getByLabel("View name", { exact: true }).fill("Lyrics to retry");
  await expect(views.getByRole("button", { name: "Update saved view", exact: true })).toBeEnabled();
  await views.getByRole("button", { name: "Update saved view", exact: true }).click();
  expect(await views.getByRole("button", { name: "Lyrics to retry", exact: true }).count()).toBe(1);
  await selectLibraryPage(page);
  await expect(page.locator(".selection-bar")).toContainText("Selected: 1");
  await page.getByRole("button", { name: "Clear filters", exact: true }).click();
  await page.getByRole("searchbox").fill("Other");
  await views.getByRole("button", { name: "Lyrics to retry", exact: true }).click();
  await expect(page.getByRole("searchbox")).toHaveValue("Track");
  await expect(page.locator(".selection-bar")).toHaveCount(0);
  await expect(page).toHaveURL(/lyrics=failed/);
  await openLibraryOptions(page);
  await expect(page.getByRole("combobox", { name: "Filter by status", exact: true })).toHaveValue("ready");
  await page.keyboard.press("Escape");
  sources = [source, { ...source, id: 2, title: "Track two", path: "Artist/Album/02.flac", output: "Artist/Album/02.opus" }];
  await page.reload();
  await views.locator(":scope > summary").click();
  await views.getByRole("button", { name: "Lyrics to retry", exact: true }).click();
  await expect(page.locator(".track-row")).toHaveCount(2);
  await views.screenshot({ path: "test-results/saved-library-views-desktop.png", animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 }); await setPreference(page, "theme", "dark"); await setPreference(page, "language", "zh-CN");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await views.screenshot({ path: "test-results/saved-library-views-phone-dark.png", animations: "disabled" });
  await views.getByRole("button", { name: "删除保存的视图：Lyrics to retry", exact: true }).click();
  expect(await page.evaluate(() => localStorage.getItem("musicforge.library.views"))).toBe("[]");
});

test("saved views reject corrupt storage, sanitize restored filters and report write failures", async ({ page }) => {
  await mock(page);
  await page.addInitScript(() => localStorage.setItem("musicforge.library.views", '{"invalid":true}'));
  await page.goto("/library");
  const views = page.locator(".saved-library-views");
  await views.locator(":scope > summary").click();
  await expect(views.locator(".saved-view-list .actions")).toHaveCount(0);
  await page.evaluate(() => {
    localStorage.setItem("musicforge.library.views", JSON.stringify([{ name: "Safe", search: "view=tracks&status=not-a-state&lyrics=failed&lyrics_track=1&selected=1&redirect=https://example.org" }, { name: {}, search: "view=tracks" }]));
    window.dispatchEvent(new StorageEvent("storage", { key: "musicforge.library.views" }));
  });
  await views.getByRole("button", { name: "Safe", exact: true }).click();
  await expect(page).toHaveURL(/\/library\?view=tracks&lyrics=failed$/);
  await expect(page.locator(".lyrics-standalone")).toHaveCount(0);
  await page.evaluate(() => {
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function(key, value) { if (key === "musicforge.library.views") throw new DOMException("Storage unavailable"); original.call(this, key, value); };
  });
  await views.getByLabel("View name", { exact: true }).fill("Cannot save");
  await views.getByRole("button", { name: "Save current view", exact: true }).click();
  await expect(views.getByRole("alert")).toContainText("Could not save");
  await expect(views.getByRole("button", { name: "Cannot save", exact: true })).toHaveCount(0);
});

test("maintenance progress and grouped recovery reuse exact existing actions", async ({ page }) => {
  await mock(page);
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [], total: 0 } }));
  const base = { total: 2, processed: 1, updated: 1, failed: 0, skipped: 0, state: "running", current: "Artist/Album/01.opus" };
  let stopped = false, resumed = false;
  await page.route("**/api/maintenance/progress", route => route.fulfill({ json: { reports: [{ ...base, kind: "lyrics", skipped: 18, state: resumed ? "running" : "stopped", remaining: 1, resumable: !resumed }, { ...base, kind: "loudness", waiting_reason: stopped ? undefined : "schedule", state: stopped ? "stopped" : "running" }, { ...base, kind: "storage", state: "stopped" }], cooldowns: [{ provider: "migu", retry_at: 1800000000 }] } }));
  await page.route("**/api/maintenance/failures", route => route.fulfill({ json: [
    { reason: "decode", jobs: 4, retryable_jobs: 2, lyrics: 0, lyric_ids: [], samples: [{ kind: "jobs", id: 9, error: "Audio decoding failed" }] },
    { reason: "rate_limit", jobs: 0, retryable_jobs: 0, lyrics: 1, lyric_ids: [7], samples: [{ kind: "lyrics", id: 7, path: "Artist/Album/07.flac", error: "migu lyrics service is cooling down; retry later" }] },
  ] }));
  const writes: { path: string; body?: unknown }[] = [];
  await page.route("**/api/lyrics/resume", route => { expect(route.request().method()).toBe("POST"); resumed = true; writes.push({ path: "/lyrics/resume" }); return route.fulfill({ status: 202, json: { selected: 1 } }); });
  let fail = true;
  await page.route("**/api/jobs/control", route => {
    writes.push({ path: "/jobs/control", body: route.request().postDataJSON() });
    return route.fulfill(fail ? { status: 409, json: { error: "Please retry shortly" } } : { status: 202, json: { changed: 1 } });
  });
  await page.route("**/api/lyrics/search", route => { writes.push({ path: "/lyrics/search", body: route.request().postDataJSON() }); return route.fulfill({ status: 202, json: { selected: 1 } }); });
  await page.route("**/api/maintenance/loudness", route => { expect(route.request().method()).toBe("DELETE"); stopped = true; writes.push({ path: "/maintenance/loudness" }); return route.fulfill({ json: { ok: true } }); });
  await page.setViewportSize({ width: 1440, height: 1000 }); await page.goto("/jobs");
  const progress = page.locator("#maintenance-progress");
  await expect(progress).toHaveAccessibleName("Maintenance progress");
  await expect(progress).toContainText("Waiting for the configured maintenance window");
  await expect(progress).toContainText("skipped 18");
  await expect(progress.getByRole("button", { name: "Continue 1 remaining tracks", exact: true })).toBeVisible();
  expect(writes).toHaveLength(0);
  await progress.getByRole("button", { name: "Continue 1 remaining tracks", exact: true }).click();
  await expect(progress.getByRole("progressbar", { name: "LRC lyrics", exact: true })).toBeVisible();
  expect(writes).toEqual([{ path: "/lyrics/resume" }]);
  writes.length = 0;
  await page.locator(".maintenance-failures > summary").click();
  const decode = page.getByRole("region", { name: "Audio decoding", exact: true });
  await expect(decode).toContainText("Paused, stopped or still active queues are preserved");
  await decode.getByRole("button", { name: "Retry 2 queue items in this group", exact: true }).click();
  await expect(page.locator(".toast")).toContainText("Please retry shortly");
  fail = false;
  await decode.getByRole("button", { name: "Retry 2 queue items in this group", exact: true }).click();
  const limited = page.getByRole("region", { name: "Service limit / cooldown", exact: true });
  await limited.getByRole("button", { name: "Retry lyrics for 1 tracks in this group", exact: true }).click();
  expect(writes).toEqual([{ path: "/jobs/control", body: { action: "retry", all: true, failure: "decode" } }, { path: "/jobs/control", body: { action: "retry", all: true, failure: "decode" } }, { path: "/lyrics/search", body: { ids: [7] } }]);
  await progress.locator(".provider-cooldowns > summary").click();
  await expect(progress).toContainText("Migu");
  await progress.screenshot({ path: "test-results/maintenance-progress-desktop.png", animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 }); await setPreference(page, "theme", "dark"); await setPreference(page, "language", "zh-CN");
  await expect(progress).toHaveAccessibleName("维护进度");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/maintenance-progress-phone-dark.png", fullPage: true, animations: "disabled" });
  await progress.locator('.maintenance-progress-row').filter({ hasText: "补全响度标签" }).getByRole("button").click();
  await expect(progress).not.toContainText("正在等待配置的维护时段");
  expect(writes.at(-1)).toEqual({ path: "/maintenance/loudness" });
});
