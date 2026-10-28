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

test.describe("phone audition", () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 3 });
  for (const view of ["tracks", "album"]) test(`${view} keeps controls visible, supports touch seeking and follows lyrics`, async ({ page, browserName }) => {
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    await mockAudition(page);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto(view === "album" ? "/library?view=albums&artist=River&album=River%2FNight" : "/library?view=tracks");
    await setPreference(page, "language", "zh-CN");
    await setPreference(page, "theme", "dark");
    const row = page.locator(".track-row").first();
    const search = page.getByLabel('搜索音乐库', { exact: true });
    expect(await search.evaluate(element => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
    for (const button of await row.locator('.track-audition-trigger, .lyrics-action:visible, .lyrics-details > summary:visible').all()) {
      const box = (await button.boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(40);
      expect(box.height).toBeGreaterThanOrEqual(40);
    }
    await row.scrollIntoViewIfNeeded();
    for (const text of ["24 bit / 96 kHz", "894 kbps", "25 MiB", "5 MiB"]) await expect(row).toContainText(text);
    expect((await row.boundingBox())!.height).toBeLessThanOrEqual(230);
    await page.screenshot({ path: `test-results/audition-phone-${view}-${browserName}-library-dark-zh.png`, animations: "disabled" });
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

// The approved HTML is independent of the product CSS. Freeze it so implementation
// changes cannot silently move the reference, then compare its actual rendered tokens.
test("approved library and audition design matches the desktop and phone implementation", async ({ page, context }) => {
  const { readFile } = await import("node:fs/promises");
  const { resolve } = await import("node:path");
  const { createHash } = await import("node:crypto");
  const design = await readFile(resolve("../docs/ui-refinement-preview.html"), "utf8");
  expect(createHash("sha256").update(design).digest("hex")).toBe("3ec45b0cfd0b59f455ccf8cd369acae2d6b5845b47575623c610ae677f680e14");
  await mockAudition(page);
  const tracks = ["Nightfall", "First Light · 海岸与夜晚的音乐记录", "Quiet Hours", "After the Rain"].map((title, index) => ({ ...sources[0], id: 91 + index, title, track: index + 1, duration: 201,
    lyrics: index === 2 ? undefined : { ...sources[0].lyrics!, state: index === 1 ? "needs_review" : "ready" },
  }));
  await page.route("**/api/library", route => route.fulfill({ json: tracks }));
  await page.route("**/api/library/91/audition", route => route.fulfill({ json: { audio_url: "/api/library/91/audition/audio?v=design", format: ".opus", size: 5242880,
    lyrics: "[00:32.00]晚风穿过寂静的街巷\n[00:36.00]留下熟悉的回响\n[00:40.00]在每一个音符停留的地方\n[00:44.00]让时间慢慢流淌", lyrics_state: "ready" } }));
  const audio = wave(201);
  await page.route("**/api/library/91/audition/audio?*", route => {
    const range = route.request().headers().range?.match(/^bytes=(\d+)-(\d*)$/);
    if (!range) return route.fulfill({ contentType: "audio/wav", headers: { "Accept-Ranges": "bytes" }, body: audio });
    const start = Number(range[1]), end = range[2] ? Math.min(Number(range[2]), audio.length - 1) : audio.length - 1;
    return route.fulfill({ status: 206, contentType: "audio/wav", headers: { "Accept-Ranges": "bytes", "Content-Range": `bytes ${start}-${end}/${audio.length}` }, body: audio.subarray(start, end + 1) });
  });
  const reference = await context.newPage();
  async function style(target: Locator, properties: string[]) {
    return target.first().evaluate((node, keys) => Object.fromEntries(keys.map(key => [key, getComputedStyle(node).getPropertyValue(key)])), properties);
  }
  for (const [width, height, theme] of [[1440, 900, "light"], [390, 844, "dark"]] as const) {
    await page.setViewportSize({ width, height });
    await page.goto("/library?view=tracks");
    await setPreference(page, "language", "zh-CN");
    await setPreference(page, "theme", theme);
    await reference.setViewportSize({ width, height });
    await reference.setContent(design);
    await reference.addStyleTag({ content: "#inspector-bar { display:none } #preview-container { padding:0 } #preview-frame { border:0 }" });
    await reference.evaluate(({ theme, width }) => { (window as any).setTheme(theme); (window as any).setVp(width === 390 ? "mobile" : "desktop"); (window as any).setPage("library"); }, { theme, width });
    const header = (await page.locator(".page-heading").boundingBox())!;
    const approvedHeader = (await reference.locator(".page-heading").boundingBox())!;
    for (const key of ["x", "y", "width", "height"] as const) expect(Math.abs(header[key] - approvedHeader[key]), `heading ${key} at ${width}`).toBeLessThanOrEqual(1);
    const toolbar = (await page.locator(".library-toolbar-box").boundingBox())!;
    const approvedToolbar = (await reference.locator(".toolbar").boundingBox())!;
    expect(toolbar.x).toBe(approvedToolbar.x);
    expect(toolbar.y).toBe(approvedToolbar.y);
    expect(toolbar.width).toBe(approvedToolbar.width);
    await page.getByRole("button", { name: "试听 Nightfall", exact: true }).click();
    const panel = page.locator(".artifact-audition");
    await expect(panel.getByRole("slider")).toBeEnabled();
    await panel.locator("audio").evaluate(node => { (node as HTMLAudioElement).currentTime = 40; });
    await expect(panel.locator('[aria-current="true"]')).toContainText("在每一个音符");
    const comparisons: [string, string, string[]][] = [
      [".heading-content h1", "h1", ["font-size", "font-weight", "line-height", "font-synthesis"]],
      [".library-navigation .segmented", ".toolbar .segmented", ["background-color", "border-radius", "padding"]],
      [".track-output .badge-ready", ".track-row > .status", ["color", "background-color", "border-width", "font-size"]],
      [".artifact-audition", ".audition-panel", ["padding", "background-color", "border-width"]],
      [".audition-body", ".audition-body", ["column-gap", "row-gap", "grid-template-columns"]],
      [".audition-playback", ".audition-controls", ["padding-top"]],
      [".audition-controls > .ui-button", ".play-button", ["width", "height", "border-radius", "background-color", "color"]],
      [".audition-transcript", ".audition-transcript", ["padding-inline-start", "border-inline-start-width", "border-top-width"]],
      [".audition-lyrics-heading .ui-button", ".follow", ["background-color", "color", "border-color", "font-size", "min-height"]],
      [".audition-line[aria-current]", ".lyric-line.current", ["background-color", "color", "font-size", "font-weight", "min-height", "padding"]],
    ];
    for (const [actual, expected, properties] of comparisons) expect(await style(page.locator(actual), properties), `${actual} at ${width}`).toEqual(await style(reference.locator(expected), properties));
    await panel.locator('[aria-current="true"]').hover();
    expect(await style(panel.locator('[aria-current="true"]'), ["background-color"])).toEqual(await style(reference.locator(".lyric-line.current"), ["background-color"]));
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.mouse.move(0, 0);
    await panel.screenshot({ path: `test-results/approved-actual-audition-${width}-${theme}.png`, animations: "disabled" });
    await reference.locator(".audition-panel").screenshot({ path: `test-results/approved-reference-audition-${width}-${theme}.png`, animations: "disabled" });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: `test-results/approved-actual-library-${width}-${theme}.png`, animations: "disabled" });
    await reference.locator("#preview-frame").screenshot({ path: `test-results/approved-reference-library-${width}-${theme}.png`, animations: "disabled" });
  }
  await reference.close();
});
