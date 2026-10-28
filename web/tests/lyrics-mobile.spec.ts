import { test, expect, type Page, type Locator } from "@playwright/test";
import type { Source } from "../src/types";
import { setPreference } from "./preferences";

async function visibleInViewport(page: Page, locator: Locator) {
  await expect(locator).toBeVisible();
  const box = (await locator.boundingBox())!;
  const viewport = page.viewportSize()!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(viewport.width + 1);
  expect(box.y + box.height).toBeLessThanOrEqual(viewport.height + 1);
}

test("phone lyric selection opens a separate tab with visible preview and confirmation and retains album review order", async ({ page, context }) => {
  await context.addInitScript(() => { localStorage.setItem("musicforge.language", "zh-CN"); localStorage.setItem("musicforge.theme", "dark"); });
  await page.setViewportSize({ width: 390, height: 844 });
  const sources: Source[] = Array.from({ length: 3 }, (_, index) => ({
    id: index + 1, path: `${index === 2 ? "Other/Elsewhere" : "Artist/Album"}/${index + 1}.flac`, artist: "Artist", album: index === 2 ? "Elsewhere" : "Album", title: `Song ${index + 1}`, track: index + 1, disc: 1, duration: 183,
    present: true, output: `${index === 2 ? "Other/Elsewhere" : "Artist/Album"}/${index + 1}.opus`, output_present: true, status: "ready", error: "", size: 123, metadata: {},
    lyrics: { state: "needs_review", output: `${index === 2 ? "Other/Elsewhere" : "Artist/Album"}/${index + 1}.opus`, signature: "audio", checked: 1 },
  }));
  await context.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await context.route("**/api/library", route => route.fulfill({ json: sources }));
  await context.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false, min_score: 96 }, report: { state: "idle" } } }));
  let searches = 0;
  await context.route("**/api/library/*/lyrics/candidates", route => {
    if (route.request().method() !== "GET") searches++;
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    return route.fulfill({ json: {
      query: `Song ${id} Artist`, instrumental: { provider: "source", token: `instrumental-${id}`, candidates: [{ index: 0 }] },
      groups: ["qq", "kugou", "netease", "petitlyrics", "lrclib"].map(provider => ({ provider, token: `${provider}-${id}`, cached: true, searched: 1800000000, error: provider === "qq" && id === 2 ? "Synthetic provider unavailable" : undefined,
        candidates: Array.from({ length: 8 }, (_, index) => ({ index, provider, title: `Song ${id} take ${index + 1}`, artist: "Artist", album: "Album", duration: 183, high_confidence: false, match_score: 90, script: "simplified" })),
      })),
    } });
  });
  const longLyrics = Array.from({ length: 120 }, (_, index) => `[${String(Math.floor(index / 60)).padStart(2, "0")}:${String(index % 60).padStart(2, "0")}.00]第 ${index + 1} 行歌词`).join("\n");
  await context.route("**/api/library/*/lyrics/preview", route => route.fulfill({ json: route.request().postDataJSON().token.startsWith("instrumental-") ? { original: "[00:00.00]纯音乐，请欣赏\n" } : { original: longLyrics, translation: "[00:00.00]Translation", romanization: "[00:00.00]Romanization", script: "simplified" } }));
  const saves: { token: string; index: number; translation: boolean; romanization: boolean }[] = [];
  let releaseSave: () => void = () => {};
  let saveGate: Promise<void> = Promise.resolve();
  let rejectSave = false;
  await context.route("**/api/library/*/lyrics", async route => {
    expect(route.request().method()).toBe("PUT");
    expect(route.request().headers()["x-csrf-token"]).toBe("test");
    saves.push(route.request().postDataJSON());
    await saveGate;
    if (rejectSave) return route.fulfill({ status: 409, json: { error: "LRC changed during publication; file preserved" } });
    const id = Number(new URL(route.request().url()).pathname.split("/")[3]);
    const source = sources.find(source => source.id === id)!;
    source.lyrics = { state: "ready", instrumental: saves.at(-1)!.token.startsWith("instrumental-"), output: source.output, signature: "audio", checked: 2, provider: "source", path: source.output.replace(/\.opus$/, ".lrc") };
    return route.fulfill({ json: source.lyrics });
  });

  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum&lyrics=needs_review");
  await page.getByLabel("搜索音乐库", { exact: true }).fill("Song");
  const originalURL = page.url();
  const [pickerTab] = await Promise.all([
    context.waitForEvent("page"),
    page.getByRole("link", { name: "选择歌词（在新标签页打开）", exact: true }).first().click(),
  ]);
  await pickerTab.setViewportSize({ width: 390, height: 844 });
  await pickerTab.waitForLoadState("domcontentloaded");
  await expect(pickerTab).toHaveURL(/lyrics_track=1/);
  await expect(pickerTab).toHaveURL(/q=Song/);
  expect(await pickerTab.evaluate(() => window.opener)).toBeNull();
  expect(page.url()).toBe(originalURL);
  await expect(page.locator(".lyrics-picker")).toHaveCount(0);
  const picker = pickerTab.locator(".lyrics-picker");
  await expect(picker).toHaveAccessibleName("选择歌词");
  await expect(picker.getByRole("radio")).toHaveCount(41);
  await expect(pickerTab.locator(".sidebar")).toBeHidden();
  const previewPane = picker.locator(".lyrics-preview-pane");
  await visibleInViewport(pickerTab, previewPane);
  await visibleInViewport(pickerTab, picker.getByRole("button", { name: "保存所选歌词", exact: true }));
  await expect(picker.getByRole("button", { name: "保存所选歌词", exact: true })).toBeDisabled();
  await picker.getByRole("radio", { name: "预览 Song 1 take 8 · Artist · 网易云", exact: true }).check();
  const selectedCandidate = picker.locator('.lyrics-candidate[data-selected="true"]');
  for (const fact of ["Song 1 take 8", "Artist · Album · 3:03", "网易云", "匹配分 90/100", "检测到简体中文字符"]) await expect(selectedCandidate).toContainText(fact);
  await expect(selectedCandidate.locator('.script-chip')).toBeVisible();
  expect((await selectedCandidate.boundingBox())!.height).toBeLessThanOrEqual(128);
  expect(await selectedCandidate.evaluate(element => element.scrollHeight <= element.clientHeight + 1 && element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  const text = picker.locator(".lyrics-preview-content");
  await expect(text).toHaveAccessibleName("可滚动歌词正文");
  await expect(text).toContainText("第 120 行歌词");
  expect(await text.evaluate(element => element.scrollHeight > element.clientHeight)).toBe(true);
  await text.evaluate(element => { element.scrollTop = element.scrollHeight; });
  await visibleInViewport(pickerTab, previewPane);
  await visibleInViewport(pickerTab, picker.getByRole("button", { name: "保存所选歌词", exact: true }));
  expect(await pickerTab.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1 && document.documentElement.scrollHeight <= innerHeight + 1)).toBe(true);
  await pickerTab.screenshot({ path: "test-results/lyrics-new-tab-390-dark-zh.png", animations: "disabled" });
  await picker.getByRole("radio", { name: "标记为纯音乐", exact: true }).check();
  await expect(text).toHaveText("[00:00.00]纯音乐，请欣赏");
  expect(saves).toHaveLength(0);
  const confirm = picker.getByRole("button", { name: "确认纯音乐", exact: true });
  await visibleInViewport(pickerTab, confirm);
  saveGate = new Promise<void>(resolve => { releaseSave = resolve; });
  rejectSave = true;
  await confirm.click();
  await expect.poll(() => saves.length).toBe(1);
  await expect(confirm).toBeDisabled();
  await expect(picker.getByRole("button", { name: "下一首歌曲", exact: true })).toBeDisabled();
  await expect(picker.getByRole("button", { name: "关闭歌词选择", exact: true })).toBeDisabled();
  await expect(pickerTab.getByText("返回音乐库", { exact: true })).toHaveAttribute("aria-disabled", "true");
  await pickerTab.keyboard.press("Escape");
  await expect(pickerTab).toHaveURL(/lyrics_track=1/);
  releaseSave();
  await expect(previewPane.getByRole("alert")).toBeVisible();
  await expect(confirm).toBeEnabled();
  await visibleInViewport(pickerTab, confirm);
  await expect(text).toContainText("纯音乐，请欣赏");
  rejectSave = false;
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await expect(picker.getByRole("status")).toContainText("已保存");
  expect(saves).toEqual(Array(2).fill({ token: "instrumental-1", index: 0, translation: false, romanization: false }));
  await pickerTab.screenshot({ path: "test-results/lyrics-instrumental-390-dark-zh.png", animations: "disabled" });
  await picker.getByRole("button", { name: "下一首待处理歌曲", exact: true }).click();
  await expect(picker.locator(".detail-heading")).toContainText("Song 2");
  await expect(pickerTab).toHaveURL(/lyrics_track=2/);
  await expect(picker.getByRole("button", { name: "下一首歌曲", exact: true })).toBeDisabled();
  await pickerTab.reload();
  await expect(picker.locator(".detail-heading")).toContainText("Song 2");
  await picker.getByRole("radio", { name: "标记为纯音乐", exact: true }).check();
  await expect(text).toContainText("纯音乐，请欣赏");
  await setPreference(pickerTab, "language", "en");
  await setPreference(pickerTab, "theme", "light");
  await pickerTab.setViewportSize({ width: 320, height: 568 });
  await expect(picker.locator(".lyrics-guidance")).toHaveAttribute("open", "");
  expect((await picker.locator(".lyrics-candidate-groups").boundingBox())!.height).toBeGreaterThanOrEqual(96);
  await visibleInViewport(pickerTab, picker.locator(".lyrics-candidate-groups"));
  await visibleInViewport(pickerTab, previewPane);
  await visibleInViewport(pickerTab, picker.getByRole("button", { name: "Confirm instrumental", exact: true }));
  await expect(text).toHaveAccessibleName("Scrollable lyric text");
  await expect(text).toContainText("纯音乐，请欣赏");
  expect(await text.evaluate(element => element.scrollHeight <= element.clientHeight + 1)).toBe(true);
  expect(await pickerTab.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1 && document.documentElement.scrollHeight <= innerHeight + 1)).toBe(true);
  await pickerTab.screenshot({ path: "test-results/lyrics-instrumental-320-light-en.png", animations: "disabled" });
  await picker.getByRole("button", { name: "Confirm instrumental", exact: true }).click();
  await expect(picker.getByRole("button", { name: "Confirm instrumental", exact: true })).toBeDisabled();
  await expect(picker.getByRole("status")).toContainText("Saved");
  expect(saves.at(-1)).toEqual({ token: "instrumental-2", index: 0, translation: false, romanization: false });
  await pickerTab.close();
  await page.bringToFront();
  expect(page.url()).toBe(originalURL);
  await expect(page.getByLabel("搜索音乐库", { exact: true })).toHaveValue("Song");
  expect(searches).toBe(0);

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/library?view=albums&artist=Artist&album=Artist%2FAlbum&lyrics=instrumental");
  await expect(page.locator(".track-row")).toHaveCount(2);
  await expect(page.locator(".lyrics-state")).toHaveText(["纯音乐", "纯音乐"]);
  await page.getByRole("button", { name: "选择歌词", exact: true }).first().click();
  await page.getByRole("radio", { name: "标记为纯音乐", exact: true }).check();
  await expect(page.getByRole("region", { name: "可滚动歌词正文", exact: true })).toContainText("纯音乐，请欣赏");
  await page.screenshot({ path: "test-results/lyrics-instrumental-desktop-dark-zh.png", animations: "disabled" });
});

test("instrumental choices renew expired and consumed targets when replacing lyrics repeatedly on the same page", async ({ page }) => {
  await page.addInitScript(() => { localStorage.setItem("musicforge.language", "en"); });
  const source: Source = { id: 1, path: "Artist/Album/01.flac", artist: "Artist", album: "Album", title: "Track", track: 1, disc: 1, duration: 183, present: true, output: "Artist/Album/01.opus", output_present: true, status: "ready", error: "", size: 123, metadata: {} };
  await page.route("**/api/auth/me", route => route.fulfill({ json: { initialized: true, authenticated: true, username: "admin", method: "local", csrf: "test", oidc: false, version: "test" } }));
  await page.route("**/api/library", route => route.fulfill({ json: [source] }));
  await page.route("**/api/lyrics", route => route.fulfill({ json: { options: { auto_search: false, min_score: 96 }, report: { state: "idle" } } }));
  let version = 0, sequence = 0;
  const tokens = new Map<string, number>();
  const candidate = { index: 0, provider: "lrclib", title: "Track", artist: "Artist", album: "Album", duration: 183, high_confidence: false, match_score: 90, script: "simplified" };
  const searches: string[] = [], previews: string[] = [], saves: string[] = [];
  await page.route("**/api/library/1/lyrics/candidates", route => {
    if (route.request().method() === "GET") {
      const token = `instrumental-${++sequence}`;
      tokens.set(token, version);
      return route.fulfill({ json: { query: "Track Artist", groups: [], instrumental: { token, candidates: [{ index: 0 }] } } });
    }
    const { provider } = route.request().postDataJSON();
    searches.push(provider);
    const token = `${provider}-${++sequence}`;
    tokens.set(token, version);
    return route.fulfill({ json: { provider, token, candidates: provider === "lrclib" ? [candidate] : [] } });
  });
  await page.route("**/api/library/1/lyrics/preview", route => {
    const { token } = route.request().postDataJSON();
    previews.push(token);
    if (!tokens.has(token)) return route.fulfill({ status: 409, json: { error: "Lyrics candidates expired; search again" } });
    return route.fulfill({ json: { original: token.startsWith("instrumental-") ? "[00:00.00]纯音乐，请欣赏\n" : "[00:01.00]Real lyrics" } });
  });
  await page.route("**/api/library/1/lyrics", route => {
    const { token } = route.request().postDataJSON();
    if (!tokens.has(token) || tokens.get(token) !== version) return route.fulfill({ status: 409, json: { error: "LRC changed during publication; file preserved" } });
    saves.push(token);
    tokens.delete(token); version++;
    source.lyrics = { state: "ready", instrumental: token.startsWith("instrumental-"), output: source.output, signature: "audio", checked: version, provider: token.startsWith("instrumental-") ? "source" : "lrclib" };
    return route.fulfill({ json: source.lyrics });
  });

  // Standalone also stays open for a one-track library after a successful save.
  await page.goto("/library?view=tracks&lyrics_track=1");
  const picker = page.locator(".lyrics-picker");
  const instrumental = picker.getByRole("radio", { name: "Mark as instrumental", exact: true });
  const confirm = picker.getByRole("button", { name: "Confirm instrumental", exact: true });
  await expect(instrumental).toBeEnabled();
  // Expire the initial token without waiting ten minutes or changing the UI.
  tokens.clear();
  await instrumental.check();
  await expect(confirm).toBeEnabled();
  expect(previews[0]).not.toBe("instrumental-1");
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await expect(picker.getByRole("status")).toContainText("Saved");
  expect(saves).toHaveLength(1);
  expect(searches).toHaveLength(0);

  async function searchAndSelect() {
    await picker.getByRole("button", { name: "Search candidates", exact: true }).click();
    await expect(picker.getByRole("button", { name: "Search candidates", exact: true })).toBeEnabled();
    await picker.getByRole("radio", { name: "Preview Track · Artist · LRCLIB", exact: true }).check();
    await expect(picker.locator(".lyrics-preview-content")).toHaveText("[00:01.00]Real lyrics");
  }
  await searchAndSelect();
  await picker.getByRole("button", { name: "Save selected lyrics", exact: true }).click();
  await expect(picker.getByRole("status")).toContainText("Saved");
  expect(saves).toHaveLength(2);
  await expect(picker.getByRole("radio", { name: "Preview Track · Artist · LRCLIB", exact: true })).toHaveCount(0);

  // The initial instrumental token was consumed and the output changed twice.
  await instrumental.check();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await expect(picker.getByRole("status")).toContainText("Saved");
  expect(saves).toHaveLength(3);
  expect(saves[2]).not.toBe(saves[0]);

  // Repeated online searches must not leave the local choice on an evicted token.
  await searchAndSelect();
  tokens.clear();
  await instrumental.check();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await expect(picker.getByRole("status")).toContainText("Saved");
  expect(saves).toHaveLength(4);
  expect(searches).toHaveLength(10);
  await expect(picker.locator(".repair-warning")).toHaveCount(0);
});
