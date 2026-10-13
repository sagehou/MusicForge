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
  let strictRebuildFailed = false;
  await page.route("**/api/library", route => route.fulfill({ json: [{ ...source, repair, ...(imported ? {
    output: "Artist/Album/01.opus", output_present: true, status: strictRebuildFailed ? "failed" : "possibly_damaged", possibly_damaged: true,
    error: strictRebuildFailed ? "Audio decoding failed after strict rebuild" : "", can_repair: false,
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
  for (const width of [320, 768, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect.poll(() => page.locator(".repair-detail-row td").getAttribute("colspan")).toBe(width <= 600 ? "3" : "4");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    expect(await page.locator(".repair-diagnostic").evaluate(element => element.getBoundingClientRect().width / element.closest("table")!.getBoundingClientRect().width)).toBeGreaterThan(0.8);
    expect(await page.locator(".track-table tbody tr:not(.repair-detail-row) td:nth-child(2)").evaluate(element => element.getBoundingClientRect().width / element.closest("table")!.getBoundingClientRect().width)).toBeGreaterThan(0.45);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: "test-results/repair-import-390-dark-zh.png", fullPage: true, animations: "disabled" });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("combobox", { name: "语言", exact: true }).selectOption("en");
  await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
  await expect.poll(() => page.locator(".repair-detail-row td").getAttribute("colspan")).toBe("5");
  expect(await page.locator(".repair-diagnostic").evaluate(element => element.getBoundingClientRect().width / element.closest("table")!.getBoundingClientRect().width)).toBeGreaterThan(0.8);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: "test-results/repair-import-1440-light.png", fullPage: true, animations: "disabled" });
  await page.locator(".library-options-body select").first().selectOption("possibly_damaged");
  await expect(page.locator(".badge-possibly_damaged")).toBeVisible();
  await page.reload();
  await expect(page.locator(".badge-possibly_damaged")).toBeVisible();
  await expect(page.getByRole("link", { name: "Download preview", exact: true })).toHaveCount(0);
  strictRebuildFailed = true;
  await page.reload();
  await page.locator(".library-options-body select").first().selectOption("possibly_damaged");
  await expect(page.getByText("Dear My Friend", { exact: true })).toBeVisible();
  await expect(page.locator(".badge-failed")).toBeVisible();
  await expect(page.locator(".repair-warning")).toHaveText("Possibly damaged");
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

test("Jobs shows repair progress, durable outcomes and safe controls in both languages", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  let jobsUnavailable = true;
  await page.route("**/api/jobs?*", route => jobsUnavailable ? route.fulfill({ status: 500, json: { error: "Internal Server Error" } }) : route.fulfill({ json: { jobs: [], total: 0 } }));
  const activity = { phase: "read", path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Repair track", processed: 0, total: 0, percent: 0, read_bytes: 12 * 1048576, read_total_bytes: 40 * 1048576 };
  const task = { id: "a".repeat(64), source_id: 77, source_hash: "b".repeat(64), state: "running", activity, profile: { codec: "opus", mode: "vbr", bitrate: 192, quality: 0 }, progress: .03, created: 1800000000, updated: 1800000001, output: "", log: "", stopping: false };
  let tasks = [task];
  let stops = 0;
  let deletions = 0;
  await page.route("**/api/repair-tasks?*", route => {
    const state = new URL(route.request().url()).searchParams.get("state");
    const filtered = tasks.filter(value => state === "all" || value.state === state);
    return route.fulfill({ json: { tasks: filtered, total: filtered.length } });
  });
  await page.route(`**/api/repair-tasks/${task.id}/stop`, route => {
    expect(route.request().method()).toBe("POST");
    stops++; task.stopping = true;
    return route.fulfill({ json: { ok: true } });
  });
  await page.route(`**/api/repair-tasks/${task.id}`, route => {
    expect(route.request().method()).toBe("DELETE");
    deletions++; tasks = [];
    return route.fulfill({ json: { ok: true } });
  });
  await page.goto("/jobs");
  const section = page.getByRole("region", { name: "Best-effort repair tasks" });
  await expect(section.getByText("Repair track", { exact: true })).toBeVisible();
  jobsUnavailable = false;
  await expect(section).toContainText("Read 12.0 / 40.0 MiB");
  await expect(section.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "3");
  await expect(section.getByRole("button", { name: "Delete history", exact: true })).toHaveCount(0);
  task.activity = { ...activity, phase: "convert", percent: 60 };
  task.progress = .55;
  await expect(section.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "55");
  page.once("dialog", dialog => dialog.dismiss());
  await section.getByRole("button", { name: "Stop", exact: true }).click();
  expect(stops).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await section.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(section.getByRole("button", { name: "Stopping repair" })).toBeDisabled();
  expect(stops).toBe(1);
  task.state = "stopped"; task.stopping = false;
  task.log = "Repair stopped; check Library for any output committed before cancellation";
  await expect(section.locator(".badge")).toHaveText("Stopped");
  await section.locator("summary").click();
  await expect(section.locator("pre")).toContainText("Repair stopped");
  await expect(section.getByRole("link")).toHaveAttribute("href", /album=Artist%2FAlbum/);
  task.state = "success"; task.output = "Artist/Album/01.opus"; task.progress = 1;
  task.log = "Possibly damaged: imported using best-effort decoding\nPCM bytes: 100 of 120\nOriginal strict conversion failure: MD5 mismatch";
  await page.reload();
  await expect(section.locator(".badge")).toHaveText("Success");
  await expect(section).toContainText("Possibly damaged");
  await section.locator("summary").click();
  await expect(section.locator("pre")).toContainText("Original strict conversion failure");
  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  const chinese = page.getByRole("region", { name: "尽力修复任务" });
  await expect(chinese).toContainText("已导入输出库 · 可能损坏");
  await page.screenshot({ path: "test-results/repair-tasks-320-zh.png", fullPage: true, animations: "disabled" });
  await chinese.getByRole("button", { name: "失败", exact: true }).click();
  await expect(chinese).toContainText("当前筛选下没有修复任务");
  await chinese.getByRole("button", { name: "全部", exact: true }).click();
  page.once("dialog", dialog => dialog.accept());
  await chinese.getByRole("button", { name: "删除记录", exact: true }).click();
  await expect(chinese).toContainText("当前筛选下没有修复任务");
  expect(deletions).toBe(1);
});
