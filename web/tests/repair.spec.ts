import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

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
  await expect(page.locator(".track-cell strong").filter({ hasText: /^Dear My Friend$/ })).toBeVisible();
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

test("repair queues share the Jobs list, filters and safe controls in both languages", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const activity = { phase: "read", path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Repair track", processed: 0, total: 0, percent: 0, read_bytes: 12 * 1048576, read_total_bytes: 40 * 1048576 };
  const task = { id: "a".repeat(64), source_id: 77, source_hash: "b".repeat(64), state: "running", activity, profile: { codec: "opus", mode: "vbr", bitrate: 192, quality: 0 }, progress: .03, created: 1800000000, updated: 1800000001, output: "", log: "", stopping: false };
  let removed = false, stops = 0, deletions = 0;
  const ordinary = { id: 100, kind: "scan", state: "failed", args: {}, attempts: 3, progress: .5, created: 1799999999, updated: 1799999999, counts: { total: 0, done: 0, failed: 0, pending: 0, running: 0, cancelled: 0 }, current: [], scope: [], can_delete: true };
  await page.route("**/api/jobs?*", route => {
    expect(new URL(route.request().url()).searchParams.get("include_repairs")).toBe("true");
    const repair = { ...ordinary, id: 0, repair_id: task.id, kind: "repair", state: task.state, progress: task.progress, current: task.state === "running" ? [task.activity] : [], profile: task.profile, stopping: task.stopping, can_delete: task.state !== "running", counts: { total: 1, done: task.state === "success" ? 1 : 0, failed: task.state === "failed" ? 1 : 0, pending: 0, running: task.state === "running" ? 1 : 0, cancelled: task.state === "stopped" ? 1 : 0 } };
    const state = new URL(route.request().url()).searchParams.get("state");
    const jobs = (removed ? [ordinary] : [repair, ordinary]).filter(job => state === "all" || job.state === state);
    return route.fulfill({ json: { jobs, total: jobs.length } });
  });
  await page.route("**/api/repair-tasks?*", route => {
    expect(new URL(route.request().url()).searchParams.get("group")).toBe(task.id);
    return route.fulfill({ json: { tasks: [task], total: 1 } });
  });
  await page.route("**/api/jobs/control", route => {
    const body = route.request().postDataJSON();
    expect(body.ids).toEqual([]); expect(body.repair_ids).toEqual([task.id]);
    if (body.action === "stop") { stops++; task.stopping = true; }
    else { expect(body.action).toBe("delete"); deletions++; removed = true; }
    return route.fulfill({ status: 202, json: { changed: 1 } });
  });
  await page.goto("/jobs");
  const row = page.locator(`[data-repair-task-id="${task.id}"]`);
  await expect(page.locator(".filter-tabs")).toHaveCount(1);
  await expect(page.locator(".repair-tasks")).toHaveCount(0);
  await expect(page.locator(".task-list > .task")).toHaveCount(2);
  await expect(row).toContainText("Read 12.0 / 40.0 MiB");
  await expect(row.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "3");
  await expect(row.getByRole("button", { name: "Delete history", exact: true })).toBeDisabled();
  await expect(row.getByRole("button", { name: "Pause", exact: true })).toHaveCount(0);
  await page.locator(".filter-tabs").getByRole("button", { name: "Failed", exact: true }).click();
  await expect(row).toHaveCount(0);
  await expect(page.locator(".task-list > .task")).toHaveCount(1);
  await page.locator(".filter-tabs").getByRole("button", { name: "All", exact: true }).click();
  task.activity = { ...activity, phase: "convert", percent: 60 }; task.progress = .55;
  await expect(row.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "55");
  page.once("dialog", dialog => dialog.dismiss());
  await row.getByRole("button", { name: "Stop", exact: true }).click(); expect(stops).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await row.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(row.getByRole("button", { name: "Stopping repair" })).toBeDisabled(); expect(stops).toBe(1);
  task.state = "stopped"; task.stopping = false; task.log = "Repair stopped; check Library for any output committed before cancellation";
  await expect(row.locator(".task-main .badge")).toHaveText("Stopped");
  await row.getByRole("button", { name: "Details", exact: true }).click();
  await row.locator("summary").click(); await expect(row.locator("pre")).toContainText("Repair stopped");
  await expect(row.getByRole("link")).toHaveAttribute("href", /album=Artist%2FAlbum/);
  task.state = "success"; task.output = "Artist/Album/01.opus"; task.progress = 1;
  task.log = "Possibly damaged: imported using best-effort decoding\nPCM bytes: 100 of 120\nOriginal strict conversion failure: MD5 mismatch";
  await page.reload();
  await expect(row.locator(".task-main .badge")).toHaveText("Success");
  await expect(row).toContainText("Possibly damaged");
  await row.getByRole("button", { name: "Details", exact: true }).click(); await row.locator("summary").click();
  await expect(row.locator("pre")).toContainText("Original strict conversion failure");
  await expect(row.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(row).toContainText("尽力修复队列");
  await expect(row).toContainText("已导入输出库 · 可能损坏");
  await page.screenshot({ path: "test-results/unified-repair-tasks-320-zh.png", fullPage: true, animations: "disabled" });
  page.once("dialog", dialog => dialog.accept());
  await row.getByRole("button", { name: "删除记录", exact: true }).click();
  await expect(row).toHaveCount(0); expect(deletions).toBe(1);
});

test("filtered failed tracks can be selected across pages and repaired with one confirmation", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const sources = Array.from({ length: 70 }, (_, i) => ({ id: i + 1, hash: "a".repeat(64), path: `Artist/Album/${i}.${i < 60 ? "flac" : "mp3"}`, artist: "Artist", album: "Album", title: `Track ${i}`, track: i + 1, disc: 1, duration: 120, size: 100, metadata: {}, present: true, output: "", output_present: false, status: i < 65 ? "failed" : "ready", error: i < 65 ? "Audio decoding failed" : "", can_repair: i < 60 }));
  await page.route("**/api/library", route => route.fulfill({ json: sources }));
  let starts = 0;
  await page.route("**/api/library/repair", route => {
    starts++;
    expect(route.request().postDataJSON()).toEqual({ tracks: sources.slice(0, 60).map(source => ({ id: source.id, hash: source.hash })) });
    return route.fulfill({ status: 202, json: { repair_id: "c".repeat(64), queued: 60, skipped: 0 } });
  });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum");
  await page.locator(".library-options-body select").first().selectOption("failed");
  await page.getByRole("checkbox", { name: "Select current page", exact: true }).check();
  await page.getByRole("button", { name: "Select all 65 matching tracks", exact: true }).click();
  const repair = page.getByRole("button", { name: "Best-effort repair selected (60)", exact: true });
  page.once("dialog", async dialog => { expect(dialog.message()).toContain("60 selected tracks"); expect(dialog.message()).toContain("one task"); await dialog.dismiss(); });
  await repair.click(); expect(starts).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await repair.click();
  await expect(page.locator(".toast")).toContainText("Queued 60 tracks for best-effort repair; skipped 5");
  await expect(repair).toHaveCount(0); expect(starts).toBe(1);
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await page.getByRole("checkbox", { name: "选择当前页", exact: true }).check();
  await page.getByRole("button", { name: "选择筛选结果中的全部 65 首曲目", exact: true }).click();
  await expect(page.getByRole("button", { name: "尽力修复所选曲目（60 首）", exact: true })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("failed queue results offer batch repair for eligible selected tracks", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const counts = { total: 2, done: 0, failed: 2, pending: 0, running: 0, cancelled: 0 };
  const job = { id: 100, kind: "scan", state: "failed", args: {}, attempts: 3, progress: 0, created: 1800000000, updated: 1800000000, counts, current: [], scope: [], can_delete: true };
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [job], total: 1 } }));
  await page.route("**/api/jobs/100/items?*", route => route.fulfill({ json: { items: [1, 2].map(id => ({ ...job, id, kind: "convert", failure: "decode", log: "Audio decoding failed", activity: { phase: "convert", path: `Artist/Album/${id}.flac`, title: `Track ${id}`, artist: "Artist", album: "Album", processed: 0, total: 0, percent: 0 }, output: "", repair_target: { id, hash: "a".repeat(64), can_repair: true } })), total: 2 } }));
  let requests = 0;
  await page.route("**/api/library/repair", route => {
    requests++;
    expect(route.request().postDataJSON()).toEqual({ tracks: [1, 2].map(id => ({ id, hash: "a".repeat(64) })) });
    return route.fulfill({ status: 202, json: { queued: 2, skipped: 0, repair_id: "a".repeat(64) } });
  });
  await page.goto("/jobs");
  await page.getByRole("button", { name: "Details", exact: true }).click();
  await page.getByRole("checkbox", { name: "Show failures only", exact: true }).check();
  await page.getByRole("checkbox", { name: "Select repairable tracks on this page", exact: true }).check();
  page.once("dialog", dialog => dialog.accept());
  await page.getByRole("button", { name: "Best-effort repair selected (2)", exact: true }).click();
  await expect(page.locator(".toast")).toContainText("Queued 2 tracks");
  expect(requests).toBe(1);
});
