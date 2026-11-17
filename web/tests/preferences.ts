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
  // Wait for authentication to replace the temporary loading controls.
  await expect(page.locator(".heading-preferences, .auth-card, .auth-layout [role='alert']").first()).toBeVisible();
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

export async function selectLibraryPage(page: Page) {
  await page.locator('.library-selection input[type="checkbox"]').check();
}

export async function openSelectionActions(page: Page) {
  const menu = page.locator(".selection-more");
  if (!await menu.evaluate(node => (node as HTMLDetailsElement).open)) await menu.locator(":scope > summary").click();
  await expect.poll(() => menu.evaluate(element => {
    const content = element.querySelector(".selection-more-body")!.getBoundingClientRect();
    return content.bottom - element.getBoundingClientRect().bottom;
  }), { message: "Expanded selection actions must fit inside their disclosure" }).toBeLessThanOrEqual(1);
}

export async function openLyricsMaintenance(page: Page) {
  const panel = page.locator(".lyrics-settings");
  if (!await panel.isVisible()) await page.locator(".library-workspace-modes > button").nth(1).click();
  if (!await panel.evaluate(node => (node as HTMLDetailsElement).open)) await panel.locator(":scope > summary").click();
}
