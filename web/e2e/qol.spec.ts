import { expect, test } from "@playwright/test"
import { register, signIn, TEACHER } from "./helpers"

test("a teacher publishes a class assignment and a learner starts, resumes, and recovers its receipt", async ({
  browser,
}) => {
  // This covers two complete attempts, reload recovery, and both user roles.
  // Keep each action bounded while allowing tracing and context cleanup on CI.
  test.setTimeout(300_000)
  const teacherContext = await browser.newContext()
  const learnerContext = await browser.newContext()
  const teacher = await teacherContext.newPage()
  const learner = await learnerContext.newPage()
  const suffix = Date.now().toString(36)
  const title = `QoL assessment ${suffix}`
  const className = `QoL class ${suffix}`
  const learnerUser = {
    email: `qol-${suffix}@slate.test`,
    password: "learner-password-123",
    name: "QoL Learner",
  }
  try {
    await signIn(teacher, TEACHER)
    await teacher
      .getByRole("button", { name: "New class", exact: true })
      .click()
    await teacher.getByLabel("Class name", { exact: true }).fill(className)
    await teacher
      .getByLabel("Subject", { exact: true })
      .fill("Assessment workflow")
    await teacher
      .getByRole("button", { name: "Create class", exact: true })
      .click()
    await expect(
      teacher.getByRole("button", { name: "Copy class code" })
    ).toBeVisible()
    const joinCode = (await teacher
      .locator("span.font-mono")
      .filter({ hasText: /^[A-Z0-9]{6,10}$/ })
      .first()
      .textContent())!.trim()
    await teacher
      .getByRole("button", { name: "Edit class", exact: true })
      .click()
    await teacher.getByLabel("Subject", { exact: true }).fill("Updated subject")
    await teacher
      .getByRole("button", { name: "Save changes", exact: true })
      .click()
    await expect(teacher.getByText(/Updated subject · created/)).toBeVisible()

    await teacher.goto("/teacher")
    await teacher.getByLabel("Title", { exact: true }).fill(title)
    await teacher
      .getByLabel("Assigned classroom")
      .selectOption({ label: className })
    await teacher
      .getByLabel("Instructions (optional)")
      .fill("Read the instructions before starting.")
    await teacher.getByLabel("Duration in minutes").fill("10")
    await teacher.getByLabel("Maximum attempts").fill("2")
    await teacher
      .getByLabel("Prompt", { exact: true })
      .fill("Which answer is correct?")
    await teacher.getByLabel(/^Option A/).fill("Correct")
    await teacher.getByLabel("Option B", { exact: true }).fill("Incorrect")
    await teacher.getByLabel("Points", { exact: true }).fill("3")
    await teacher
      .getByLabel("Feedback (optional)")
      .fill("The first option is the correct choice.")
    await teacher.getByRole("button", { name: "Bank", exact: true }).click()
    await expect(
      teacher.getByText("Question synced to your bank", { exact: true })
    ).toBeVisible()
    await teacher
      .getByRole("button", { name: "Publish quiz", exact: true })
      .click()
    await expect(teacher.getByRole("tab", { name: "Manage" })).toHaveAttribute(
      "aria-selected",
      "true"
    )
    await expect(teacher.getByText(title, { exact: true })).toBeVisible()

    await register(learner, learnerUser)
    await expect(learner.getByText(title, { exact: true })).not.toBeVisible()
    await learner
      .getByRole("button", { name: "Join with code", exact: true })
      .click()
    await learner.getByLabel("Class or invitation code").fill(joinCode)
    await learner
      .getByRole("button", { name: "Join class", exact: true })
      .click()
    await expect(
      learner.getByText(`Joined ${className}`, { exact: true })
    ).toBeVisible()
    await learner.reload()
    const card = learner
      .locator("[data-slot='card']")
      .filter({ hasText: title })
      .first()
    await card.getByRole("button", { name: "Start assessment" }).click()
    await expect(
      learner.getByText("Before you begin", { exact: true })
    ).toBeVisible()
    await expect(
      learner.getByText("Read the instructions before starting.")
    ).toBeVisible()
    const examId = new URL(learner.url()).pathname.split("/").at(-1)!
    const headers = {
      Authorization: `Bearer ${await learner.evaluate(() => JSON.parse(localStorage.getItem("slate.auth")!).accessToken)}`,
    }
    const info = await learner.request.get(`/api/exam/${examId}/info`, {
      headers,
    })
    expect((await info.json()).attempt_status).toBe("not_started")
    await learner
      .getByRole("button", { name: "Begin assessment", exact: true })
      .click()
    await learner
      .getByRole("radio", { name: /Correct$/, exact: false })
      .first()
      .click()
    await learner.getByRole("button", { name: "Flag for review" }).click()
    await learner.reload()
    await learner
      .getByRole("button", { name: "Resume assessment", exact: true })
      .click()
    await expect(learner.getByRole("radio", { checked: true })).toBeVisible()
    await expect(
      learner.getByRole("button", { name: "Flagged for review", exact: true })
    ).toBeVisible()
    await learner
      .getByRole("button", { name: "Clear answer", exact: true })
      .click()
    await expect(learner.getByRole("radio", { checked: true })).toHaveCount(0)
    await learner.getByRole("radio").first().click()
    await learner
      .getByRole("button", { name: "Submit quiz", exact: true })
      .click()
    await learner
      .getByRole("alertdialog")
      .getByRole("button", { name: "Save and submit", exact: true })
      .click()
    await expect(
      learner.getByText("100%", { exact: true }).first()
    ).toBeVisible()
    await expect(
      learner.getByText("The first option is the correct choice.", {
        exact: true,
      })
    ).toBeVisible()
    await learner.reload()
    await expect(
      learner.getByText("Answer review", { exact: true })
    ).toBeVisible()
    await expect(
      learner.getByRole("button", { name: /Download receipt/ })
    ).toBeVisible()

    const firstResult = await (
      await learner.request.get(`/api/exam/${examId}/result`, { headers })
    ).json()
    const downloadPromise = learner.waitForEvent("download")
    await learner
      .getByRole("button", { name: "Download receipt", exact: true })
      .click()
    const download = await downloadPromise
    expect(download.suggestedFilename()).toContain(examId)
    await expect(
      learner.getByRole("button", { name: "Retake assessment", exact: true })
    ).toBeVisible()
    await learner
      .getByRole("button", { name: "Retake assessment", exact: true })
      .click()
    await learner
      .getByRole("alertdialog")
      .getByRole("button", { name: "Start timed attempt", exact: true })
      .click()
    await learner
      .getByRole("button", { name: "Resume assessment", exact: true })
      .click()
    await expect(learner.getByRole("radio").first()).toBeVisible()
    await expect(learner.getByRole("radio", { checked: true })).toHaveCount(0)
    await expect(
      learner.getByRole("button", { name: "Flag for review", exact: true })
    ).toBeVisible()
    await learner.getByRole("radio", { name: "Incorrect", exact: true }).click()
    await expect
      .poll(async () => {
        const saved = await learner.request.get(`/api/exam/${examId}/attempt`, {
          headers,
        })
        return (await saved.json()).answers
      })
      .toEqual({ "0": 1 })
    await learner.reload()
    await learner
      .getByRole("button", { name: "Resume assessment", exact: true })
      .click()
    await expect(
      learner.getByRole("radio", { name: "Incorrect", exact: true })
    ).toBeChecked()
    await learner
      .getByRole("button", { name: "Submit quiz", exact: true })
      .click()
    await learner
      .getByRole("alertdialog")
      .getByRole("button", { name: "Save and submit", exact: true })
      .click()
    await expect(
      learner.getByText("Answer review", { exact: true })
    ).toBeVisible()
    await expect(
      learner.getByRole("button", { name: "Retake assessment", exact: true })
    ).toHaveCount(0)
    const secondResult = await (
      await learner.request.get(`/api/exam/${examId}/result`, { headers })
    ).json()
    expect(secondResult.attempt_number).toBe(2)
    expect(secondResult.request_id).not.toBe(firstResult.request_id)
    const replay = await learner.request.post(`/api/exam/${examId}/submit`, {
      headers,
      data: { answers: { "0": 0 }, request_id: firstResult.request_id },
    })
    expect(replay.status()).toBe(200)
    expect(await replay.json()).toMatchObject({
      status: "duplicate",
      request_id: firstResult.request_id,
      score: 3,
      attempt_number: 1,
    })

    await teacher.getByRole("tab", { name: "Analytics", exact: true }).click()
    await teacher
      .getByLabel("Choose quiz for analytics")
      .selectOption({ label: title })
    await expect(
      teacher.getByText("QoL Learner", { exact: true })
    ).toBeVisible()
    await expect(teacher.getByText("QoL Learner", { exact: true })).toHaveCount(
      1
    )
  } finally {
    await Promise.all([teacherContext.close(), learnerContext.close()])
  }
})

test("profile and password changes persist through sign-out", async ({
  page,
}) => {
  const pageErrors: string[] = []
  page.on("pageerror", (error) => pageErrors.push(error.message))
  const suffix = Date.now().toString(36)
  const user = {
    email: `profile-${suffix}@slate.test`,
    password: "original-password-123",
    name: "Original Name",
  }
  await register(page, user)
  await page.goto("/settings")
  await page.getByLabel("Display name").fill("Updated Name")
  await page.getByRole("button", { name: "Save profile" }).click()
  await expect(page.getByText("Profile updated", { exact: true })).toBeVisible()
  await page.getByLabel("Current password", { exact: true }).fill(user.password)
  await page
    .getByLabel("New password", { exact: true })
    .fill("replacement-password-123")
  await page
    .getByRole("button", { name: "Change password", exact: true })
    .click()
  await expect(page).toHaveURL(/\/login/)
  await signIn(page, { ...user, password: "replacement-password-123" })
  await page.goto("/settings")
  await expect(page.getByLabel("Display name")).toHaveValue("Updated Name")
  expect(pageErrors).toEqual([])
})
