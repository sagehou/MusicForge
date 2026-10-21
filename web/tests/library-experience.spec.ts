import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { Source } from "../src/types";

const me = { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" };
const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const sources: Source[] = [
  { id: 1, path: "River Ensemble/Night Studies/01.flac", artist: "River Ensemble", album: "Night Studies", title: "First Light", track: 1, disc: 1, duration: 201, artwork_url: "/api/library/1/artwork?v=night", present: true, output: "River Ensemble/Night Studies/01.opus", output_present: true, status: "ready", error: "", size: 100, metadata: {} },
  { id: 2, path: "River Ensemble/Night Studies/Disc 2/01.flac", artist: "River Ensemble", album: "Night Studies", title: "Second Light", track: 1, disc: 2, duration: 183, artwork_url: "/api/library/2/artwork?v=night", present: true, output: "", output_present: false, status: "unverified", error: "", size: 100, metadata: {} },
  { id: 3, path: "River Ensemble/Field Notes/01.mp3", artist: "River Ensemble", album: "Field Notes", title: "Coast Road", track: 1, disc: 1, duration: 175, artwork_url: "/api/library/3/artwork?v=field", present: true, output: "", output_present: false, status: "failed", error: "Synthetic test diagnostic", size: 100, metadata: {} },
  { id: 4, path: "Alma/Morning Set/01.flac", artist: "Alma", album: "Morning Set", title: "Morning Set", track: 1, disc: 1, duration: 220, present: true, output: "Alma/Morning Set/01.opus", output_present: true, status: "ready", error: "", size: 100, metadata: {} },
];
const collection: Source[] = [...sources, ...Array.from({ length: 64 }, (_, index) => ({
  ...sources[0], id: index + 100, artist: `Ensemble ${index % 8 + 1}`,
  album: index === 0 ? "A Long Album Name / 海岸与夜晚的音乐记录 / Complete Sessions" : `Study ${String(index + 1).padStart(2, "0")}`,
  path: `Ensemble ${index % 8 + 1}/Study ${index + 1}/01.flac`,
  artwork_url: `/api/library/${index + 100}/artwork?v=study-${index}`,
  status: index % 7 === 0 ? "needs_rebuild" : "ready",
}))];

async function mockLibrary(page: Page, library = sources) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); if (!localStorage.getItem("musicforge.theme")) localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: me }));
  await page.route("**/api/library", route => route.fulfill({ json: library }));
  await page.route("**/api/library/*/artwork?*", async route => {
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    const body = await readFile(resolve(process.env.MUSICFORGE_TEST_ROOT || "../.ci", "artwork", `${id % 4}.png`));
    await route.fulfill({ contentType: "image/png", body });
  });
}

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

test("artwork, hierarchy, multi-disc tracks, selection and browser history", async ({ page }) => {
  await mockLibrary(page);
  await page.goto("/library");
  const artistCard = page.locator(".library-group").filter({ has: page.getByRole("button", { name: "Browse artist River Ensemble", exact: true }) });
  await expect(artistCard.locator(".cover img")).toHaveCount(2);
  await expect(artistCard.getByText("2 albums", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Browse artist River Ensemble", exact: true }).click();
  await expect(page).toHaveURL(/artist=River/);
  await page.getByRole("button", { name: "Browse album Night Studies", exact: true }).click();
  await expect(page.locator(".album-summary .cover img")).toBeVisible();
  await expect(page.getByRole("table").getByText("Disc 1", { exact: true })).toBeVisible();
  await expect(page.getByRole("table").getByText("Disc 2", { exact: true })).toBeVisible();
  await expect(page.locator(".track-facts").getByText("3:21", { exact: true })).toBeVisible();
  await page.getByLabel("Select First Light", { exact: true }).check();
  await page.getByLabel("Search library", { exact: true }).fill("Light");
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("dark");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.getByLabel("搜索音乐库", { exact: true })).toHaveValue("Light");
  await expect(page.getByLabel("选择 First Light", { exact: true })).toBeChecked();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.getByRole("table").getByText("第 2 碟", { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("button", { name: "浏览专辑 Night Studies", exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("button", { name: "浏览歌手 River Ensemble", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "浏览歌手 River Ensemble", exact: true }).click();
  await page.getByRole("button", { name: "浏览专辑 Night Studies", exact: true }).click();
  await page.getByRole("link", { name: "音乐库", exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  await expect(page.locator(".album-summary")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "浏览歌手 River Ensemble", exact: true })).toBeVisible();
});

test("all-album and list views, unverified filter, missing images and persisted theme", async ({ page }) => {
  await mockLibrary(page);
  await page.goto("/library?view=albums");
  await expect(page.locator(".library-group")).toHaveCount(3);
  await page.getByRole("button", { name: "List view", exact: true }).click();
  await expect(page.locator(".library-groups")).toHaveClass(/library-list/);
  await page.getByLabel("Filter by status", { exact: true }).selectOption("unverified");
  await expect(page.locator(".library-group")).toHaveCount(1);
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("dark");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator(".cover-fallback")).toBeVisible();
  await page.route("**/api/library/*/artwork?*", route => route.fulfill({ status: 404, json: { error: "artwork not found" } }));
  await page.reload();
  await expect(page.locator(".cover-fallback")).toHaveCount(3);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("all songs share filters, pagination and batch lyrics with visible file facts", async ({ page }) => {
  const library = collection.map(source => ({ ...source, title: source.id >= 100 ? `Song ${source.id}` : source.title }));
  Object.assign(library[0], { size: 26214400, source_bitrate: 894123, source_audio: { codec: "flac", bit_depth: 24, sample_rate: 96000, channels: 2 }, output_size: 5242880, lyrics: { state: "not_found", output: library[0].output, signature: "first", checked: 1 } });
  Object.assign(library[3], { lyrics: { state: "not_found", output: library[3].output, signature: "morning", checked: 1 } });
  const searches: unknown[] = [];
  await mockLibrary(page, library);
  await page.route("**/api/lyrics/search", route => { searches.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { selected: 2 } }); });
  await page.goto("/library");
  await expect(page.getByRole("button", { name: "Fill lyrics for all artifacts", exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^All songs/ }).click();
  await expect(page).toHaveURL(/view=tracks/);
  const rows = page.locator(".track-table tbody > tr");
  await expect(rows).toHaveCount(50);
  await expect(page.locator(".album-summary, .disc-heading, .layout-control")).toHaveCount(0);
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(rows).toHaveCount(18);
  await page.getByLabel("Filter by lyrics status", { exact: true }).selectOption("not_found");
  await expect(rows).toHaveCount(2);
  await expect(page.getByRole("group", { name: "Active filters", exact: true })).toContainText("No synchronized match");
  await page.getByRole("button", { name: "Remove filter: No synchronized match", exact: true }).click();
  await expect(page.getByLabel("Filter by lyrics status", { exact: true })).toHaveValue("all");
  await expect(rows).toHaveCount(50);
  await page.getByLabel("Filter by lyrics status", { exact: true }).selectOption("not_found");
  const first = rows.filter({ has: page.locator(".track-cell strong").filter({ hasText: /^First Light$/ }) });
  for (const text of ["3:21", "FLAC · 24 bit / 96 kHz · 2 ch", "894 kbps", "25 MiB", "5 MiB"]) await expect(first.locator(".track-facts")).toContainText(text);
  await expect(first.locator(".track-file")).not.toHaveAttribute("open", "");
  await expect(first.getByRole("button", { name: "Choose lyrics", exact: true })).toBeVisible();
  await expect(first.getByRole("button", { name: "Search again", exact: true })).toBeVisible();
  await expect(first.locator(".lyrics-details")).not.toHaveAttribute("open", "");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: "test-results/all-songs-1440-light-en.png", animations: "disabled" });
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Fill lyrics for selected artifacts", exact: true }).click();
  await expect.poll(() => searches.length).toBe(1);
  expect(searches[0]).toEqual({ ids: [1, 4], refresh: false });
  await page.reload();
  await expect(page.getByRole("button", { name: /^All songs/ })).toHaveAttribute("aria-pressed", "true");
  await page.getByLabel("Filter by status", { exact: true }).selectOption("unverified");
  await expect(rows).toHaveCount(1);
  await expect(rows.locator(".track-facts")).toContainText("3:03");
  await expect(rows.locator(".track-facts")).not.toContainText("Output size");
  await expect(rows.locator(".track-facts")).toContainText("—");
  await page.getByLabel("Filter by status", { exact: true }).selectOption("all");
  await page.getByLabel("Filter by lyrics status", { exact: true }).selectOption("not_found");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await page.setViewportSize({ width: 320, height: 844 });
  for (const theme of ["light", "dark"]) {
    await page.getByRole("combobox", { name: "外观", exact: true }).selectOption(theme);
    await expect(first.locator(".track-facts")).toContainText("3:21");
    await expect(first.locator(".track-facts")).toContainText("FLAC · 24 bit / 96 kHz · 2 声道");
    await expect(first.getByRole("button", { name: "选择歌词", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    expect(await page.locator(".table-scroll").evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
    await page.screenshot({ path: `test-results/all-songs-320-${theme}-zh.png`, fullPage: true });
  }
  await page.getByRole("button", { name: "清除筛选", exact: true }).click();
  await expect(page.locator(".active-filters")).toHaveCount(0);
  await page.locator(".library-options > summary").click();
  await expect(page.getByLabel("歌词匹配状态", { exact: true })).toHaveValue("all");
});

test("expired session reauthentication keeps unsaved Settings and never replays a write", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  let authenticated = true;
  let rejectSave = true;
  let saves = 0;
  let checks = 0;
  let reads = 0;
  await page.route("**/api/auth/me", route => { checks++; return route.fulfill({ json: { ...me, authenticated } }); });
  await page.route("**/api/auth/login", route => { authenticated = true; return route.fulfill({ json: { ok: true } }); });
  await page.route("**/api/settings", route => {
    if (route.request().method() === "PUT") {
      saves++;
      if (rejectSave) { authenticated = false; return route.fulfill({ status: 401, json: { error: "login required" } }); }
      return route.fulfill({ json: { encoding_changed: false } });
    }
    reads++;
    return route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } });
  });
  await page.goto("/settings");
  await page.getByLabel("Source audio directory", { exact: true }).fill("/unsaved/source");
  const beforeForeground = checks;
  const loadedReads = reads;
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => checks).toBeGreaterThan(beforeForeground);
  expect(reads).toBe(loadedReads);
  await expect(page.getByLabel("Source audio directory", { exact: true })).toHaveValue("/unsaved/source");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Session expired", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Username", { exact: true })).toHaveValue("admin");
  await dialog.getByLabel("Password", { exact: true }).fill("test-admin-password");
  rejectSave = false;
  await dialog.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("#main-content")).toBeFocused();
  await expect(page.getByLabel("Source audio directory", { exact: true })).toHaveValue("/unsaved/source");
  expect(saves).toBe(1);
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Settings saved");
  expect(saves).toBe(2);
});

test("reauthentication retries a non-polling Settings resource that has never loaded", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  let authenticated = true;
  let reads = 0;
  await page.route("**/api/auth/me", route => route.fulfill({ json: { ...me, authenticated } }));
  await page.route("**/api/auth/login", route => { authenticated = true; return route.fulfill({ json: { ok: true } }); });
  await page.route("**/api/settings", route => {
    reads++;
    if (reads === 1) { authenticated = false; return route.fulfill({ status: 401, json: { error: "login required" } }); }
    return route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } });
  });
  await page.goto("/settings");
  const dialog = page.getByRole("dialog", { name: "Session expired", exact: true });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Password", { exact: true }).fill("test-admin-password");
  await dialog.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(page.getByLabel("Source audio directory", { exact: true })).toHaveValue(settings.source);
  expect(reads).toBe(2);
});

test("a changed browser session refreshes CSRF without automatically replaying a mutation", async ({ page }) => {
  await mockLibrary(page);
  let csrf = "test";
  let writes = 0;
  await page.route("**/api/auth/me", route => route.fulfill({ json: { ...me, csrf } }));
  await page.route("**/api/library/scan", route => {
    writes++;
    if (route.request().headers()["x-csrf-token"] !== csrf) return route.fulfill({ status: 403, json: { error: "invalid request origin or CSRF token" } });
    return route.fulfill({ status: 202, json: { job_id: 1 } });
  });
  await page.goto("/library");
  await expect(page.getByRole("heading", { name: "Library", exact: true })).toBeVisible();
  csrf = "renewed-in-another-tab";
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Session changed; please retry this action");
  expect(writes).toBe(1);
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Scan");
  expect(writes).toBe(2);
});

test("failed action diagnostics persist until dismissal while successful notices expire", async ({ page }) => {
  await mockLibrary(page);
  await page.clock.install();
  let fail = true;
  await page.route("**/api/library/scan", route => fail
    ? route.fulfill({ status: 500, headers: { "X-Request-ID": "ci-persistent-error" }, json: { error: "Synthetic scan failure" } })
    : route.fulfill({ status: 202, json: { job_id: 1 } }));
  await page.goto("/library");
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  const error = page.getByRole("alert");
  await expect(error).toContainText("Synthetic scan failure");
  await expect(error).toContainText("ci-persistent-error");
  await page.clock.fastForward(6500);
  await expect(error).toBeVisible();
  await error.getByRole("button", { name: "Dismiss notification", exact: true }).click();
  await expect(error).toHaveCount(0);
  fail = false;
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Scan");
  await page.clock.fastForward(6500);
  await expect(page.getByRole("status")).toHaveCount(0);
});

test("protected password changes recover authentication without replaying a write", async ({ page }) => {
  await mockLibrary(page);
  let authenticated = true;
  let writes = 0;
  await page.route("**/api/auth/me", route => route.fulfill({ json: { ...me, authenticated } }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  await page.route("**/api/auth/password", route => {
    writes++; authenticated = false;
    return route.fulfill({ status: 401, json: { error: "login required" } });
  });
  await page.goto("/settings");
  await page.getByRole("tab", { name: "Account & security", exact: true }).click();
  await page.getByLabel("Current password", { exact: true }).fill("test-admin-password");
  await page.getByLabel("New password", { exact: true }).fill("new-admin-password");
  await page.getByRole("button", { name: "Update password", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Session expired", exact: true })).toBeVisible();
  expect(writes).toBe(1);
});

test("system appearance follows the OS and reduced motion disables the toast entrance", async ({ page }) => {
  await mockLibrary(page);
  await page.route("**/api/library/scan", route => route.fulfill({ status: 202, json: { job_id: 1 } }));
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await page.goto("/library");
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("system");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Scan");
  expect(await page.locator(".toast").evaluate(node => getComputedStyle(node).animationName)).toBe("none");
  let transitions = 0;
  await page.exposeFunction("recordTransition", () => { transitions++; });
  await page.evaluate(() => {
    const start = document.startViewTransition.bind(document);
    document.startViewTransition = (...args: Parameters<typeof start>) => {
      void (window as unknown as { recordTransition: () => Promise<void> }).recordTransition();
      return start(...args);
    };
  });
  await page.getByRole("button", { name: "Browse artist River Ensemble", exact: true }).click();
  await page.getByRole("button", { name: "Browse album Night Studies", exact: true }).click();
  await expect(page.getByRole("table")).toBeVisible();
  expect(transitions).toBe(0);
});

test("cover density and list preferences persist, pagination and polling retain artwork and selection", async ({ page }) => {
  await mockLibrary(page, collection);
  await page.setViewportSize({ width: 1440, height: 1000 });
  let reads = 0;
  await page.route("**/api/library", route => { reads++; return route.fulfill({ json: collection }); });
  await page.goto("/library?view=albums");
  await expect(page.locator(".library-group")).toHaveCount(50);
  const columns = () => page.locator(".library-groups").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  expect(await columns()).toBe(5);
  await page.getByLabel("Cover size", { exact: true }).selectOption("compact");
  expect(await columns()).toBe(7);
  await page.getByLabel("Cover size", { exact: true }).selectOption("large");
  expect(await columns()).toBe(4);
  await page.reload();
  await expect(page.getByLabel("Cover size", { exact: true })).toHaveValue("large");
  await page.getByLabel("Select album Night Studies", { exact: true }).check();
  const image = page.locator(".cover img").first();
  await expect(image).toHaveClass("cover-loaded");
  await image.evaluate(node => { node.setAttribute("data-poll-identity", "retained"); });
  const loadedReads = reads;
  await expect.poll(() => reads).toBeGreaterThan(loadedReads);
  await expect(image).toHaveAttribute("data-poll-identity", "retained");
  await expect(image).toHaveClass("cover-loaded");
  await expect(page.getByLabel("Select album Night Studies", { exact: true })).toBeChecked();
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(page.locator(".library-group")).toHaveCount(17);
  await page.getByRole("button", { name: "Previous page", exact: true }).click();
  await expect(page.getByLabel("Select album Night Studies", { exact: true })).toBeChecked();
  await page.getByRole("button", { name: "List view", exact: true }).click();
  await page.reload();
  await expect(page.getByRole("button", { name: "List view", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(await columns()).toBe(1);
});

test("album artwork continuity is progressive and keyboard navigation remains focused", async ({ page }) => {
  await mockLibrary(page);
  let transitions = 0;
  await page.exposeFunction("recordTransition", () => { transitions++; });
  await page.goto("/library?view=albums");
  await page.evaluate(() => {
    const start = document.startViewTransition.bind(document);
    document.startViewTransition = (...args: Parameters<typeof start>) => {
      void (window as unknown as { recordTransition: () => Promise<void> }).recordTransition();
      return start(...args);
    };
  });
  const open = page.getByRole("button", { name: "Browse album Night Studies", exact: true });
  await open.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".album-summary")).toBeVisible();
  await expect(page.locator("#library-title")).toBeFocused();
  await expect.poll(() => transitions).toBe(1);
  await page.getByRole("button", { name: "Back to albums", exact: true }).click();
  await expect(page.getByRole("button", { name: "Browse album Night Studies", exact: true })).toBeVisible();
  await page.evaluate(() => { Object.defineProperty(document, "startViewTransition", { configurable: true, value: undefined }); });
  await page.getByRole("button", { name: "Browse album Night Studies", exact: true }).click();
  await expect(page.getByRole("table")).toBeVisible();
  expect(transitions).toBe(1);
});

test("narrow-screen filter and display menu stays usable and inside the viewport", async ({ page }) => {
  await mockLibrary(page, collection);
  await page.setViewportSize({ width: 320, height: 844 });
  await page.goto("/library?view=albums");
  const summary = page.locator(".library-options > summary");
  await expect(summary).toBeVisible();
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel("Cover size", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Sort library", { exact: true })).toBeVisible();
  const menu = await page.locator(".library-options-body").boundingBox();
  expect(menu!.x).toBeGreaterThanOrEqual(0);
  expect(menu!.x + menu!.width).toBeLessThanOrEqual(320);
  await page.getByLabel("Sort library", { exact: true }).selectOption("attention");
  await expect(page.locator(".library-options")).not.toHaveAttribute("open", "");
  await summary.click();
  await page.getByLabel("Filter by status", { exact: true }).selectOption("failed");
  await expect(page.locator(".library-group")).toHaveCount(1);
  await expect(page.locator(".library-options")).toHaveAttribute("data-filtered", "true");
  for (const state of ["all", "failed", "all"]) {
    await summary.click();
    await page.getByLabel("Filter by status", { exact: true }).selectOption(state);
    await expect(page.locator(".library-group")).toHaveCount(state === "failed" ? 1 : 50);
    await expect(page.locator(".library-options")).not.toHaveAttribute("open", "");
  }
  await summary.click();
  await page.getByLabel("Sort library", { exact: true }).focus();
  await page.keyboard.press("Escape");
  await expect(summary).toBeFocused();
  await expect(page.locator(".library-options")).not.toHaveAttribute("open", "");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(summary).toHaveAttribute("aria-label", "筛选与显示选项");
});

test("Navidrome waiting is a localized non-error state", async ({ page }) => {
  await mockLibrary(page);
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [{
    id: 1, kind: "refresh", state: "pending", attempts: 0, progress: 0, created: 1800000000, updated: 1800000000, args: {},
    counts: { total: 0, done: 0, failed: 0, pending: 0, running: 0, cancelled: 0 }, current: [], scope: [], can_delete: false,
    waiting_reason: "Waiting for Navidrome scan completion", retry_at: 1800000060,
  }], total: 1 } }));
  await page.route("**/api/repair-tasks?*", route => route.fulfill({ json: { tasks: [], total: 0 } }));
  await page.goto("/jobs");
  await expect(page.locator(".task-waiting")).toContainText("Waiting for Navidrome scan completion");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator(".task-waiting")).toContainText("等待 Navidrome 完成扫描");
});

for (const width of [1920, 1440, 768, 390, 320]) {
  test(`library theme, responsive layout and screenshot evidence at ${width}px`, async ({ page }) => {
    await mockLibrary(page, collection);
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    await page.setViewportSize({ width, height: width > 800 ? 1000 : 844 });
    await page.goto("/library?view=albums");
    await expect(page).toHaveTitle(/MusicForge/);
    await expect(page.getByRole("heading", { name: "Library", exact: true })).toBeVisible();
    await expect(page.locator(".library-group")).toHaveCount(50);
    await expect.poll(() => page.locator(".cover img").first().evaluate((node: HTMLImageElement) => node.naturalWidth)).toBeGreaterThan(0);
    if (width <= 800) {
      const name = await page.getByRole("button", { name: "Field Notes", exact: true }).boundingBox();
      const navigation = width <= 600 ? await page.locator(".sidebar").boundingBox() : null;
      expect(name!.y + name!.height).toBeLessThan(navigation?.y ?? 844);
    }
    for (const theme of ["light", "dark"]) {
      await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption(theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      await page.screenshot({ path: `test-results/library-${width}-${theme}.png`, animations: "disabled" });
    }
    await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
    await expect(page.getByRole("heading", { name: "音乐库", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.screenshot({ path: `test-results/library-${width}-dark-zh.png`, animations: "disabled" });
    await page.getByRole("combobox", { name: "语言", exact: true }).selectOption("en");
    await page.getByRole("button", { name: "List view", exact: true }).click();
    expect(await page.locator(".library-groups").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length)).toBe(1);
    await page.screenshot({ path: `test-results/library-list-${width}-dark.png`, animations: "disabled" });
    await page.getByRole("button", { name: "Grid view", exact: true }).click();
    await page.getByRole("button", { name: "Browse album Night Studies", exact: true }).click();
    await expect(page.getByRole("table")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.screenshot({ path: `test-results/album-${width}-dark.png`, fullPage: true, animations: "disabled" });
    await page.goto("/library");
    await expect(page.getByRole("button", { name: "Browse artist River Ensemble", exact: true })).toBeVisible();
    await expect(page.locator(".cover-mosaic").first().locator("img.cover-loaded")).toHaveCount(4);
    await page.screenshot({ path: `test-results/artists-${width}-dark.png`, animations: "disabled" });
    expect(errors).toEqual([]);
  });
}
