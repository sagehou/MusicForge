import { test, expect } from "@playwright/test";

test("best-effort repair requires consent and imports normal audio with a persistent damage warning", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const source = {
    id: 77, hash: "a".repeat(64), path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Dear My Friend",
    track: 1, disc: 1, duration: 120, size: 28363652, metadata: {}, present: true,
    output: "", output_present: false, status: "failed", error: "Audio decoding failed\nMD5 mismatch", can_repair: true,
  };
  let repair: Record<string, unknown> | undefined;
  let starts = 0;
  let failStart = false;
  let removes = 0;
  let imported = false;
  await page.route("**/api/library", route => route.fulfill({ json: [{ ...source, repair, ...(imported ? {
    output: "Artist/Album/01.opus", output_present: true, status: "possibly_damaged", possibly_damaged: true, error: "", can_repair: false,
  } : {}) }] }));
  await page.route("**/api/library/77/repair", route => {
    if (route.request().method() === "DELETE") {
      removes++; repair = undefined;
      return route.fulfill({ json: { removed: true } });
    }
    starts++;
    expect(route.request().postDataJSON()).toEqual({ hash: source.hash });
    if (failStart) return route.fulfill({ status: 409, json: { error: "A best-effort repair is already running" } });
    repair = { state: "running", pcm_verified: false };
    return route.fulfill({ status: 202, json: repair });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  const button = page.getByRole("button", { name: "Best-effort repair", exact: true });
  await expect(button).toBeVisible();
  page.once("dialog", async dialog => {
    expect(dialog.message()).toContain("missing sections");
    expect(dialog.message()).toContain("normal output path");
    expect(dialog.message()).toContain("appear in Navidrome");
    expect(dialog.message()).toContain("existing outputs will not be replaced");
    await dialog.dismiss();
  });
  await button.click();
  expect(starts).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await button.click();
  await expect(page.getByText("Repairing and importing", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel best-effort repair", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel best-effort repair", exact: true }).click();
  await expect(button).toBeVisible();
  expect(removes).toBe(1);

  failStart = true;
  page.once("dialog", dialog => dialog.accept());
  await button.click();
  await expect(page.getByRole("alert")).toContainText("already running");
  failStart = false;
  repair = { state: "failed", pcm_verified: false, diagnostics: "No complete PCM samples" };
  await page.reload();
  await expect(page.getByText("Best-effort repair failed", { exact: true })).toBeVisible();
  await expect(button).toBeEnabled();
  imported = true;
  repair = { state: "imported", pcm_verified: false, decoded_samples: 100, declared_samples: 110, diagnostics: `PCM MD5 mismatch\nSource SHA-256: ${"f".repeat(64)}\nOriginal strict conversion failure: decode error` };
  await page.reload();
  await expect(page.locator(".badge-possibly_damaged")).toHaveText("Possibly damaged");
  await expect(button).toHaveCount(0);
  await expect(page.getByText("Imported into the output library", { exact: false })).toBeVisible();
  await page.locator(".track-file summary").click();
  await expect(page.getByText("Artist/Album/01.opus", { exact: true })).toBeVisible();
  await page.locator(".repair-diagnostic summary").click();
  await expect(page.locator(".repair-diagnostic pre")).toContainText("Original strict conversion failure");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator(".badge-possibly_damaged")).toHaveText("可能损坏");
  await expect(page.getByText("已导入输出库", { exact: false })).toBeVisible();
  await page.getByRole("combobox", { name: "外观", exact: true }).selectOption("dark");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(await page.locator(".repair-diagnostic").evaluate(element => element.getBoundingClientRect().width / element.closest("table")!.getBoundingClientRect().width)).toBeGreaterThan(0.8);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: "test-results/repair-import-390-dark-zh.png", fullPage: true, animations: "disabled" });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("combobox", { name: "语言", exact: true }).selectOption("en");
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
  expect(await page.locator(".repair-diagnostic").evaluate(element => element.getBoundingClientRect().width / element.closest("table")!.getBoundingClientRect().width)).toBeGreaterThan(0.8);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: "test-results/repair-import-1440-light.png", fullPage: true, animations: "disabled" });
  await page.locator(".library-options-body select").first().selectOption("possibly_damaged");
  await expect(page.locator(".badge-possibly_damaged")).toBeVisible();
  await page.reload();
  await expect(page.locator(".badge-possibly_damaged")).toBeVisible();
  await expect(page.getByRole("link", { name: "Download preview", exact: true })).toHaveCount(0);
});

test("an existing playable artifact is preserved without offering best-effort replacement", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [{
    id: 78, hash: "b".repeat(64), path: "Artist/Album/02.flac", artist: "Artist", album: "Album", title: "Existing track",
    track: 2, disc: 1, duration: 120, size: 1000, metadata: {}, present: true,
    output: "Artist/Album/02.opus", output_present: true, status: "failed", error: "Audio decoding failed", can_repair: false,
  }] }));
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  await expect(page.getByText("Existing artifact is still playable", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Best-effort repair", exact: true })).toHaveCount(0);
});
