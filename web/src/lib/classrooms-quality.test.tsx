import { act, renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { setAuth } from "./auth"
import { api } from "./api"
import {
  addClassroomMember,
  createClassroom,
  createClassroomOnServer,
  deleteClassroomOnServer,
  getClassroom,
  joinClassroom,
  mergeJoinedMember,
  syncClassroomRosterFromApi,
  syncClassroomsFromApi,
  updateClassroomOnServer,
  useClassrooms,
} from "./classrooms"
import type { ClassroomMemberRecord, ClassroomRecord } from "./api"
import type * as ApiModule from "./api"

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof ApiModule>("./api")
  return {
    ...actual,
    api: {
      ...actual.api,
      createClassroom: vi.fn(),
      listClassrooms: vi.fn(),
      deleteClassroom: vi.fn(),
      classroomRoster: vi.fn(),
      classroomInvitations: vi.fn(),
      updateClassroom: vi.fn(),
    },
  }
})

let sequence = 0
let userId = ""
const stamp = "2026-01-01T00:00:00Z"
function session(id: string) {
  return {
    user: {
      user_id: id,
      name: "Teacher",
      email: "teacher@example.test",
      role: "teacher" as const,
    },
    accessToken: "access",
    refreshToken: "refresh",
  }
}
function record(id: string): ClassroomRecord {
  return {
    id,
    owner_id: userId,
    name: "Class",
    join_code: "ABC234",
    created_at: stamp,
    updated_at: stamp,
    member_count: 1,
  }
}
function member(id: string): ClassroomMemberRecord {
  return {
    classroom_id: "class",
    user_id: id,
    name: id,
    email: `${id}@example.test`,
    role: "student",
    joined_at: stamp,
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
async function syncedClass() {
  const local = createClassroom({
    name: "Class",
    owner: { id: userId, name: "Teacher" },
  })
  vi.mocked(api.createClassroom).mockResolvedValue(record("class"))
  return (await createClassroomOnServer(local))!
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  userId = `class-quality-${++sequence}`
  setAuth(session(userId))
  vi.mocked(api.classroomInvitations).mockResolvedValue([])
})

describe("classroom asynchronous ownership", () => {
  it("never treats an unsynced empty code as a join credential", () => {
    createClassroom({ name: "Pending", owner: { id: userId, name: "Teacher" } })
    expect(joinClassroom(" ", { id: "learner", name: "Learner" })).toBeNull()
  })

  it("replaces an invitation's identity when its learner joins", () => {
    const old = {
      id: "invitation",
      name: "Old",
      email: "same@example.test",
      role: "student" as const,
      status: "pending" as const,
      joined_at: stamp,
    }
    const next = {
      ...old,
      id: "learner",
      name: "Current",
      status: "active" as const,
    }
    expect(mergeJoinedMember([old], next, old.email)).toEqual([next])
  })

  it("rejects a mutation if the account changes after its preflight", async () => {
    await syncedClass()
    const pendingList = deferred<ClassroomRecord[]>()
    vi.mocked(api.listClassrooms).mockReturnValue(pendingList.promise)
    const deletion = deleteClassroomOnServer("class")
    setAuth(session("another-user"))
    pendingList.resolve([record("class")])
    await expect(deletion).rejects.toThrow(/sign-in changed/)
    expect(api.deleteClassroom).not.toHaveBeenCalled()
  })

  it("keeps the latest classroom list when responses arrive out of order", async () => {
    const first = deferred<ClassroomRecord[]>()
    const second = deferred<ClassroomRecord[]>()
    vi.mocked(api.listClassrooms)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    const one = syncClassroomsFromApi()
    const two = syncClassroomsFromApi()
    second.resolve([record("new")])
    expect(await two).toBe(true)
    first.resolve([record("old")])
    expect(await one).toBe(false)
    expect(getClassroom("new")).not.toBeNull()
    expect(getClassroom("old")).toBeNull()
  })

  it("keeps the latest roster when responses arrive out of order", async () => {
    await syncedClass()
    const first = deferred<ClassroomMemberRecord[]>()
    const second = deferred<ClassroomMemberRecord[]>()
    vi.mocked(api.classroomRoster)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    const one = syncClassroomRosterFromApi("class")
    const two = syncClassroomRosterFromApi("class")
    second.resolve([member("new")])
    await two
    first.resolve([member("old")])
    await one
    expect(getClassroom("class")?.members.some((row) => row.id === "new")).toBe(
      true
    )
    expect(getClassroom("class")?.members.some((row) => row.id === "old")).toBe(
      false
    )
  })

  it("does not let a roster response overwrite a concurrent local mutation", async () => {
    await syncedClass()
    const request = deferred<ClassroomMemberRecord[]>()
    vi.mocked(api.classroomRoster).mockReturnValue(request.promise)
    const pending = syncClassroomRosterFromApi("class")
    addClassroomMember("class", { id: "new", name: "New" })
    request.resolve([])
    await pending
    expect(getClassroom("class")?.members.some((row) => row.id === "new")).toBe(
      true
    )
  })

  it("preserves members added during a class-details update", async () => {
    await syncedClass()
    const response = deferred<ClassroomRecord>()
    vi.mocked(api.updateClassroom).mockReturnValue(response.promise)
    const pending = updateClassroomOnServer("class", { name: "Renamed" })
    addClassroomMember("class", { id: "new", name: "New" })
    response.resolve({ ...record("class"), name: "Renamed" })
    expect((await pending).members.some((row) => row.id === "new")).toBe(true)
  })

  it("deletes a synced class even when it is absent from a limited catalog", async () => {
    await syncedClass()
    vi.mocked(api.listClassrooms).mockResolvedValue([])
    vi.mocked(api.deleteClassroom).mockResolvedValue(undefined)
    await deleteClassroomOnServer("class")
    expect(api.deleteClassroom).toHaveBeenCalledWith("class")
    expect(getClassroom("class")).toBeNull()
  })

  it("updates classroom hooks immediately on identity changes", () => {
    createClassroom({ name: "Private", owner: { id: userId, name: "Teacher" } })
    const hook = renderHook(() => useClassrooms())
    expect(hook.result.current).toHaveLength(1)
    act(() => setAuth(session("another-user")))
    expect(hook.result.current).toEqual([])
  })
})
