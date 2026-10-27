import { test, expect, type Page } from "@playwright/test";
import { openAccount, openLibraryOptions, setPreference } from "./preferences";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
async function setup(page: Page) {
  await page.addInitScript(() => { if (!localStorage.getItem("musicforge.language")) localStorage.setItem("musicforge.language", "en"); if (!localStorage.getItem("musicforge.theme")) localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [1, 2].map(id => ({ id, path: `Artist/Album/${id}.flac`, title: `Song ${id}`, artist: "Artist", album: "Album", track: id, disc: 1, duration: 201, present: true, output: `Artist/Album/${id}.opus`, output_present: true, status: "ready", error: "", size: 26214400, metadata: {}, source_bitrate: 894000, source_audio: { codec: "flac", bit_depth: 24, sample_rate: 96000, channels: 2 }, output_size: 5242880 })) }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
}
async function colors(page: Page) {
  const root = page.locator(".page-heading");
  const preferences = root.locator(".heading-preferences");
  if (await preferences.count() && !await preferences.locator(".appearance-controls").isVisible()) await preferences.locator(":scope > summary").click();
  const menu = root.locator(".palette-menu");
  if (!await menu.evaluate(element => (element as HTMLDetailsElement).open)) await menu.locator("summary").click();
  return menu;
}
async function readable(page: Page) {
  const ratios = await page.evaluate(() => {
    function luminance(color: string) {
      const digits = color.slice(1);
      const full = digits.length === 3 ? [...digits].map(value => value + value).join("") : digits;
      const values = color.startsWith("#") ? [0, 2, 4].map(index => parseInt(full.slice(index, index + 2), 16)) : color.match(/[\d.]+/g)!.slice(0, 3).map(value => Number(value) * (color.startsWith("color(srgb ") ? 255 : 1));
      const [r, g, b] = values.map(value => value / 255 <= .04045 ? value / 255 / 12.92 : ((value / 255 + .055) / 1.055) ** 2.4);
      return .2126 * r + .7152 * g + .0722 * b;
    }
    function contrast(a: string, b: string) { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05); }
    const style = getComputedStyle(document.documentElement);
    const value = (name: string) => style.getPropertyValue(name).trim();
    const selected = getComputedStyle(document.querySelector('.page-heading .palette-presets button[aria-pressed="true"]')!);
    return [contrast(value("--accent-fill"), value("--accent-ink")), contrast(value("--accent"), value("--canvas")), contrast(value("--accent"), value("--surface")), contrast(selected.color, selected.backgroundColor)];
  });
  for (const ratio of ratios) expect(ratio).toBeGreaterThanOrEqual(4.5);
}

test("default albums, compact headings and the text account link retain settings drafts", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/library");
  await expect(page.getByRole("button", { name: /^Albums/ })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".library-group")).toHaveCount(1);
  await expect(page.locator("html")).toHaveAttribute("data-palette", "apple");
  await expect(page.locator(".palette-menu > summary > svg")).toHaveCount(1);
  await expect(page.locator(".palette-menu > summary .color-swatch")).toHaveCount(0);
  const header = await page.locator(".page-heading").boundingBox();
  const heading = await page.locator(".heading-content").boundingBox();
  const trigger = page.locator(".heading-preferences > summary");
  const control = await trigger.boundingBox();
  expect(header!.x).toBe(236);
  expect(header!.width).toBe(1440 - 236);
  expect(header!.height).toBeLessThanOrEqual(82);
  expect(Math.abs(heading!.y + heading!.height / 2 - control!.y - control!.height / 2)).toBeLessThan(2);
  const toolbar = await page.locator(".library-toolbar-box").boundingBox();
  expect(toolbar!.y - header!.height).toBe(24);
  const search = await page.locator(".library-toolbar-box > .search").boundingBox();
  expect(search!.width).toBeGreaterThan(toolbar!.width - 36);
  await expect(page.locator(".library-group").first()).toBeInViewport();
  await trigger.click();
  expect((await page.locator(".page-heading").boundingBox())!.height).toBe(header!.height);
  await trigger.click();
  await expect(page.locator(".account-link svg, .sidebar .appearance-controls")).toHaveCount(0);
  await expect(page.locator(".brand-version")).toHaveText("test");
  await page.screenshot({ path: "test-results/appearance-default-albums.png", animations: "disabled" });
  const reference = await page.context().newPage();
  const design = await readFile(resolve("../docs/ui-refinement-preview.html"), "utf8");
  for (const width of [1440, 390]) {
    await reference.setViewportSize({ width, height: 1000 });
    await reference.setContent(design);
    // Capture the current design at its actual width, without the preview controls.
    await reference.addStyleTag({ content: `#inspector-bar { display: none; } #preview-container { padding: 0; } #preview-frame { border: 0; }` });
    await reference.evaluate(`setVp('${width === 390 ? "mobile" : "desktop"}'); setTheme('dark')`);
    for (const screen of ["overview", "library", "settings", "jobs", "lyrics", "compare"]) {
      await reference.evaluate(`setPage('${screen}')`);
      await reference.locator("#preview-frame").screenshot({ path: `test-results/design-flat-${screen}-${width}.png`, animations: "disabled" });
      if (screen === "library") {
        await reference.evaluate("setTheme('light')");
        await reference.locator("#preview-frame").screenshot({ path: `test-results/design-flat-library-${width}-light.png`, animations: "disabled" });
        await reference.evaluate("setTheme('dark')");
      }
    }
  }
  await reference.close();
  const shortcuts = page.getByRole("navigation", { name: "Workspace shortcuts", exact: true });
  await expect(shortcuts.getByRole("link")).toHaveCount(7);
  for (const [label, view] of [["All songs", "tracks"], ["All artists", "artists"], ["Albums", "albums"]]) {
    await shortcuts.getByRole("link", { name: label, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`view=${view}`));
    await expect(page.getByRole("button", { name: new RegExp(`^${label}`) })).toHaveAttribute("aria-pressed", "true");
  }
  await shortcuts.getByRole("link", { name: "Review lyrics", exact: true }).click();
  await expect(page).toHaveURL(/view=tracks&lyrics=needs_review/);
  await openAccount(page);
  await expect(page).toHaveURL(/section=security/);
  await expect(page.locator(".account-summary")).toContainText("admin");
  await expect(page.getByLabel("Current password", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Library", exact: true }).click();
  await page.getByLabel("Source audio directory", { exact: true }).fill("/keep-this-draft");
  for (const [label, section, tab] of [["Encoding profile", "encoding", "Encoding"], ["Lyrics settings", "lyrics", "Lyrics"], ["Integrations", "integrations", "Integrations"]]) {
    await shortcuts.getByRole("link", { name: label, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`section=${section}`));
    await expect(page.getByRole("tab", { name: tab, exact: true })).toHaveAttribute("aria-selected", "true");
  }
  await openAccount(page);
  await page.getByRole("tab", { name: "Library", exact: true }).click();
  await expect(page.getByLabel("Source audio directory", { exact: true })).toHaveValue("/keep-this-draft");
  await page.getByRole("button", { name: "Collapse sidebar", exact: true }).click();
  await expect(page.locator(".account-link")).toBeVisible();
  await expect(page.locator(".account-initial")).toHaveText("A");
  await openAccount(page);
  await page.goBack();
  await page.getByRole("tab", { name: "Library", exact: true }).click();
  await expect(page.getByLabel("Source audio directory", { exact: true })).toHaveValue("/keep-this-draft");
});

test("presets and custom colors persist, retain contrast and stay synchronized while song details are expanded", async ({ page, context }) => {
  await setup(page);
  await page.goto("/library?view=tracks");
  for (const mode of ["light", "dark"]) {
    await setPreference(page, "theme", mode);
    const menu = await colors(page);
    for (const [name, value] of [["Spotify", "spotify"], ["Violet", "violet"], ["Amber", "amber"], ["Apple Music", "apple"]]) {
      await menu.getByRole("button", { name, exact: true }).click();
      await expect(page.locator("html")).toHaveAttribute("data-palette", value);
      await readable(page);
    }
    await menu.getByRole("button", { name: "Custom", exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("data-palette", "custom");
    await menu.getByLabel("Color hex code", { exact: true }).fill("#12ab7e");
    await expect(page.locator("html")).toHaveAttribute("data-palette", "custom");
    await readable(page);
    const saved = await page.evaluate(() => document.documentElement.style.getPropertyValue("--accent-fill"));
    await menu.getByLabel("Color hex code", { exact: true }).fill("#zzzzzz");
    await expect(menu.getByRole("alert")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.style.getPropertyValue("--accent-fill"))).toBe(saved);
    for (const value of ["#ffffff", "#000000", "#7c7c7c", "#12ab7e"]) { await menu.getByLabel("Color hex code", { exact: true }).fill(value); await readable(page); }
    await page.keyboard.press("Escape");
    await expect(menu.locator("summary")).toBeFocused();
  }
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-palette", "custom");
  expect(await page.evaluate(() => localStorage.getItem("musicforge.custom-color"))).toBe("#12ab7e");
  await page.locator(".track-open").first().click();
  const panel = page.locator(".inline-details");
  await expect(panel).toHaveAccessibleName("Song details");
  const panelMenu = await colors(page);
  await panelMenu.getByRole("button", { name: "Spotify", exact: true }).click();
  await page.keyboard.press("Escape");
  await expect(panel).toBeVisible();
  await expect(panelMenu.locator("summary")).toBeFocused();
  await setPreference(page, "language", "zh-CN");
  await expect(panel).toHaveAccessibleName("歌曲详情");
  await expect(page.locator(".page-heading .palette-label")).toHaveText("Spotify");
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(page.locator(".page-heading .palette-label")).toHaveText("Spotify");
  const second = await context.newPage();
  await setup(second);
  await second.goto("/library");
  await expect(second.locator("html")).toHaveAttribute("data-palette", "spotify");
  const menu = await colors(second);
  await menu.getByRole("button", { name: "Apple Music", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-palette", "apple");
  await setPreference(second, "theme", "system");
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await readable(page);
  await second.close();
});

test("checkbox, partial selection, hover, focus, switch and disabled states share the compact controls", async ({ page }) => {
  await setup(page);
  await page.goto("/library?view=tracks");
  const first = page.getByLabel("Select Song 1", { exact: true });
  await openLibraryOptions(page);
  const all = page.getByLabel("Select current page", { exact: true });
  expect(await first.evaluate(element => getComputedStyle(element).appearance)).toBe("none");
  await first.check();
  await expect(page.locator(".track-table tr.selected")).toHaveCount(1);
  expect(await all.evaluate(element => (element as HTMLInputElement).indeterminate)).toBe(true);
  const shape = await all.evaluate(element => getComputedStyle(element, "::after").height);
  expect(shape).toBe("2px");
  await page.locator(".library-options > summary").press("Escape");
  await page.keyboard.press("Tab");
  await first.focus();
  expect(await first.evaluate(element => getComputedStyle(element).outlineStyle)).toBe("solid");
  const scan = page.getByRole("button", { name: "Scan", exact: true });
  const normal = await scan.evaluate(element => getComputedStyle(element).backgroundColor);
  await scan.hover();
  await expect.poll(() => scan.evaluate(element => getComputedStyle(element).backgroundColor)).not.toBe(normal);
  await page.mouse.down();
  await expect.poll(() => scan.evaluate(element => getComputedStyle(element).transform)).not.toBe("none");
  await page.mouse.move(1, 1); await page.mouse.up();
  await page.screenshot({ path: "test-results/appearance-selected-controls.png", animations: "disabled" });
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  const save = page.getByRole("button", { name: "Save settings", exact: true });
  await expect(save).toBeDisabled();
  await save.hover();
  expect(Number(await save.evaluate(element => getComputedStyle(element).opacity))).toBeLessThan(.5);
  const toggle = page.getByRole("switch");
  const before = await toggle.evaluate(element => getComputedStyle(element, "::after").transform);
  await toggle.uncheck();
  await expect.poll(() => toggle.evaluate(element => getComputedStyle(element, "::after").transform)).not.toBe(before);
  await expect(save).toBeEnabled();
});

test("top-right controls and color menus fit narrow screens in either language and appearance", async ({ page }) => {
  await setup(page);
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/library");
    for (const locale of ["en", "zh-CN"]) {
      await setPreference(page, "language", locale);
      for (const mode of ["light", "dark"]) {
        await setPreference(page, "theme", mode);
        const menu = await colors(page);
        const box = await menu.locator(".palette-popover").boundingBox();
        expect(box!.x).toBeGreaterThanOrEqual(0);
        expect(box!.x + box!.width).toBeLessThanOrEqual(width);
        await page.screenshot({ path: `test-results/appearance-palette-${width}-${locale}-${mode}.png`, animations: "disabled" });
        await page.keyboard.press("Escape");
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        const controls = await page.locator(".page-heading .appearance-controls").boundingBox();
        const trigger = await page.locator(".heading-preferences > summary").boundingBox();
        expect(controls!.y).toBeGreaterThanOrEqual(trigger!.y + trigger!.height);
        expect(controls!.x + controls!.width).toBeGreaterThan(width - 24);
      }
    }
  }
});

test("approved settings grid and category tabs match at desktop and phone widths", async ({ page, context }) => {
  await setup(page);
  const reference = await context.newPage();
  const design = await readFile(resolve("../docs/ui-refinement-preview.html"), "utf8");
  for (const [width, height, theme] of [[1440, 900, "light"], [390, 844, "dark"]] as const) {
    await page.setViewportSize({ width, height });
    await page.goto("/settings?section=encoding");
    await setPreference(page, "language", "zh-CN");
    await setPreference(page, "theme", theme);
    await expect(page.locator("#settings-tab-encoding")).toHaveAttribute("aria-selected", "true");
    await expect(page.locator(".settings-nav")).toHaveAttribute("aria-orientation", width > 800 ? "vertical" : "horizontal");
    await reference.setViewportSize({ width, height });
    await reference.setContent(design);
    await reference.addStyleTag({ content: "#inspector-bar { display:none } #preview-container { padding:0 } #preview-frame { border:0 }" });
    await reference.evaluate(({ theme, width }) => { (window as any).setTheme(theme); (window as any).setVp(width === 390 ? "mobile" : "desktop"); (window as any).setPage("settings"); }, { theme, width });
    for (const [actual, approved] of [[".settings-layout", ".settings-layout"], [".settings-nav", ".settings-tabs"], [".settings-content", ".settings-layout > section"]]) {
      const a = (await page.locator(actual).boundingBox())!, b = (await reference.locator(approved).boundingBox())!;
      expect(Math.abs(a.x - b.x), `${actual} x at ${width}`).toBeLessThanOrEqual(1);
      expect(Math.abs(a.width - b.width), `${actual} width at ${width}`).toBeLessThanOrEqual(1);
    }
    const grid = await page.locator('[data-settings-section="encoding"] .form-grid').evaluate(node => { const s = getComputedStyle(node); return [s.gridTemplateColumns, s.gap]; });
    const approvedGrid = await reference.locator(".form-grid").evaluate(node => { const s = getComputedStyle(node); return [s.gridTemplateColumns, s.gap]; });
    expect(grid).toEqual(approvedGrid);
    await page.screenshot({ path: `test-results/approved-actual-settings-${width}-${theme}.png`, animations: "disabled" });
    await reference.locator("#preview-frame").screenshot({ path: `test-results/approved-reference-settings-${width}-${theme}.png`, animations: "disabled" });
  }
  await reference.close();
});
