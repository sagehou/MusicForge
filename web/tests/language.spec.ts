import { test, expect } from "@playwright/test";
import en from "../src/locales/en.json";
import zh from "../src/locales/zh-CN.json";

test.use({ locale: "en-US" });

test("translation catalogs keep keys and placeholders aligned", () => {
  expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort());
  const placeholders = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map(match => match[1]).sort();
  for (const key of Object.keys(en) as (keyof typeof en)[]) {
    const english = typeof en[key] === "string" ? [en[key] as string] : Object.values(en[key]);
    const chinese = zh[key];
    for (const text of english) {
      expect(text, key).not.toMatch(/[\u3400-\u9fff]/);
      expect(placeholders(chinese), key).toEqual(placeholders(text));
    }
  }
});

for (const [browserLocale, expected] of [["en-US", "en"], ["zh-CN", "zh-CN"], ["fr-FR", "en"]]) {
  test(`detects ${browserLocale} and persists an explicit choice`, async ({ browser, baseURL }) => {
    const context = await browser.newContext({ locale: browserLocale, baseURL });
    try {
      const page = await context.newPage();
      await page.goto("/");
      await expect(page.locator("html")).toHaveAttribute("lang", expected);
      await expect(page).toHaveTitle(expected === "en" ? "MusicForge · Streaming music library" : "MusicForge · 流媒体音乐库");
      const language = page.getByRole("combobox", { name: expected === "en" ? "Language" : "语言", exact: true });
      await expect(language).toHaveValue(expected);
      const choice = expected === "en" ? "zh-CN" : "en";
      await language.selectOption(choice);
      await page.reload();
      await expect(page.locator("html")).toHaveAttribute("lang", choice);
      await expect(page.getByRole("combobox", { name: choice === "en" ? "Language" : "语言", exact: true })).toHaveValue(choice);
    } finally { await context.close(); }
  });
}

test("invalid saved languages fall back to browser preference", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("musicforge.language", "invalid"));
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("combobox", { name: "Language", exact: true })).toHaveValue("en");
});

test("language selection works when browser storage is unavailable", async ({ page }) => {
  await page.addInitScript(() => {
    Storage.prototype.getItem = () => { throw new Error("Storage disabled"); };
    Storage.prototype.setItem = () => { throw new Error("Storage disabled"); };
  });
  await page.goto("/");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(/创建你的音乐工作空间|欢迎回来/);
});

test("technical diagnostics remain available when switching language", async ({ page }) => {
  await page.route("**/api/auth/me", route => route.fulfill({
    status: 503, contentType: "application/json", body: JSON.stringify({ error: "disk I/O error: /config/musicforge.db" }),
  }));
  await page.goto("/");
  await expect(page.getByRole("alert")).toHaveText("disk I/O error: /config/musicforge.db");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("zh-CN");
  await expect(page.getByRole("alert")).toHaveText("disk I/O error: /config/musicforge.db");
  await expect(page.getByRole("button", { name: "重新连接", exact: true })).toBeVisible();
});
