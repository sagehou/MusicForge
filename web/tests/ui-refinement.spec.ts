import { setPreference, openAccount } from "./preferences";
import { test, expect, type Page } from "@playwright/test";

const me = { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" };
const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const response = { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] };
async function setup(page: Page) {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: me }));
  await page.route("**/api/settings", route => route.fulfill({ json: response }));
}

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

test("settings sections preserve drafts, reveal invalid controls, discard and save together", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  const writes: Record<string, unknown>[] = [];
  await page.route("**/api/settings", route => {
    if (route.request().method() === "PUT") { writes.push(route.request().postDataJSON()); return route.fulfill({ json: { encoding_changed: false } }); }
    return route.fulfill({ json: response });
  });
  await page.goto("/settings");
  const saveBar = page.locator('.settings-save:visible');
  async function expectSaveActionsReachable() {
    await expect(saveBar).toBeInViewport({ ratio: 1 });
    for (const button of await saveBar.getByRole("button").all()) {
      expect(await button.evaluate(element => {
        const box = element.getBoundingClientRect();
        return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2));
      })).toBe(true);
    }
  }
  await page.getByLabel("Source audio directory", { exact: true }).fill("");
  await expectSaveActionsReachable();
  await page.getByRole("tab", { name: "Encoding", exact: true }).click();
  await page.getByLabel("Concurrent conversions", { exact: true }).fill("17");
  await page.getByRole("tab", { name: "Account & security", exact: true }).click();
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Library", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Source audio directory", { exact: true })).toBeFocused();
  expect(writes).toHaveLength(0);
  await page.getByLabel("Source audio directory", { exact: true }).fill("/draft/source");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect(page.getByLabel("Concurrent conversions", { exact: true })).toBeFocused();
  await page.getByLabel("Concurrent conversions", { exact: true }).fill("2");
  await page.setViewportSize({ width: 390, height: 568 });
  await page.getByRole("tab", { name: "Integrations", exact: true }).click();
  await page.getByLabel("Independent webhook secret", { exact: true }).fill("draft-webhook-secret-value");
  await page.evaluate(() => window.scrollTo(0, 0));
  await expectSaveActionsReachable();
  const dock = (await page.locator('.sidebar').boundingBox())!;
  const strip = (await saveBar.boundingBox())!;
  expect(strip.y + strip.height).toBeLessThanOrEqual(dock.y + 1);
  for (const button of await saveBar.getByRole("button").all()) expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(40);
  await page.screenshot({ path: "test-results/settings-dirty-390-short-light-en.png", animations: "disabled" });
  await setPreference(page, "language", "zh-CN");
  await page.getByRole("tab", { name: "音乐库", exact: true }).click();
  await expect(page.getByLabel("音频源目录", { exact: true })).toHaveValue("/draft/source");
  await expectSaveActionsReachable();
  await page.getByRole("button", { name: "放弃修改", exact: true }).click();
  await expect(page.getByLabel("音频源目录", { exact: true })).toHaveValue(settings.source);
  await expect(page.getByRole("button", { name: "保存设置", exact: true })).toBeDisabled();
  await page.getByRole("tab", { name: "集成", exact: true }).click();
  await expect(page.getByLabel("Webhook 独立密钥", { exact: true })).toHaveValue("");
  await setPreference(page, "language", "en");
  await page.getByRole("tab", { name: "Encoding", exact: true }).focus();
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Library", exact: true })).toBeFocused();
  await page.getByLabel("Source audio directory", { exact: true }).fill("/saved/source");
  await page.keyboard.press("Tab");
  await page.getByRole("tab", { name: "Encoding", exact: true }).click();
  await page.getByLabel("Concurrent conversions", { exact: true }).fill("2");
  await page.getByRole("tab", { name: "Integrations", exact: true }).click();
  await page.evaluate(() => window.scrollTo(0, 0));
  await expectSaveActionsReachable();
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({ source: "/saved/source", concurrency: 2, webhook_secret: "", clear_nav_password: false, clear_oidc_secret: false });
  await expect(saveBar.getByRole("status")).toHaveText("Settings saved");
  await expectSaveActionsReachable();
  await page.screenshot({ path: "test-results/settings-saved-390-short-light-en.png", animations: "disabled" });
  const dismiss = saveBar.getByRole("button", { name: "Dismiss notification", exact: true });
  expect((await dismiss.boundingBox())!.height).toBeGreaterThanOrEqual(40);
  await dismiss.click();
  await expect(saveBar.getByRole("status")).toHaveCount(0);
  expect(writes).toHaveLength(1);
});

test("settings layouts in both themes and narrow viewports", async ({ page }) => {
  await setup(page);
  await page.goto("/settings");
  await setPreference(page, "language", "zh-CN");
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    for (const theme of ["light", "dark"]) {
      await setPreference(page, "theme", theme);
      for (const [id, label] of [["library", "音乐库"], ["encoding", "编码"], ["integrations", "集成"], ["security", "账户安全"]]) {
        await page.getByRole("tab", { name: label, exact: true }).click();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        if (width <= 600) {
          if (id === "library") {
            const sourceHint = page.locator('[data-settings-section="library"] .field > small').first();
            expect(await sourceHint.evaluate(element => element.getBoundingClientRect().height <= 2 * parseFloat(getComputedStyle(element).lineHeight) + 1)).toBe(true);
            await expect(sourceHint).toContainText("MKA");
          }
          for (const field of await page.locator('.settings-panel input:visible:not([type="checkbox"]), .settings-panel select:visible, .settings-panel textarea:visible').all()) {
            expect(await field.evaluate(element => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
          }
        }
        await page.screenshot({ path: `test-results/settings-${id}-${width}-${theme}.png`, fullPage: true, animations: "disabled" });
      }
    }
  }
});

test("large library returns instantly from memory, revalidates with 304 and clears on logout", async ({ page }) => {
  await setup(page);
  const sources = Array.from({ length: 5000 }, (_, index) => ({ id: index + 1, path: `Artist ${Math.floor(index / 100)}/Album ${Math.floor(index / 10)}/${index}.flac`, artist: `Artist ${Math.floor(index / 100)}`, album: `Album ${Math.floor(index / 10)}`, title: `Track ${index}`, track: index % 10 + 1, disc: 1, duration: 200, present: true, output: "", output_present: false, status: "missing", error: "", size: 100, metadata: {} }));
  let reads = 0, authenticated = true, fail = false, hold = true;
  let release: (() => void) | undefined;
  const conditionals: (string | undefined)[] = [];
  await page.route("**/api/auth/me", route => route.fulfill({ json: { ...me, authenticated } }));
  await page.route("**/api/auth/logout", route => { authenticated = false; return route.fulfill({ json: {} }); });
  await page.route("**/api/auth/login", route => { authenticated = true; return route.fulfill({ json: {} }); });
  await page.route("**/api/library/scan", route => {
    sources[0].status = "failed"; sources[0].error = "Synthetic changed status";
    return route.fulfill({ status: 202, json: { job_id: 1 } });
  });
  await page.route("**/api/library", async route => {
    reads++;
    const etag = route.request().headers()["if-none-match"];
    conditionals.push(etag);
    if (hold) await new Promise<void>(resolve => { release = resolve; });
    if (fail) await route.fulfill({ status: 503, json: { error: "Database is busy; please retry" } });
    else if (etag) await route.fulfill({ status: 304, headers: { ETag: 'W/"library-test"' } });
    else await route.fulfill({ json: sources, headers: { ETag: 'W/"library-test"' } });
  });
  await page.goto("/library?view=artists");
  await expect(page.getByRole("status", { name: "Loading" })).toBeVisible();
  await expect.poll(() => reads).toBe(1);
  await page.screenshot({ path: "test-results/library-skeleton.png", animations: "disabled" });
  hold = false; release!();
  await expect(page.locator(".library-group")).toHaveCount(50);
  await expect(page.locator(".library-comparison > summary").filter({ hasText: "File counts · compare with Lidarr" })).toBeInViewport();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect(page.getByLabel("Source audio directory", { exact: true })).toBeVisible();
  hold = true;
  await page.getByRole("link", { name: "Library", exact: true }).click();
  await expect.poll(() => reads).toBe(2);
  // Data is visible while the network response is still deliberately blocked.
  await page.getByRole("button", { name: /^All artists/ }).click();
  await expect(page.locator(".library-group")).toHaveCount(50);
  await expect(page.getByRole("status", { name: "Loading" })).toHaveCount(0);
  expect(conditionals[1]).toBe('W/"library-test"');
  fail = true; hold = false; release!();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.locator(".library-group")).toHaveCount(50);
  fail = false;
  await page.getByRole("button", { name: "Try again", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator(".library-group")).toHaveCount(50);
  expect(conditionals.at(-1)).toBe('W/"library-test"');
  await page.getByRole("button", { name: "Scan library", exact: true }).click();
  const changedBadge = page.locator(".library-group").filter({ has: page.getByRole("button", { name: "Browse artist Artist 0", exact: true }) }).locator(".badge");
  await expect(changedBadge.locator(".badge-label-full")).toHaveText("Failed");
  await expect(changedBadge.locator(".badge-label-short")).toBeVisible();
  await expect(changedBadge.locator(".badge-label-short")).toHaveText("Failed");
  expect(conditionals.at(-1)).toBeUndefined();
  await openAccount(page);
  await page.getByRole("button", { name: "Log out", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome back", exact: true })).toBeVisible();
  hold = true;
  const beforeLogin = reads;
  await page.getByLabel("Username", { exact: true }).fill("admin");
  await page.getByLabel("Password", { exact: true }).fill("test-password");
  await page.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(page.locator(".account-summary")).toBeVisible();
  await page.getByRole("link", { name: "Library", exact: true }).click();
  await expect(page.getByRole("status", { name: "Loading" })).toBeVisible();
  await expect(page.locator(".library-group")).toHaveCount(0);
  await expect.poll(() => reads).toBeGreaterThan(beforeLogin);
  expect(conditionals.at(-1)).toBeUndefined();
  hold = false; release!();
  await page.getByRole("button", { name: /^All artists/ }).click();
  await expect(page.locator(".library-group")).toHaveCount(50);
});
