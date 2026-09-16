import { beforeEach, describe, expect, it } from "vitest"

import {
  addClassroomMember,
  createClassroom,
  joinClassroom,
  removeClassroomMember,
} from "./classrooms"

describe("classroom workspace", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("creates a class with an owner and join code", () => {
    const classroom = createClassroom({
      name: "Systems 101",
      subject: "Distributed systems",
      owner: { id: "teacher-1", name: "Ada", email: "ada@example.com" },
    })

    expect(classroom.name).toBe("Systems 101")
    expect(classroom.join_code).toHaveLength(6)
    expect(classroom.members).toHaveLength(1)
    expect(classroom.members[0]?.role).toBe("teacher")
  })

  it("joins by code and manages invited roster members", () => {
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

    const joined = joinClassroom(classroom.join_code.toLowerCase(), {
      id: "student-1",
      name: "Sam",
      email: "sam@example.com",
    })
    expect(joined?.members.at(-1)?.status).toBe("active")

    const removed = removeClassroomMember(classroom.id, invited?.members[1]?.id ?? "")
    expect(removed?.members.some((member) => member.name === "Lin")).toBe(false)
  })
})
