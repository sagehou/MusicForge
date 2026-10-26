import { test, expect, type Page } from "@playwright/test";
import { activeLyric, lyricsTimeline } from "../src/lyrics-timeline";
import type { Source } from "../src/types";
import { setPreference } from "./preferences";

const sources: Source[] = [
  { id: 91, title: "Nightfall", artist: "River Ensemble", album: "Night Studies", path: "River/Night/01.flac", track: 1, disc: 1, duration: 180, present: true, output: "River/Night/01.opus", output_present: true, status: "ready", error: "", size: 10485760, source_audio: { codec: "flac", sample_rate: 44100, bit_depth: 16, channels: 2 }, metadata: {} },
  { id: 92, title: "Still Water", artist: "River Ensemble", album: "Night Studies", path: "River/Night/02.flac", track: 2, disc: 1, duration: 180, present: false, output: "River/Night/02.opus", output_present: true, status: "expired", error: "", size: 10485760, metadata: {} },
  { id: 93, title: "Unbuilt", artist: "River Ensemble", album: "Night Studies", path: "River/Night/03.flac", track: 3, disc: 1, present: true, output: "", output_present: false, status: "missing", error: "", size: 10485760, metadata: {} },
];
const lrc = "[ti:Nightfall]\n[offset:500]\n" + Array.from({ length: 70 }, (_, i) => `[00:${String(i % 60).padStart(2, "0")}.500]${i < 60 ? `Lyric ${i}` : `Translation ${i - 60}`}`).join("\n");

// A real decodable PCM fixture isolates the UI checks from network/providers.
// workflow.spec.ts additionally plays the application's actual generated Opus.
function wave(seconds = 80) {
  const data = Buffer.alloc(44 + seconds * 8000 * 2);
  data.write("RIFF", 0); data.writeUInt32LE(data.length - 8, 4); data.write("WAVEfmt ", 8);
  data.writeUInt32LE(16, 16); data.writeUInt16LE(1, 20); data.writeUInt16LE(1, 22);
  data.writeUInt32LE(8000, 24); data.writeUInt32LE(16000, 28); data.writeUInt16LE(2, 32); data.writeUInt16LE(16, 34);
  data.write("data", 36); data.writeUInt32LE(data.length - 44, 40);
  return data;
}

async function mockAudition(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", csrf: "test", method: "local", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: sources }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
  await page.route("**/api/library/*/audition", route => {
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    return route.fulfill({ json: { audio_url: `/api/library/${id}/audition/audio?v=fixture`, format: ".opus", size: 1280044, lyrics: id === 91 ? lrc : "[00:00.00]纯音乐，请欣赏\n", lyrics_state: "ready" } });
  });
  const audio = wave();
  await page.route("**/api/library/*/audition/audio?*", route => {
    const range = route.request().headers().range?.match(/^bytes=(\d+)-(\d*)$/);
    if (!range) return route.fulfill({ contentType: "audio/wav", headers: { "Accept-Ranges": "bytes" }, body: audio });
    const start = Number(range[1]), end = range[2] ? Math.min(Number(range[2]), audio.length - 1) : audio.length - 1;
    return route.fulfill({ status: 206, contentType: "audio/wav", headers: { "Accept-Ranges": "bytes", "Content-Range": `bytes ${start}-${end}/${audio.length}` }, body: audio.subarray(start, end + 1) });
  });
}

test("LRC timeline respects offsets, repeats, translations, blanks and seeking boundaries", () => {
  const cues = lyricsTimeline("\uFEFF[ti:Ignored]\n[offset:+500]\n[00:02.50][00:04:500]Repeat\n[00:02.500]译文\n[00:03.5]\n[00:99]Invalid\n[00:02.500]译文");
  expect(cues).toEqual([{ time: 2, text: "Repeat\n译文" }, { time: 3, text: "" }, { time: 4, text: "Repeat" }]);
  expect(activeLyric(cues, 1.99)).toBe(-1);
  expect(activeLyric(cues, 2)).toBe(0);
  expect(activeLyric(cues, 3.9)).toBe(1);
  expect(activeLyric(cues, 200)).toBe(2);
  expect(activeLyric([], 0)).toBe(-1);
  expect(lyricsTimeline("[offset:-200]\n[00:00.1]Start")[0].time).toBeCloseTo(0.3);
});

test("inline audition plays actual audio, seeks and follows lyrics without scrolling the page", async ({ page }) => {
  await mockAudition(page);
  const writes: string[] = [];
  page.on("request", request => { if (request.url().includes("/api/") && request.method() !== "GET") writes.push(request.url()); });
  await page.goto("/library?view=tracks");
  await expect(page.getByRole("button", { name: "Audition Unbuilt", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Audition Nightfall", exact: true }).click();
  const panel = page.locator(".artifact-audition");
  const audio = panel.locator("audio");
  await expect(panel).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).duration)).toBe(80);
  const seek = panel.getByRole("slider", { name: "Playback position", exact: true });
  await expect(seek).toHaveAttribute("max", "80");
  await panel.getByRole("button", { name: "Play artifact", exact: true }).click();
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeGreaterThan(0.05);
  await panel.getByRole("button", { name: "Pause audition", exact: true }).click();
  const scroll = await page.evaluate(() => scrollY);
  await audio.evaluate(node => { (node as HTMLAudioElement).currentTime = 45.1; });
  await expect(panel.locator('[aria-current="true"]')).toContainText("Lyric 45");
  await expect.poll(() => panel.locator(".audition-lyrics").evaluate(node => node.scrollTop)).toBeGreaterThan(400);
  expect(Math.abs(await page.evaluate(() => scrollY) - scroll)).toBeLessThan(2);
  await seek.focus();
  await seek.press("Home");
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeLessThan(0.1);
  await expect(panel.locator('[aria-current="true"]')).toContainText("Translation 0");
  await panel.locator(".audition-lyrics").dispatchEvent("wheel", { deltaY: 80 });
  await expect(panel.getByRole("button", { name: "Follow lyrics", exact: true })).toHaveAttribute("aria-pressed", "false");
  await audio.evaluate(node => { (node as HTMLAudioElement).currentTime = 30.1; });
  await panel.getByRole("button", { name: "Follow lyrics", exact: true }).click();
  await expect(panel.locator('[aria-current="true"]')).toContainText("Lyric 30");
  await expect.poll(() => panel.locator(".audition-lyrics").evaluate(node => node.scrollTop)).toBeGreaterThan(300);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: "test-results/audition-desktop-light-en.png", animations: "disabled" });
  expect(writes).toEqual([]);
});

test("switching, collapsing, other details and navigation release the previous audio", async ({ page }) => {
  await mockAudition(page);
  await page.goto("/library?view=tracks");
  const open = page.getByRole("button", { name: "Audition Nightfall", exact: true });
  await open.click();
  const first = await page.locator(".artifact-audition audio").elementHandle();
  await page.getByRole("button", { name: "Play artifact", exact: true }).click();
  await expect.poll(() => first!.evaluate(node => !(node as HTMLAudioElement).paused)).toBe(true);
  await page.getByRole("button", { name: "Audition Still Water", exact: true }).click();
  await expect(page.locator(".artifact-audition")).toHaveCount(1);
  await expect(page.locator(".artifact-audition")).toContainText("Still Water");
  expect(await first!.evaluate(node => (node as HTMLAudioElement).paused && !node.hasAttribute("src"))).toBe(true);
  await expect(page.locator(".artifact-audition")).toContainText("纯音乐，请欣赏");
  await page.keyboard.press("Escape");
  await expect(page.locator(".artifact-audition")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Audition Still Water", exact: true })).toBeFocused();
  await open.click();
  const second = await page.locator(".artifact-audition audio").elementHandle();
  await page.getByRole("button", { name: "Play artifact", exact: true }).click();
  await page.getByRole("button", { name: "View details for Nightfall", exact: true }).click();
  await expect(page.locator(".artifact-audition")).toHaveCount(0);
  expect(await second!.evaluate(node => (node as HTMLAudioElement).paused && !node.hasAttribute("src"))).toBe(true);
  await open.click();
  const third = await page.locator(".artifact-audition audio").elementHandle();
  await page.getByRole("button", { name: "Play artifact", exact: true }).click();
  await page.getByRole("link", { name: "Overview", exact: true }).click();
  await expect(page.locator(".artifact-audition")).toHaveCount(0);
  expect(await third!.evaluate(node => (node as HTMLAudioElement).paused && !node.hasAttribute("src"))).toBe(true);
});

test("audition errors retry and missing or changed lyrics do not block playback", async ({ page }) => {
  await mockAudition(page);
  let state = "failed";
  await page.route("**/api/library/91/audition", route => state === "failed" ? route.fulfill({ status: 409, json: { error: "Generated audio is unavailable; reload the library and try again" } }) : route.fulfill({ json: { audio_url: "/api/library/91/audition/audio?v=fixture", format: ".opus", size: 100, lyrics: "", lyrics_state: state } }));
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "Audition Nightfall", exact: true }).click();
  await expect(page.locator(".artifact-audition").getByRole("alert")).toContainText("Generated audio is unavailable");
  state = "missing";
  await page.getByRole("button", { name: "Reload audition", exact: true }).click();
  await expect(page.locator(".audition-empty")).toContainText("No saved output lyrics");
  await page.getByRole("button", { name: "Play artifact", exact: true }).click();
  await expect.poll(() => page.locator(".artifact-audition audio").evaluate(node => !(node as HTMLAudioElement).paused)).toBe(true);
  await page.locator(".artifact-audition audio").dispatchEvent("error");
  await expect(page.locator(".artifact-audition").getByRole("alert")).toContainText("Cannot play this artifact");
  state = "unavailable";
  await page.getByRole("button", { name: "Reload audition", exact: true }).click();
  await expect(page.locator(".audition-empty")).toContainText("have changed");
  await expect(page.getByRole("slider", { name: "Playback position", exact: true })).toBeEnabled();
});

test("phone audition keeps controls visible, follows global theme and stops on session expiry", async ({ page }) => {
  await mockAudition(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/library?view=albums&artist=River&album=River%2FNight");
  await setPreference(page, "language", "zh-CN");
  await setPreference(page, "theme", "dark");
  await page.getByRole("button", { name: "试听 Nightfall", exact: true }).click();
  const panel = page.locator(".artifact-audition");
  await expect(panel.locator("audio")).toHaveAttribute("src", /audition\/audio/);
  await expect(panel.getByRole("slider", { name: "播放进度", exact: true })).toBeEnabled();
  await expect.poll(() => panel.locator(".audition-controls").evaluate(node => { const box = node.getBoundingClientRect(); return box.top >= 0 && box.bottom < innerHeight - 70; })).toBe(true);
  await expect.poll(() => panel.locator(".audition-lyrics").evaluate(node => { const box = node.getBoundingClientRect(); return box.top > 0 && box.bottom < innerHeight - 70; })).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/audition-phone-dark-zh.png", animations: "disabled" });
  await panel.getByRole("button", { name: "播放产物", exact: true }).click();
  const audio = await panel.locator("audio").elementHandle();
  await expect.poll(() => audio!.evaluate(node => !(node as HTMLAudioElement).paused)).toBe(true);
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("session-updated", { detail: { initialized: true, authenticated: false, csrf: "", oidc: false, version: "test" } })));
  await expect.poll(() => audio!.evaluate(node => (node as HTMLAudioElement).paused)).toBe(true);
});
