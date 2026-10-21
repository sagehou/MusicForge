import { test, expect } from "@playwright/test";
import type { Source } from "../src/types";

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

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

  await page.goto("/library");
  await expect(page.locator(".maintenance-count")).toHaveText("6 / 7");
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  const inventory = page.getByRole("region", { name: "Library lyrics status", exact: true });
  await expect(inventory).toContainText("Library checked 6/7 tracks");
  await expect(inventory).toContainText("Available 2 · No match 1 · Instrumental 1 · Review 1 · Failed 1 · Unchecked 1");
  const lyricsFilter = page.getByLabel("Filter by lyrics status", { exact: true });
  await lyricsFilter.selectOption("failed");
  await expect(page.locator(".library-group")).toHaveCount(1);
  await page.getByRole("button", { name: "Browse artist Artist", exact: true }).click();
  await page.getByRole("button", { name: "Browse album Album", exact: true }).click();
  const rows = page.locator(".track-table tbody > tr");
  await page.getByLabel("Filter by status", { exact: true }).selectOption("ready");
  await expect(rows).toHaveCount(0);
  await page.getByLabel("Filter by status", { exact: true }).selectOption("all");
  for (const state of states) {
    await lyricsFilter.selectOption(state || "unchecked");
    await expect(rows).toHaveCount(1);
    await expect(rows).toContainText(`Lyrics ${state || "unchecked"}`);
    await expect(inventory).toContainText("Library checked 6/7 tracks");
  }
  await lyricsFilter.selectOption("not_found");
  await page.getByLabel("Search library", { exact: true }).fill("Lyrics ready");
  await expect(rows).toHaveCount(0);
  await page.getByLabel("Search library", { exact: true }).fill("");
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Fill lyrics for selected artifacts", exact: true }).click();
  await expect.poll(() => searches.length).toBe(1);
  expect(searches).toEqual([{ ids: [2], refresh: false }]);
  await page.getByRole("button", { name: "Clear selection", exact: true }).click();
  await lyricsFilter.selectOption("all");
  await expect(rows).toHaveCount(7);

  const added: Source = { ...base, id: 9, title: "New track", path: "Artist/Album/9.flac", output: "Artist/Album/9.opus" };
  sources.push(added);
  report = { ...report, state: "running", total: 1 };
  await refresh();
  await expect(inventory).toContainText("Library checked 6/8 tracks");
  added.lyrics = { state: "ready", output: added.output, signature: "new", checked: 2 };
  report = { ...report, state: "complete", processed: 1, found: 1 };
  await refresh();
  await expect(inventory).toContainText("Library checked 7/8 tracks");
  await expect(inventory).toContainText("Available 3");
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toContainText("This batch: checked 1/1");
  report = { ...report, state: "running", processed: 0, found: 0 };
  await refresh();
  added.lyrics.checked++;
  report = { ...report, state: "complete", processed: 1, found: 1 };
  await refresh();
  await expect(inventory).toContainText("Library checked 7/8 tracks");

  report = { ...report, state: "idle", total: 0, processed: 0, found: 0 };
  await page.reload();
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  await expect(inventory).toContainText("Library checked 7/8 tracks");
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toHaveCount(0);
  sources[0].output_present = false;
  sources[1].lyrics = undefined;
  await refresh();
  await expect(inventory).toContainText("Library checked 5/7 tracks");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.locator(".library-options > summary").click();
  await page.getByLabel("歌词匹配状态", { exact: true }).selectOption("failed");
  await expect(rows).toHaveCount(1);
  await expect(page.locator(".library-options")).toHaveAttribute("data-filtered", "true");
  await expect(page.getByRole("region", { name: "全库歌词匹配状态", exact: true })).toContainText("全库已检查 5/7 首");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-inventory-mobile-zh-CN.png", fullPage: true });
});

test("lyrics selection, background progress, stop, automatic option and bilingual mobile UI", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  const source: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {}, lyrics: { state: "not_found", output: "Artist/Album/01.opus", signature: "audio", checked: 1 } };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source, { ...source, id: 2, title: "Pending track", path: "Artist/Album/02.flac", output: "", output_present: false, status: "unverified", lyrics: undefined }] }));
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
  await expect(page.getByRole("status", { name: "Current lyrics batch", exact: true })).toBeVisible();
  await page.locator(".lyrics-panel > .lyrics-settings > summary").click();
  await expect(page.locator(".lyrics-panel")).toContainText("Searching for lyrics");
  await expect(page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true })).toBeDisabled();
  await page.locator(".lyrics-options > summary").click();
  await page.getByLabel("Automatically fill lyrics for new and existing artifacts", { exact: true }).check();
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await expect(page.locator(".lyrics-panel")).toContainText("Lyrics search stopped");
  await expect(page.getByLabel("Automatically fill lyrics for new and existing artifacts", { exact: true })).not.toBeChecked();
  await page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests.at(-1)).toEqual({ all: true, refresh: false });
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await page.locator(".lyrics-details > summary").click();
  await expect(page.locator(".lyrics-track")).toContainText("No synchronized match");
  await page.getByRole("button", { name: "Search again", exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests.at(-1)).toEqual({ ids: [1], refresh: true });
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator(".lyrics-panel")).toContainText("正在搜索歌词");
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
    return route.fulfill({ json: { token: `${body.provider}-choices`, candidates: body.provider === "kugou" ? [] : [{ index: 0, provider: body.provider, title: "別名", artist: "Artist", album: "Album", duration: 183, high_confidence: false }] } });
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
  const picker = page.getByRole("dialog", { name: "Choose lyrics", exact: true });
  await picker.getByLabel("Title or artist", { exact: true }).fill("別名 Artist");
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect.poll(() => searches.length).toBe(4);
  expect(searches.map(search => search.provider).sort()).toEqual(["kugou", "lrclib", "netease", "qq"]);
  await expect(picker.locator("fieldset > legend")).toHaveText(["LRCLIB Preferred", "QQ Music", "Kugou", "NetEase"]);
  expect(searches.every(search => search.query === "別名 Artist")).toBe(true);
  await expect(picker).toContainText("QQ Music returned HTTP 403; retry later");
  await expect(picker).toContainText("No candidates found");
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
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.getByRole("button", { name: "选择歌词", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "选择歌词", exact: true })).toBeVisible();
  expect(await page.locator(".lyrics-picker").evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("song details and saved recommendations survive reopening and page reload", async ({ page }) => {
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
  await page.route("**/api/library/1/lyrics/preview", route => route.fulfill({ json: { original: "[00:01.00]爱这风云", script: "simplified" } }));
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  const details = page.locator(".track-file").first();
  await details.locator(":scope > summary").click();
  for (const text of ["Artist", "Album", "3:04 (183.5 s)", "1 / 2", "2.5 MiB (2,621,440 bytes)", source.hash!, source.path, source.output]) await expect(details).toContainText(text);
  await details.getByText("All audio tags", { exact: true }).click();
  await expect(details).toContainText("Pop");
  await page.locator(".lyrics-details > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  let picker = page.getByRole("dialog", { name: "Choose lyrics", exact: true });
  await expect(picker.getByText("Saved search", { exact: false })).toHaveCount(2);
  const recommended = picker.locator(".lyrics-candidate").filter({ hasText: "Recommended" });
  await expect(recommended).toContainText("Simplified Chinese characters detected");
  await expect(recommended).toContainText("Match score 99/100");
  await expect(recommended).toContainText("Duration difference 0.5 s");
  expect(searches).toHaveLength(0);
  await recommended.getByRole("radio").check();
  await expect(recommended).toHaveAttribute("data-selected", "true");
  await expect(picker.locator("pre")).toContainText("爱这风云");
  await page.setViewportSize({ width: 1440, height: 1000 });
  const candidates = await picker.locator(".lyrics-candidate-groups").boundingBox();
  const preview = await picker.locator(".lyrics-preview-pane").boundingBox();
  expect(preview!.x).toBeGreaterThan(candidates!.x + candidates!.width);
  await page.screenshot({ path: "test-results/lyrics-recommendations-1440-light.png", animations: "disabled" });
  await picker.getByRole("button", { name: "Close lyrics selection", exact: true }).click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  await expect(picker.getByText("Recommended", { exact: true })).toBeVisible();
  expect(searches).toHaveLength(0);
  await page.reload();
  await page.locator(".lyrics-details > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  await expect(picker.getByText("Recommended", { exact: true })).toBeVisible();
  expect(searches).toHaveLength(0);
  await picker.getByRole("button", { name: "Force new search", exact: true }).click();
  await expect.poll(() => searches.length).toBe(4);
  expect(searches.every(search => search.refresh)).toBe(true);
  await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect.poll(() => searches.length).toBe(8);
  expect(searches.slice(4).every(search => !search.refresh)).toBe(true);
  await page.keyboard.press("Escape");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  await page.locator(".track-file > summary").first().click();
  await expect(page.locator(".track-file").first()).toContainText("源文件 SHA-256");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/song-details-mobile-zh-CN.png", fullPage: true });
  await page.getByRole("button", { name: "选择歌词", exact: true }).click();
  picker = page.getByRole("dialog", { name: "选择歌词", exact: true });
  await expect(picker.getByText("推荐", { exact: true })).toBeVisible();
  expect(await picker.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/lyrics-recommendations-mobile-zh-CN.png", fullPage: true });
});
