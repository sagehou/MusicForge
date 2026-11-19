import { routeLibrary } from "./library-fixture";
import { test, expect, type Page } from "@playwright/test";
import type { MaintenanceOptions, Source } from "../src/types";
import { libraryQuality, numberingIssues } from "../src/library-quality";
import { shiftLyrics } from "../src/lyrics-timeline";
import { openWorkbench, setPreference } from "./preferences";

const options: MaintenanceOptions = { version: 1, schedule_enabled: false, start: "22:00", end: "07:00", timezone: "UTC", day_concurrency: 0, prioritize_new: false, notify_url: "https://notify.example.com/topic", notify_mode: "ntfy", notify_completion: false };
const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 2, scan_minutes: 60, lidarr_prefix: "/music", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const sources: Source[] = [
  { id: 1, path: "Artist/Album/01.flac", title: "First track", artist: "Artist", album: "Album", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 1000000, duration: 120, hash: "same-hash", metadata: {} },
  { id: 2, path: "Artist/Album/03.flac", title: "Third track", artist: "Artist", album: "Album", track: 3, disc: 1, present: true, output: "", output_present: false, status: "missing", error: "", size: 1000000, duration: 120, hash: "same-hash", metadata: {} },
];

async function mock(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", version: "test" } }));
  await routeLibrary(page, route => route.fulfill({ json: sources }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0 } } }));
  await page.route("**/api/maintenance/loudness", route => route.fulfill({ json: { state: "idle", total: 0, processed: 0, updated: 0, skipped: 0, failed: 0 } }));
  await page.route("**/api/maintenance/issues", route => route.fulfill({ json: [] }));
}

test("quality decisions require an ignore reason and can be reopened", async ({ page }) => {
  await mock(page);
  let decisions: { id: number; kind: string; state: string; reason: string }[] = [], writes = 0;
  await page.route("**/api/maintenance/issues", route => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON();
      expect(body.ids).toEqual([1, 2]); expect(body.kind).toBe("duplicates");
      decisions = body.state === "open" ? [] : body.ids.map((id: number) => ({ id, kind: body.kind, state: body.state, reason: body.reason })); writes++;
      return route.fulfill({ json: { updated: 2 } });
    }
    return route.fulfill({ json: decisions });
  });
  await page.goto("/library?view=tracks&mode=organize");
  await openWorkbench(page, "issues");
  const quality = page.getByRole("region", { name: "Library quality report", exact: true });
  await expect(quality.locator(".source-check-list")).toContainText("Album missing numbers: 2");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-source-checks-desktop.png", animations: "disabled" });
  await quality.getByRole("button", { name: "Identical source file hashes 2", exact: true }).click();
  await expect(quality.locator(".source-check-list")).toContainText("Identical contents at 1 other source paths");
  await expect(quality.locator(".source-check-list")).toContainText(sources[1].path);
  await expect(page.locator(".selection-bar")).toHaveCount(0);
  await quality.getByRole("button", { name: "Select this page of issues", exact: true }).click();
  await quality.getByRole("combobox", { name: "Decision", exact: true }).selectOption("ignored");
  await expect(quality.getByRole("button", { name: "Save decision", exact: true })).toBeDisabled();
  await quality.getByLabel("Reason / note", { exact: true }).fill("Intentional duplicate edition");
  expect(writes).toBe(0);
  await quality.getByRole("button", { name: "Save decision", exact: true }).click();
  await expect(quality.getByRole("button", { name: "Identical source file hashes 0", exact: true })).toHaveAttribute("aria-pressed", "true");
  await quality.getByLabel("Show ignored issues (2)", { exact: true }).check();
  await expect(quality.locator(".review-issues")).toContainText("Intentional duplicate edition");
  await quality.getByRole("combobox", { name: "Decision", exact: true }).selectOption("open");
  await quality.getByRole("button", { name: "Save decision", exact: true }).click();
  await expect(quality.getByRole("button", { name: "Identical source file hashes 2", exact: true })).toBeEnabled();
  expect(writes).toBe(2);
  await quality.getByRole("textbox", { name: "Reason / note", exact: true }).focus();
  await page.keyboard.press("Escape");
  await expect(quality).toBeVisible();
  await page.locator("#workbench-lyrics").focus();
  await page.keyboard.press("Enter");
  await expect(quality).toBeHidden();
  await openWorkbench(page, "issues");
  await expect(quality.getByRole("textbox", { name: "Reason / note", exact: true })).toHaveValue("Intentional duplicate edition");
});

test("audio conversion has one preview flow and never queues lyrics or loudness", async ({ page }) => {
  await mock(page);
  let plans = 0, builds = 0;
  const otherWrites: string[] = [];
  await page.route("**/api/library/plan", route => {
    plans++; expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] });
    return route.fulfill({ json: { encoding: settings.encoding, tracks: 1, skipped: 1, unverified: 0, input_bytes: 1000000, estimated_output_bytes: 2880000, current_output_bytes: 123, largest_source_bytes: 1000000, staging_limit_bytes: 4294967296, unknown_duration: 0, reasons: { missing: 1 } } });
  });
  await page.route("**/api/library/rebuild", route => { builds++; expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] }); return route.fulfill({ status: 202, json: { ok: true } }); });
  for (const path of ["/lyrics/search", "/maintenance/loudness"]) await page.route(`**/api${path}`, route => {
    if (route.request().method() === "POST") otherWrites.push(path);
    return route.fulfill({ json: { state: "idle" } });
  });
  await page.goto("/library?mode=organize"); await openWorkbench(page, "suggestions");
  const conversion = page.getByRole("region", { name: "Audio conversion", exact: true });
  await expect(conversion).toContainText("No output1");
  expect(plans + builds).toBe(0);
  await expect(conversion.getByRole("button", { name: "Start this build", exact: true })).toHaveCount(0);
  await conversion.getByRole("button", { name: "Preview current scope", exact: true }).click();
  await expect(conversion).toContainText("1 tracks need building · 1 skipped");
  expect(builds).toBe(0);
  await conversion.getByRole("button", { name: "Start this build", exact: true }).click();
  await expect.poll(() => builds).toBe(1);
  expect(plans).toBe(1); expect(otherWrites).toEqual([]);
});

test("operation snapshots precede explicit undo and conflicts stay visible", async ({ page }) => {
  await mock(page);
  const operation = { id: "operation", kind: "lyrics", created: 1700000000000000000, items: [{ id: 1, path: sources[0].path, output: sources[0].output, state: "applied", undoable: true }] };
  let writes = 0;
  await page.route("**/api/maintenance/operations", route => route.fulfill({ json: [operation] }));
  await page.route("**/api/maintenance/operations/operation/snapshot?*", route => route.fulfill({ json: { text: new URL(route.request().url()).searchParams.get("side") === "before" ? "[00:01]Original" : "[00:02]New" } }));
  await page.route("**/api/maintenance/operations/operation/undo", route => { expect(route.request().postDataJSON()).toEqual({ items: [0] }); writes++; return route.fulfill({ status: 202, json: { ok: true } }); });
  await page.route("**/api/maintenance/undo", route => route.fulfill({ json: writes ? { state: "complete", total: 1, processed: 1, updated: 0, failed: 1, skipped: 0, issues: [{ id: 1, error: "Maintenance undo conflict: lyrics changed; current file preserved" }] } : { state: "idle" } }));
  await page.goto("/library?mode=organize"); await openWorkbench(page, "history");
  const log = page.getByRole("region", { name: "Change log", exact: true });
  await log.getByRole("button", { name: "View changes", exact: true }).click();
  await log.getByRole("button", { name: "Compare saved snapshots", exact: true }).click();
  await expect(log.getByRole("textbox", { name: "Before", exact: true })).toHaveValue("[00:01]Original");
  await expect(log.getByRole("textbox", { name: "After", exact: true })).toHaveValue("[00:02]New"); expect(writes).toBe(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await log.screenshot({ path: "test-results/maintenance-undo-desktop.png", animations: "disabled" });
  await expect(log.getByRole("button", { name: "Confirm undo of 0 items", exact: true })).toBeDisabled();
  await log.getByRole("checkbox").check();
  await log.getByRole("button", { name: "Confirm undo of 1 items", exact: true }).click();
  await expect(log.getByRole("status")).toContainText("current file preserved"); expect(writes).toBe(1);
});

test("change log searches paths and filters results without editing files", async ({ page }) => {
  await mock(page);
  const operations = Array.from({ length: 12 }, (_, index) => ({ id: `change-${index}`, kind: index % 2 ? "artwork" : "lyrics", created: 1700000000000000000 + index * 1e9, items: [{ id: index + 1, path: `Artist/Album/${index}.flac`, output: `Artist/Album/${index}.opus`, state: index === 2 ? "failed" : "applied", undoable: index !== 2, error: index === 2 ? "Lyrics changed; current file preserved" : undefined }] }));
  let writes = 0;
  await page.route("**/api/maintenance/operations", route => route.fulfill({ json: operations }));
  await page.route("**/api/maintenance/operations/*/undo", route => { writes++; return route.fulfill({ json: {} }); });
  await page.route("**/api/maintenance/undo", route => route.fulfill({ json: { state: "idle" } }));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?mode=organize"); await openWorkbench(page, "history");
  const log = page.locator("#workbench-panel-history .operation-log");
  await expect(log.locator("tbody tr")).toHaveCount(10);
  await expect(log.locator(".maintenance-report")).toHaveCount(0);
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-change-log-desktop.png", animations: "disabled" });
  await log.getByRole("button", { name: "Next changes page", exact: true }).click();
  await expect(log.locator("tbody tr")).toHaveCount(2);
  await log.getByRole("searchbox", { name: "Search paths or errors", exact: true }).fill("2.flac");
  await expect(log.locator("tbody tr")).toHaveCount(1);
  await expect(log.locator("tbody")).toContainText("Failed 1");
  await log.getByRole("combobox", { name: "Change type", exact: true }).selectOption("artwork");
  await expect(log).toContainText("No changes match these filters");
  await log.getByRole("combobox", { name: "Change type", exact: true }).selectOption("all");
  await log.getByRole("searchbox", { name: "Search paths or errors", exact: true }).fill("");
  await log.getByRole("combobox", { name: "Result", exact: true }).selectOption("failed");
  await expect(log.locator("tbody tr")).toHaveCount(1);
  await expect(log.getByRole("link", { name: "Open task logs", exact: true })).toHaveAttribute("href", "/jobs");
  expect(writes).toBe(0);
  await page.setViewportSize({ width: 320, height: 844 });
  await setPreference(page, "language", "zh-CN"); await setPreference(page, "theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await expect(log.locator("tbody")).toContainText("失败 1");
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-change-log-phone-dark-zh.png", animations: "disabled" });
});

test("storage checks and migration previews keep settings unchanged on desktop and phone", async ({ page }) => {
  await mock(page);
  let checks = 0, previews = 0, settingsWrites = 0, verifies = 0;
  const space = { path: "/new/output", total_bytes: 1000000000, free_bytes: 800000000, empty: true };
  await page.route("**/api/settings", route => { if (route.request().method() === "PUT") settingsWrites++; return route.fulfill({ json: { settings } }); });
  await page.route("**/api/maintenance/storage/health", route => route.fulfill({ json: { source: "/music/source", output: "/music/output", config: "/config", online: true, checked_at: 1700000000, message: "", tracks: 2, outputs: 1, output_bytes: 12345, staging_bytes: 0, staging_limit_bytes: 4294967296 } }));
  await page.route("**/api/maintenance/storage/check", route => { checks++; return route.fulfill({ json: { output: space, source: { error: "Source mount offline" } } }); });
  await page.route("**/api/maintenance/storage/verify", route => { expect(route.request().postDataJSON()).toEqual({ ids: [1] }); verifies++; return route.fulfill({ status: 202, json: { ok: true } }); });
  await page.route("**/api/maintenance/storage", route => route.fulfill({ json: verifies ? { state: "complete", total: 1, processed: 1, updated: 1, failed: 0, skipped: 0 } : { state: "idle" } }));
  await page.route("**/api/maintenance/migration/preview", route => { expect(route.request().postDataJSON()).toEqual({ source: "/new/source", output: "/new/output" }); previews++; return route.fulfill({ json: { source: "/new/source", output: space, source_bytes: 1000000, output_bytes: 12345, paths_applied: false } }); });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?mode=organize"); await openWorkbench(page, "storage");
  const storage = page.getByRole("region", { name: "Storage health", exact: true });
  await expect(storage).toContainText("/music/output"); expect(checks + previews + verifies + settingsWrites).toBe(0);
  await expect(storage).toContainText("Last checked:");
  await storage.getByRole("button", { name: "Check mounts and free space", exact: true }).click();
  await expect(storage).toContainText("Source mount offline");
  await storage.getByRole("button", { name: "Verify 1 output checksums", exact: true }).click();
  await storage.locator(".migration-guide > summary").click();
  await storage.getByLabel("Proposed source directory", { exact: true }).fill("/new/source");
  await storage.getByLabel("Proposed output directory", { exact: true }).fill("/new/output");
  await storage.getByRole("button", { name: "Check migration paths", exact: true }).click();
  await expect(storage).toContainText("Settings have not changed");
  await expect(storage.getByRole("link", { name: "Open library path settings", exact: true })).toHaveAttribute("href", "/settings?section=library");
  expect(checks).toBe(1); expect(previews).toBe(1); expect(verifies).toBe(1); expect(settingsWrites).toBe(0);
  await storage.screenshot({ path: "test-results/maintenance-storage-desktop.png", animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 }); await setPreference(page, "theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await storage.screenshot({ path: "test-results/maintenance-storage-phone-dark.png", animations: "disabled" });
});

test("quality report flags exact duplicates and numbering without changing source data", () => {
  const before = JSON.stringify(sources);
  const report = libraryQuality(sources);
  expect(report.duplicates.map(source => source.id)).toEqual([1, 2]);
  expect(report.numbering.map(source => source.id)).toEqual([1, 2]);
  expect(numberingIssues(sources)).toEqual({ missing: ["2"], repeated: [], untagged: 0 });
  expect(numberingIssues([{ ...sources[0], track: 0 }, { ...sources[1], track: 3 }, { ...sources[1], id: 3, track: 3 }])).toEqual({ missing: ["1–2"], repeated: [3], untagged: 1 });
  expect(libraryQuality([{ ...sources[0], path: "Root/Artist/Album A/01.flac" }, { ...sources[0], id: 3, path: "Root/Artist/Album B/01.flac" }]).numbering).toHaveLength(0);
  expect(report.duration).toHaveLength(0);
  expect(report.lyrics.map(source => source.id)).toEqual([1]);
  expect(JSON.stringify(sources)).toBe(before);
  expect(shiftLyrics("[offset:250]\n[00:01.25][00:03.250]Original\n[00:01.25]译文", -100)).toBe("[00:00.900][00:02.900]Original\n[00:00.900]译文");
});

test("maintenance workbench previews before queueing and preserves scope on phone", async ({ page }) => {
  await mock(page);
  let builds = 0;
  let gainState = "idle";
  await page.route("**/api/maintenance/loudness", route => {
    if (route.request().method() === "POST") { expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] }); gainState = "running"; }
    if (route.request().method() === "DELETE") gainState = "stopped";
    return route.fulfill({ json: { state: gainState, total: 1, processed: 0, updated: 0, skipped: 0, failed: 0 } });
  });
  await page.route("**/api/library/plan", route => {
    expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] });
    return route.fulfill({ json: { encoding: settings.encoding, tracks: 1, skipped: 1, unverified: 0, input_bytes: 1000000, estimated_output_bytes: 2880000, current_output_bytes: 0, largest_source_bytes: 1000000, staging_limit_bytes: 4294967296, unknown_duration: 0, reasons: { missing: 1 } } });
  });
  await page.route("**/api/library/rebuild", route => { builds++; expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] }); return route.fulfill({ json: { ok: true } }); });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?mode=organize");
  await openWorkbench(page, "suggestions");
  const workbench = page.locator("#maintenance-workbench");
  await workbench.getByRole("button", { name: "Preview current scope", exact: true }).click();
  await expect(workbench.getByRole("region", { name: "Build preview" })).toContainText("1 tracks need building · 1 skipped");
  await openWorkbench(page, "loudness");
  await expect(workbench.getByRole("region", { name: "Build preview" })).toBeHidden();
  await openWorkbench(page, "suggestions");
  await expect(workbench.getByRole("region", { name: "Build preview" })).toContainText("1 tracks need building · 1 skipped");
  expect(builds).toBe(0);
  await page.screenshot({ path: "test-results/maintenance-desktop.png", animations: "disabled" });
  await workbench.getByRole("button", { name: "Start this build", exact: true }).click();
  await expect.poll(() => builds).toBe(1);
  await openWorkbench(page, "issues");
  await workbench.getByRole("button", { name: "Identical source file hashes 2", exact: true }).click();
  await expect(page.locator(".selection-bar")).toHaveCount(0);
  await workbench.getByRole("button", { name: "View track", exact: true }).first().click();
  await expect(page.locator(".selection-bar")).toContainText("Selected: 1");
  await expect(page).not.toHaveURL(/mode=organize/);
  await expect(page.locator(".track-table tbody > tr.track-row")).toHaveCount(2);
  await openWorkbench(page, "loudness");
  await workbench.getByRole("button", { name: "Fill current scope", exact: true }).click();
  await expect(workbench.getByRole("button", { name: "Fill current scope", exact: true })).toBeDisabled();
  await workbench.getByRole("button", { name: "Stop loudness maintenance", exact: true }).click();
  await expect.poll(() => gainState).toBe("stopped");
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "theme", "dark");
  await workbench.scrollIntoViewIfNeeded();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/maintenance-phone-dark.png", animations: "disabled" });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum&mode=organize");
  await openWorkbench(page, "lyrics");
  await expect(page.locator("#library-comparison")).toHaveCount(0);
  await expect(page.locator("#maintenance-workbench").getByRole("button", { name: "Compare with Lidarr", exact: true })).toHaveCount(0);
});

test("schedule and notifications share save, discard and secret controls", async ({ page }) => {
  await mock(page);
  let current = { ...options };
  let writes = 0, tests = 0;
  await page.route("**/api/settings", route => {
    if (route.request().method() === "PUT") { const body = route.request().postDataJSON(); writes++; current = body.maintenance; expect(current.notify_token).toBe("new-token"); return route.fulfill({ json: { encoding_changed: false } }); }
    return route.fulfill({ json: { settings, maintenance: { ...current, notify_token: undefined }, configured: { notification_token: true }, public_url: "", allowed_origins: [] } });
  });
  await page.route("**/api/maintenance/notify-test", route => { tests++; return route.fulfill({ json: { ok: true } }); });
  await page.goto("/settings?section=encoding");
  await page.getByLabel("Enable a build window", { exact: true }).check();
  await page.getByLabel("Time zone (IANA)", { exact: true }).fill("Asia/Shanghai");
  await page.getByLabel("Prioritize tracks without an output", { exact: true }).check();
  await page.getByRole("tab", { name: "Integrations", exact: true }).click();
  await expect(page.getByRole("button", { name: "Send test notification", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await page.getByRole("tab", { name: "Encoding", exact: true }).click();
  await expect(page.getByLabel("Enable a build window", { exact: true })).not.toBeChecked();
  await page.getByLabel("Enable a build window", { exact: true }).check();
  await page.getByLabel("Time zone (IANA)", { exact: true }).fill("Asia/Shanghai");
  await page.getByRole("tab", { name: "Integrations", exact: true }).click();
  await page.getByLabel("Bearer token (optional)", { exact: true }).fill("new-token");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
  await expect(page.getByLabel("Bearer token (optional)", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "Send test notification", exact: true }).click();
  await expect.poll(() => tests).toBe(1);
  expect(current.timezone).toBe("Asia/Shanghai");
  expect(current.schedule_enabled).toBe(true);
});

test("workbench finds lyric candidates and saves pasted LRC for the scoped playable tracks", async ({ page }) => {
  await mock(page);
  const outside = { ...sources[0], id: 3, path: "Other/Album/01.flac", artist: "Other", output: "Other/Album/01.opus" };
  let text = "", writes = 0, searches = 0;
  await routeLibrary(page, route => route.fulfill({ json: [text ? { ...sources[0], lyrics: { state: "ready", output: sources[0].output, signature: "current", checked: 100 } } : sources[0], sources[1], outside] }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { providers: ["lrclib"], min_score: 96 }, report: { state: "idle" } } }));
  await page.route("**/api/library/1/lyrics/candidates", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { query: "First track Artist", groups: [] } });
    expect(route.request().postDataJSON()).toEqual({ provider: "lrclib", query: "First track", refresh: false }); searches++;
    return route.fulfill({ json: { provider: "lrclib", token: "candidate", candidates: [{ index: 0, provider: "lrclib", title: "First track", artist: "Artist", album: "Album", duration: 120, high_confidence: true, match_score: 100, duration_delta: 0, script: "simplified" }] } });
  });
  await page.route("**/api/library/1/lyrics/preview", route => route.fulfill({ json: { original: "[00:01.00]Online candidate" } }));
  await page.route("**/api/library/1/audition", route => route.fulfill({ json: { audio_url: "/fixture.wav", format: ".opus", size: 10 } }));
  let releaseSave!: () => void;
  const saving = new Promise<void>(resolve => { releaseSave = resolve; });
  await page.route("**/api/library/1/lyrics/edit", async route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "manual-token", text } });
    expect(route.request().postDataJSON()).toEqual({ token: "manual-token", text: "[00:02.00]Pasted lyrics", delay_ms: 0 }); writes++;
    await saving; text = "[00:02.00]Pasted lyrics";
    return route.fulfill({ json: { text } });
  });
  await page.goto("/library?view=tracks&artist=Artist&album=Artist%2FAlbum&q=Third&mode=organize");
  await openWorkbench(page, "lyrics");
  const list = page.locator("#workbench-panel-lyrics .content-maintenance");
  await expect(list.locator(".content-maintenance-list > li")).toHaveCount(1);
  await expect(list).toContainText("First track");
  await page.locator("#maintenance-search-1").click();
  const picker = page.locator("#maintenance-picker-1");
  await expect(picker.getByLabel("Title or artist", { exact: true })).toHaveValue("First track Artist");
  expect(searches + writes).toBe(0);
  await picker.getByLabel("Title or artist", { exact: true }).fill("First track");
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("Online candidate");
  expect(searches).toBe(1); expect(writes).toBe(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-lyrics-search-desktop.png", animations: "disabled" });
  await picker.getByRole("button", { name: "Close lyrics selection", exact: true }).click();
  await expect(page.locator("#maintenance-search-1")).toBeFocused();
  await page.locator("#maintenance-edit-1").click();
  const editor = page.locator("#maintenance-lyrics-editor-1");
  await editor.getByLabel("LRC text", { exact: true }).fill("[00:02.00]Pasted lyrics");
  await editor.getByRole("button", { name: "Save output lyrics", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
  await expect(page.locator("#workbench-covers")).toBeDisabled();
  await expect(editor.getByRole("button", { name: "Close panel", exact: true })).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(editor).toBeVisible();
  releaseSave();
  await expect(editor).toHaveCount(0);
  await expect(list).toContainText("Nothing needs attention in this scope.");
  await list.getByRole("button", { name: "All", exact: true }).click();
  await expect(list.locator(".content-maintenance-list > li")).toHaveCount(1);
  await expect(list).toContainText("Available");
  await expect(list.getByRole("button", { name: "Edit lyrics", exact: true })).toBeVisible();
  expect(searches).toBe(1); expect(writes).toBe(1);
});

test("workbench imports LRC directly and preserves drafts across categories and publication conflicts", async ({ page }) => {
  await mock(page);
  let saved = false;
  await page.route("**/api/library/1/lyrics/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "edit-token", text: "[00:01.00]Original" } });
    expect(route.request().postDataJSON()).toEqual({ token: "edit-token", text: "[00:02.00]Imported", delay_ms: 100 });
    saved = true;
    return route.fulfill({ status: 409, json: { error: "LRC changed during publication; file preserved" } });
  });
  await page.goto("/library?view=tracks&mode=organize");
  await openWorkbench(page, "lyrics");
  await page.locator("#maintenance-edit-1").click();
  const editor = page.locator("#maintenance-lyrics-editor-1");
  await editor.getByLabel("Import LRC", { exact: true }).setInputFiles({ name: "test.lrc", mimeType: "text/plain", buffer: Buffer.from("[00:02.00]Imported") });
  await editor.getByRole("button", { name: "+100 ms", exact: true }).click();
  await openWorkbench(page, "covers");
  await expect(editor).toBeHidden();
  await openWorkbench(page, "lyrics");
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue("[00:02.00]Imported");
  await expect(editor.getByLabel("Timing adjustment (ms)", { exact: true })).toHaveValue("100");
  await editor.getByRole("button", { name: "Save output lyrics", exact: true }).click();
  await expect(editor.getByRole("alert")).toContainText("file preserved");
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue("[00:02.00]Imported");
  expect(saved).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-lyrics-edit-desktop.png", animations: "disabled" });
  await page.setViewportSize({ width: 320, height: 844 });
  await setPreference(page, "language", "zh-CN");
  await setPreference(page, "theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await expect(editor.getByLabel("LRC 文本", { exact: true })).toHaveValue("[00:02.00]Imported");
  await editor.screenshot({ path: "test-results/workbench-lyrics-edit-phone-dark-zh.png", animations: "disabled" });
});

test("output cover correction previews a crop and sends an owned replacement explicitly", async ({ page }) => {
  await mock(page);
  let writes = 0;
  await page.route("**/api/library/1/artwork/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "cover-token", override: false } });
    const body = route.request().postDataJSON();
    expect(body.token).toBe("cover-token"); expect(Buffer.from(body.image, "base64").subarray(0, 2)).toEqual(Buffer.from([255, 216]));
    writes++; return route.fulfill({ json: { ok: true } });
  });
  await page.goto("/library?view=tracks&mode=organize");
  await openWorkbench(page, "covers");
  await expect(page.locator("#workbench-panel-covers .content-maintenance-list > li")).toHaveCount(1);
  await page.locator("#maintenance-cover-1").click();
  const editor = page.locator("#maintenance-artwork-1");
  await expect(editor.getByLabel("Artist / album search", { exact: true })).toBeVisible();
  const png = await page.evaluate(() => { const canvas = document.createElement("canvas"); canvas.width = 64; canvas.height = 32; const ctx = canvas.getContext("2d")!; ctx.fillStyle = "#4ca3b2"; ctx.fillRect(0, 0, 64, 32); return canvas.toDataURL("image/png").split(",")[1]; });
  await page.route("**/api/library/1/artwork/search", route => {
    const body = route.request().postDataJSON();
    expect(body.query).toBe("Artist Album");
    if (body.provider === "itunes") return route.fulfill({ status: 502, json: { error: "Cover provider returned HTTP 503" } });
    expect(body.provider).toBe("netease");
    return route.fulfill({ json: { candidates: [{ id: "netease:23505", title: "Album", artist: "Artist", country: "", date: "2026", source: "NetEase Music", url: "https://music.163.com/album?id=23505" }] } });
  });
  await page.route("**/api/library/1/artwork/preview?release=netease%3A23505", route => route.fulfill({ json: { image: png, width: 64, height: 32, source: "NetEase Music", url: "https://music.163.com/album?id=23505" } }));
  await expect(editor.getByLabel("Cover source", { exact: true })).toHaveValue("itunes");
  await editor.getByLabel("Artist / album search", { exact: true }).press("Enter");
  await expect(editor.getByRole("alert")).toContainText("503");
  await editor.getByRole("button", { name: "Search the other source", exact: true }).click();
  await expect(editor.getByLabel("Cover source", { exact: true })).toHaveValue("netease");
  await expect(editor.getByRole("alert")).toHaveCount(0);
  await editor.getByRole("button", { name: "Preview this cover", exact: true }).click();
  await expect(editor.getByRole("status")).toContainText("64 × 32 px");
  expect(writes).toBe(0);
  await editor.getByLabel("Choose JPEG / PNG", { exact: true }).setInputFiles({ name: "cover.png", mimeType: "image/png", buffer: Buffer.from(png, "base64") });
  const preview = editor.getByRole("img", { name: "Output cover preview", exact: true });
  await expect.poll(() => preview.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(64);
  await editor.getByLabel("Center crop to square", { exact: true }).check();
  await expect.poll(() => preview.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(32);
  expect(writes).toBe(0);
  await openWorkbench(page, "lyrics");
  await openWorkbench(page, "covers");
  await expect.poll(() => preview.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(32);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator("#maintenance-workbench").screenshot({ path: "test-results/workbench-covers-desktop.png", animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await editor.screenshot({ path: "test-results/workbench-covers-phone-dark.png", animations: "disabled" });
  await editor.getByRole("button", { name: "Save output cover", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
  await expect(editor).toHaveCount(0);
});
