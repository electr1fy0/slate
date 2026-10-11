export type Role = "teacher" | "student"

export interface ExamMeta {
  id: string
  title: string
  question_count: number
  created_at: string
  owner_id?: string
  status?: "draft" | "published" | string
  opens_at?: string
  closes_at?: string
  duration_minutes?: number
  max_attempts?: number
  allow_late?: boolean
  late_grace_minutes?: number
  classroom_id?: string
  instructions?: string
  attempt_status?: "not_started" | "in_progress" | "submitted"
  attempt_number?: number
  score?: number
  total?: number
  submitted_at?: string
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
  answer?: number
  points?: number
  feedback?: string
}

export interface Exam {
  id: string
  title: string
  question_count: number
  questions: ExamQuestion[]
  attempt?: AttemptSnapshot
  attempt_started_at?: number
  deadline_ms?: number
  instructions?: string
  duration_minutes?: number
  max_attempts?: number
  opens_at?: string
  closes_at?: string
  classroom_id?: string
}

export interface QuestionDraft {
  question: string
  options: string[]
  answer: number
  points?: number
  feedback?: string
}

export interface ExamDraft {
  id?: string
  title: string
  questions: QuestionDraft[]
  opens_at?: string
  closes_at?: string
  duration_minutes?: number
  max_attempts?: number
  allow_late?: boolean
  late_grace_minutes?: number
  classroom_id?: string
  instructions?: string
}

export interface QuizDraft extends ExamDraft {
  id: string
  owner_id: string
  status: "draft" | "published" | string
  question_count: number
  created_at: string
  updated_at: string
  published_at?: string
}

export interface AttemptSnapshot {
  answers: Record<string, number>
  version: number
  started_at?: number
  updated_at?: number
  attempt_number?: number
  deadline_ms?: number
}

export interface AttemptSaveResult {
  status: "saved" | "duplicate"
  request_id: string
  answers: Record<string, number>
  version: number
  started_at?: number
  deadline_ms?: number
}

export interface SubmitResult {
  status: "committed" | "duplicate"
  request_id: string
  version: number
  score: number
  total: number
  attempt_number?: number
  committed_at?: number
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
  points?: number
  feedback?: string
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
  question_stats: QuestionStats[]
}

export interface QuestionStats {
  index: number
  question: string
  answered: number
  correct: number
  correct_pct: number | null
  option_counts: number[]
}

export interface AdminStatus {
  region: string
  role: "active" | "standby"
  boards: { active_rooms: number }
  chaos: { error_pct: number; latency_ms: number; db_stall: boolean }
}

export type ContinuumAction =
  | "crash"
  | "partial"
  | "blip"
  | "unsafe_standby"
  | "lost_response"
  | "reset"
  | "verify"
  | "failback"
  | "reconcile"

export interface ContinuumBackendStatus {
  id: "A" | "B" | string
  healthy: boolean
  liveness: boolean
  ready: boolean
  reason: string
  url?: string
}

export interface ContinuumEvent {
  id: string
  at: string
  type: string
  message: string
  backend?: string
  epoch?: number
}

export interface ContinuumStatus {
  mode: "local" | "aws" | string
  region: string
  active_backend: string
  epoch: number
  paused: boolean
  policy: {
    version: string
    confirmation_probes: number
    probe_interval_ms: number
  }
  backends: ContinuumBackendStatus[]
  events: ContinuumEvent[]
  metrics: {
    failovers: number
    suppressed: number
    recovery_ms: number | null
    reconciled?: number | boolean | null
    fenced_writes?: number | null
  }
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

export interface BoardRoomMetadata {
  room_id: string
  version: number
  students_draw: boolean
}

export interface BoardVersion {
  room_id: string
  version: number
  element_count: number
  saved_at?: string
}

export type ClientMessage =
  | { type: "join"; name: string; role: Role }
  | { type: "elements"; elements: BoardElement[] }
  | { type: "cursor"; x: number; y: number; tool: string }
  | { type: "permissions"; students_draw: boolean }

export type ServerMessage =
  | { type: "init"; you: string; elements: BoardElement[]; peers: PeerInfo[]; room?: BoardRoomMetadata }
  | { type: "elements"; elements: BoardElement[] }
  | { type: "restored"; elements: BoardElement[]; version: number }
  | { type: "permissions"; students_draw: boolean }
  | { type: "peer-joined"; peer: PeerInfo }
  | { type: "peer-left"; peer: PeerInfo }
  | { type: "cursor"; peer: PeerInfo }
  | { type: "pong" }
  | { type: "error"; code?: string; message?: string }
