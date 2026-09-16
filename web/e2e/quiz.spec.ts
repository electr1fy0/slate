import { expect, test } from "@playwright/test"
import type { Page } from "@playwright/test"

import { clickUntil, register, signIn, signOut, STUDENT } from "./helpers"

const QUIZ_HEADING = "Which AWS service provides cross-region asynchronous table replication?"

async function answerCurrent(page: Page, optionText: string): Promise<void> {
  await expect(page.getByRole("heading", { level: 2 })).toBeVisible()
  await page.getByText(optionText, { exact: true }).click()
}

async function gotoQuiz(page: Page): Promise<void> {
  await page.goto("/")
  const card = page
    .locator("div[data-slot='card']")
    .filter({ hasText: "CS101 Midterm Examination" })
    .first()
  await clickUntil(
    page,
    () => card.getByRole("link", { name: /take quiz/i }).click(),
    () => expect(page.getByRole("heading", { name: QUIZ_HEADING })).toBeVisible(),
  )
}

test.describe("quiz journey", () => {
  test("student registers, takes the seeded quiz with draft persistence, and reviews results", async ({ page }) => {
    await register(page)
    await gotoQuiz(page)

    // Answer Q1, reload — the draft must survive.
    await answerCurrent(page, "DynamoDB Global Tables")
    await page.reload()
    await expect(page.getByRole("radio", { checked: true })).toBeHidden // radio is decorative; the label state carries it
    await expect(page.getByText("DynamoDB Global Tables")).toBeVisible()

    const answers = [
      "Maximum tolerable data loss window", // Q2 correct
      "Catch", // Q3 correct
      "In-flight write loss during failover", // Q4 correct
      "DNS propagation delay", // Q5 wrong on purpose -> 3/5
    ]
    for (const answer of answers) {
      await page.getByRole("button", { name: "Next" }).click()
      await answerCurrent(page, answer)
    }

    await page.getByRole("button", { name: "Submit quiz" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Submit" }).click()

    // Result screen: 3/5 = 60%, with per-question review.
    await expect(page.getByText("60%")).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/You answered/)).toContainText("3")
    await expect(page.getByText("Answer review")).toBeVisible()

    // Leaving and returning shows the stored review without retaking.
    await page.getByRole("link", { name: "Back home" }).click()
    await gotoQuiz(page)
    await expect(page.getByText("Answer review")).toBeVisible()

    await signOut(page)
    void STUDENT
  })

  test("signed-out users are routed through login when opening a quiz link", async ({ page }) => {
    await page.goto("/quiz/cs101-midterm")
    await expect(page).toHaveURL(/\/login\?redirect=%2Fquiz%2Fcs101-midterm/)
    await signIn(page, STUDENT, /\/quiz\/cs101-midterm/)
    await expect(page.getByText("Answer review")).toBeVisible()
  })
})
