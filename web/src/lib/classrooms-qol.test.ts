import { beforeEach, describe, expect, it, vi } from "vitest"

import { api } from "./api"
import { clearAuth, setAuth } from "./auth"
import {
  createClassroom,
  createClassroomOnServer,
  deleteClassroomOnServer,
  getClassroom,
  inviteClassroomMemberOnServer,
  leaveClassroomOnServer,
  removeClassroomInvitationOnServer,
  syncClassroomsFromApi,
  updateClassroomOnServer,
} from "./classrooms"

vi.mock("./api", () => ({
  api: {
    listClassrooms: vi.fn(),
    createClassroom: vi.fn(),
    joinClassroom: vi.fn(),
    classroomRoster: vi.fn(),
    classroomInvitations: vi.fn(),
    inviteClassroomMember: vi.fn(),
    deleteClassroomInvitation: vi.fn(),
    removeClassroomMember: vi.fn(),
    updateClassroom: vi.fn(),
    deleteClassroom: vi.fn(),
    leaveClassroom: vi.fn(),
  },
}))

const teacher = (userId: string) => ({
  user: {
    user_id: userId,
    email: `${userId}@example.com`,
    name: "Ada",
    role: "teacher" as const,
  },
  accessToken: "access-token",
  refreshToken: "refresh-token",
})

const classroomRecord = (id: string, name = "Systems 101") => ({
  id,
  owner_id: "teacher-qol",
  name,
  description: "Distributed systems",
  join_code: "ABC234",
  created_at: "2026-10-01T10:00:00.000Z",
  updated_at: "2026-10-01T10:00:00.000Z",
  member_count: 1,
})

const invitation = {
  id: "invite-1",
  classroom_id: "server-class",
  name: "Lin Chen",
  email: "lin@example.com",
  status: "pending" as const,
  created_at: "2026-10-02T10:00:00.000Z",
  join_code: "INV234",
  delivery: "not_configured" as const,
}

describe("server-backed classroom workflows", () => {
  let nextUser = 0

  beforeEach(() => {
    clearAuth()
    localStorage.clear()
    nextUser += 1
    setAuth(teacher(`teacher-qol-${nextUser}`))
    vi.resetAllMocks()
  })

  it("retries class creation with one stable client id and waits for a server code", async () => {
    const draft = createClassroom({
      name: "Systems 101",
      subject: "Distributed systems",
      owner: { id: "teacher-qol", name: "Ada" },
    })
    expect(draft.join_code).toBe("")

    vi.mocked(api.createClassroom)
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValueOnce(classroomRecord("server-class"))

    expect(await createClassroomOnServer(draft)).toBeNull()
    expect(getClassroom(draft.id)?.sync_state).toBe("error")
    const synced = await createClassroomOnServer(getClassroom(draft.id)!)

    expect(api.createClassroom).toHaveBeenCalledTimes(2)
    expect(vi.mocked(api.createClassroom).mock.calls[0]?.[0].client_id).toBe(
      draft.id
    )
    expect(vi.mocked(api.createClassroom).mock.calls[1]?.[0].client_id).toBe(
      draft.id
    )
    expect(synced).toMatchObject({
      id: "server-class",
      join_code: "ABC234",
      sync_state: "synced",
    })
  })

  it("keeps cached drafts isolated between signed-in users", async () => {
    setAuth(teacher("teacher-a"))
    const draft = createClassroom({
      name: "Private class",
      owner: { id: "teacher-a", name: "Teacher A" },
    })
    setAuth(teacher("teacher-other"))
    vi.mocked(api.listClassrooms).mockResolvedValue([])
    expect(getClassroom(draft.id)).toBeNull()
    expect(
      localStorage.getItem(
        `slate.classrooms.${encodeURIComponent("teacher-a")}`
      )
    ).not.toBeNull()
    expect(await syncClassroomsFromApi()).toBe(true)
  })

  it("updates details and safely retries delete and leave after a completed server action", async () => {
    const draft = createClassroom({
      name: "Old name",
      owner: { id: "teacher-qol", name: "Ada" },
    })
    vi.mocked(api.createClassroom).mockResolvedValue(
      classroomRecord("server-class", "Old name")
    )
    const synced = await createClassroomOnServer(draft)
    expect(synced?.id).toBe("server-class")

    vi.mocked(api.updateClassroom).mockResolvedValue(
      classroomRecord("server-class", "New name")
    )
    const updated = await updateClassroomOnServer("server-class", {
      name: "New name",
      description: "Math",
    })
    expect(api.updateClassroom).toHaveBeenCalledWith("server-class", {
      name: "New name",
      description: "Math",
    })
    expect(updated.name).toBe("New name")

    vi.mocked(api.listClassrooms)
      .mockResolvedValueOnce([classroomRecord("server-class", "New name")])
      .mockResolvedValue([])
    vi.mocked(api.deleteClassroom).mockResolvedValue(undefined)
    await deleteClassroomOnServer("server-class")
    await deleteClassroomOnServer("server-class")
    expect(api.deleteClassroom).toHaveBeenCalledTimes(1)
    expect(getClassroom("server-class")).toBeNull()

    const learnerDraft = createClassroom({
      name: "Learner class",
      owner: { id: "teacher-qol", name: "Ada" },
    })
    vi.mocked(api.createClassroom).mockResolvedValue(
      classroomRecord("joined-class", "Learner class")
    )
    await createClassroomOnServer(learnerDraft)
    vi.mocked(api.listClassrooms)
      .mockResolvedValueOnce([classroomRecord("joined-class", "Learner class")])
      .mockResolvedValue([])
    vi.mocked(api.leaveClassroom).mockResolvedValue(undefined)
    await leaveClassroomOnServer("joined-class")
    await leaveClassroomOnServer("joined-class")
    expect(api.leaveClassroom).toHaveBeenCalledTimes(1)
    expect(getClassroom("joined-class")).toBeNull()
  })

  it("deduplicates invitation retries from server state and removes only confirmed pending invites", async () => {
    const draft = createClassroom({
      name: "Systems",
      owner: { id: "teacher-qol", name: "Ada" },
    })
    vi.mocked(api.createClassroom).mockResolvedValue(
      classroomRecord("server-class")
    )
    await createClassroomOnServer(draft)
    vi.mocked(api.classroomInvitations)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([invitation])
      .mockResolvedValueOnce([invitation])
      .mockResolvedValue([])
    vi.mocked(api.inviteClassroomMember).mockRejectedValueOnce(
      new Error("response lost after create")
    )

    await expect(
      inviteClassroomMemberOnServer("server-class", {
        name: "Lin Chen",
        email: "Lin@Example.com",
      })
    ).rejects.toThrow("response lost")
    const retried = await inviteClassroomMemberOnServer("server-class", {
      name: "Lin Chen",
      email: "lin@example.com",
    })
    expect(retried).toEqual(invitation)
    expect(api.inviteClassroomMember).toHaveBeenCalledTimes(1)

    await removeClassroomInvitationOnServer("server-class", invitation.id)
    await removeClassroomInvitationOnServer("server-class", invitation.id)
    expect(api.deleteClassroomInvitation).toHaveBeenCalledTimes(1)
  })

  it("does not write a completed create into a newly active account", async () => {
    const draft = createClassroom({
      name: "Race safe",
      owner: { id: "teacher-qol", name: "Ada" },
    })
    let finishCreate:
      ((value: ReturnType<typeof classroomRecord>) => void) | undefined
    vi.mocked(api.createClassroom).mockReturnValue(
      new Promise((resolve) => {
        finishCreate = resolve
      })
    )
    const pending = createClassroomOnServer(draft)
    setAuth(teacher("teacher-switched"))
    finishCreate?.(classroomRecord("server-class"))
    expect(await pending).toBeNull()
    expect(getClassroom("server-class")).toBeNull()
  })
})
