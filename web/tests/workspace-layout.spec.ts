import { test, expect, type Page } from "@playwright/test";
import { setPreference } from "./preferences";
import type { Source } from "../src/types";

const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const tracks: Source[] = Array.from({ length: 30 }, (_, index) => ({
  id: index + 1, path: `Artist/Album/${index + 1}.flac`, title: `Song ${String(index + 1).padStart(2, "0")}`, artist: "River Ensemble", album: "Night Studies", track: index + 1, disc: 1, duration: 201,
  present: true, output: `Artist/Album/${index + 1}.opus`, output_present: true, status: "ready", error: "", size: 26214400, hash: "abcdef01".repeat(8), metadata: { genre: "Jazz" },
  source_bitrate: 894000, source_audio: { codec: "flac", bit_depth: 24, sample_rate: 96000, channels: 2 }, output_size: 5242880,
  lyrics: { state: "needs_review", output: `Artist/Album/${index + 1}.opus`, signature: "audio", checked: 1 },
}));
async function setup(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: tracks }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [], total: 0 } }));
  await page.route("**/api/dashboard", route => route.fulfill({ json: { source_count: 30, output_count: 30, ready_count: 28, rebuild_count: 1, failed_count: 1, expired_count: 0, online: true, codec: "opus", enabled: true, version: "test" } }));
}

test("aligned records fit six tracks, and the icon rail keeps navigation and preferences usable", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/library?view=tracks");
  await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
  await expect(page.getByRole("button", { name: /^All songs/ })).toHaveAttribute("aria-pressed", "true");
  const rows = page.locator(".track-table tbody > tr");
  await expect(rows).toHaveCount(30);
  const sixth = await rows.nth(5).boundingBox();
  expect(sixth!.y + sixth!.height).toBeLessThan(900);
  const first = await rows.first().boundingBox();
  expect(first!.height).toBeLessThanOrEqual(110);
  await expect(rows.first().locator(".track-facts")).toContainText("24 bit / 96 kHz");
  await expect(rows.first().locator(".track-facts")).toContainText("25 MiB");
  await expect(rows.first().locator(".track-facts")).toContainText("5 MiB");
  expect(await rows.first().locator(".lyrics-action").evaluateAll(buttons => buttons.every(button => getComputedStyle(button).whiteSpace === "nowrap"))).toBe(true);
  await page.screenshot({ path: "test-results/workspace-records-desktop.png", animations: "disabled" });
  await page.getByRole("button", { name: "Collapse sidebar", exact: true }).click();
  await expect.poll(async () => (await page.locator(".sidebar").boundingBox())!.width).toBe(76);
  await expect(page.locator(".sidebar .nav-label").first()).toBeHidden();
  await expect(page.locator(".sidebar .account-name")).toBeHidden();
  await page.reload();
  await expect(page.getByRole("button", { name: "Expand sidebar", exact: true })).toHaveAttribute("aria-expanded", "false");
  await setPreference(page, "language", "zh-CN");
  await setPreference(page, "theme", "dark");
  await page.getByRole("link", { name: "设置", exact: true }).click();
  await expect(page.getByRole("heading", { name: "设置", exact: true })).toBeVisible();
  await page.screenshot({ path: "test-results/workspace-settings-collapsed-dark.png", animations: "disabled" });
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await expect.poll(async () => (await page.locator(".sidebar").boundingBox())!.width).toBe(236);
  await page.goto("/library?view=tracks");
  await expect(page.locator(".track-open").first()).toBeVisible();
  for (const width of [801, 900, 1024]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  }
  await page.setViewportSize({ width: 900, height: 900 });
  await page.screenshot({ path: "test-results/workspace-records-tablet.png", animations: "disabled" });
});

test("inline song details keep the filtered list and restore keyboard focus without a source read", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  const unexpected: string[] = [];
  page.on("request", request => { if (/artwork|\/scan|\/rebuild/.test(request.url())) unexpected.push(request.url()); });
  await page.goto("/library?view=tracks&lyrics=needs_review");
  await page.getByLabel("Search library", { exact: true }).fill("Song");
  const title = page.getByRole("button", { name: "View details for Song 08", exact: true });
  await title.scrollIntoViewIfNeeded();
  await title.focus();
  const scroll = await page.evaluate(() => scrollY);
  await title.press("Enter");
  const inspector = page.getByRole("region", { name: "Song details", exact: true });
  await expect(inspector).toContainText("Song 08");
  await expect(inspector).toContainText(tracks[0].hash!);
  await inspector.getByRole("button", { name: "Next track", exact: true }).click();
  await expect(inspector.locator(".detail-heading")).toContainText("Song 09");
  await expect(page.getByLabel("Search library", { exact: true })).toHaveValue("Song");
  await page.keyboard.press("Escape");
  await expect(inspector).toHaveCount(0);
  await expect(page.getByRole("button", { name: "View details for Song 09", exact: true })).toBeFocused();
  await expect(page.getByRole("button", { name: "View details for Song 09", exact: true })).toBeInViewport();
  expect(Math.abs(await page.evaluate(() => scrollY) - scroll)).toBeLessThan(140);
  expect(unexpected).toEqual([]);
});

test("cached lyric review saves and advances in the same inline region without repeating provider searches", async ({ page }) => {
  await setup(page);
  let searches = 0, saves = 0;
  await page.route("**/api/library", route => route.fulfill({ json: tracks.map(source => saves && source.id === 1 ? { ...source, lyrics: { ...source.lyrics, state: "ready" } } : source) }));
  await page.route("**/api/library/*/lyrics/candidates", route => {
    if (route.request().method() !== "GET") searches++;
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    return route.fulfill({ json: { query: `Song ${id}`, groups: [{ provider: "lrclib", token: `cache-${id}`, cached: true, searched: 1800000000, candidates: [{ index: 0, provider: "lrclib", title: `Song ${id}`, artist: "River Ensemble", album: "Night Studies", duration: 201, high_confidence: true, match_score: 100, duration_delta: 0, script: "simplified" }] }] } });
  });
  await page.route("**/api/library/*/lyrics/preview", route => route.fulfill({ json: { original: "[00:01.00]海岸与夜晚", script: "simplified" } }));
  await page.route("**/api/library/*/lyrics", route => { saves++; return route.fulfill({ json: {} }); });
  await page.goto("/library?view=tracks&lyrics=needs_review");
  await page.locator(".track-table tbody > tr").first().getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await expect(picker.getByRole("radio")).toBeChecked();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  await picker.getByRole("button", { name: "Save and next", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 02");
  await expect(picker.getByText("Saved search", { exact: false })).toBeVisible();
  await expect(picker.getByRole("radio")).toBeChecked();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  expect(saves).toBe(1);
  expect(searches).toBe(0);
  await picker.getByRole("button", { name: "Previous track", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 01");
  expect(searches).toBe(0);
  await expect(picker.getByRole("radio")).toBeChecked();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  await page.screenshot({ path: "test-results/workspace-lyrics-review.png", animations: "disabled" });
});

test("signing in after expiry restores the open inline lyric selection and its unsaved query", async ({ page }) => {
  await setup(page);
  let authenticated = true;
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/auth/login", route => { authenticated = true; return route.fulfill({ json: {} }); });
  await page.route("**/api/library/*/lyrics/candidates", route => route.fulfill({ json: { query: "Song 01 River Ensemble", groups: [] } }));
  await page.goto("/library?view=tracks");
  await page.locator(".track-table tbody > tr").first().getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await picker.getByLabel("Title or artist", { exact: true }).fill("Unsaved alternate title");
  authenticated = false;
  await page.evaluate(() => window.dispatchEvent(new Event("session-expired")));
  const login = page.getByRole("dialog", { name: "Session expired", exact: true });
  await expect(login).toBeVisible();
  await expect(picker).toBeHidden();
  await login.getByLabel("Password", { exact: true }).fill("test-password");
  await login.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(login).toHaveCount(0);
  await expect(picker).toBeVisible();
  await expect(picker.getByLabel("Title or artist", { exact: true })).toHaveValue("Unsaved alternate title");
  expect(await picker.evaluate(element => element.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Escape");
  await expect(picker).toHaveCount(0);
});

test("phone pages retain real content, readable facts and inline song details in both themes", async ({ page }) => {
  await setup(page);
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/library?view=tracks");
    await expect(page.locator(".track-table tbody > tr").first().locator(".track-open")).toBeInViewport();
    const first = await page.locator(".track-table tbody > tr").first().boundingBox();
    expect(first!.y + first!.height).toBeLessThan(780);
    const lyricsTitle = await page.locator(".lyrics-settings > summary").boundingBox();
    expect(lyricsTitle!.height).toBeLessThanOrEqual(40);
    expect(await page.locator(".lyrics-state").first().evaluate(element => element.scrollHeight <= element.clientHeight + 1)).toBe(true);
    for (const theme of ["light", "dark"]) {
      await setPreference(page, "theme", theme);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      await page.screenshot({ path: `test-results/workspace-songs-${width}-${theme}.png`, animations: "disabled" });
      await page.locator(".track-open").first().click();
      const panel = page.getByRole("region", { name: "Song details", exact: true });
      await expect(panel).toBeVisible();
      const box = await panel.boundingBox();
      expect(box!.x).toBeGreaterThanOrEqual(12);
      expect(box!.x + box!.width).toBeLessThanOrEqual(width - 12);
      expect(box!.width).toBeGreaterThan(width - 90);
      expect(await panel.evaluate(element => getComputedStyle(element).position)).toBe("static");
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
      await page.screenshot({ path: `test-results/workspace-details-${width}-${theme}.png`, animations: "disabled" });
      await page.keyboard.press("Escape");
    }
    for (const path of ["/", "/jobs", "/settings"]) {
      await page.goto(path);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      const heading = await page.getByRole("heading", { level: 1 }).boundingBox();
      expect(heading!.height).toBeLessThan(100);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      await page.screenshot({ path: `test-results/workspace-${path.replaceAll("/", "") || "overview"}-${width}.png`, animations: "disabled" });
    }
  }
});

test("album rows expose useful audio facts and toggle song and lyric details directly below the row", async ({ page }) => {
  await setup(page);
  await page.route("**/api/library", route => route.fulfill({ json: tracks.slice(0, 3).map((source, index) => index === 2 ? { ...source, artist: "Guest Vocalist", metadata: { ...source.metadata, albumartist: "River Ensemble" } } : source) }));
  await page.route("**/api/library/*/lyrics/candidates", route => route.fulfill({ json: { query: "Song 01", groups: [] } }));
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  const row = page.locator(".track-row").first();
  await expect(page.locator(".track-row").nth(2).locator(".track-byline")).toHaveText("Guest Vocalist");
  for (const text of ["3:21", "24 bit / 96 kHz", "894 kbps", "25 MiB", "5 MiB"]) await expect(row).toContainText(text);
  await expect(row).not.toContainText(tracks[0].path);
  for (const button of await row.locator(".lyrics-action").all()) {
    await expect(button).toHaveAttribute("title", /.+/);
    expect(await button.innerText()).toBe("");
  }
  const title = row.locator(".track-open");
  await title.click();
  const details = page.getByRole("region", { name: "Song details", exact: true });
  await expect(title).toHaveAttribute("aria-expanded", "true");
  await expect(row.locator("xpath=following-sibling::tr[1]").getByRole("region", { name: "Song details", exact: true })).toBeVisible();
  await expect(details).toContainText(tracks[0].path);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(await page.evaluate(() => document.body.style.overflow)).not.toBe("hidden");
  await title.click();
  await expect(details).toHaveCount(0);
  await expect(title).toHaveAttribute("aria-expanded", "false");
  await title.click();
  await row.getByRole("button", { name: "Choose lyrics", exact: true }).click();
  await expect(details).toHaveCount(0);
  const lyrics = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await expect(row.locator("xpath=following-sibling::tr[1]").locator(".lyrics-picker")).toBeVisible();
  await lyrics.getByLabel("Title or artist", { exact: true }).fill("Unsaved lyric query");
  await setPreference(page, "language", "zh-CN");
  await setPreference(page, "theme", "dark");
  await expect(page.locator(".lyrics-query input")).toHaveValue("Unsaved lyric query");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.locator(".inline-details h2").focus();
    await page.screenshot({ path: `test-results/album-lyrics-inline-${width}-dark-zh.png`, animations: "disabled" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(page.locator(".inline-details")).toHaveCount(0);
  await expect(row.getByRole("button", { name: "选择歌词", exact: true })).toBeFocused();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await title.click();
    await page.screenshot({ path: `test-results/album-song-inline-${width}-dark-zh.png`, animations: "disabled" });
    await page.keyboard.press("Escape");
  }
});

test("inline track navigation crosses pages and explicit filters dismiss the expanded row", async ({ page }) => {
  await setup(page);
  const library = Array.from({ length: 65 }, (_, index) => ({ ...tracks[0], id: index + 1, track: index + 1, title: `Song ${String(index + 1).padStart(2, "0")}` }));
  await page.route("**/api/library", route => route.fulfill({ json: library }));
  await page.goto("/library?view=tracks");
  await page.getByRole("button", { name: "View details for Song 50", exact: true }).click();
  const details = page.getByRole("region", { name: "Song details", exact: true });
  await details.getByRole("button", { name: "Next track", exact: true }).click();
  await expect(details.locator(".detail-heading")).toContainText("Song 51");
  await expect(page.locator(".track-row").first()).toContainText("Song 51");
  await details.getByRole("button", { name: "Previous track", exact: true }).click();
  await expect(details.locator(".detail-heading")).toContainText("Song 50");
  await page.getByLabel("Search library", { exact: true }).fill("Song 01");
  await expect(details).toHaveCount(0);
  await expect(page.locator(".track-row")).toHaveCount(1);
  await page.locator(".track-open").click();
  await expect(details.locator(".detail-heading")).toContainText("Song 01");
  await expect(details.getByRole("button", { name: "Next track", exact: true })).toBeDisabled();
});

test("saving inline lyrics protects the editor and preserves its review order across a filtered page boundary", async ({ page }) => {
  await setup(page);
  const ready = new Set<number>();
  const library = Array.from({ length: 52 }, (_, index) => ({ ...tracks[0], id: index + 1, track: index + 1, title: `Song ${String(index + 1).padStart(2, "0")}`, path: `Artist/Album/${index + 1}.flac`, output: `Artist/Album/${index + 1}.opus` }));
  await page.route("**/api/library", route => route.fulfill({ json: library.map(source => ready.has(source.id) ? { ...source, lyrics: { ...source.lyrics, state: "ready" } } : source) }));
  let searches = 0;
  await page.route("**/api/library/*/lyrics/candidates", route => {
    if (route.request().method() !== "GET") searches++;
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    return route.fulfill({ json: { query: `Song ${id}`, groups: [{ provider: "lrclib", token: `cache-${id}`, cached: true, candidates: [{ index: 0, provider: "lrclib", title: `Song ${id}`, artist: "River Ensemble", duration: 201, high_confidence: true, match_score: 100, duration_delta: 0, script: "simplified" }] }] } });
  });
  await page.route("**/api/library/*/lyrics/preview", route => route.fulfill({ json: { original: "[00:01.00]海岸与夜晚", script: "simplified" } }));
  let saving = false;
  let releaseSave!: () => void;
  const heldSave = new Promise<void>(resolve => { releaseSave = resolve; });
  await page.route("**/api/library/50/lyrics", async route => {
    saving = true;
    await heldSave;
    ready.add(50);
    await route.fulfill({ json: {} });
  });
  await page.goto("/library?view=tracks&lyrics=needs_review");
  await page.locator(".track-row").filter({ has: page.getByRole("button", { name: "View details for Song 50", exact: true }) }).getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("region", { name: "Choose lyrics", exact: true });
  await expect(picker.getByRole("radio")).toBeChecked();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  await picker.getByRole("button", { name: "Save and next", exact: true }).click();
  await expect.poll(() => saving).toBe(true);
  await expect(picker.getByRole("button", { name: "Close lyrics selection", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "View details for Song 01", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Next page", exact: true })).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(picker).toBeVisible();
  releaseSave();
  await expect(picker.locator(".detail-heading")).toContainText("Song 51");
  await expect(page.locator(".track-row[data-expanded=true]")).toContainText("Song 51");
  await picker.getByRole("button", { name: "Previous track", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 50");
  await expect(page.locator(".track-row[data-expanded=true]")).toContainText("Available");
  await expect(picker.getByRole("radio")).toBeChecked();
  expect(searches).toBe(0);
  await picker.getByRole("button", { name: "Close lyrics selection", exact: true }).click();
  await expect(picker).toHaveCount(0);
  await expect(page.getByRole("button", { name: "View details for Song 50", exact: true })).toHaveCount(0);
});
