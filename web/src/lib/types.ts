export type Role = "teacher" | "student"

export interface ExamMeta {
  id: string
  title: string
  question_count: number
  created_at: string
  opens_at?: string
  closes_at?: string
  duration_minutes?: number
}

export function examState(meta: Pick<ExamMeta, "opens_at" | "closes_at">,
                          now: number = Date.now()): "upcoming" | "open" | "closed" {
  if (meta.opens_at && now < Date.parse(meta.opens_at)) return "upcoming"
  if (meta.closes_at && now > Date.parse(meta.closes_at)) return "closed"
  return "open"
}

export interface ExamQuestion {
  index: number
  question: string
  options: string[]
}

export interface Exam {
  id: string
  title: string
  question_count: number
  questions: ExamQuestion[]
  attempt_started_at?: number
  deadline_ms?: number
}

export interface QuestionDraft {
  question: string
  options: string[]
  answer: number
}

export interface ExamDraft {
  id?: string
  title: string
  questions: QuestionDraft[]
}

export interface SubmitResult {
  status: "committed" | "duplicate"
  request_id: string
  version: number
  score: number
  total: number
}

export interface Submission {
  pk: string
  sk: string
  answers: Record<string, number>
  version: number
  updated_at: number
  updated_at_iso: string
  written_by_region: string
}

export interface ReviewQuestion {
  index: number
  question: string
  options: string[]
  chosen: number | null
  correct_answer: number | null
  was_correct: boolean
}

export interface ResultResponse {
  status: string
  score: number
  total: number
  answers: Record<string, number>
  submitted_at?: string
  request_id?: string
  review: ReviewQuestion[]
}

export interface ExamResultRow {
  student_id: string
  student_name: string
  answers: Record<string, number>
  score: number
  total: number
  submitted_at?: string
}

export interface ExamResultsResponse {
  exam_id: string
  count: number
  average_pct: number | null
  submissions: ExamResultRow[]
}

export interface AdminStatus {
  region: string
  role: "active" | "standby"
  boards: { active_rooms: number }
  chaos: { error_pct: number; latency_ms: number; db_stall: boolean }
}

export interface BoardElement {
  id: string
  version?: number
  [key: string]: unknown
}

export interface PeerCursor {
  x: number | null
  y: number | null
  tool: string | null
}

export interface PeerInfo {
  id: string
  name: string
  role: Role
  cursor: PeerCursor | null
}

export type ClientMessage =
  | { type: "join"; name: string; role: Role }
  | { type: "elements"; elements: BoardElement[] }
  | { type: "cursor"; x: number; y: number; tool: string }

export type ServerMessage =
  | { type: "init"; you: string; elements: BoardElement[]; peers: PeerInfo[] }
  | { type: "elements"; elements: BoardElement[] }
  | { type: "peer-joined"; peer: PeerInfo }
  | { type: "peer-left"; peer: PeerInfo }
  | { type: "cursor"; peer: PeerInfo }
  | { type: "pong" }
  | { type: "error"; code?: string; message?: string }
