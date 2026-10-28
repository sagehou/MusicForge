import { expect, type Page } from "@playwright/test";

export async function openAccount(page: Page) {
  const account = page.locator(".sidebar .account-link");
  if (await account.isVisible()) await account.click();
  else {
    await page.locator('.sidebar nav a[href="/settings"]').click();
    await page.locator("#settings-tab-security").click();
  }
  await expect(page.locator("#settings-tab-security")).toHaveAttribute("aria-selected", "true");
}

export async function setPreference(page: Page, kind: "language" | "theme", value: string) {
  const login = page.locator(".session-dialog");
  const root = await login.count() ? login : page.locator("body");
  const preferences = root.locator(".heading-preferences");
  if (await preferences.count() && !await preferences.locator(`.${kind}-selector select`).isVisible()) await preferences.locator(":scope > summary").click();
  const control = root.locator(`.${kind}-selector select`).filter({ visible: true }).first();
  await expect(control).toBeVisible();
  await control.selectOption(value);
  if (await preferences.count() && await preferences.evaluate(node => (node as HTMLDetailsElement).open)) await preferences.locator(":scope > summary").click();
}

export async function openLibraryOptions(page: Page) {
  const menu = page.locator(".library-options");
  if (!await menu.evaluate(node => (node as HTMLDetailsElement).open)) await menu.locator(":scope > summary").click();
}

export async function openLibraryTools(page: Page) {
  const menu = page.locator(".page-tools");
  if (!await menu.locator(":scope > summary").isVisible()) await openLibraryOptions(page);
  if (!await menu.getAttribute("open").then(value => value !== null)) await menu.locator(":scope > summary").click();
  await expect(menu).toHaveAttribute("open", "");
}

export async function selectLibraryPage(page: Page) {
  await openLibraryOptions(page);
  await page.locator('.library-selection input[type="checkbox"]').check();
  await page.locator('.library-selection input[type="checkbox"]').press("Escape");
}
