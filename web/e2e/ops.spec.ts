import { expect, test } from "@playwright/test"

import { register } from "./helpers"

const ADMIN_TOKEN = process.env.ADMIN_TOKEN ?? "continuum-demo-token"

test.describe("ops console", () => {
  test("requires sign-in, then drives chaos injection with hydrated state", async ({ page }) => {
    await register(page)

    await page.goto("/ops")

    // Admin token gate comes first.
    await page.getByLabel("Admin token").fill(ADMIN_TOKEN)

    // Status cards hydrate.
    await expect(page.getByText("Mumbai")).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText(/ACTIVE/).first()).toBeVisible()

    // Inject error chaos via the keyboard (slider step = 5% -> four presses).
    const slider = page.getByRole("slider").first()
    await slider.click()
    for (let i = 0; i < 4; i += 1) {
      await page.keyboard.press("ArrowRight")
    }
    await page.getByRole("button", { name: "Apply chaos" }).click()
    await expect(page.getByText("Chaos applied")).toBeVisible()

    // Server state now reads degraded after the refetch.
    await expect(page.getByText("degraded")).toBeVisible({ timeout: 15_000 })

    // Reset clears it.
    await page.getByRole("button", { name: "Reset" }).click()
    await expect(page.getByText("Chaos cleared")).toBeVisible()
    await expect(page.getByText("stable")).toBeVisible({ timeout: 15_000 })
  })

  test("ops is unreachable without a session", async ({ page }) => {
    await page.goto("/ops")
    await expect(page).toHaveURL(/\/login\?redirect=%2Fops/)
  })
})
