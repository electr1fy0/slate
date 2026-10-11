import { beforeEach, describe, expect, it } from "vitest"

import {
  addClassroomMember,
  createClassroom,
  removeClassroomMember,
} from "./classrooms"

describe("classroom workspace", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("creates a local class draft without inventing a server join code", () => {
    const classroom = createClassroom({
      name: "Systems 101",
      subject: "Distributed systems",
      owner: { id: "teacher-1", name: "Ada", email: "ada@example.com" },
    })

    expect(classroom.name).toBe("Systems 101")
    expect(classroom.join_code).toBe("")
    expect(classroom.sync_state).toBe("pending")
    expect(classroom.members).toHaveLength(1)
    expect(classroom.members[0]?.role).toBe("teacher")
  })

  it("keeps local roster helpers limited to explicit roster edits", () => {
    const classroom = createClassroom({
      name: "Operations",
      owner: { id: "teacher-1", name: "Grace" },
    })

    const invited = addClassroomMember(classroom.id, {
      name: "Lin",
      email: "lin@example.com",
      status: "invited",
    })
    expect(invited?.members).toHaveLength(2)

    const removed = removeClassroomMember(
      classroom.id,
      invited?.members[1]?.id ?? ""
    )
    expect(removed?.members.some((member) => member.name === "Lin")).toBe(false)
  })
})
