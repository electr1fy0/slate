export type Role = "teacher" | "student"

export interface ExamMeta {
  id: string
  title: string
  question_count: number
  created_at: string
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
