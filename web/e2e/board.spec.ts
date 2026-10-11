import { expect, test } from "@playwright/test"

import { clickUntil, register, signIn, TEACHER } from "./helpers"

test.describe("live board", () => {
  const ROOM = `e2e${Date.now().toString(36)}`

  test("two clients share presence and collaborative cursors", async ({
    browser,
  }) => {
    const teacherContext = await browser.newContext()
    const studentContext = await browser.newContext()
    const teacher = await teacherContext.newPage()
    const student = await studentContext.newPage()

    await signIn(teacher, TEACHER)
    await register(student)

    // Both join the same room.
    await clickUntil(
      teacher,
      async () => {
        await teacher
          .goto("/")
          .then(() => teacher.getByLabel("Room code").fill(ROOM))
          .then(() => teacher.getByRole("button", { name: "Join" }).click())
      },
      () => expect(teacher.getByText(`Board · ${ROOM}`)).toBeVisible()
    )
    await student.goto(`/board/${ROOM}`)
    await expect(student.getByText(`Board · ${ROOM}`)).toBeVisible()

    // Live badges on both sides once sockets are open.
    await expect(teacher.getByText("live", { exact: true })).toBeVisible({
      timeout: 15_000,
    })
    await expect(student.getByText("live", { exact: true })).toBeVisible({
      timeout: 15_000,
    })

    // Shared permission is acknowledged by the server and survives reload.
    await expect(student.getByText(/View-only/).first()).toBeVisible()
    await teacher.getByRole("switch", { name: "students draw" }).click()
    await expect(student.getByText("You can draw")).toBeVisible()
    await student.reload()
    await expect(student.getByText("You can draw")).toBeVisible()
    await teacher.getByRole("switch", { name: "students draw" }).click()
    await expect(student.getByText(/View-only/).first()).toBeVisible()

    // Presence: both participants see a count of two.
    const teacherPresence = teacher.getByRole("button", {
      name: /participants \(2\)/i,
    })
    const studentPresence = student.getByRole("button", {
      name: /participants \(2\)/i,
    })
    await expect(teacherPresence).toBeVisible({ timeout: 15_000 })
    await expect(studentPresence).toBeVisible()

    // Full participant list shows names and roles.
    await teacherPresence.click()
    await expect(
      teacher.getByText(TEACHER.name, { exact: true }).last()
    ).toBeVisible()
    await expect(teacher.getByText("E2E Student")).toBeVisible()
    await teacher.keyboard.press("Escape")

    // Teacher sweeps the pointer over their canvas; the student's overlay
    // renders the remote cursor label.
    const canvas = teacher.locator(".excalidraw").first()
    await expect(canvas).toBeVisible({ timeout: 20_000 })
    const box = (await canvas.boundingBox())!
    for (const fraction of [0.3, 0.4, 0.5]) {
      await teacher.mouse.move(
        box.x + box.width * fraction,
        box.y + box.height * fraction,
        { steps: 4 }
      )
      await teacher.waitForTimeout(80)
    }
    await expect(
      student
        .locator("div[aria-hidden='true']")
        .getByText(TEACHER.name, { exact: true })
    ).toBeVisible({ timeout: 10_000 })

    await teacherContext.close()
    await studentContext.close()
  })

  test("board index lists recently visited rooms", async ({ page }) => {
    await register(page)
    await page.goto(`/board/${ROOM}`)
    await expect(page.getByText(`Board · ${ROOM}`)).toBeVisible()
    await expect(page.getByText("live", { exact: true })).toBeVisible()

    await page.getByRole("link", { name: "Live board" }).click()
    await expect(page.getByText("Recent rooms", { exact: true })).toBeVisible()
    await expect(page.getByText(ROOM).first()).toBeVisible()
  })
})
