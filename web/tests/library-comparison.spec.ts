import { test, expect } from "@playwright/test";

test("file comparison explains count differences and queues only selected directories", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [{
    id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, duration: 120,
    present: true, output: "", output_present: false, status: "failed", error: "Synthetic decoder failure", size: 100, metadata: {},
  }] }));
  let connected = false;
  let state = "idle";
  let checks = 0;
  let selectedDirs: string[] = [];
  await page.route("**/api/lidarr/connection", route => {
    if (route.request().method() === "PUT") {
      expect(route.request().postDataJSON()).toEqual({ url: "http://lidarr:8686", api_key: "test-only-key" });
      connected = true;
      return route.fulfill({ json: { saved: true } });
    }
    return route.fulfill({ json: { url: connected ? "http://lidarr:8686" : "", key_configured: connected } });
  });
  await page.route("**/api/lidarr/check", route => {
    if (route.request().method() === "POST") { checks++; state = "complete"; }
    return route.fulfill({ json: {
      state, started: 1800000000, finished: 1800000001, processed: 2, total: 2, source: "/music/source", prefix: "/music",
      lidarr_count: 3, source_count: 1, matched: 1, only_lidarr: 2, only_musicforge: 0, unmapped: 0, outside_scope: 8,
      differences: state === "complete" ? [
        { path: "Artist/Album/01.flac", kind: "failed", source_id: 1, error: "Synthetic decoder failure" },
        { path: "Artist/Album/02.flac", kind: "not_indexed", scan_dir: "Artist/Album" },
        { path: "Artist/Album/03.flac", kind: "expired", scan_dir: "Artist/Album" },
      ] : [],
    } });
  });
  await page.route("**/api/library/scan", route => {
    selectedDirs = route.request().postDataJSON().dirs;
    return route.fulfill({ json: { job_id: 7 } });
  });
  await page.goto("/library");
  await page.getByText("File counts · compare with Lidarr", { exact: true }).click();
  const compare = page.getByRole("button", { name: "Compare file lists", exact: true });
  await expect(compare).toBeDisabled();
  await page.getByLabel("Lidarr URL", { exact: true }).fill("http://lidarr:8686");
  await page.getByLabel("Lidarr API key", { exact: true }).fill("test-only-key");
  await page.getByRole("button", { name: "Save connection", exact: true }).click();
  await expect(page.getByLabel("Lidarr API key", { exact: true })).toHaveValue("");
  await expect(compare).toBeEnabled();
  await compare.click();
  await expect(page.getByText("Some Lidarr paths are outside", { exact: false })).toBeVisible();
  await expect(page.getByText("This is an index snapshot", { exact: false })).toBeVisible();
  await expect(page.locator(".comparison-results > li")).toHaveCount(3);
  await page.getByLabel("Select re-scannable files on this page", { exact: true }).check();
  await page.getByRole("button", { name: "Re-scan selected directories (2 files)", exact: true }).click();
  expect(selectedDirs).toEqual(["Artist/Album"]);
  expect(checks).toBe(1);
  await page.getByLabel("Difference type", { exact: true }).selectOption("failed");
  await expect(page.locator(".comparison-results > li")).toHaveCount(1);
  await expect(page.locator(".comparison-results")).toContainText("not a source-count difference");
  await page.getByLabel("Language", { exact: true }).selectOption("zh-CN");
  await expect(page.getByRole("button", { name: "核对文件清单", exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
