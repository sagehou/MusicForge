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
  await page.goto("/library");
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
  await expect.poll(async () => (await page.locator(".sidebar").boundingBox())!.width).toBe(216);
  await page.goto("/library");
  await expect(page.locator(".track-open").first()).toBeVisible();
  for (const width of [801, 900, 1024]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  }
  await page.setViewportSize({ width: 900, height: 900 });
  await page.screenshot({ path: "test-results/workspace-records-tablet.png", animations: "disabled" });
});

test("track inspectors keep the filtered list and restore keyboard focus without a source read", async ({ page }) => {
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
  const inspector = page.getByRole("dialog", { name: "Song details", exact: true });
  await expect(inspector).toContainText("Song 08");
  await expect(inspector).toContainText(tracks[0].hash!);
  await inspector.getByRole("button", { name: "Next track", exact: true }).click();
  await expect(inspector.locator(".detail-heading")).toContainText("Song 09");
  await expect(page.getByLabel("Search library", { exact: true })).toHaveValue("Song");
  await page.keyboard.press("Escape");
  await expect(inspector).toHaveCount(0);
  await expect(title).toBeFocused();
  expect(await page.evaluate(() => scrollY)).toBe(scroll);
  expect(unexpected).toEqual([]);
});

test("cached lyric review saves and advances in the same panel without repeating provider searches", async ({ page }) => {
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
  const picker = page.getByRole("dialog", { name: "Choose lyrics", exact: true });
  await picker.getByRole("radio").check();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  await picker.getByRole("button", { name: "Save and next", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 02");
  await expect(picker.getByText("Saved search", { exact: false })).toBeVisible();
  expect(saves).toBe(1);
  expect(searches).toBe(0);
  await picker.getByRole("button", { name: "Previous track", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 01");
  expect(searches).toBe(0);
  await picker.getByRole("radio").check();
  await expect(picker.locator(".lyrics-preview pre")).toContainText("海岸与夜晚");
  await page.screenshot({ path: "test-results/workspace-lyrics-review.png", animations: "disabled" });
});

test("signing in after expiry restores the open lyric panel and its unsaved query", async ({ page }) => {
  await setup(page);
  let authenticated = true;
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/auth/login", route => { authenticated = true; return route.fulfill({ json: {} }); });
  await page.route("**/api/library/*/lyrics/candidates", route => route.fulfill({ json: { query: "Song 01 River Ensemble", groups: [] } }));
  await page.goto("/library");
  await page.locator(".track-table tbody > tr").first().getByRole("button", { name: "Choose lyrics", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "Choose lyrics", exact: true });
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

test("phone pages retain real content, readable facts and full-screen inspectors in both themes", async ({ page }) => {
  await setup(page);
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/library");
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
      const panel = page.getByRole("dialog", { name: "Song details", exact: true });
      await expect(panel).toBeVisible();
      const box = await panel.boundingBox();
      expect(box!.width).toBe(width);
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
