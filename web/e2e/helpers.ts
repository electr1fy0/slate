import { expect } from "@playwright/test"
import type { Page } from "@playwright/test"

export const STUDENT = {
  email: "e2e-student@slate.test",
  password: "e2e-password-123",
  name: "E2E Student",
}

/**
 * Clicks an element until it takes effect — the first interaction after a
 * page load races React hydration, and a lost click looks identical to a
 * broken handler. Retrying the click+effect pair removes the race.
 */
export async function clickUntil(
  page: Page,
  click: () => Promise<void>,
  effect: () => Promise<void>,
  timeout = 15_000
): Promise<void> {
  void page
  await expect(async () => {
    await click()
    await effect()
  }).toPass({ timeout })
}

export async function register(page: Page, user = STUDENT): Promise<void> {
  await page.goto("/login")
  const registerTab = page.getByRole("tab", { name: "Create account" })
  await expect(registerTab).toBeVisible({ timeout: 30_000 })
  await clickUntil(
    page,
    () => registerTab.click(),
    () => expect(registerTab).toHaveAttribute("aria-selected", "true")
  )
  await page.getByLabel("Display name").fill(user.name)
  await page.getByLabel("Email").fill(user.email)
  await page.getByLabel("Password", { exact: true }).fill(user.password)
  const registration = page.waitForResponse(
    (response) =>
      response.url().endsWith("/auth/register") &&
      response.request().method() === "POST"
  )
  await page.getByRole("button", { name: "Create account" }).click()
  const response = await registration
  if (response.status() === 409) {
    // Another journey in this isolated suite registered the shared learner.
    await signIn(page, user)
    return
  }
  expect(response.status()).toBe(201)
  await expect(
    page.getByRole("heading", {
      name: /Protect your final submission|Deliver timed assessments/i,
    })
  ).toBeVisible({ timeout: 30_000 })
}

export const TEACHER = {
  email: "teacher@slate.local",
  password: "teach-me-slate",
  name: "Teacher",
}

export async function signIn(
  page: Page,
  user = STUDENT,
  expectedUrlPattern?: RegExp
): Promise<void> {
  if (
    !page.url().startsWith("http") ||
    !new URL(page.url()).pathname.startsWith("/login")
  )
    await page.goto("/login")
  await page.getByRole("tab", { name: "Sign in" }).click()
  await page.getByLabel("Email").fill(user.email)
  await page.getByLabel("Password", { exact: true }).fill(user.password)
  await page.getByRole("button", { name: "Sign in" }).click()
  if (expectedUrlPattern) {
    await expect(page).toHaveURL(expectedUrlPattern, { timeout: 20_000 })
  } else {
    await expect(
      page.getByRole("heading", {
        name: /Protect your final submission|Deliver timed assessments/i,
      })
    ).toBeVisible({
      timeout: 20_000,
    })
  }
}

export async function signOut(page: Page): Promise<void> {
  await page
    .getByRole("button", { name: "Sign out", exact: true })
    .first()
    .click()
  await expect(page.getByText("Signed out")).toBeVisible()
}
