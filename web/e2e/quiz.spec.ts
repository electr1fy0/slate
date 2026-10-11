import { expect, test } from "@playwright/test"
import type { Page } from "@playwright/test"

import { clickUntil, register, signIn, signOut, STUDENT } from "./helpers"

async function answerCurrent(page: Page, optionText: string): Promise<void> {
  await expect(page.getByRole("radio").first()).toBeVisible()
  await page.getByRole("radio", { name: optionText, exact: true }).click()
}

async function gotoQuiz(page: Page): Promise<void> {
  await page.goto("/")
  const card = page
    .locator("div[data-slot='card']")
    .filter({ hasText: "Cloud Architecture CAT" })
    .first()
  await clickUntil(
    page,
    () =>
      card
        .getByRole("button", {
          name: /Start assessment|Resume assessment|View result/i,
        })
        .click(),
    () => expect(page).toHaveURL(/\/quiz\/cs101-midterm/)
  )
  await expect(
    page
      .getByText("Before you begin", { exact: true })
      .or(page.getByText("Answer review", { exact: true }))
  ).toBeVisible()
  const begin = page.getByRole("button", {
    name: /Begin assessment|Resume assessment/,
  })
  if (await begin.isVisible()) await begin.click()
}

test.describe("quiz journey", () => {
  test("student registers, takes the seeded quiz with draft persistence, and reviews results", async ({
    page,
  }) => {
    await register(page)
    await gotoQuiz(page)

    // Answer Q1, reload — the draft must survive.
    await answerCurrent(page, "DynamoDB Global Tables")
    await page.reload()
    await page.getByRole("button", { name: /Resume assessment/ }).click()
    await expect(page.getByRole("radio", { checked: true })).toBeVisible()
    await expect(page.getByText("DynamoDB Global Tables")).toBeVisible()

    const answers = [
      "Maximum tolerable data loss window", // Q2 correct
      "2", // Q3 correct
      "Retry", // Q4 wrong on purpose
      "A DNS TTL", // Q5 wrong on purpose -> 3/5
    ]
    for (const answer of answers) {
      await page.getByRole("button", { name: "Next" }).click()
      await answerCurrent(page, answer)
    }

    await page.getByRole("button", { name: "Submit quiz" }).click()
    await page
      .getByRole("alertdialog")
      .getByRole("button", { name: "Save and submit" })
      .click()

    // Result screen: 3/5 = 60%, with per-question review.
    await expect(page.getByText("60%")).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/You earned/)).toContainText("3")
    await expect(page.getByText("Answer review")).toBeVisible()

    // Leaving and returning shows the stored review without retaking.
    await page.getByRole("button", { name: "Back home" }).click()
    await gotoQuiz(page)
    await expect(page.getByText("Answer review")).toBeVisible()

    await signOut(page)
    void STUDENT
  })

  test("signed-out users are routed through login when opening a quiz link", async ({
    page,
  }) => {
    test.setTimeout(120_000)
    const user = { ...STUDENT, email: `redirect-${Date.now()}@slate.test` }
    await register(page, user)
    await signOut(page)
    await page.goto("/quiz/cs101-midterm")
    await expect(page).toHaveURL(/\/login\?redirect=%2Fquiz%2Fcs101-midterm/)
    await signIn(page, user, /\/quiz\/cs101-midterm/)
    await expect(
      page.getByText("Before you begin", { exact: true })
    ).toBeVisible()
    await page
      .getByRole("button", { name: "Begin assessment", exact: true })
      .click()
    await expect(page.getByRole("radio").first()).toBeVisible()
  })
})
