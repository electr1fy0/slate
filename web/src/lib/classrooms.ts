import { useSyncExternalStore } from "react"

import { api } from "./api"
import type { ClassroomMemberRecord, ClassroomRecord } from "./api"
import type { Role } from "./types"

export type ClassroomMemberStatus = "active" | "invited"

export interface ClassroomMember {
  id: string
  name: string
  email: string
  role: Role
  status: ClassroomMemberStatus
  joined_at: string
}

export interface Classroom {
  id: string
  name: string
  subject: string
  join_code: string
  created_at: string
  members: ClassroomMember[]
  active_room_id?: string
}

interface MemberInput {
  id?: string
  name: string
  email?: string
  role?: Role
  status?: ClassroomMemberStatus
}

const STORAGE_KEY = "slate.classrooms"
const SELECTED_KEY = "slate.selected-classroom"
const MAX_CLASSROOMS = 20
const SERVER_SNAPSHOT: Classroom[] = []

let classroomCache: Classroom[] | null = null
let selectedClassroomCache: string | null | undefined
const listeners = new Set<() => void>()

function nowIso(): string {
  return new Date().toISOString()
}

function randomId(prefix: string): string {
  const suffix =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID().slice(0, 8)
      : Math.random().toString(36).slice(2, 10)
  return `${prefix}-${suffix}`
}

function readClassrooms(): Classroom[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []

    return parsed.filter(isClassroom).slice(0, MAX_CLASSROOMS)
  } catch {
    return []
  }
}

function isClassroom(value: unknown): value is Classroom {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<Classroom>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.subject === "string" &&
    typeof candidate.join_code === "string" &&
    typeof candidate.created_at === "string" &&
    Array.isArray(candidate.members) &&
    candidate.members.every(isMember)
  )
}

function isMember(value: unknown): value is ClassroomMember {
  if (!value || typeof value !== "object") return false
  const candidate = value as Partial<ClassroomMember>
  return (
    typeof candidate.id === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.email === "string" &&
    (candidate.role === "teacher" || candidate.role === "student") &&
    (candidate.status === "active" || candidate.status === "invited") &&
    typeof candidate.joined_at === "string"
  )
}

function readSelectedClassroom(): string | null {
  if (typeof window === "undefined") return null
  try {
    return window.localStorage.getItem(SELECTED_KEY)
  } catch {
    return null
  }
}

function getClassroomSnapshot(): Classroom[] {
  classroomCache ??= readClassrooms()
  return classroomCache
}

function getSelectedSnapshot(): string | null {
  selectedClassroomCache ??= readSelectedClassroom()
  return selectedClassroomCache
}

function getServerClassrooms(): Classroom[] {
  return SERVER_SNAPSHOT
}

function getServerSelected(): string | null {
  return null
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function notify(): void {
  for (const listener of listeners) listener()
}

function persistClassrooms(next: Classroom[]): void {
  classroomCache = next.slice(0, MAX_CLASSROOMS)
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(classroomCache))
  } catch {
    // Keep the in-memory workspace usable when storage is blocked or full.
  }
  notify()
}

function persistSelected(id: string | null): void {
  selectedClassroomCache = id
  try {
    if (id) window.localStorage.setItem(SELECTED_KEY, id)
    else window.localStorage.removeItem(SELECTED_KEY)
  } catch {
    // Selection is still available for the current session.
  }
  notify()
}

function fromServer(
  record: ClassroomRecord,
  previous?: Classroom,
  members: ClassroomMember[] = previous?.members ?? [],
): Classroom {
  return {
    id: record.id,
    name: record.name,
    subject: previous?.subject || record.description || "General studies",
    join_code: record.join_code || previous?.join_code || "",
    created_at: record.created_at || previous?.created_at || nowIso(),
    members,
    active_room_id: previous?.active_room_id,
  }
}

function membersFromServer(rows: ClassroomMemberRecord[], previous: Classroom | undefined): ClassroomMember[] {
  const owner = previous?.members.find((member) => member.role === "teacher")
  const members: ClassroomMember[] = rows.map((member) => ({
    id: member.user_id,
    name: member.name,
    email: member.email,
    role: member.role,
    status: "active" as const,
    joined_at: member.joined_at,
  }))
  if (owner && !members.some((member) => member.id === owner.id)) members.unshift(owner)
  return members
}

export function mergeJoinedMember(
  members: ClassroomMember[],
  nextMember: ClassroomMember,
  email?: string,
): ClassroomMember[] {
  return members.some((item) => item.id === nextMember.id || (email && item.email === email))
    ? members.map((item) => (item.id === nextMember.id || (email && item.email === email) ? { ...item, status: "active" as const } : item))
    : [...members, nextMember]
}

function replaceClassroom(previousId: string, next: Classroom): Classroom {
  persistClassrooms([next, ...getClassroomSnapshot().filter((item) => item.id !== previousId && item.id !== next.id)])
  if (getSelectedSnapshot() === previousId) persistSelected(next.id)
  return next
}

export async function syncClassroomsFromApi(): Promise<void> {
  try {
    const records = await api.listClassrooms()
    const local = getClassroomSnapshot()
    const remoteIds = new Set(records.map((record) => record.id))
    const merged = [
      ...records.map((record) => fromServer(record, local.find((item) => item.id === record.id))),
      ...local.filter((item) => !remoteIds.has(item.id)),
    ]
    persistClassrooms(merged)
  } catch {
    // Offline-first fallback: keep local classes available when the API is down.
  }
}

export async function createClassroomOnServer(local: Classroom): Promise<Classroom | null> {
  try {
    const remote = await api.createClassroom({ name: local.name, description: local.subject })
    return replaceClassroom(local.id, fromServer(remote, local, local.members))
  } catch {
    return null
  }
}

export async function joinClassroomOnServer(code: string, member: MemberInput): Promise<Classroom | null> {
  try {
    const remote = await api.joinClassroom(code)
    const existing = getClassroom(remote.id)
    const email = member.email?.trim().toLowerCase()
    const nextMember = memberFromInput({ ...member, email, role: "student", status: "active" })
    const members = existing?.members ?? []
    const mergedMembers = mergeJoinedMember(members, nextMember, email)
    const next = fromServer(remote, existing ?? undefined, mergedMembers)
    persistClassrooms([next, ...getClassroomSnapshot().filter((item) => item.id !== next.id)])
    persistSelected(next.id)
    return next
  } catch {
    return null
  }
}

export async function syncClassroomRosterFromApi(classroomId: string): Promise<Classroom | null> {
  try {
    const rows = await api.classroomRoster(classroomId)
    const classroom = getClassroom(classroomId)
    if (!classroom) return null
    const next = { ...classroom, members: membersFromServer(rows, classroom) }
    persistClassrooms(getClassroomSnapshot().map((item) => (item.id === classroomId ? next : item)))
    return next
  } catch {
    return null
  }
}

export async function removeClassroomMemberOnServer(classroomId: string, memberId: string): Promise<boolean> {
  try {
    await api.removeClassroomMember(classroomId, memberId)
    return true
  } catch {
    return false
  }
}

function memberFromInput(input: MemberInput, fallbackRole: Role = "student"): ClassroomMember {
  return {
    id: input.id ?? randomId("member"),
    name: input.name.trim(),
    email: input.email?.trim() ?? "",
    role: input.role ?? fallbackRole,
    status: input.status ?? "active",
    joined_at: nowIso(),
  }
}

export function newClassJoinCode(): string {
  const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
  const bytes = new Uint8Array(6)
  if (typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function") {
    crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 255)
    }
  }
  return Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join("")
}

export function createClassroom(input: {
  name: string
  subject?: string
  owner: MemberInput
}): Classroom {
  const createdAt = nowIso()
  const classroom: Classroom = {
    id: randomId("class"),
    name: input.name.trim(),
    subject: input.subject?.trim() || "General studies",
    join_code: newClassJoinCode(),
    created_at: createdAt,
    members: [memberFromInput({ ...input.owner, role: "teacher", status: "active" }, "teacher")],
  }
  persistClassrooms([classroom, ...getClassroomSnapshot()])
  persistSelected(classroom.id)
  return classroom
}

export function addClassroomMember(classroomId: string, input: MemberInput): Classroom | null {
  const classroom = getClassroom(classroomId)
  if (!classroom) return null

  const email = input.email?.trim().toLowerCase()
  const duplicate = classroom.members.some(
    (member) =>
      (email && member.email.toLowerCase() === email) ||
      (!email && member.name.toLowerCase() === input.name.trim().toLowerCase()),
  )
  if (duplicate) return classroom

  const updated: Classroom = {
    ...classroom,
    members: [...classroom.members, memberFromInput({ ...input, email })],
  }
  persistClassrooms(getClassroomSnapshot().map((item) => (item.id === classroomId ? updated : item)))
  return updated
}

export function joinClassroom(code: string, member: MemberInput): Classroom | null {
  const normalised = code.trim().toUpperCase()
  const classroom = getClassroomSnapshot().find((item) => item.join_code === normalised)
  if (!classroom) return null

  const email = member.email?.trim().toLowerCase()
  const existing = classroom.members.find(
    (item) => (email && item.email.toLowerCase() === email) || item.id === member.id,
  )
  const updated = existing
    ? {
        ...classroom,
        members: classroom.members.map((item) =>
          item.id === existing.id ? { ...item, status: "active" as const } : item,
        ),
      }
    : {
        ...classroom,
        members: [...classroom.members, memberFromInput({ ...member, email, role: "student" })],
      }

  persistClassrooms(getClassroomSnapshot().map((item) => (item.id === classroom.id ? updated : item)))
  persistSelected(classroom.id)
  return updated
}

export function removeClassroomMember(classroomId: string, memberId: string): Classroom | null {
  const classroom = getClassroom(classroomId)
  if (!classroom || classroom.members.length <= 1) return classroom
  const owner = classroom.members.find((member) => member.role === "teacher")
  if (owner?.id === memberId) return classroom

  const updated = { ...classroom, members: classroom.members.filter((member) => member.id !== memberId) }
  persistClassrooms(getClassroomSnapshot().map((item) => (item.id === classroomId ? updated : item)))
  return updated
}

export function setClassroomActiveRoom(classroomId: string, roomId: string | undefined): void {
  const updated = getClassroomSnapshot().map((classroom) =>
    classroom.id === classroomId ? { ...classroom, active_room_id: roomId } : classroom,
  )
  persistClassrooms(updated)
}

export function selectClassroom(classroomId: string | null): void {
  const exists = classroomId ? getClassroomSnapshot().some((item) => item.id === classroomId) : true
  persistSelected(exists ? classroomId : null)
}

export function getClassroom(classroomId: string): Classroom | null {
  return getClassroomSnapshot().find((classroom) => classroom.id === classroomId) ?? null
}

/** Imperative selection read for non-React consumers such as hydration helpers. */
export function getSelectedClassroomId(): string | null {
  return getSelectedSnapshot()
}

export function useClassrooms(): Classroom[] {
  return useSyncExternalStore(subscribe, getClassroomSnapshot, getServerClassrooms)
}

export function useSelectedClassroom(): Classroom | null {
  const classrooms = useClassrooms()
  const selectedId = useSyncExternalStore(subscribe, getSelectedSnapshot, getServerSelected)
  const selected = classrooms.find((classroom) => classroom.id === selectedId)
  if (selected) return selected
  return classrooms.length > 0 ? classrooms[0] : null
}
