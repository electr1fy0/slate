import { expect, test } from "@playwright/test"

import { register } from "./helpers"

const ADMIN_TOKEN = process.env.ADMIN_TOKEN ?? "continuum-demo-token"

test.describe("ops console", () => {
  test("requires sign-in, then drives a Continuum fault and observes recovery", async ({ page }) => {
    await register(page)

    await page.goto("/ops")

    // Admin token gate comes first.
    await page.getByLabel("Admin token").fill(ADMIN_TOKEN)

    // Backend health and the selected Region come from the control service.
    await expect(page.getByText("Sydney")).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText("ACTIVE").first()).toBeVisible()
    await expect(page.getByText("Write epoch")).toBeVisible()

    await page.getByRole("button", { name: /Partial failure/ }).click()
    await expect(page.getByText(/partial/i).last()).toBeVisible({ timeout: 15_000 })

    // The recovered service snapshot reports a healthy active backend and a decision event.
    await expect(page.getByText("Healthy").first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText("Event timeline")).toBeVisible()

    await page.getByRole("button", { name: "Reset injected faults" }).click()
    await expect(page.getByText("Healthy").first()).toBeVisible({ timeout: 15_000 })
  })

  test("ops is unreachable without a session", async ({ page }) => {
    await page.goto("/ops")
    await expect(page).toHaveURL(/\/login\?redirect=%2Fops/)
  })
})
