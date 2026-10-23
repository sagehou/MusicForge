import { setPreference } from "./preferences";
import { test, expect } from "@playwright/test";

test("task details expand in the page with keyboard access, result filters and pagination", async ({ page }) => {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); localStorage.setItem("musicforge.theme", "light"); });
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const job = { id: 7, kind: "scan", state: "failed", attempts: 3, progress: .5, created: 1800000000, updated: 1800000060, args: {}, log: "Source read failed; retry remains available.", counts: { total: 51, done: 0, failed: 51, pending: 0, running: 0, cancelled: 0 }, current: [], scope: ["River Ensemble/Night Studies"], can_delete: true };
  await page.route("**/api/jobs?*", route => route.fulfill({ json: { jobs: [job], total: 1 } }));
  const reads: URLSearchParams[] = [];
  await page.route("**/api/jobs/7/items?*", route => {
    const query = new URL(route.request().url()).searchParams;
    reads.push(query);
    return route.fulfill({ json: { items: [{ id: Number(query.get("offset")) + 1, kind: "scan", state: "failed", attempts: 3, progress: 0, updated: 1800000060, activity: { phase: "read", title: "First Light", artist: "River Ensemble", album: "Night Studies", path: "River Ensemble/Night Studies/01.flac", processed: 0, total: 1, percent: 0 }, output: "", log: "Source read timed out" }], total: 51 } });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/jobs");
  const task = page.locator('[data-task-id="7"]');
  const toggle = task.locator("#task-toggle-7");
  await toggle.focus();
  await page.keyboard.press("Enter");
  const details = task.getByRole("region");
  await expect(details).toBeVisible();
  await expect(toggle).toBeFocused();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(toggle).toHaveAttribute("aria-controls", await details.getAttribute("id") as string);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(await details.evaluate(node => getComputedStyle(node).position)).toBe("static");
  await details.getByLabel("Show failures only", { exact: true }).check();
  await expect.poll(() => reads.at(-1)?.get("state")).toBe("failed");
  await details.getByRole("button", { name: "Next results page", exact: true }).click();
  await expect.poll(() => reads.at(-1)?.get("offset")).toBe("50");
  await details.locator(".task-diagnostic summary").click();
  await expect(details.locator("pre")).toContainText("Source read timed out");
  await setPreference(page, "language", "zh-CN");
  await setPreference(page, "theme", "dark");
  await page.screenshot({ path: "test-results/task-details-inline-1440-dark-zh.png", fullPage: true, animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  await setPreference(page, "language", "en");
  await setPreference(page, "theme", "light");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  await page.screenshot({ path: "test-results/task-details-inline-390-light-en.png", fullPage: true, animations: "disabled" });
  await details.getByRole("button", { name: "Previous results page", exact: true }).focus();
  await page.keyboard.press("Escape");
  await expect(details).toHaveCount(0);
  await expect(task.getByRole("button", { name: "Details", exact: true })).toBeFocused();
});

test("jobs support page and filtered selection, mixed states and accurate bulk results", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "en"));
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test" } }));
  const jobs = Array.from({ length: 205 }, (_, i) => ({
    id: i + 1, kind: "scan", state: i === 0 ? "pending" : i === 1 ? "success" : "failed", attempts: 0,
    progress: 0, created: 1800000000, updated: 1800000000, args: {},
    counts: { total: 0, done: 0, failed: 0, pending: 0, running: 0, cancelled: 0 }, current: [], scope: [], can_delete: i !== 0,
  }));
  await page.route("**/api/jobs?*", route => {
    const query = new URL(route.request().url()).searchParams;
    const filtered = jobs.filter(job => query.get("state") === "all" || job.state === query.get("state"));
    const offset = Number(query.get("offset"));
    return route.fulfill({ json: { jobs: filtered.slice(offset, offset + 100), total: filtered.length } });
  });
  const requests: Record<string, unknown>[] = [];
  let changed = 98;
  await page.route("**/api/jobs/control", route => {
    requests.push(route.request().postDataJSON());
    return route.fulfill({ status: 202, json: { changed } });
  });
  page.on("dialog", dialog => dialog.accept());
  await page.goto("/jobs");
  const pageSelection = page.getByRole("checkbox", { name: "Select this page", exact: true });
  await pageSelection.check();
  await expect(page.locator(".selection-bar")).toContainText(["Select this page", "Selected: 100"]);
  await page.getByRole("checkbox", { name: "Select task 3", exact: true }).uncheck();
  await expect(pageSelection).toHaveJSProperty("indeterminate", true);
  await page.getByRole("checkbox", { name: "Select task 3", exact: true }).check();
  await page.locator(".selection-bar").getByRole("button", { name: "Retry", exact: true }).click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0]).toEqual({ action: "retry", ids: Array.from({ length: 98 }, (_, i) => i + 3) });
  await expect(page.locator(".toast")).toContainText("Queued failed work in 98 tasks for retry");

  await pageSelection.check();
  await page.getByRole("button", { name: "Next jobs page", exact: true }).click();
  await expect(page.getByRole("checkbox", { name: "Select task 101", exact: true })).not.toBeChecked();
  await expect(page.locator(".selection-hint")).toHaveCount(0);
  await page.getByRole("button", { name: "Select all 205 matching tasks", exact: true }).click();
  await page.getByRole("button", { name: "Next jobs page", exact: true }).click();
  await expect(page.getByRole("checkbox", { name: "Select task 205", exact: true })).toBeChecked();
  await expect(page.locator(".selection-bar")).toContainText(["Select this page", "All 205 matching tasks selected"]);
  changed = 1;
  await page.locator(".selection-bar").getByRole("button", { name: "Stop", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]).toEqual({ action: "stop", all: true, state: "all", include_repairs: true });
  await expect(page.locator(".toast")).toContainText("Stopped 1 task; completed audio kept");

  await page.locator(".panel .filter-tabs").getByRole("button", { name: "Failed", exact: true }).click();
  await expect(pageSelection).not.toBeChecked();
  await page.getByRole("button", { name: "Select all 203 matching tasks", exact: true }).click();
  await setPreference(page, "language", "zh-CN");
  await expect(page.locator(".selection-bar")).toContainText(["全选本页", "已选择筛选结果中的全部 203 项任务"]);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".selection-bar").getByRole("button", { name: "删除记录", exact: true })).toBeVisible();
  changed = 0;
  await page.locator(".selection-bar").getByRole("button", { name: "删除记录", exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests[2]).toEqual({ action: "delete", all: true, state: "failed", include_repairs: true });
  await expect(page.locator(".toast")).toContainText("没有符合操作条件的任务");
  await expect(page.locator(".selection-hint")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
