import { expect, type Page } from "@playwright/test";

export async function openPreferences(page: Page) {
  const panels = page.locator(".detail-panel[open] .preferences");
  const menu = await panels.count() ? panels : page.locator(".sidebar .preferences");
  if (!await menu.getAttribute("open").then(value => value !== null)) await menu.locator(":scope > summary").click();
}

export async function setPreference(page: Page, kind: "language" | "theme", value: string) {
  let control = page.locator(`.${kind}-selector select`).filter({ visible: true }).first();
  await expect(control.or(page.locator(".sidebar .preferences > summary")).first()).toBeVisible();
  if (!await control.count()) {
    await openPreferences(page);
    control = page.locator(`.preferences[open] .${kind}-selector select`);
  }
  await control.selectOption(value);
  const menu = page.locator(".preferences[open]");
  if (await menu.count()) await menu.locator(":scope > summary").click();
}

export async function openLibraryTools(page: Page) {
  const menu = page.locator(".page-tools");
  if (!await menu.getAttribute("open").then(value => value !== null)) await menu.locator(":scope > summary").click();
  await expect(menu).toHaveAttribute("open", "");
}
