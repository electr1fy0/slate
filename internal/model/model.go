// Package model defines persistence and business values shared by backend
// components. The dynamodbav tags intentionally document the existing table.
package model

type User struct {
	PK             string `json:"-" dynamodbav:"pk"`
	SK             string `json:"-" dynamodbav:"sk"`
	UserID         string `json:"user_id" dynamodbav:"user_id"`
	Email          string `json:"email" dynamodbav:"email"`
	Name           string `json:"name" dynamodbav:"name"`
	Role           string `json:"role" dynamodbav:"role"`
	PasswordHash   string `json:"-" dynamodbav:"password_hash"`
	SessionVersion int64  `json:"-" dynamodbav:"session_version,omitempty"`
	CreatedAt      string `json:"created_at" dynamodbav:"created_at"`
}

// Classroom is a teacher-owned class space. PK and SK are the canonical
// single-table keys; ID is populated for API responses and is deliberately
// not duplicated in DynamoDB.
type Classroom struct {
	PK          string `json:"-" dynamodbav:"pk"`
	SK          string `json:"-" dynamodbav:"sk"`
	ID          string `json:"id,omitempty" dynamodbav:"-"`
	OwnerID     string `json:"owner_id" dynamodbav:"owner_id"`
	Name        string `json:"name" dynamodbav:"name"`
	Description string `json:"description,omitempty" dynamodbav:"description,omitempty"`
	JoinCode    string `json:"join_code,omitempty" dynamodbav:"join_code"`
	CreatedAt   string `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt   string `json:"updated_at" dynamodbav:"updated_at"`
	MemberCount int    `json:"member_count" dynamodbav:"member_count"`
}

// ClassroomPatch contains only fields that a classroom owner may change.
// Pointer fields distinguish an omitted field from an intentional empty
// description.
type ClassroomPatch struct {
	Name        *string
	Description *string
}

// ClassroomMember is a student roster entry. Name and Email are denormalized
// at join time so roster reads do not require one user lookup per member.
type ClassroomMember struct {
	PK          string `json:"-" dynamodbav:"pk"`
	SK          string `json:"-" dynamodbav:"sk"`
	ClassroomID string `json:"classroom_id" dynamodbav:"classroom_id"`
	UserID      string `json:"user_id" dynamodbav:"user_id"`
	Name        string `json:"name" dynamodbav:"name"`
	Email       string `json:"email" dynamodbav:"email"`
	Role        string `json:"role" dynamodbav:"role"`
	JoinedAt    string `json:"joined_at" dynamodbav:"joined_at"`
}

// ClassroomInvitation is a teacher-created invitation code. The raw join code
// is returned to classroom owners so they can share a link; password reset
// tokens use a different, hashed-at-rest representation.
type ClassroomInvitation struct {
	PK          string `json:"-" dynamodbav:"pk"`
	SK          string `json:"-" dynamodbav:"sk"`
	ID          string `json:"id" dynamodbav:"id"`
	ClassroomID string `json:"classroom_id" dynamodbav:"classroom_id"`
	Name        string `json:"name" dynamodbav:"name"`
	Email       string `json:"email" dynamodbav:"email"`
	Status      string `json:"status" dynamodbav:"status"`
	CreatedAt   string `json:"created_at" dynamodbav:"created_at"`
	JoinCode    string `json:"join_code" dynamodbav:"join_code"`
	Delivery    string `json:"delivery,omitempty" dynamodbav:"-"`
	AcceptedBy  string `json:"-" dynamodbav:"accepted_by,omitempty"`
	AcceptedAt  string `json:"-" dynamodbav:"accepted_at,omitempty"`
}

type EmailIndex struct {
	PK     string `dynamodbav:"pk"`
	SK     string `dynamodbav:"sk"`
	UserID string `dynamodbav:"user_id"`
	TTL    int64  `dynamodbav:"ttl"`
}

type Question struct {
	Index    int      `json:"index" dynamodbav:"-"`
	Question string   `json:"question" dynamodbav:"question"`
	Options  []string `json:"options" dynamodbav:"options"`
	Answer   int      `json:"answer,omitempty" dynamodbav:"answer"`
	Points   int      `json:"points,omitempty" dynamodbav:"points,omitempty"`
	Feedback string   `json:"feedback,omitempty" dynamodbav:"feedback,omitempty"`
}

type ExamMeta struct {
	PK               string `json:"-" dynamodbav:"pk"`
	SK               string `json:"-" dynamodbav:"sk"`
	ID               string `json:"id,omitempty" dynamodbav:"-"`
	Title            string `json:"title" dynamodbav:"title"`
	CreatedAt        string `json:"created_at" dynamodbav:"created_at"`
	OwnerID          string `json:"owner_id,omitempty" dynamodbav:"owner_id,omitempty"`
	ClassroomID      string `json:"classroom_id,omitempty" dynamodbav:"classroom_id,omitempty"`
	Instructions     string `json:"instructions,omitempty" dynamodbav:"instructions,omitempty"`
	Status           string `json:"status,omitempty" dynamodbav:"status,omitempty"`
	QuestionCount    int    `json:"question_count" dynamodbav:"question_count"`
	OpensAt          string `json:"opens_at,omitempty" dynamodbav:"opens_at,omitempty"`
	ClosesAt         string `json:"closes_at,omitempty" dynamodbav:"closes_at,omitempty"`
	DurationMinutes  int    `json:"duration_minutes,omitempty" dynamodbav:"duration_minutes,omitempty"`
	MaxAttempts      int    `json:"max_attempts,omitempty" dynamodbav:"max_attempts,omitempty"`
	AllowLate        bool   `json:"allow_late,omitempty" dynamodbav:"allow_late,omitempty"`
	LateGraceMinutes int    `json:"late_grace_minutes,omitempty" dynamodbav:"late_grace_minutes,omitempty"`
	AttemptStatus    string `json:"attempt_status,omitempty" dynamodbav:"-"`
	Score            int    `json:"score,omitempty" dynamodbav:"-"`
	Total            int    `json:"total,omitempty" dynamodbav:"-"`
	SubmittedAt      string `json:"submitted_at,omitempty" dynamodbav:"-"`
	AttemptNumber    int    `json:"attempt_number,omitempty" dynamodbav:"-"`
}

type Exam struct {
	ExamMeta
	Questions        []Question `json:"questions" dynamodbav:"-"`
	Attempt          *Attempt   `json:"attempt,omitempty" dynamodbav:"-"`
	AttemptStartedAt int64      `json:"attempt_started_at,omitempty" dynamodbav:"-"`
	DeadlineMS       int64      `json:"deadline_ms,omitempty" dynamodbav:"-"`
}

// QuizDraft is the teacher-owned, unpublished representation of a quiz. The
// questions are stored as separate single-table items and are intentionally
// excluded from the draft metadata item.
type QuizDraft struct {
	PK               string     `json:"-" dynamodbav:"pk"`
	SK               string     `json:"-" dynamodbav:"sk"`
	ID               string     `json:"id" dynamodbav:"-"`
	OwnerID          string     `json:"owner_id" dynamodbav:"owner_id"`
	ClassroomID      string     `json:"classroom_id,omitempty" dynamodbav:"classroom_id,omitempty"`
	Title            string     `json:"title" dynamodbav:"title"`
	Instructions     string     `json:"instructions,omitempty" dynamodbav:"instructions,omitempty"`
	Questions        []Question `json:"questions" dynamodbav:"-"`
	QuestionCount    int        `json:"question_count" dynamodbav:"question_count"`
	OpensAt          string     `json:"opens_at,omitempty" dynamodbav:"opens_at,omitempty"`
	ClosesAt         string     `json:"closes_at,omitempty" dynamodbav:"closes_at,omitempty"`
	DurationMinutes  int        `json:"duration_minutes,omitempty" dynamodbav:"duration_minutes,omitempty"`
	MaxAttempts      int        `json:"max_attempts,omitempty" dynamodbav:"max_attempts,omitempty"`
	AllowLate        bool       `json:"allow_late,omitempty" dynamodbav:"allow_late,omitempty"`
	LateGraceMinutes int        `json:"late_grace_minutes,omitempty" dynamodbav:"late_grace_minutes,omitempty"`
	Status           string     `json:"status" dynamodbav:"status"`
	CreatedAt        string     `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt        string     `json:"updated_at" dynamodbav:"updated_at"`
	PublishedAt      string     `json:"published_at,omitempty" dynamodbav:"published_at,omitempty"`
}

// QuestionBankItem is a reusable teacher-owned authored question.
type QuestionBankItem struct {
	PK       string   `json:"-" dynamodbav:"pk"`
	SK       string   `json:"-" dynamodbav:"sk"`
	ID       string   `json:"id" dynamodbav:"id"`
	OwnerID  string   `json:"-" dynamodbav:"owner_id"`
	SavedAt  string   `json:"saved_at" dynamodbav:"saved_at"`
	Question string   `json:"question" dynamodbav:"question"`
	Options  []string `json:"options" dynamodbav:"options"`
	Answer   int      `json:"answer" dynamodbav:"answer"`
	Points   int      `json:"points,omitempty" dynamodbav:"points,omitempty"`
	Feedback string   `json:"feedback,omitempty" dynamodbav:"feedback,omitempty"`
}

// PasswordResetRecord stores no raw reset token. The partition key contains
// the SHA-256 digest and the value is single-use and time limited.
type PasswordResetRecord struct {
	PK        string `json:"-" dynamodbav:"pk"`
	SK        string `json:"-" dynamodbav:"sk"`
	UserID    string `json:"-" dynamodbav:"user_id"`
	ExpiresAt int64  `json:"-" dynamodbav:"expires_at"`
	CreatedAt string `json:"-" dynamodbav:"created_at"`
}

type Attempt struct {
	PK            string         `json:"-" dynamodbav:"pk"`
	SK            string         `json:"-" dynamodbav:"sk"`
	AttemptNumber int            `json:"attempt_number" dynamodbav:"attempt_number"`
	StartedAt     int64          `json:"started_at" dynamodbav:"started_at"`
	StartedAtISO  string         `json:"started_at_iso" dynamodbav:"started_at_iso"`
	Answers       map[string]int `json:"answers,omitempty" dynamodbav:"answers,omitempty"`
	Version       int            `json:"version" dynamodbav:"version"`
	LastRequestID string         `json:"last_request_id,omitempty" dynamodbav:"last_request_id,omitempty"`
	UpdatedAt     int64          `json:"updated_at,omitempty" dynamodbav:"updated_at,omitempty"`
	UpdatedAtISO  string         `json:"updated_at_iso,omitempty" dynamodbav:"updated_at_iso,omitempty"`
	Submitted     bool           `json:"submitted,omitempty" dynamodbav:"submitted,omitempty"`
	SubmittedAt   int64          `json:"submitted_at,omitempty" dynamodbav:"submitted_at,omitempty"`
}

// SubmissionRequest is the durable idempotency record for one final-submit
// request ID. Its receipt fields make a retry independent of the application's
// local state and of DynamoDB's temporary transaction client-token window.
type SubmissionRequest struct {
	PK            string `json:"-" dynamodbav:"pk"`
	SK            string `json:"-" dynamodbav:"sk"`
	RequestID     string `json:"request_id" dynamodbav:"request_id"`
	PayloadHash   string `json:"-" dynamodbav:"payload_hash"`
	ExamID        string `json:"-" dynamodbav:"exam_id"`
	StudentID     string `json:"-" dynamodbav:"student_id"`
	Version       int    `json:"version" dynamodbav:"version"`
	CommittedAt   int64  `json:"committed_at" dynamodbav:"committed_at"`
	Score         int    `json:"score" dynamodbav:"score"`
	Total         int    `json:"total" dynamodbav:"total"`
	AttemptNumber int    `json:"attempt_number,omitempty" dynamodbav:"attempt_number,omitempty"`
}

func (r SubmissionRequest) Result(status string) SubmissionResult {
	return SubmissionResult{Status: status, RequestID: r.RequestID, Version: r.Version,
		CommittedAt: r.CommittedAt, Score: r.Score, Total: r.Total, AttemptNumber: r.AttemptNumber}
}

type Submission struct {
	PK              string         `json:"-" dynamodbav:"pk"`
	SK              string         `json:"-" dynamodbav:"sk"`
	Answers         map[string]int `json:"answers" dynamodbav:"answers"`
	Version         int            `json:"version" dynamodbav:"version"`
	RequestID       string         `json:"request_id" dynamodbav:"request_id"`
	PayloadHash     string         `json:"-" dynamodbav:"payload_hash,omitempty"`
	UpdatedAt       int64          `json:"updated_at" dynamodbav:"updated_at"`
	UpdatedAtISO    string         `json:"updated_at_iso" dynamodbav:"updated_at_iso"`
	WrittenByRegion string         `json:"written_by_region" dynamodbav:"written_by_region"`
	StudentName     string         `json:"student_name" dynamodbav:"student_name"`
	AttemptNumber   int            `json:"attempt_number,omitempty" dynamodbav:"attempt_number,omitempty"`
	Score           int            `json:"score" dynamodbav:"score"`
	Total           int            `json:"total" dynamodbav:"total"`
}

type OutboxEntry struct {
	PK            string         `json:"pk" dynamodbav:"pk"`
	SK            string         `json:"sk" dynamodbav:"sk"`
	TargetPK      string         `json:"target_pk" dynamodbav:"target_pk"`
	TargetSK      string         `json:"target_sk" dynamodbav:"target_sk"`
	Version       int            `json:"version" dynamodbav:"version"`
	RequestID     string         `json:"request_id" dynamodbav:"request_id"`
	CreatedAt     int64          `json:"created_at" dynamodbav:"created_at"`
	Answers       map[string]int `json:"answers" dynamodbav:"answers"`
	StudentName   string         `json:"student_name" dynamodbav:"student_name"`
	AttemptNumber int            `json:"attempt_number,omitempty" dynamodbav:"attempt_number,omitempty"`
	Score         int            `json:"score" dynamodbav:"score"`
	Total         int            `json:"total" dynamodbav:"total"`
	TTL           int64          `json:"ttl" dynamodbav:"ttl"`
	Processed     bool           `json:"processed" dynamodbav:"processed"`
}

// AuditSummary is the answer-free, request-ID keyed local equivalent of the
// summary item written by the demo outbox reconciler.
type AuditSummary struct {
	RequestID   string `json:"request_id"`
	TargetPK    string `json:"target_pk"`
	TargetSK    string `json:"target_sk"`
	ProcessedAt string `json:"processed_at"`
	Score       int    `json:"score"`
	Total       int    `json:"total"`
}

type SubmissionResult struct {
	Status        string `json:"status"`
	RequestID     string `json:"request_id"`
	Version       int    `json:"version"`
	CommittedAt   int64  `json:"committed_at,omitempty"`
	Score         int    `json:"score"`
	Total         int    `json:"total"`
	AttemptNumber int    `json:"attempt_number,omitempty"`
}

type AttemptSaveResult struct {
	Status     string   `json:"status"`
	RequestID  string   `json:"request_id"`
	Attempt    *Attempt `json:"-"`
	DeadlineMS int64    `json:"deadline_ms,omitempty"`
}
