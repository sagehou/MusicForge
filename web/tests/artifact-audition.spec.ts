import { routeLibrary } from "./library-fixture";
import { test, expect, type Locator, type Page } from "@playwright/test";
import { activeLyric, lyricsTimeline } from "../src/lyrics-timeline";
import type { Source } from "../src/types";
import { setPreference } from "./preferences";

const sources: Source[] = [
  { id: 91, title: "Nightfall", artist: "River Ensemble", album: "Night Studies", path: "River/Night/01.flac", track: 1, disc: 1, duration: 180, present: true, output: "River/Night/01.opus", output_present: true, status: "ready", error: "", size: 26214400, source_bitrate: 894000, output_size: 5242880, source_audio: { codec: "flac", sample_rate: 96000, bit_depth: 24, channels: 2 }, lyrics: { state: "ready", output: "River/Night/01.opus", signature: "audio", checked: 1 }, metadata: {} },
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
  await routeLibrary(page, route => route.fulfill({ json: sources }));
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

async function expectCurrentVisible(panel: Locator) {
  await expect.poll(() => panel.locator(".audition-lyrics").evaluate(container => {
    const current = container.querySelector('[aria-current="true"]');
    if (!current) return false;
    const pane = container.getBoundingClientRect(), line = current.getBoundingClientRect();
    return line.top >= pane.top + 1 && line.bottom <= pane.bottom - 1;
  })).toBe(true);
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
  await page.setViewportSize({ width: 1440, height: 1000 });
  const writes: string[] = [];
  page.on("request", request => { if (request.url().includes("/api/") && request.method() !== "GET") writes.push(request.url()); });
  await page.goto("/library?view=tracks");
  await expect(page.getByRole("button", { name: "Audition Unbuilt", exact: true })).toHaveCount(0);
  await expect(page.locator("#lyrics-edit-trigger-93")).toHaveCount(0);
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
  await expectCurrentVisible(panel);
  expect(Math.abs(await page.evaluate(() => scrollY) - scroll)).toBeLessThan(2);
  await seek.focus();
  await seek.press("Home");
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeLessThan(0.1);
  await expect(panel.locator('[aria-current="true"]')).toContainText("Translation 0");
  await panel.locator(".audition-lyrics").dispatchEvent("wheel", { deltaY: 80 });
  await expect(panel.getByRole("button", { name: "Follow lyrics", exact: true })).toHaveAttribute("aria-pressed", "true");
  await audio.evaluate(node => { (node as HTMLAudioElement).playbackRate = 4; });
  await panel.getByRole("button", { name: "Play artifact", exact: true }).click();
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime), { timeout: 10000 }).toBeGreaterThan(12);
  await expectCurrentVisible(panel);
  await panel.locator(".audition-lyrics").evaluate(node => { node.scrollTop = 0; });
  await panel.locator(".audition-lyrics").dispatchEvent("wheel", { deltaY: -400 });
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime), { timeout: 10000 }).toBeGreaterThan(20);
  await expectCurrentVisible(panel);
  await panel.getByRole("button", { name: "Pause audition", exact: true }).click();
  expect(Math.abs(await page.evaluate(() => scrollY) - scroll)).toBeLessThan(2);
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: "test-results/audition-desktop-light-en.png", animations: "disabled" });
  expect(writes).toEqual([]);
});

test("lyric clicks and keyboard seeking update the audio, slider and follow state", async ({ page }) => {
  await mockAudition(page);
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "Audition Nightfall", exact: true }).click();
  const panel = page.locator(".artifact-audition"), audio = panel.locator("audio");
  const slider = panel.getByRole("slider", { name: "Playback position", exact: true });
  const follow = panel.getByRole("button", { name: "Follow lyrics", exact: true });
  await expect(slider).toBeEnabled();
  await follow.click();
  await expect(follow).toHaveAttribute("aria-pressed", "false");
  await panel.getByRole("button", { name: "Seek to 0:40: Lyric 40", exact: true }).click();
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeCloseTo(40, 1);
  await expect(slider).toHaveValue("40");
  await expect(panel.locator('[aria-current="true"]')).toContainText("Lyric 40");
  expect(await audio.evaluate(node => (node as HTMLAudioElement).paused)).toBe(true);
  await expect(follow).toHaveAttribute("aria-pressed", "true");
  await expectCurrentVisible(panel);
  const earlier = panel.getByRole("button", { name: "Seek to 0:12: Lyric 12", exact: true });
  await earlier.focus();
  await earlier.press("Enter");
  await expect(slider).toHaveValue("12");
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeCloseTo(12, 1);
  await expectCurrentVisible(panel);
  await panel.getByRole("button", { name: "Play artifact", exact: true }).click();
  await panel.getByRole("button", { name: "Seek to 0:30: Lyric 30", exact: true }).click();
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeGreaterThanOrEqual(30);
  expect(await audio.evaluate(node => (node as HTMLAudioElement).paused)).toBe(false);
  await expect.poll(() => slider.inputValue().then(Number)).toBeGreaterThanOrEqual(30);
  await expectCurrentVisible(panel);
  await panel.getByRole("button", { name: "Pause audition", exact: true }).click();
  const translated = panel.getByRole("button", { name: "Seek to 0:00: Lyric 0 Translation 0", exact: true });
  await translated.focus();
  await translated.press("Space");
  await expect(slider).toHaveValue("0");
  await expectCurrentVisible(panel);
});

test("individual lyric lines keep repeats and translations while editing and auditioning", async ({ page }) => {
  await mockAudition(page);
  const draft = "[ti:Nightfall]\n[offset:500]\n[00:01.000][00:03.000]Original\n[00:01.000]译文\n";
  const writes: unknown[] = [];
  await page.route("**/api/library/91/lyrics/edit", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { token: "edit", text: draft } });
    const body = route.request().postDataJSON(); writes.push(body);
    return route.fulfill({ json: { text: body.text } });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?view=tracks");
  const edit = page.locator("#lyrics-edit-trigger-91");
  await expect(edit).toHaveText("Edit lyrics");
  await page.screenshot({ path: "test-results/lyrics-edit-entry-desktop-light-en.png", animations: "disabled" });
  await edit.focus(); await edit.press("Enter");
  const editor = page.locator(".lyrics-editor"), audio = page.locator(".artifact-audition audio");
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue(draft);
  await expect(edit).toHaveAttribute("aria-expanded", "true");
  expect(await audio.evaluate(node => (node as HTMLAudioElement).paused)).toBe(true);
  await editor.locator(".lyric-line-editor > summary").click();
  await editor.getByRole("button", { name: "Line 1 100 ms later", exact: true }).click();
  const time = editor.getByLabel("Line 1 time (seconds)", { exact: true });
  await time.focus(); await time.press("Alt+ArrowRight");
  await expect(time).toHaveValue("0.7");
  await editor.getByLabel("Line 1 text", { exact: true }).fill("Edited");
  await expect(editor.getByLabel("Line 2 text", { exact: true })).toHaveValue("Edited");
  await expect(editor.getByLabel("Line 3 text", { exact: true })).toHaveValue("译文");
  await expect(editor.getByLabel("Line 2 time (seconds)", { exact: true })).toHaveValue("2.5");
  await editor.getByRole("button", { name: "Audition line 2", exact: true }).click();
  await expect.poll(() => audio.evaluate(node => (node as HTMLAudioElement).currentTime)).toBeGreaterThanOrEqual(2.5);
  await page.getByRole("button", { name: "Pause audition", exact: true }).click();
  await audio.evaluate(node => { (node as HTMLAudioElement).currentTime = 12.345; node.dispatchEvent(new Event("timeupdate")); });
  const translationTime = editor.getByLabel("Line 3 time (seconds)", { exact: true });
  await translationTime.focus(); await translationTime.press("Alt+Enter");
  await expect(translationTime).toHaveValue("12.345");
  const expected = "[ti:Nightfall]\n[00:00.700][00:02.500]Edited\n[00:12.345]译文\n";
  await expect(editor.getByLabel("LRC text", { exact: true })).toHaveValue(expected);
  expect(writes).toEqual([]);
  await editor.screenshot({ path: "test-results/lyric-lines-desktop-light-en.png", animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "theme", "dark"); await setPreference(page, "language", "zh-CN");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await editor.screenshot({ path: "test-results/lyric-lines-phone-dark-zh.png", animations: "disabled" });
  await editor.getByRole("button", { name: "保存输出歌词", exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ token: "edit", text: expected, delay_ms: 0 });
  await page.keyboard.press("Escape");
  await expect(page.locator(".artifact-audition")).toHaveCount(0);
  await expect(edit).toBeFocused();
});

test("existing lyrics require explicit association review and retain the preview on conflicts", async ({ page }) => {
  await mockAudition(page);
  const original = "[ti:愛這風雲]\n[00:01.000]愛這風雲 Love!\n";
  let associated = false, reviews = 0;
  const writes: unknown[] = [];
  await page.route("**/api/library/91/audition", route => route.fulfill({ json: { audio_url: "/api/library/91/audition/audio?v=fixture", format: ".opus", size: 1280044, lyrics: "", lyrics_state: "unavailable" } }));
  await page.route("**/api/library/91/lyrics/edit", route => {
    expect(route.request().method()).toBe("GET");
    return route.fulfill(associated ? { json: { token: "edit", text: original } } : { status: 409, json: { error: "Invalid lyrics ownership annotation" } });
  });
  await page.route("**/api/library/91/lyrics/association", route => {
    if (route.request().method() === "GET") { reviews++; return route.fulfill({ json: { path: "River/Night/01.lrc", text: original, fingerprint: `review-${reviews}`, required: !associated } }); }
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    writes.push(route.request().postDataJSON());
    if (writes.length === 1) return route.fulfill({ status: 409, json: { error: "Lyrics or song changed after association review; review again" } });
    associated = true;
    return route.fulfill({ json: { text: original } });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "Audition Nightfall", exact: true }).click();
  await page.getByRole("button", { name: "Edit lyrics / timing", exact: true }).click();
  const editor = page.locator(".lyrics-editor");
  await expect(editor.getByRole("alert")).toContainText("Invalid lyrics ownership annotation");
  await editor.getByRole("button", { name: "Review existing lyric association", exact: true }).focus();
  await page.keyboard.press("Enter");
  const preview = editor.getByRole("region", { name: "Existing lyric association", exact: true });
  await expect(preview.getByLabel("Existing output LRC", { exact: true })).toHaveValue(original);
  await expect(preview.getByLabel("Existing output LRC", { exact: true })).toHaveJSProperty("readOnly", true);
  expect(writes).toEqual([]);
  await editor.screenshot({ path: "test-results/lyrics-association-desktop-light-en.png", animations: "disabled" });
  await preview.getByRole("button", { name: "Cancel association review", exact: true }).click();
  await expect(preview).toHaveCount(0);
  expect(writes).toEqual([]);
  await editor.getByRole("button", { name: "Review existing lyric association", exact: true }).click();
  await preview.getByRole("button", { name: "Confirm lyrics belong to this song", exact: true }).click();
  await expect(editor.getByRole("alert")).toContainText("review again");
  await expect(editor.getByRole("alert").getByRole("button")).toHaveCount(0);
  await expect(preview.getByLabel("Existing output LRC", { exact: true })).toHaveValue(original);
  await setPreference(page, "theme", "dark"); await setPreference(page, "language", "zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await editor.screenshot({ path: "test-results/lyrics-association-mobile-dark-zh-CN.png", animations: "disabled" });
  await editor.getByRole("button", { name: "检查已有歌词关联", exact: true }).click();
  await editor.getByRole("button", { name: "确认歌词属于这首歌", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(editor).toHaveCount(0);
  expect(writes).toEqual([{ fingerprint: "review-2" }, { fingerprint: "review-3" }]);
  await expect(page.locator(".audition-lyrics")).toContainText("愛這風雲 Love!");
  await page.getByRole("button", { name: "编辑歌词／时间轴", exact: true }).click();
  await editor.getByRole("button", { name: "检查已有歌词关联", exact: true }).click();
  await expect(editor.getByRole("status")).toContainText("歌词已关联到当前歌曲");
  await expect(editor.getByRole("button", { name: "确认歌词属于这首歌", exact: true })).toHaveCount(0);
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
  await expect(page.locator(".audition-empty")).toContainText("No lyrics available");
  await page.getByRole("button", { name: "Play artifact", exact: true }).click();
  await expect.poll(() => page.locator(".artifact-audition audio").evaluate(node => !(node as HTMLAudioElement).paused)).toBe(true);
  await page.locator(".artifact-audition audio").dispatchEvent("error");
  await expect(page.locator(".artifact-audition").getByRole("alert")).toContainText("Could not play this file");
  state = "unavailable";
  await page.getByRole("button", { name: "Reload audition", exact: true }).click();
  await expect(page.locator(".audition-empty")).toContainText("Could not load lyrics");
  await expect(page.getByRole("slider", { name: "Playback position", exact: true })).toBeEnabled();
});

test.describe("phone audition", () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 3 });
  for (const view of ["tracks", "album"]) test(`${view} keeps controls visible, supports touch seeking and follows lyrics`, async ({ page, browserName }) => {
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    await mockAudition(page);
    await page.route("**/api/library/91/lyrics/edit", route => route.fulfill({ json: { token: "edit", text: lrc } }));
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto(view === "album" ? "/library?view=albums&artist=River&album=River%2FNight" : "/library?view=tracks");
    await setPreference(page, "language", "zh-CN");
    await setPreference(page, "theme", "dark");
    const row = page.locator(".track-row").first();
    const search = page.getByLabel('搜索音乐库', { exact: true });
    expect(await search.evaluate(element => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
    for (const button of await row.locator('.track-audition-trigger, .lyrics-edit-action, .lyrics-action:visible, .lyrics-details > summary:visible').all()) {
      const box = (await button.boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(40);
      expect(box.height).toBeGreaterThanOrEqual(40);
    }
    await row.scrollIntoViewIfNeeded();
    for (const text of ["24 bit / 96 kHz", "894 kbps", "25 MiB", "5 MiB"]) await expect(row).toContainText(text);
    expect((await row.boundingBox())!.height).toBeLessThanOrEqual(230);
    await page.screenshot({ path: `test-results/audition-phone-${view}-${browserName}-library-dark-zh.png`, animations: "disabled" });
    const edit = row.getByRole("button", { name: "编辑歌词", exact: true });
    await expect(edit).toBeVisible();
    await edit.tap();
    await expect(page.locator(".lyrics-editor").getByLabel("LRC 文本", { exact: true })).toHaveValue(lrc);
    expect(await page.locator(".artifact-audition audio").evaluate(node => (node as HTMLAudioElement).paused)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.screenshot({ path: `test-results/lyrics-edit-entry-phone-${view}-${browserName}-dark-zh.png`, animations: "disabled" });
    await page.keyboard.press("Escape");
    await expect(page.locator(".artifact-audition")).toHaveCount(0);
    await expect(edit).toBeFocused();
    await page.getByRole("button", { name: "试听 Nightfall", exact: true }).tap();
    const panel = page.locator(".artifact-audition");
    await expect(panel.locator("audio")).toHaveAttribute("src", /audition\/audio/);
    await expect(panel.getByRole("slider", { name: "播放进度", exact: true })).toBeEnabled();
    await expect.poll(() => panel.locator(".audition-controls").evaluate(node => { const box = node.getBoundingClientRect(); return box.top >= 0 && box.bottom < innerHeight - 70; })).toBe(true);
    await expect.poll(() => panel.locator(".audition-lyrics").evaluate(node => { const box = node.getBoundingClientRect(); return box.top > 0 && box.bottom < innerHeight - 70; })).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await expect(panel).toBeInViewport({ ratio: 1 });
    await expect.poll(() => panel.evaluate(node => node.getBoundingClientRect().width)).toBeGreaterThan(300);
    await panel.locator(".audition-lyrics").dispatchEvent("touchmove");
    await expect(panel.getByRole("button", { name: "跟随歌词", exact: true })).toHaveAttribute("aria-pressed", "true");
    await panel.getByRole("button", { name: "跳转到 0:40：Lyric 40", exact: true }).tap();
    await expect(panel.getByRole("slider", { name: "播放进度", exact: true })).toHaveValue("40");
    await expect.poll(() => panel.locator("audio").evaluate(node => (node as HTMLAudioElement).currentTime)).toBeCloseTo(40, 1);
    await expectCurrentVisible(panel);
    await page.screenshot({ path: `test-results/audition-phone-${view}-${browserName}-dark-zh.png`, animations: "disabled" });
    await panel.getByRole("button", { name: "播放产物", exact: true }).tap();
    const audio = await panel.locator("audio").elementHandle();
    await expect.poll(() => audio!.evaluate(node => !(node as HTMLAudioElement).paused)).toBe(true);
    await expect.poll(() => audio!.evaluate(node => (node as HTMLAudioElement).currentTime), { timeout: 10000 }).toBeGreaterThan(43);
    await expectCurrentVisible(panel);
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("session-updated", { detail: { initialized: true, authenticated: false, csrf: "", oidc: false, version: "test" } })));
    await expect.poll(() => audio!.evaluate(node => (node as HTMLAudioElement).paused)).toBe(true);
    expect(errors).toEqual([]);
  });
});
