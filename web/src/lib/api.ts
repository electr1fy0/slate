import type {
  AdminStatus,
  Exam,
  ExamDraft,
  ExamMeta,
  Submission,
  SubmitResult,
} from "./types"

const BASE = "/api"

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${BASE}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  })
  if (!response.ok) {
    let detail = `Request failed (${response.status})`
    try {
      const body = await response.json()
      if (typeof body?.detail === "string") detail = body.detail
    } catch {
      // non-JSON error body — keep the generic message
    }
    throw new ApiError(detail, response.status)
  }
  return response.json() as Promise<T>
}

function adminHeaders(token: string): Record<string, string> {
  return { "x-admin-token": token }
}

export const api = {
  listExams: () => request<ExamMeta[]>("/exams"),

  getExam: (examId: string) => request<Exam>(`/exam/${encodeURIComponent(examId)}`),

  createExam: (draft: ExamDraft, token: string) =>
    request<{ id: string; title: string; question_count: number }>("/exam", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(draft),
    }),

  submitAnswers: (
    examId: string,
    payload: { student_id: string; answers: Record<string, number>; request_id?: string },
  ) => request<SubmitResult>(`/exam/${encodeURIComponent(examId)}/submit`, {
    method: "POST",
    body: JSON.stringify(payload),
  }),

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

export function boardSocketUrl(roomId: string): string {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws"
  return `${scheme}://${window.location.host}/ws/board/${encodeURIComponent(roomId)}`
}
