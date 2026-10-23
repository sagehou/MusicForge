import { openLibraryOptions, setPreference } from "./preferences";
import { test, expect } from "@playwright/test";
import type { Source } from "../src/types";

const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

test("incremental filling shows pending and skipped counts and succeeds when no work remains", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics: { state: "ready", output: "Album/01.opus", signature: "audio", checked: 1 } };
  let report = { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false, skipped: 0 };
  let pending = 0;
  const searches: unknown[] = [];
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source] }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false, min_score: 96 }, report } }));
  await page.route("**/api/lyrics/search", route => {
    searches.push(route.request().postDataJSON());
    report = { ...report, state: "complete", total: pending, processed: pending, found: pending, skipped: 1 };
    return route.fulfill({ status: 202, json: { selected: pending, skipped: 1 } });
  });
  await page.goto("/library?view=tracks");
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  await page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true }).click();
  const progress = page.getByRole("status", { name: "Current lyrics batch", exact: true });
  await expect(progress).toContainText("No lyrics need filling. Existing results were retained.");
  await expect(progress).toContainText("Skipped 1 tracks with lyrics or no work due");
  await expect(progress).not.toContainText("0/0");
  await expect(page.locator(".toast")).toContainText("No lyrics need filling");
  pending = 1;
  await page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true }).click();
  await expect(progress).toContainText("This batch: checked 1/1");
  await expect(progress).toContainText("Skipped 1 tracks with lyrics or no work due");
  expect(searches).toEqual([{ all: true, refresh: false }, { all: true, refresh: false }]);
  await setPreference(page, "language", "zh-CN");
  await expect(page.getByRole("status", { name: "本次补齐进度", exact: true })).toContainText("已跳过 1 首");
  await page.setViewportSize({ width: 320, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test("lyrics settings save thresholds, retain drafts and offer output-only inspection", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  let options = { auto_search: false, min_score: 96 };
  const saves: unknown[] = [], batches: unknown[] = [];
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
  await page.route("**/api/lyrics/options", route => { expect(route.request().headers()["x-csrf-token"]).toBe("test"); options = route.request().postDataJSON(); saves.push(options); return route.fulfill({ json: options }); });
  await page.route("**/api/lyrics/search", route => { batches.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { selected: 1 } }); });
  await page.goto("/settings?section=lyrics");
  await expect(page.getByRole("tab", { name: "Lyrics", exact: true })).toHaveAttribute("aria-selected", "true");
  const score = page.getByLabel("Minimum score for automatic matching", { exact: true });
  await expect(score).toHaveValue("96");
  await page.getByRole("button", { name: "90 · Relaxed", exact: true }).click();
  await page.getByRole("switch", { name: "Automatically fill lyrics for new and existing artifacts", exact: true }).check();
  await page.getByRole("tab", { name: "Encoding", exact: true }).click();
  await page.getByRole("tab", { name: "Lyrics", exact: true }).click();
  await expect(score).toHaveValue("90");
  expect(saves).toHaveLength(0);
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect.poll(() => saves.length).toBe(1);
  expect(options).toEqual({ auto_search: true, min_score: 90 });
  await page.getByRole("button", { name: "100 · Strictest", exact: true }).click();
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await expect(score).toHaveValue("90");
  await score.fill("101");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  expect(saves).toHaveLength(1);
  await score.fill("90");
  await page.getByRole("button", { name: "Identify existing lyric scripts", exact: true }).click();
  await expect.poll(() => batches.length).toBe(1);
  expect(batches[0]).toEqual({ all: true, refresh: false, inspect_only: true });
  await page.reload();
  await expect(score).toHaveValue("90");
  await page.screenshot({ path: "test-results/lyrics-settings-desktop-en.png", fullPage: true });
  await setPreference(page, "language", "zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await expect(page.getByRole("tab", { name: "歌词设置", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-settings-mobile-zh-CN.png", fullPage: true });
  await page.route("**/api/library", route => route.fulfill({ json: [] }));
  await page.getByRole("link", { name: "查看繁体歌词并批量替换", exact: true }).click();
  await expect(page).toHaveURL(/view=tracks&lyrics_script=traditional/);
});

test("traditional script filter supports cross-page batch replacement and shared URLs", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const tracks: Source[] = Array.from({ length: 64 }, (_, index) => ({ id: index + 1, path: `Artist/Album/${index + 1}.flac`, artist: "Artist", album: "Album", title: `Song ${String(index + 1).padStart(2, "0")}`, track: index + 1, disc: 1, present: true, output: `Artist/Album/${index + 1}.opus`, output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics_script: index < 60 ? "traditional" : "simplified", lyrics: { state: "ready", output: `Artist/Album/${index + 1}.opus`, signature: "audio", checked: 1 } }));
  const batches: unknown[] = [];
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: tracks }));
  await page.route("**/api/lyrics/search", route => { batches.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { selected: 60 } }); });
  await page.goto("/library?view=tracks&lyrics_script=traditional");
  const filter = page.getByLabel("Lyric script", { exact: true });
  await expect(filter).toHaveValue("traditional");
  await expect(page.locator(".track-table tbody > tr")).toHaveCount(50);
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Select all 60 matching tracks", exact: true }).click();
  await page.getByRole("button", { name: "Replace traditional with simplified", exact: true }).click();
  await expect.poll(() => batches.length).toBe(1);
  expect(batches[0]).toEqual({ ids: Array.from({ length: 60 }, (_, index) => index + 1), refresh: false, replace_traditional: true });
  await page.getByRole("button", { name: "Clear selection", exact: true }).click();
  await openLibraryOptions(page);
  await filter.selectOption("simplified");
  await expect(page.locator(".track-table tbody > tr")).toHaveCount(4);
  await expect(page).toHaveURL(/lyrics_script=simplified/);
  await page.reload();
  await expect(filter).toHaveValue("simplified");
  await page.getByRole("button", { name: "Clear filters", exact: true }).click();
  await expect(filter).toHaveValue("all");
  await expect(page).not.toHaveURL(/lyrics_script/);
});

for (const { threshold, score, selected } of [{ threshold: 96, score: 90, selected: false }, { threshold: 90, score: 90, selected: true }, { threshold: 98, score: 97, selected: false }]) {
  test(`candidate recommendation respects saved threshold ${threshold} for score ${score}`, async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
    const source: Source = { id: 1, path: "Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {} };
    await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
    await page.route("**/api/library", route => route.fulfill({ json: [source] }));
    await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false, min_score: threshold }, report: { state: "idle" } } }));
    await page.route("**/api/library/1/lyrics/candidates", route => { expect(route.request().method()).toBe("GET"); return route.fulfill({ json: { query: "Track Artist", groups: [{ token: "cached", provider: "lrclib", cached: true, candidates: [{ index: 0, provider: "lrclib", title: "Track", artist: "Artist", album: score === 90 ? "Other" : "Album", duration: 183, high_confidence: score >= 96, identity_match: true, match_score: score, duration_delta: 0, script: "simplified" }] }] } }); });
    let previews = 0;
    await page.route("**/api/library/1/lyrics/preview", route => { previews++; return route.fulfill({ json: { original: "[00:01.00]爱这风云", script: "simplified" } }); });
    await page.goto("/library?view=tracks");
    await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
    const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
    await expect(picker.getByRole("radio")).toHaveCount(1);
    if (selected) { await expect(picker.getByRole("radio")).toBeChecked(); await expect.poll(() => previews).toBe(1); }
    else { await expect(picker.getByRole("radio")).not.toBeChecked(); expect(previews).toBe(0); }
  });
}

test("lyrics filters compose with audio status and library coverage survives new batches and reloads", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const base: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {} };
  const states = ["ready", "not_found", "instrumental", "needs_review", "failed", undefined] as const;
  const sources: Source[] = states.map((state, index) => ({
    ...base, id: index + 1, track: index + 1, title: `Lyrics ${state || "unchecked"}`,
    path: `Artist/Album/${index + 1}.flac`, output: `Artist/Album/${index + 1}.opus`,
    present: index !== 0, status: index === 0 ? "expired" : state === "failed" ? "failed" : "ready",
    lyrics: state ? { state, output: `Artist/Album/${index + 1}.opus`, signature: "audio", checked: 1 } : undefined,
  }));
  sources.push({ ...base, id: 7, title: "No audio yet", path: "Artist/Album/7.flac", output: "", output_present: false, status: "unverified", lyrics: { state: "ready", output: "old.opus", signature: "old", checked: 1 } });
  sources.push({ ...base, id: 8, title: "Other album", artist: "Other", album: "Other", path: "Other/Other/1.flac", output: "Other/Other/1.opus", lyrics: { state: "ready", output: "Other/Other/1.opus", signature: "other", checked: 1 } });
  let report = { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false };
  const searches: unknown[] = [];
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: sources }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: true }, report } }));
  await page.route("**/api/lyrics/search", route => { searches.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { selected: 1 } }); });
  async function refresh() {
    await Promise.all([
      page.waitForResponse(response => new URL(response.url()).pathname === "/api/library"),
      page.evaluate(() => document.dispatchEvent(new Event("visibilitychange"))),
    ]);
  }

  await page.goto("/library?view=artists");
  await expect(page.locator(".maintenance-count")).toHaveText("6 / 7");
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  const inventory = page.locator(".lyrics-library-summary");
  await expect(inventory).toHaveAccessibleName("Library lyrics status");
  await expect(inventory).toContainText("Library checked 6/7 tracks");
  await expect(inventory).toContainText("Available 2 · No match 1 · Instrumental 1 · Review 1 · Failed 1 · Unchecked 1");
  const lyricsFilter = page.getByLabel("Filter by lyrics status", { exact: true });
  await openLibraryOptions(page);
  await lyricsFilter.selectOption("failed");
  await expect(page.locator(".library-group")).toHaveCount(1);
  await page.getByRole("button", { name: "Browse artist Artist", exact: true }).click();
  await page.getByRole("button", { name: "Browse album Album", exact: true }).click();
  const rows = page.locator(".track-table tbody > tr");
  await openLibraryOptions(page);
  await page.getByLabel("Filter by status", { exact: true }).selectOption("ready");
  await expect(rows).toHaveCount(0);
  await openLibraryOptions(page);
  await page.getByLabel("Filter by status", { exact: true }).selectOption("all");
  for (const state of states) {
    await openLibraryOptions(page);
    await lyricsFilter.selectOption(state || "unchecked");
    await expect(rows).toHaveCount(1);
    await expect(rows).toContainText(`Lyrics ${state || "unchecked"}`);
    await expect(inventory).toHaveAccessibleName("Album lyrics status");
    await expect(inventory).toContainText("Album checked 5/6 tracks");
  }
  await openLibraryOptions(page);
  await lyricsFilter.selectOption("not_found");
  await page.getByLabel("Search library", { exact: true }).fill("Lyrics ready");
  await expect(rows).toHaveCount(0);
  await page.getByLabel("Search library", { exact: true }).fill("");
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Fill lyrics for selected artifacts", exact: true }).click();
  await expect.poll(() => searches.length).toBe(1);
  expect(searches).toEqual([{ ids: [2], refresh: false }]);
  await page.getByRole("button", { name: "Clear selection", exact: true }).click();
  await openLibraryOptions(page);
  await lyricsFilter.selectOption("all");
  await expect(rows).toHaveCount(7);

  const added: Source = { ...base, id: 9, title: "New track", path: "Artist/Album/9.flac", output: "Artist/Album/9.opus" };
  sources.push(added);
  report = { ...report, state: "running", total: 1 };
  await refresh();
  await expect(inventory).toContainText("Album checked 5/7 tracks");
  added.lyrics = { state: "ready", output: added.output, signature: "new", checked: 2 };
  report = { ...report, state: "complete", processed: 1, found: 1 };
  await refresh();
  await expect(inventory).toContainText("Album checked 6/7 tracks");
  await expect(inventory).toContainText("Available 2");
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toHaveCount(0);
  await page.locator(".library-breadcrumbs > button").first().click();
  await expect(inventory).toContainText("Library checked 7/8 tracks");
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toContainText("This batch: checked 1/1");
  await page.goBack();
  await expect(inventory).toContainText("Album checked 6/7 tracks");
  report = { ...report, state: "running", processed: 0, found: 0 };
  await refresh();
  added.lyrics.checked++;
  report = { ...report, state: "complete", processed: 1, found: 1 };
  await refresh();
  await expect(inventory).toContainText("Album checked 6/7 tracks");

  report = { ...report, state: "idle", total: 0, processed: 0, found: 0 };
  await page.reload();
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  await expect(inventory).toContainText("Album checked 6/7 tracks");
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toHaveCount(0);
  sources[0].output_present = false;
  sources[1].lyrics = undefined;
  await refresh();
  await expect(inventory).toContainText("Album checked 4/6 tracks");
  await setPreference(page, "language", "zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.locator(".library-options > summary").click();
  await openLibraryOptions(page);
  await page.getByLabel("歌词匹配状态", { exact: true }).selectOption("failed");
  await expect(rows).toHaveCount(1);
  await expect(page.locator(".library-options")).toHaveAttribute("data-filtered", "true");
  await expect(page.getByRole("region", { name: "本专辑歌词匹配状态", exact: true })).toContainText("本专辑已检查 4/6 首");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-inventory-mobile-zh-CN.png", fullPage: true });
});

test("lyrics selection, background progress, stop and bilingual mobile UI", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics: { state: "not_found", output: "Artist/Album/01.opus", signature: "audio", checked: 1 } };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source, { ...source, id: 2, title: "Pending track", path: "Artist/Album/02.flac", output: "", output_present: false, status: "unverified", lyrics: undefined }] }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  let autoSearch = false;
  let running = false;
  const requests: unknown[] = [];
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: autoSearch }, report: { state: running ? "running" : requests.length ? "stopped" : "idle", total: 1, processed: 0, found: 0, not_found: 0, failed: 0, current: running ? source.path : "", stopping: false } } }));
  await page.route("**/api/lyrics/options", route => {
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    autoSearch = route.request().postDataJSON().auto_search;
    return route.fulfill({ json: { auto_search: autoSearch } });
  });
  await page.route("**/api/lyrics/search", route => {
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    if (route.request().method() === "DELETE") { running = false; autoSearch = false; }
    else { requests.push(route.request().postDataJSON()); running = true; }
    return route.fulfill({ status: running ? 202 : 200, json: { selected: 1 } });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Fill lyrics for selected artifacts", exact: true }).click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests).toEqual([{ ids: [1], refresh: false }]);
  await expect(page.getByRole("link", { name: "View background lyrics task", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Fill album lyrics", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Stop lyrics search", exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Automatically fill lyrics for new and existing artifacts", { exact: true })).toHaveCount(0);
  await page.getByRole("link", { name: "View background lyrics task", exact: true }).click();
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await expect(page.locator(".lyrics-batch-settings")).toContainText("Lyrics search stopped");
  expect(autoSearch).toBe(false);
  await page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests.at(-1)).toEqual({ all: true, refresh: false });
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  await page.locator(".lyrics-details > summary").click();
  await expect(page.locator(".lyrics-track")).toContainText("No synchronized match");
  await page.getByRole("button", { name: "Search again", exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests.at(-1)).toEqual({ ids: [1], refresh: true });
  await setPreference(page, "language", "zh-CN");
  await expect(page.getByRole("link", { name: "查看后台歌词任务", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "补齐本专辑歌词", exact: true })).toBeDisabled();
  await expect(page.locator(".lyrics-track")).toContainText("未找到同步歌词");
  await page.setViewportSize({ width: 320, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-mobile-zh-CN.png", fullPage: true });
});

test("lyrics candidates keep partial results, preview languages and save an explicit selection", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics: { state: "needs_review", output: "Artist/Album/01.opus", signature: "audio", checked: 1 } };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source] }));
  const searches: { provider: string; query: string }[] = [];
  await page.route("**/api/library/1/lyrics/candidates", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { query: "Track Artist", groups: [] } });
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    const body = route.request().postDataJSON(); searches.push(body);
    if (body.provider === "qq") return route.fulfill({ status: 502, json: { error: "qq lyrics service returned HTTP 403; retry later" } });
    return route.fulfill({ json: { token: `${body.provider}-choices`, candidates: body.provider === "kugou" ? [] : [{ index: 0, provider: body.provider, title: "別名", artist: "Artist", album: "Album", duration: 183, high_confidence: false, match_score: 100, script: "simplified" }] } });
  });
  const previews: unknown[] = [];
  await page.route("**/api/library/1/lyrics/preview", route => {
    const body = route.request().postDataJSON(); previews.push(body);
    return route.fulfill({ json: body.token === "netease-choices" ? { original: "[00:01.00]こんにちは", translation: "[00:01.00]你好", romanization: "[00:01.00]konnichiwa" } : { original: "[00:02.00]Other recording" } });
  });
  let applied: unknown;
  await page.route("**/api/library/1/lyrics", route => {
    expect(route.request().method()).toBe("PUT");
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    applied = route.request().postDataJSON();
    source.lyrics = { state: "ready", output: source.output, signature: "audio", checked: 2, provider: "netease", path: "Artist/Album/01.lrc" };
    return route.fulfill({ json: source.lyrics });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  await page.locator(".lyrics-details > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await picker.getByLabel("Title or artist", { exact: true }).fill("別名 Artist");
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect.poll(() => searches.length).toBe(4);
  expect(searches.map(search => search.provider).sort()).toEqual(["kugou", "lrclib", "netease", "qq"]);
  await expect(picker.locator(".lyrics-provider-status fieldset > legend")).toHaveText(["LRCLIB Preferred", "QQ Music", "Kugou", "NetEase"]);
  expect(searches.every(search => search.query === "別名 Artist")).toBe(true);
  await expect(picker).toContainText("QQ Music returned HTTP 403; retry later");
  await expect(picker).toContainText("No candidates found");
  await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
  await expect(picker.locator('input[type="radio"]:checked')).toHaveCount(0);
  expect(previews).toHaveLength(0);
  await picker.getByRole("radio", { name: "Preview 別名 · Artist · NetEase", exact: true }).check();
  await expect(picker.getByRole("region", { name: "Lyrics preview", exact: true })).toContainText("こんにちは");
  expect(applied).toBeUndefined();
  await expect(picker.getByLabel("Include synchronized translation", { exact: true })).not.toBeChecked();
  await picker.getByLabel("Include synchronized translation", { exact: true }).check();
  await picker.getByLabel("Include synchronized romanization", { exact: true }).check();
  await picker.getByRole("radio", { name: "Preview 別名 · Artist · LRCLIB", exact: true }).check();
  await expect(picker).toContainText("Other recording");
  await expect(picker.getByLabel("Include synchronized translation", { exact: true })).toHaveCount(0);
  await picker.getByRole("radio", { name: "Preview 別名 · Artist · NetEase", exact: true }).check();
  await expect(picker.getByLabel("Include synchronized translation", { exact: true })).not.toBeChecked();
  await picker.getByLabel("Include synchronized translation", { exact: true }).check();
  await picker.getByLabel("Include synchronized romanization", { exact: true }).check();
  await picker.getByRole("button", { name: "Save selected lyrics", exact: true }).click();
  await expect(picker).not.toBeVisible();
  expect(applied).toEqual({ token: "netease-choices", index: 0, translation: true, romanization: true });
  expect(previews).toHaveLength(3);
  await expect(page.locator(".lyrics-track")).toContainText("From NetEase");
  await setPreference(page, "language", "zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.getByRole("button", { name: "选择歌词", exact: true }).click();
  await expect(page.getByRole("region", { name: "选择歌词", exact: true })).toBeVisible();
  expect(await page.locator(".lyrics-picker").evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("automatic lyric previews wait for all providers, preserve manual choices and respect cancellation", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, duration: 183, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {} };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source] }));
  let delayed: Promise<void>, release: () => void;
  await page.route("**/api/library/1/lyrics/candidates", async route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { query: "Track Artist", groups: [] } });
    const { provider } = route.request().postDataJSON();
    if (provider === "qq") await delayed;
    return route.fulfill({ json: { provider, token: `${provider}-choices`, candidates: ["lrclib", "qq"].includes(provider) ? [{ index: 0, provider, title: "Track", artist: "Artist", album: "Album", duration: 183, high_confidence: true, match_score: provider === "lrclib" ? 100 : 99, script: provider === "lrclib" ? "traditional" : "simplified" }] : [] } });
  });
  const previews: unknown[] = [];
  let saves = 0;
  await page.route("**/api/library/1/lyrics/preview", route => {
    const body = route.request().postDataJSON(); previews.push(body);
    const simplified = body.token === "qq-choices";
    return route.fulfill({ json: { original: simplified ? "[00:01.00]爱这风云" : "[00:01.00]愛這風雲", script: simplified ? "simplified" : "traditional" } });
  });
  await page.route("**/api/library/1/lyrics", route => { saves++; return route.fulfill({ json: {} }); });
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  const lrclib = picker.getByRole("radio", { name: "Preview Track · Artist · LRCLIB", exact: true });
  const qq = picker.getByRole("radio", { name: "Preview Track · Artist · QQ Music", exact: true });
  async function startSearch() {
    delayed = new Promise<void>(resolve => { release = resolve; });
    await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
    await expect(lrclib).toBeVisible();
    await expect(picker.getByRole("button", { name: "Cancel candidate search", exact: true })).toBeVisible();
  }
  await startSearch();
  await expect(lrclib).not.toBeChecked();
  expect(previews).toHaveLength(0);
  release!();
  await expect(qq).toBeChecked();
  await expect(picker.locator("pre")).toContainText("爱这风云");
  expect(previews).toEqual([{ token: "qq-choices", index: 0 }]);
  await startSearch();
  await lrclib.check();
  await expect(picker.locator("pre")).toContainText("愛這風雲");
  release!();
  await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
  await expect(qq).toBeVisible();
  await expect(lrclib).toBeChecked();
  expect(previews).toHaveLength(2);
  await startSearch();
  await picker.getByRole("button", { name: "Cancel candidate search", exact: true }).click();
  release!();
  await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
  await expect(picker.locator('input[type="radio"]:checked')).toHaveCount(0);
  expect(previews).toHaveLength(2);
  expect(saves).toBe(0);
});

test("song details and automatic saved recommendations survive reopening and page reload", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Artist/Album/01.flac", hash: "0123456789abcdef".repeat(4), artist: "Artist", album: "Album", title: "Track", track: 1, disc: 2, duration: 183.5, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 2621440, metadata: { genre: "Pop", date: "2026" }, lyrics: { state: "needs_review", output: "Artist/Album/01.opus", signature: "audio", checked: 1 } };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source] }));
  const candidate = { index: 0, title: "Track", artist: "Artist", album: "Album", duration: 183.5, duration_delta: 0, match_score: 100, high_confidence: true, script: "traditional" };
  const groups = [
    { provider: "lrclib", token: "lrclib-saved", searched: 1800000000, cached: true, candidates: [{ ...candidate, provider: "lrclib" }] },
    { provider: "qq", token: "qq-saved", searched: 1800000000, cached: true, candidates: [{ ...candidate, provider: "qq", script: "simplified", duration: 184, duration_delta: 0.5, match_score: 99 }] },
  ];
  const searches: { provider: string; refresh: boolean }[] = [];
  await page.route("**/api/library/1/lyrics/candidates", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { query: "Track Artist", groups } });
    const body = route.request().postDataJSON(); searches.push(body);
    return route.fulfill({ json: groups.find(group => group.provider === body.provider) || { provider: body.provider, token: `${body.provider}-empty`, cached: false, candidates: [] } });
  });
  const previews: unknown[] = [];
  await page.route("**/api/library/1/lyrics/preview", route => {
    previews.push(route.request().postDataJSON());
    return route.fulfill({ json: { original: "[00:01.00]爱这风云", script: "simplified" } });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  const details = page.locator(".track-file").first();
  await page.locator(".track-open").first().click();
  for (const text of ["Artist", "Album", "3:04 (183.5 s)", "1 / 2", "2.5 MiB (2,621,440 bytes)", source.hash!, source.path, source.output]) await expect(details).toContainText(text);
  await details.getByText("All audio tags", { exact: true }).click();
  await expect(details).toContainText("Pop");
  await page.getByRole("button", { name: "Close panel", exact: true }).click();
  await page.locator(".lyrics-details > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  let picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await expect(picker.getByText("Saved search", { exact: false })).toHaveCount(2);
  const recommended = picker.locator(".lyrics-candidate").filter({ hasText: "Recommended" });
  await expect(recommended).toContainText("Simplified Chinese characters detected");
  await expect(recommended).toContainText("Match score 99/100");
  await expect(recommended).toContainText("Duration difference 0.5 s");
  expect(searches).toHaveLength(0);
  await expect(recommended.getByRole("radio")).toBeChecked();
  await expect(recommended).toHaveAttribute("data-selected", "true");
  await expect(picker.locator("pre")).toContainText("爱这风云");
  expect(previews).toEqual([{ token: "qq-saved", index: 0 }]);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const candidates = await picker.locator(".lyrics-candidate-groups").boundingBox();
  const preview = await picker.locator(".lyrics-preview-pane").boundingBox();
  expect(preview!.x).toBeGreaterThan(candidates!.x + candidates!.width);
  await page.screenshot({ path: "test-results/lyrics-recommendations-1440-light.png", animations: "disabled" });
  await picker.getByRole("button", { name: "Close lyrics selection", exact: true }).click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  await expect(picker.getByText("Recommended", { exact: true })).toBeVisible();
  await expect(recommended.getByRole("radio")).toBeChecked();
  await expect(picker.locator("pre")).toContainText("爱这风云");
  expect(searches).toHaveLength(0);
  await page.reload();
  await page.locator(".lyrics-details > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  await expect(picker.getByText("Recommended", { exact: true })).toBeVisible();
  await expect(recommended.getByRole("radio")).toBeChecked();
  await expect(picker.locator("pre")).toContainText("爱这风云");
  expect(searches).toHaveLength(0);
  await picker.getByRole("button", { name: "Force new search", exact: true }).click();
  await expect.poll(() => searches.length).toBe(4);
  expect(searches.every(search => search.refresh)).toBe(true);
  await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect.poll(() => searches.length).toBe(8);
  expect(searches.slice(4).every(search => !search.refresh)).toBe(true);
  await page.keyboard.press("Escape");
  await setPreference(page, "language", "zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.locator(".track-open").first().click();
  await expect(page.locator(".track-file").first()).toContainText("源文件 SHA-256");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/song-details-mobile-zh-CN.png", fullPage: true });
  await page.getByRole("button", { name: "关闭面板", exact: true }).click();
  await page.getByRole("button", { name: "选择歌词", exact: true }).click();
  picker = page.getByRole("region", { name: "选择歌词", exact: true });
  await expect(picker.getByText("推荐", { exact: true })).toBeVisible();
  expect(await picker.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-recommendations-mobile-zh-CN.png", fullPage: true });
});
