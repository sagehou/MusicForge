import { test, expect, type Page } from "@playwright/test";
import type { MaintenanceOptions, Source } from "../src/types";
import { libraryQuality } from "../src/library-quality";
import { shiftLyrics } from "../src/lyrics-timeline";
import { setPreference } from "./preferences";

const options: MaintenanceOptions = { version: 1, schedule_enabled: false, start: "22:00", end: "07:00", timezone: "UTC", day_concurrency: 0, prioritize_new: false, notify_url: "https://notify.example.com/topic", notify_mode: "ntfy", notify_completion: false };
const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 2, scan_minutes: 60, lidarr_prefix: "/music", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const sources: Source[] = [
  { id: 1, path: "Artist/Album/01.flac", title: "First track", artist: "Artist", album: "Album", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 1000000, duration: 120, hash: "same-hash", metadata: {} },
  { id: 2, path: "Artist/Album/03.flac", title: "Third track", artist: "Artist", album: "Album", track: 3, disc: 1, present: true, output: "", output_present: false, status: "missing", error: "", size: 1000000, duration: 120, hash: "same-hash", metadata: {} },
];

async function mock(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: sources }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0 } } }));
  await page.route("**/api/maintenance/loudness", route => route.fulfill({ json: { state: "idle", total: 0, processed: 0, updated: 0, skipped: 0, failed: 0 } }));
}

test("quality report flags exact duplicates and numbering without changing source data", () => {
  const before = JSON.stringify(sources);
  const report = libraryQuality(sources);
  expect(report.duplicates.map(source => source.id)).toEqual([1, 2]);
  expect(report.numbering.map(source => source.id)).toEqual([1, 2]);
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
  await page.goto("/library");
  await page.locator("#maintenance-workbench > summary").click();
  const workbench = page.locator("#maintenance-workbench");
  await workbench.getByRole("button", { name: "Preview current scope", exact: true }).click();
  await expect(workbench.getByRole("region", { name: "Build preview" })).toContainText("1 tracks need building · 1 skipped");
  expect(builds).toBe(0);
  await page.screenshot({ path: "test-results/maintenance-desktop.png", animations: "disabled" });
  await workbench.getByRole("button", { name: "Start this build", exact: true }).click();
  await expect.poll(() => builds).toBe(1);
  await workbench.getByRole("button", { name: "Identical source file hashes 2", exact: true }).click();
  await expect(page.locator(".selection-bar")).toContainText("2");
  await expect(page.locator(".track-table tbody > tr.track-row")).toHaveCount(2);
  await workbench.getByRole("button", { name: "Fill selected outputs", exact: true }).click();
  await expect(workbench.getByRole("button", { name: "Fill current scope", exact: true })).toBeDisabled();
  await workbench.getByRole("button", { name: "Stop loudness maintenance", exact: true }).click();
  await expect.poll(() => gainState).toBe("stopped");
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "theme", "dark");
  await workbench.scrollIntoViewIfNeeded();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/maintenance-phone-dark.png", animations: "disabled" });
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

test("lyric editor previews imports and preserves drafts after publication conflicts", async ({ page }) => {
  await mock(page);
  await page.route("**/api/library/1/audition", route => route.fulfill({ json: { audio_url: "/fixture.wav", format: ".opus", size: 10, lyrics: "[00:01.00]Original", lyrics_state: "ready" } }));
  let saved = false;
  await page.route("**/api/library/1/lyrics/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "edit-token", text: "[00:01.00]Original" } });
    expect(route.request().postDataJSON()).toEqual({ token: "edit-token", text: "[00:02.00]Imported", delay_ms: 100 });
    saved = true;
    return route.fulfill({ status: 409, json: { error: "LRC changed during publication; file preserved" } });
  });
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "Audition First track", exact: true }).click();
  await page.getByRole("button", { name: "Edit lyrics / timing", exact: true }).click();
  const editor = page.getByRole("region", { name: "Edit lyrics / timing", exact: true });
  await editor.getByLabel("Import LRC", { exact: true }).setInputFiles({ name: "test.lrc", mimeType: "text/plain", buffer: Buffer.from("[00:02.00]Imported") });
  await editor.getByRole("button", { name: "+100 ms", exact: true }).click();
  await expect(page.locator(".audition-line").first()).toContainText("Imported");
  await editor.getByRole("button", { name: "Save output lyrics", exact: true }).click();
  await expect(editor.getByRole("alert")).toContainText("file preserved");
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue("[00:02.00]Imported");
  expect(saved).toBe(true);
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
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "View details for First track", exact: true }).click();
  await page.getByRole("button", { name: "Correct output cover", exact: true }).click();
  const editor = page.getByRole("region", { name: "Correct output cover", exact: true });
  const png = await page.evaluate(() => { const canvas = document.createElement("canvas"); canvas.width = 64; canvas.height = 32; const ctx = canvas.getContext("2d")!; ctx.fillStyle = "#4ca3b2"; ctx.fillRect(0, 0, 64, 32); return canvas.toDataURL("image/png").split(",")[1]; });
  await editor.getByLabel("Choose JPEG / PNG", { exact: true }).setInputFiles({ name: "cover.png", mimeType: "image/png", buffer: Buffer.from(png, "base64") });
  const preview = editor.getByRole("img", { name: "Output cover preview", exact: true });
  await expect.poll(() => preview.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(64);
  await editor.getByLabel("Center crop to square", { exact: true }).check();
  await expect.poll(() => preview.evaluate(node => (node as HTMLImageElement).naturalWidth)).toBe(32);
  expect(writes).toBe(0);
  await editor.getByRole("button", { name: "Save output cover", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
  await expect(editor).toHaveCount(0);
});
