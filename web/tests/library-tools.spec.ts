import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import type { Source } from "../src/types";
import { setPreference } from "./preferences";

const tracks: Source[] = [1, 2].map(id => ({ id, path: `Artist/Album/0${id}.flac`, title: `Track ${id}`, artist: "Artist", album: "Album", track: id, disc: 1, duration: 180, present: true, output: `Artist/Album/0${id}.opus`, output_present: true, status: "ready", error: "", size: 10000, metadata: { title: `Track ${id}` } }));
const idle = { state: "idle", total: 0, processed: 0, updated: 0, skipped: 0, failed: 0 };

async function mock(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: tracks }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0 } } }));
  await page.route("**/api/maintenance/*", route => route.fulfill({ json: idle }));
  await page.route("**/api/playlists", route => route.fulfill({ json: [] }));
  await page.goto("/library?view=tracks");
}

test("output tags require a scoped preview and retain drafts on conflicts", async ({ page }) => {
  await mock(page);
  let writes = 0;
  await page.route("**/api/library/tags/preview", route => {
    expect(route.request().postDataJSON()).toEqual({ ids: [1], tags: { title: "Corrected" } });
    return route.fulfill({ json: { token: "tag-preview", tracks: [{ id: 1, path: tracks[0].output, before: { title: "Track 1" }, after: { title: "Corrected" } }] } });
  });
  await page.route("**/api/maintenance/tags", route => {
    if (route.request().method() === "POST") { writes++; expect(route.request().postDataJSON()).toEqual({ token: "tag-preview" }); return route.fulfill({ status: 409, json: { error: "Tag preview expired; preview the changes again" } }); }
    return route.fulfill({ json: idle });
  });
  await page.getByLabel("Select Track 1", { exact: true }).check();
  await page.locator("#maintenance-workbench > summary").click();
  await page.locator(".library-tool > summary").filter({ hasText: "Correct output tags" }).click();
  const editor = page.getByRole("region", { name: "Correct output tags", exact: true });
  await editor.getByRole("checkbox", { name: "Title", exact: true }).check();
  await editor.getByRole("textbox", { name: "Title", exact: true }).fill("Corrected");
  await editor.getByRole("button", { name: "Preview tag changes", exact: true }).click();
  await expect(editor.getByRole("table")).toContainText("Track 1");
  await expect(editor.getByRole("table")).toContainText("Corrected");
  expect(writes).toBe(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await editor.scrollIntoViewIfNeeded();
  await page.screenshot({ path: "test-results/library-tools-tags-desktop.png", animations: "disabled" });
  await editor.getByRole("button", { name: "Apply to 1 outputs", exact: true }).click();
  await expect(editor.getByRole("alert")).toContainText("preview the changes again");
  await expect(editor.getByRole("textbox", { name: "Title", exact: true })).toHaveValue("Corrected");
  expect(writes).toBe(1);
});

test("playlists preview broken paths, save mappings and refresh output extensions", async ({ page }) => {
  await mock(page);
  let saved = false;
  const result = { name: "Favorites", content: "#EXTM3U\n./Artist/Album/01.opus\n", ready: 1, missing: [{ path: "Artist/Album/missing.flac", original: "missing.flac" }] };
  await page.route("**/api/library/playlists/preview", route => { expect(route.request().postDataJSON()).toEqual({ name: "Favorites", text: "01.flac\nmissing.flac", base: "Artist/Album" }); return route.fulfill({ json: result }); });
  await page.route("**/api/playlists", route => {
    if (route.request().method() === "POST") { saved = true; return route.fulfill({ json: { ...result, id: "playlist-id" } }); }
    return route.fulfill({ json: saved ? [{ id: "playlist-id", name: "Favorites", count: 2 }] : [] });
  });
  await page.route("**/api/playlists/playlist-id", route => route.fulfill({ json: { ...result, id: "playlist-id", content: "#EXTM3U\n./Artist/Album/01.mp3\n" } }));
  await page.locator("#maintenance-workbench > summary").click();
  await page.locator(".library-tool > summary").filter({ hasText: "Playlists" }).click();
  const editor = page.getByRole("region", { name: "Playlists", exact: true });
  await editor.getByLabel("Playlist name", { exact: true }).fill("Favorites");
  await editor.getByLabel("Imported playlist directory relative to the source root", { exact: true }).fill("Artist/Album");
  await editor.getByLabel("M3U paths", { exact: true }).fill("01.flac\nmissing.flac");
  await editor.getByRole("button", { name: "Preview path mapping", exact: true }).click();
  await expect(editor).toContainText("1 playable entries · 1 unresolved entries omitted");
  await editor.locator("summary").filter({ hasText: "Review unresolved paths" }).click();
  await expect(editor.getByText("missing.flac", { exact: true })).toBeVisible();
  expect(saved).toBe(false);
  await editor.getByRole("button", { name: "Save mapping for future exports", exact: true }).click();
  await editor.getByRole("button", { name: "Refresh Favorites (2 entries)", exact: true }).click();
  const downloading = page.waitForEvent("download");
  await editor.getByRole("button", { name: "Download M3U8", exact: true }).click();
  const download = await downloading;
  expect(download.suggestedFilename()).toBe("Favorites.m3u8");
  expect(readFileSync((await download.path())!, "utf8")).toContain("./Artist/Album/01.mp3");
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "theme", "dark");
  await editor.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/library-tools-playlists-phone-dark.png", animations: "disabled" });
});

test("fingerprint candidates only select tracks for human review", async ({ page }) => {
  await mock(page);
  let compared = false;
  await page.route("**/api/maintenance/fingerprints", route => {
    if (route.request().method() === "POST") { expect(route.request().postDataJSON()).toEqual({ ids: [1, 2] }); compared = true; return route.fulfill({ status: 202, json: { ok: true } }); }
    expect(route.request().method()).toBe("GET");
    return route.fulfill({ json: compared ? { ...idle, state: "complete", total: 2, processed: 2, updated: 2, groups: [[1, 2]] } : idle });
  });
  await page.locator("#maintenance-workbench > summary").click();
  await page.locator(".library-tool > summary").filter({ hasText: "Acoustic duplicate candidates" }).click();
  const tool = page.getByRole("region", { name: "Acoustic duplicate candidates", exact: true });
  await tool.getByRole("button", { name: "Compare current scope", exact: true }).click();
  await expect(tool).toContainText("Candidate groups in this scope: 1");
  await tool.getByRole("button", { name: "Select group 1 for review", exact: true }).click();
  await expect(page.getByLabel("Select Track 1", { exact: true })).toBeChecked();
  await expect(page.getByLabel("Select Track 2", { exact: true })).toBeChecked();
  await expect(page.locator(".track-table tbody > tr.track-row")).toHaveCount(2);
});

test("lyrics history compares revisions before an explicit restore save", async ({ page }) => {
  await mock(page);
  let writes = 0;
  await page.route("**/api/library/1/audition", route => route.fulfill({ json: { audio_url: "/fixture.wav", format: ".opus", size: 10, lyrics: "[00:01.00]Current", lyrics_state: "ready" } }));
  await page.route("**/api/library/1/lyrics/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "edit", text: "[00:01.00]Current" } });
    writes++; expect(route.request().postDataJSON()).toEqual({ token: "edit", text: "[00:01.00]Historical", delay_ms: 0 });
    return route.fulfill({ json: { text: "[00:01.00]Historical" } });
  });
  await page.route("**/api/library/1/history?*", route => route.fulfill({ json: route.request().url().includes("revision=") ? { text: "[00:01.00]Historical" } : { version: 1, entries: [{ hash: "old", at: 1800000000, bytes: 30 }] } }));
  await page.getByRole("button", { name: "Audition Track 1", exact: true }).click();
  await page.getByRole("button", { name: "Edit lyrics / timing", exact: true }).click();
  const editor = page.getByRole("region", { name: "Edit lyrics / timing", exact: true });
  await editor.locator(".edit-history > summary").click();
  await editor.getByLabel("Saved revision", { exact: true }).selectOption("old");
  await expect(editor.getByLabel("Historical preview", { exact: true })).toHaveValue("[00:01.00]Historical");
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue("[00:01.00]Current");
  await editor.getByRole("button", { name: "Load revision into editor", exact: true }).click();
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue("[00:01.00]Historical");
  expect(writes).toBe(0);
  await editor.getByRole("button", { name: "Save output lyrics", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
});

test("online cover preview shows dimensions and requires a separate save", async ({ page }) => {
  await mock(page);
  let writes = 0;
  const image = await page.evaluate(() => { const canvas = document.createElement("canvas"); canvas.width = 64; canvas.height = 32; const ctx = canvas.getContext("2d")!; ctx.fillStyle = "#7a4caa"; ctx.fillRect(0, 0, 64, 32); return canvas.toDataURL("image/jpeg").split(",")[1]; });
  await page.route("**/api/library/1/artwork/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "cover", override: false } });
    writes++; expect(route.request().postDataJSON().token).toBe("cover"); return route.fulfill({ json: { ok: true } });
  });
  await page.route("**/api/library/1/artwork/search", route => { expect(route.request().postDataJSON()).toEqual({ query: "Artist Album" }); return route.fulfill({ json: { candidates: [{ id: "release", title: "Album", artist: "Artist", country: "GB", date: "2026", source: "MusicBrainz / Cover Art Archive", url: "https://musicbrainz.org/release/release" }] } }); });
  await page.route("**/api/library/1/artwork/preview?release=release", route => route.fulfill({ json: { image, width: 64, height: 32, source: "MusicBrainz / Cover Art Archive", url: "https://musicbrainz.org/release/release" } }));
  await page.getByRole("button", { name: "View details for Track 1", exact: true }).click();
  await page.getByRole("button", { name: "Correct output cover", exact: true }).click();
  const editor = page.getByRole("region", { name: "Correct output cover", exact: true });
  await editor.locator(".cover-search > summary").click();
  await editor.getByRole("button", { name: "Search cover candidates", exact: true }).click();
  await editor.getByRole("button", { name: "Preview this cover", exact: true }).click();
  await expect(editor).toContainText("64 × 32 px");
  await expect(editor.getByRole("img", { name: "Output cover preview", exact: true })).toBeVisible();
  expect(writes).toBe(0);
  await editor.getByRole("button", { name: "Save output cover", exact: true }).click();
  await expect.poll(() => writes).toBe(1);
});
