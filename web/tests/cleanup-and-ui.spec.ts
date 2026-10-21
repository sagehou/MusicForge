import { setPreference } from "./preferences";
import { test, expect, type Page } from "@playwright/test";
import type { Dashboard, Job, Source } from "../src/types";

const me = { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" };
const settings = { enabled: true, source: "/music/source", output: "/music/output", encoding: { codec: "opus", mode: "vbr", bitrate: 192, quality: 2 }, concurrency: 1, scan_minutes: 60, lidarr_prefix: "", nav_url: "", nav_user: "", nav_library: 1, oidc_issuer: "", oidc_client_id: "" };
const dashboard: Dashboard = { source_count: 1215, output_count: 1149, ready_count: 1103, expired_count: 4, rebuild_count: 46, failed_count: 12, online: true, storage_message: "", codec: "opus", enabled: true, version: "test" };
const jobs: Job[] = ["running", "success", "failed", "paused", "pending"].map((state, index) => ({
  id: 130 - index, kind: index === 4 ? "refresh" : index === 0 ? "scan" : "convert", state, attempts: state === "failed" ? 3 : 0, progress: state === "success" ? 1 : .44,
  created: 1800000000, updated: 1800000060 - index, args: {}, counts: { total: 100, done: state === "success" ? 100 : 44, pending: state === "running" ? 55 : 0, running: state === "running" ? 1 : 0, failed: state === "failed" ? 12 : 0, cancelled: 0 },
  scan: index === 0 ? { phase: "indexed", path: "", artist: "", album: "", title: "", processed: 1215, total: 1215, percent: 100 } : undefined,
  current: state === "running" ? [{ phase: "convert", path: "River Ensemble/Night Studies/01 - First Light · 海岸与夜晚的音乐记录.flac", artist: "River Ensemble", album: "Night Studies", title: "First Light · 海岸与夜晚的音乐记录", processed: 44, total: 100, percent: 61 }] : [],
  scope: [], can_delete: !["running", "pending", "paused"].includes(state),
}));
async function setup(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("musicforge.language", "en");
    if (!localStorage.getItem("musicforge.theme")) localStorage.setItem("musicforge.theme", "light");
  });
  await page.route("**/api/auth/me", route => route.fulfill({ json: me }));
  await page.route("**/api/settings", route => route.fulfill({ json: { settings, configured: { webhook: false, nav_password: false, oidc_secret: false }, public_url: "", allowed_origins: [] } }));
  await page.route("**/api/library", route => route.fulfill({ json: [] }));
  await page.route("**/api/dashboard", route => route.fulfill({ json: dashboard }));
  await page.route("**/api/jobs?*", route => {
    const params = new URL(route.request().url()).searchParams;
    const state = params.get("state") || "all";
    const list = jobs.filter(job => state === "all" || job.state === state);
    return route.fulfill({ json: { jobs: list.slice(0, Number(params.get("limit") || 100)), total: list.length } });
  });
}

test.beforeEach(async ({ page }) => {
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false }, report: { state: "idle", total: 0, processed: 0, found: 0, not_found: 0, failed: 0, stopping: false } } }));
});

test("missing records have confirmed individual, selected and all cleanup with protected outputs", async ({ page }) => {
  await setup(page);
  let sources: Source[] = Array.from({ length: 5 }, (_, index) => ({
    id: index + 1, path: `River Ensemble/Archive/${index + 1}.flac`, artist: "River Ensemble", album: "Archive", title: `Track ${index + 1}`, track: index + 1, disc: 1,
    present: index === 2, output: index === 1 ? "River Ensemble/Archive/2.opus" : "", output_present: index === 1, status: index === 2 ? "missing" : "expired", error: "", size: 100, metadata: {},
  }));
  const requests: Record<string, unknown>[] = [];
  const dialogs: string[] = [];
  let accept = false;
  page.on("dialog", dialog => { dialogs.push(dialog.message()); void (accept ? dialog.accept() : dialog.dismiss()); });
  await page.route("**/api/library", route => route.fulfill({ json: sources }));
  await page.route("**/api/library/forget", route => {
    const body = route.request().postDataJSON(); requests.push(body);
    const selected = sources.filter(source => !source.present && !source.output && (body.all || body.ids.includes(source.id)));
    const removed = selected.filter(source => source.id !== 5);
    sources = sources.filter(source => !removed.includes(source));
    return route.fulfill({ json: { removed: removed.length, skipped: selected.length - removed.length } });
  });
  await page.goto("/library?view=albums&artist=River+Ensemble&album=River+Ensemble%2FArchive");
  await expect(page.getByRole("table").getByRole("button", { name: "Remove record", exact: true })).toHaveCount(3);
  const retained = page.locator("tbody tr").filter({ hasText: "Track 2" });
  await expect(retained.getByRole("button", { name: "Remove record", exact: true })).toHaveCount(0);
  await page.getByLabel("Select current page", { exact: true }).check();
  await page.getByRole("button", { name: "Remove 3 selected records", exact: true }).click();
  expect(requests).toHaveLength(0);
  expect(dialogs[0]).toContain("no files or task history are deleted");
  accept = true;
  await page.getByRole("button", { name: "Remove 3 selected records", exact: true }).click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0]).toEqual({ ids: [1, 4, 5] });
  await expect(page.locator(".toast")).toContainText("Library records removed: 2; protected records skipped: 1");
  await expect(page.locator("tbody tr")).toHaveCount(3);
  await expect(page.getByLabel("Select Track 1", { exact: true })).toHaveCount(0);
  await expect(retained).toBeVisible();
  await page.getByRole("button", { name: "Remove missing records", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]).toEqual({ all: true });
  await setPreference(page, "language", "zh-CN");
  await page.getByRole("table").getByRole("button", { name: "移除记录", exact: true }).click();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests[2]).toEqual({ ids: [5] });
  expect(dialogs.at(-1)).toContain("不删除文件或任务历史");
  await expect(page.locator(".toast")).toContainText("已移除 0 条库记录；跳过 1 条受保护的记录");
});

test("sidebar collapse persists and icon navigation stays accessible across languages and mobile", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/");
  await page.getByRole("button", { name: "Collapse sidebar", exact: true }).click();
  await expect(page.locator(".app-shell")).toHaveAttribute("data-sidebar-collapsed", "true");
  await expect.poll(async () => (await page.locator(".sidebar").boundingBox())?.width).toBe(76);
  await expect(page.locator(".sidebar .nav-label").first()).toBeHidden();
  await expect(page.getByRole("link", { name: "MusicForge", exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: "Expand sidebar", exact: true })).toHaveAttribute("aria-expanded", "false");
  await page.getByRole("link", { name: "Jobs", exact: true }).click();
  await expect(page.getByRole("link", { name: "Jobs", exact: true })).toHaveAttribute("aria-current", "page");
  await setPreference(page, "language", "zh-CN");
  await expect(page.getByRole("link", { name: "任务", exact: true })).toHaveAttribute("title", "任务");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("button", { name: "展开侧栏", exact: true })).toBeHidden();
  await expect(page.locator(".sidebar .nav-label").first()).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await expect.poll(async () => (await page.locator(".sidebar").boundingBox())?.width).toBe(216);
});

test("dashboard uses real clamped completion and functional quick actions", async ({ page }) => {
  await setup(page);
  let data = { ...dashboard, ready_count: 2000 };
  await page.route("**/api/dashboard", route => route.fulfill({ json: data }));
  const scans: unknown[] = [];
  await page.route("**/api/library/scan", route => { scans.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { job_id: 1 } }); });
  await page.goto("/");
  await expect(page.getByRole("progressbar", { name: "Build completion", exact: true })).toHaveAttribute("aria-valuenow", "100");
  await page.getByRole("button", { name: "Scan library", exact: true }).click();
  await expect.poll(() => scans.length).toBe(1);
  await expect(page.locator(".toast")).toContainText("Scan queued");
  await page.locator(".hero-actions").getByRole("button", { name: "View library", exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  data = { ...data, source_count: 0, ready_count: 0 };
  await page.goto("/");
  await expect(page.getByRole("progressbar", { name: "Build completion", exact: true })).toHaveAttribute("aria-valuenow", "0");
  await expect(page.locator(".hero-completion")).toContainText("0%");
});

for (const width of [1440, 1024, 768, 390, 320]) {
  test(`dashboard and compact job visual evidence at ${width}px`, async ({ page }) => {
    await setup(page);
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.setViewportSize({ width, height: width > 800 ? 1000 : 844 });
    await page.goto("/");
    await expect(page.locator(".hero-profile")).toContainText("192 kbps");
    for (const theme of ["light", "dark"]) {
      await setPreference(page, "theme", theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      await page.screenshot({ path: `test-results/forge-overview-${width}-${theme}.png`, fullPage: true, animations: "disabled" });
    }
    await setPreference(page, "language", "zh-CN");
    await expect(page.locator(".hero-completion")).toContainText("91%");
    await page.screenshot({ path: `test-results/forge-overview-${width}-dark-zh.png`, fullPage: true, animations: "disabled" });
    await page.getByRole("link", { name: "任务", exact: true }).click();
    await expect(page.locator(".task")).toHaveCount(5);
    await expect(page.locator('.task[data-state="running"] .task-activity')).toContainText("First Light");
    await expect(page.locator('.task[data-state="running"] [role="progressbar"]')).toHaveAttribute("aria-valuenow", "44");
    await expect(page.locator('.task[data-state="success"] .badge > svg')).toHaveCount(1);
    await expect(page.locator('.task[data-state="success"] .task-meter')).toBeHidden();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    // A breakpoint change must settle immediately, without layout overflow.
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.setViewportSize({ width, height: width > 800 ? 1000 : 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    if (width >= 1440) {
      const success = await page.locator('.task[data-state="success"]').boundingBox();
      expect(success!.height).toBeLessThan(150);
    }
    await page.screenshot({ path: `test-results/forge-jobs-${width}-dark-zh.png`, fullPage: true, animations: "disabled" });
    await setPreference(page, "language", "en");
    await setPreference(page, "theme", "light");
    await page.screenshot({ path: `test-results/forge-jobs-${width}-light.png`, fullPage: true, animations: "disabled" });
    await page.emulateMedia({ reducedMotion: "reduce" });
    expect(await page.locator('.task[data-state="running"] .task-meter > span').evaluate(node => getComputedStyle(node, "::after").animationName)).toBe("none");
    expect(errors).toEqual([]);
  });
}

test("task states use the active palette and neutral completion across light and dark themes", async ({ page }) => {
  await setup(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/jobs");
  await expect(page.locator(".task")).toHaveCount(5);
  for (const mode of ["light", "dark"]) {
    await setPreference(page, "theme", mode);
    const menu = page.locator(".page-heading .palette-menu");
    await menu.locator("summary").click();
    for (const name of ["Apple Music", "Spotify", "Custom"]) {
      await menu.getByRole("button", { name, exact: true }).click();
      if (name === "Custom") await menu.getByLabel("Color hex code", { exact: true }).fill("#8975ce");
      const colors = await page.evaluate(() => {
        const sample = document.createElement("span");
        document.body.append(sample);
        function token(name: string) { sample.style.color = `var(--${name})`; return getComputedStyle(sample).color; }
        function style(state: string, selector: string) { return getComputedStyle(document.querySelector(`.task[data-state="${state}"] ${selector}`)!); }
        const result = {
          accent: token("accent"), neutral: token("muted"), neutralFill: token("subtle"), warning: token("warning"), danger: token("danger"),
          running: style("running", ".badge").color, runningIcon: style("running", ".job-symbol").color, progress: style("running", ".task-meter > span").backgroundColor,
          success: style("success", ".badge").color, successIcon: style("success", ".job-symbol").color, successFill: style("success", ".badge").backgroundColor,
          paused: style("paused", ".badge").color, failed: style("failed", ".badge").color,
        };
        sample.remove();
        return result;
      });
      expect([colors.running, colors.runningIcon, colors.progress]).toEqual([colors.accent, colors.accent, colors.accent]);
      expect([colors.success, colors.successIcon, colors.successFill]).toEqual([colors.neutral, colors.neutral, colors.neutralFill]);
      expect(colors.paused).toBe(colors.warning);
      expect(colors.failed).toBe(colors.danger);
      await page.screenshot({ path: `test-results/task-colors-${name.replaceAll(" ", "-")}-${mode}.png`, animations: "disabled" });
    }
    await page.keyboard.press("Escape");
  }
});
