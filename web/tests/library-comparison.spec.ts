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
  const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "/music", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
  await page.route("**/api/settings", route => {
    if (route.request().method() === "PUT") {
      expect(route.request().postDataJSON().lidarr_connection).toEqual({ url: "http://lidarr:8686", api_key: "test-only-key" });
      connected = true;
      return route.fulfill({ json: { encoding_changed: false } });
    }
    return route.fulfill({ json: { settings, lidarr: { url: connected ? "http://lidarr:8686" : "", key_configured: connected }, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } });
  });
  await page.route("**/api/lidarr/connection", route => {
    expect(route.request().method()).toBe("GET");
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
  await expect(page.getByLabel("Lidarr URL", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Lidarr API key", { exact: true })).toHaveCount(0);
  await page.getByRole("link", { name: "Lidarr settings", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Integrations", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.getByLabel("Lidarr URL", { exact: true }).fill("http://lidarr:8686");
  await page.getByLabel("Lidarr API key", { exact: true }).fill("test-only-key");
  await page.getByRole("tab", { name: "Encoding", exact: true }).click();
  await page.getByRole("tab", { name: "Integrations", exact: true }).click();
  await expect(page.getByLabel("Lidarr API key", { exact: true })).toHaveValue("test-only-key");
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await expect(page.getByLabel("Lidarr URL", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("Lidarr API key", { exact: true })).toHaveValue("");
  await page.getByLabel("Lidarr URL", { exact: true }).fill("http://lidarr:8686");
  await page.getByLabel("Lidarr API key", { exact: true }).fill("test-only-key");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect(page.getByLabel("Lidarr API key", { exact: true })).toHaveValue("");
  await page.getByRole("link", { name: "Library", exact: true }).click();
  await page.getByText("File counts · compare with Lidarr", { exact: true }).click();
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
