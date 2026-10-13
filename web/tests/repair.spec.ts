import { test, expect } from "@playwright/test";

test("best-effort repair requires consent, remains possibly damaged and preserves the old output", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const source = {
    id: 77, hash: "a".repeat(64), path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Dear My Friend",
    track: 1, disc: 1, duration: 120, size: 28363652, metadata: {}, present: true,
    output: "Artist/Album/01.opus", output_present: true, status: "failed", error: "Audio decoding failed\nMD5 mismatch", can_repair: true,
  };
  let repair: Record<string, unknown> | undefined;
  let starts = 0;
  let failStart = false;
  let removes = 0;
  await page.route("**/api/library", route => route.fulfill({ json: [{ ...source, repair }] }));
  await page.route("**/api/library/77/repair", route => {
    if (route.request().method() === "DELETE") {
      removes++; repair = undefined;
      return route.fulfill({ json: { removed: true } });
    }
    starts++;
    expect(route.request().postDataJSON()).toEqual({ hash: source.hash });
    if (failStart) return route.fulfill({ status: 409, json: { error: "A repair preview is already running" } });
    repair = { state: "running", pcm_verified: false };
    return route.fulfill({ status: 202, json: repair });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  const button = page.getByRole("button", { name: "Best-effort repair", exact: true });
  await expect(button).toBeVisible();
  page.once("dialog", async dialog => {
    expect(dialog.message()).toContain("missing sections");
    expect(dialog.message()).toContain("will not be replaced");
    await dialog.dismiss();
  });
  await button.click();
  expect(starts).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await button.click();
  await expect(page.getByText("Repairing preview", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel repair preview", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel repair preview", exact: true }).click();
  await expect(button).toBeVisible();
  expect(removes).toBe(1);

  failStart = true;
  page.once("dialog", dialog => dialog.accept());
  await button.click();
  await expect(page.getByRole("alert")).toContainText("already running");
  failStart = false;
  repair = { state: "ready", pcm_verified: false, filename: "01.possibly-damaged.opus", download_url: "/api/library/77/repair/download", decoded_samples: 100, declared_samples: 110, diagnostics: "PCM MD5 mismatch" };
  await page.reload();
  await expect(page.getByText("Possibly damaged", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Download preview", exact: true })).toHaveAttribute("href", "/api/library/77/repair/download");
  await expect(page.getByText("Existing artifact is still playable", { exact: true })).toBeVisible();
  await expect(page.locator(".badge-failed")).toBeVisible();
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.getByText("可能损坏", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "下载试听副本", exact: true })).toBeVisible();
  await page.getByRole("combobox", { name: "外观", exact: true }).selectOption("dark");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: "test-results/repair-390-dark-zh.png", fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("combobox", { name: "语言", exact: true }).selectOption("en");
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
  await page.screenshot({ path: "test-results/repair-1440-light.png", fullPage: true });
  await page.getByRole("button", { name: "Remove repair preview", exact: true }).click();
  await expect(button).toBeVisible();
  await expect(page.getByText("Possibly damaged", { exact: true })).toHaveCount(0);
});
