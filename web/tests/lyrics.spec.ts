import { test, expect } from "@playwright/test";
import type { Source } from "../src/types";

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
  await page.locator(".lyrics-panel > summary").click();
  await expect(page.locator(".lyrics-panel")).toContainText("Searching for lyrics");
  await expect(page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true })).toBeDisabled();
  await page.getByLabel("Automatically fill lyrics for new and existing artifacts", { exact: true }).check();
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await expect(page.locator(".lyrics-panel")).toContainText("Lyrics search stopped");
  await expect(page.getByLabel("Automatically fill lyrics for new and existing artifacts", { exact: true })).not.toBeChecked();
  await page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests.at(-1)).toEqual({ all: true, refresh: false });
  await page.getByRole("button", { name: "Stop lyrics search", exact: true }).click();
  await page.locator(".lyrics-track > summary").click();
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
  await page.locator(".lyrics-track > summary").click();
  await page.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "Choose lyrics", exact: true });
  await picker.getByLabel("Title or artist", { exact: true }).fill("別名 Artist");
  await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
  await expect.poll(() => searches.length).toBe(4);
  expect(searches.map(search => search.provider).sort()).toEqual(["kugou", "lrclib", "netease", "qq"]);
  await expect(picker.locator("fieldset > legend")).toHaveText(["LRCLIB", "QQ Music", "Kugou", "NetEase"]);
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
