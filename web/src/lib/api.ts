import { clearAuth, getAccessToken, getRefreshToken, setAuth } from "./auth"
import type {
  AdminStatus,
  Exam,
  ExamDraft,
  ExamMeta,
  ExamResultsResponse,
  ResultResponse,
  Submission,
  SubmitResult,
} from "./types"

const BASE = "/api"

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

  listExams: () => request<ExamMeta[]>("/exams"),

  getExam: (examId: string) => request<Exam>(`/exam/${encodeURIComponent(examId)}`),

  createExam: (draft: ExamDraft) =>
    request<{ id: string; title: string; question_count: number }>("/exam", {
      method: "POST",
      body: JSON.stringify(draft),
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
    payload: { title?: string; opens_at?: string; closes_at?: string; duration_minutes?: number },
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
