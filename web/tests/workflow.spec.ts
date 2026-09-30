import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

test("administrator setup, real incremental build, and login", async ({ page }) => {
  const root = process.env.MUSICFORGE_TEST_ROOT!;
  await expect.poll(async () => {
    try { return (await page.request.get("/healthz")).status(); } catch { return 0; }
  }).toBe(200);
  const log = readFileSync(join(root, "server.log"), "utf8");
  const code = JSON.parse(log.split("\n").find(line => line.includes('"setup_code"'))!).setup_code as string;
  await page.goto("/");
  await page.getByLabel("初始化码").fill(code);
  await page.getByLabel("用户名", { exact: true }).fill("admin");
  await page.getByLabel("密码", { exact: true }).fill("test-admin-password");
  await page.getByRole("button", { name: "创建管理员" }).click();
  await expect(page.getByRole("heading", { name: "音乐，从母库到播放" })).toBeVisible();
  await page.getByRole("link", { name: "设置", exact: true }).click();
  await page.getByLabel("FLAC 源目录").fill(join(root, "source"));
  await page.getByLabel("转码输出目录").fill(join(root, "output"));
  await page.getByLabel("启用音乐库扫描和后台任务").check();
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("设置已保存");
  await page.getByRole("link", { name: "音乐库", exact: true }).click();
  await expect(page.getByText("CI Track", { exact: true })).toBeVisible({ timeout: 30000 });
  await expect(page.getByRole("table").getByText("已就绪", { exact: true })).toBeVisible({ timeout: 30000 });
  await page.getByRole("link", { name: "概览", exact: true }).click();
  await expect(page.getByText("100%", { exact: true })).toBeVisible();
  await page.screenshot({ path: "test-results/dashboard.png", fullPage: true });
  await page.getByRole("link", { name: "任务", exact: true }).click();
  await expect(page.getByRole("heading", { name: "后台任务" })).toBeVisible();
  await page.getByRole("button", { name: "详情", exact: true }).first().click();
  await expect(page.getByText("任务日志", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "退出登录" }).click();
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill("admin");
  await page.getByLabel("密码", { exact: true }).fill("test-admin-password");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "后台任务" })).toBeVisible();
});
