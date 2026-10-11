import { clearAuth, getAccessToken, getRefreshToken, setAuth } from "./auth"
import type {
  AdminStatus,
  ContinuumAction,
  ContinuumStatus,
  BoardElement,
  Exam,
  ExamDraft,
  ExamMeta,
  ExamResultsResponse,
  AttemptSaveResult,
  AttemptSnapshot,
  QuizDraft,
  ResultResponse,
  Submission,
  SubmitResult,
  QuestionDraft,
} from "./types"
import type { AuthUser } from "./auth"
import type { QuestionBankItem } from "./question-bank"

const BASE = (
  import.meta.env.VITE_API_ORIGIN ??
  import.meta.env.VITE_API_BASE_URL ??
  "/api"
).replace(/\/$/, "")

export class ApiError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
  }
}

export interface ClassroomRecord {
  id: string
  owner_id: string
  name: string
  description?: string
  join_code?: string
  created_at: string
  updated_at: string
  member_count: number
}

export interface ClassroomMemberRecord {
  classroom_id: string
  user_id: string
  name: string
  email: string
  role: "teacher" | "student"
  joined_at: string
}

export interface ClassroomInvitation {
  id: string
  classroom_id: string
  name: string
  email: string
  status: "pending" | "accepted"
  created_at: string
  join_code: string
  delivery?: "sent" | "not_configured" | "failed"
}

interface TokenResponse {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
  user: { user_id: string; email: string; name: string; role: "student" | "teacher" }
}

let refreshInFlight: Promise<boolean> | null = null

async function refreshTokens(): Promise<boolean> {
  refreshInFlight ??= (async () => {
    const refreshToken = getRefreshToken()
    if (!refreshToken) return false
    try {
      const response = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      })
      if (!response.ok) {
        clearAuth()
        return false
      }
      const tokens = (await response.json()) as TokenResponse
      setAuth({
        user: tokens.user,
        accessToken: tokens.access_token,
        refreshToken: tokens.refresh_token,
      })
      return true
    } catch {
      // Network hiccup — keep the session; the next call retries.
      return false
    } finally {
      refreshInFlight = null
    }
  })()
  return refreshInFlight
}

async function rawRequest(path: string, init?: RequestInit): Promise<Response> {
  const token = getAccessToken()
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string> | undefined),
  }
  if (token && !headers.Authorization) headers.Authorization = `Bearer ${token}`
  return fetch(`${BASE}${path}`, { ...init, headers })
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response = await rawRequest(path, init)
  if (response.status === 401 && getRefreshToken()) {
    const refreshed = await refreshTokens()
    if (refreshed) response = await rawRequest(path, init)
  }
  if (!response.ok) {
    let detail = `Request failed (${response.status})`
    let code: string | undefined
    try {
      const body = await response.json()
      if (typeof body?.detail === "string") {
        detail = body.detail
      } else if (typeof body?.detail?.message === "string") {
        detail = body.detail.message
        if (typeof body.detail.code === "string") code = body.detail.code
      }
    } catch {
      // non-JSON error body — keep the generic message
    }
    throw new ApiError(detail, response.status, code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

function adminHeaders(token: string): Record<string, string> {
  return { "x-admin-token": token }
}

export const api = {
  register: (payload: { email: string; password: string; name?: string }) =>
    request<TokenResponse>("/auth/register", { method: "POST", body: JSON.stringify(payload) }),

  login: (payload: { email: string; password: string }) =>
    request<TokenResponse>("/auth/login", { method: "POST", body: JSON.stringify(payload) }),

  updateProfile: (payload: { name: string }) =>
    request<AuthUser>("/auth/me", { method: "PATCH", body: JSON.stringify(payload) }),

  changePassword: (payload: { current_password: string; password: string }) =>
    request<{ message: string }>("/auth/change-password", { method: "POST", body: JSON.stringify(payload) }),

  forgotPassword: (email: string) =>
    request<{ message: string }>("/auth/forgot-password", { method: "POST", body: JSON.stringify({ email }) }),

  resetPassword: (payload: { token: string; password: string }) =>
    request<{ message: string }>("/auth/reset-password", { method: "POST", body: JSON.stringify(payload) }),

  listExams: () => request<ExamMeta[]>("/exams"),

  listClassrooms: () => request<ClassroomRecord[]>("/classrooms"),

  createClassroom: (payload: { name: string; description?: string; client_id?: string }) =>
    request<ClassroomRecord>("/classrooms", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  updateClassroom: (id: string, payload: { name?: string; description?: string }) =>
    request<ClassroomRecord>(`/classrooms/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(payload) }),

  deleteClassroom: (id: string) =>
    request<void>(`/classrooms/${encodeURIComponent(id)}`, { method: "DELETE" }),

  leaveClassroom: (id: string) =>
    request<void>(`/classrooms/${encodeURIComponent(id)}/leave`, { method: "POST" }),

  inviteClassroomMember: (id: string, payload: { name: string; email: string }) =>
    request<ClassroomInvitation>(`/classrooms/${encodeURIComponent(id)}/invitations`, { method: "POST", body: JSON.stringify(payload) }),

  classroomInvitations: (id: string) =>
    request<ClassroomInvitation[]>(`/classrooms/${encodeURIComponent(id)}/invitations`),

  deleteClassroomInvitation: (id: string, invitationId: string) =>
    request<void>(`/classrooms/${encodeURIComponent(id)}/invitations/${encodeURIComponent(invitationId)}`, { method: "DELETE" }),

  joinClassroom: (joinCode: string) =>
    request<ClassroomRecord>("/classrooms/join", {
      method: "POST",
      body: JSON.stringify({ join_code: joinCode }),
    }),

  classroomRoster: (classroomId: string) =>
    request<ClassroomMemberRecord[]>(`/classrooms/${encodeURIComponent(classroomId)}/roster`),

  removeClassroomMember: (classroomId: string, memberId: string) =>
    request<void>(`/classrooms/${encodeURIComponent(classroomId)}/members/${encodeURIComponent(memberId)}`, {
      method: "DELETE",
    }),

  boardHistory: (roomId: string, limit = 50) =>
    request<Array<{ room_id: string; version: number; element_count: number; saved_at?: string }>>(
      `/boards/${encodeURIComponent(roomId)}/history?limit=${limit}`,
    ),

  boardExport: (roomId: string) =>
    request<{ schema_version: number; room_id: string; version: number; elements: BoardElement[] }>(
      `/boards/${encodeURIComponent(roomId)}/export`,
    ),

  boardRestore: (roomId: string, version: number) =>
    request<{ room_id: string; version: number; elements: BoardElement[] }>(
      `/boards/${encodeURIComponent(roomId)}/restore`,
      { method: "POST", body: JSON.stringify({ version }) },
    ),

  getExam: (examId: string) => request<Exam>(`/exam/${encodeURIComponent(examId)}`),

  examInfo: (examId: string) => request<ExamMeta>(`/exam/${encodeURIComponent(examId)}/info`),

  previewExam: (examId: string) =>
    request<Exam>(`/exam/${encodeURIComponent(examId)}/preview`),

  createExam: (draft: ExamDraft) =>
    request<{ id: string; title: string; question_count: number }>("/exam", {
      method: "POST",
      body: JSON.stringify(draft),
    }),

  listQuizDrafts: () => request<QuizDraft[]>("/quiz/drafts"),

  listQuestionBank: () => request<QuestionBankItem[]>("/question-bank"),

  createBankQuestion: (question: QuestionDraft) =>
    request<QuestionBankItem>("/question-bank", { method: "POST", body: JSON.stringify(question) }),

  deleteBankQuestion: (id: string) =>
    request<void>(`/question-bank/${encodeURIComponent(id)}`, { method: "DELETE" }),

  getQuizDraft: (draftId: string) =>
    request<QuizDraft>(`/quiz/drafts/${encodeURIComponent(draftId)}`),

  createQuizDraft: (draft: ExamDraft) =>
    request<QuizDraft>("/quiz/drafts", {
      method: "POST",
      body: JSON.stringify(draft),
    }),

  updateQuizDraft: (draftId: string, draft: ExamDraft) =>
    request<QuizDraft>(`/quiz/drafts/${encodeURIComponent(draftId)}`, {
      method: "PATCH",
      body: JSON.stringify(draft),
    }),

  deleteQuizDraft: (draftId: string) =>
    request<void>(`/quiz/drafts/${encodeURIComponent(draftId)}`, { method: "DELETE" }),

  publishQuizDraft: (draftId: string) =>
    request<ExamMeta>(`/quiz/drafts/${encodeURIComponent(draftId)}/publish`, { method: "POST" }),

  duplicateQuiz: (payload: { source_id: string; id?: string; title?: string }) =>
    request<QuizDraft>("/quiz/duplicate", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  duplicateQuizDraft: (draftId: string, payload?: { id?: string; title?: string }) =>
    request<QuizDraft>(`/quiz/drafts/${encodeURIComponent(draftId)}/duplicate`, {
      method: "POST",
      body: JSON.stringify(payload ?? {}),
    }),

  getAttempt: (examId: string) =>
    request<AttemptSnapshot>(`/exam/${encodeURIComponent(examId)}/attempt`),

  saveAttempt: (
    examId: string,
    payload: { answers: Record<string, number>; expected_version: number; request_id: string },
  ) => request<AttemptSaveResult>(`/exam/${encodeURIComponent(examId)}/attempt`, {
    method: "PUT",
    body: JSON.stringify(payload),
  }),

  submitSavedAttempt: (examId: string, requestId?: string) =>
    request<SubmitResult>(`/exam/${encodeURIComponent(examId)}/attempt/submit`, {
      method: "POST",
      body: JSON.stringify({ request_id: requestId }),
    }),

  submitAnswers: (
    examId: string,
    payload: { answers: Record<string, number>; request_id?: string },
  ) => request<SubmitResult>(`/exam/${encodeURIComponent(examId)}/submit`, {
    method: "POST",
    body: JSON.stringify(payload),
  }),

  myResult: (examId: string) =>
    request<ResultResponse>(`/exam/${encodeURIComponent(examId)}/result`),

  patchExam: (
    examId: string,
    payload: Partial<Omit<ExamDraft, "id" | "questions">>,
  ) => request<ExamMeta>(`/exam/${encodeURIComponent(examId)}`, {
    method: "PATCH",
    body: JSON.stringify(payload),
  }),

  closeExam: (examId: string) =>
    request<{ id: string; closed_at: string }>(`/exam/${encodeURIComponent(examId)}/close`, {
      method: "POST",
    }),

  deleteExam: (examId: string) =>
    request<void>(`/exam/${encodeURIComponent(examId)}`, { method: "DELETE" }),

  examResults: (examId: string) =>
    request<ExamResultsResponse>(`/admin/exams/${encodeURIComponent(examId)}/results`),

  submissions: (token: string, limit = 100) =>
    request<Submission[]>(`/admin/submissions?limit=${limit}`, {
      headers: adminHeaders(token),
    }),

  status: (token: string) => request<AdminStatus>("/admin/status", {
    headers: adminHeaders(token),
  }),

  continuumStatus: (token: string) => request<ContinuumStatus>("/continuum/status", {
    headers: adminHeaders(token),
  }),

  continuumAction: (token: string, action: ContinuumAction, backend?: string) =>
    request<ContinuumStatus | Record<string, unknown>>("/continuum/action", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify({ action, ...(backend ? { backend } : {}) }),
    }),

  setChaos: (
    token: string,
    payload: { error_pct?: number; latency_ms?: number; db_stall?: boolean },
  ) => request<AdminStatus["chaos"]>("/admin/chaos", {
    method: "POST",
    headers: adminHeaders(token),
    body: JSON.stringify(payload),
  }),

  resetChaos: (token: string) =>
    request<AdminStatus["chaos"]>("/admin/chaos/reset", {
      method: "POST",
      headers: adminHeaders(token),
    }),

  promote: (token: string) =>
    request<{ region: string; role: string }>("/admin/promote", {
      method: "POST",
      headers: adminHeaders(token),
    }),
}

export function boardSocketUrl(roomId: string, token: string | null): string {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws"
  const query = token ? `?token=${encodeURIComponent(token)}` : ""
  return `${scheme}://${window.location.host}/ws/board/${encodeURIComponent(roomId)}${query}`
}

export function hasExternalApiOrigin(
  apiBase = BASE,
  appOrigin = window.location.origin
): boolean {
  try {
    return new URL(apiBase, appOrigin).origin !== new URL(appOrigin).origin
  } catch {
    return false
  }
}
