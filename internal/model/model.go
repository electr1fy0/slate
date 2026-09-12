// Package model defines persistence and business values shared by backend
// components. The dynamodbav tags intentionally document the existing table.
package model

type User struct {
	PK           string `json:"-" dynamodbav:"pk"`
	SK           string `json:"-" dynamodbav:"sk"`
	UserID       string `json:"user_id" dynamodbav:"user_id"`
	Email        string `json:"email" dynamodbav:"email"`
	Name         string `json:"name" dynamodbav:"name"`
	Role         string `json:"role" dynamodbav:"role"`
	PasswordHash string `json:"-" dynamodbav:"password_hash"`
	CreatedAt    string `json:"created_at" dynamodbav:"created_at"`
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
}

type ExamMeta struct {
	PK              string `json:"-" dynamodbav:"pk"`
	SK              string `json:"-" dynamodbav:"sk"`
	ID              string `json:"id,omitempty" dynamodbav:"-"`
	Title           string `json:"title" dynamodbav:"title"`
	CreatedAt       string `json:"created_at" dynamodbav:"created_at"`
	QuestionCount   int    `json:"question_count" dynamodbav:"question_count"`
	OpensAt         string `json:"opens_at,omitempty" dynamodbav:"opens_at,omitempty"`
	ClosesAt        string `json:"closes_at,omitempty" dynamodbav:"closes_at,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty" dynamodbav:"duration_minutes,omitempty"`
}

type Exam struct {
	ExamMeta
	ID               string     `json:"id,omitempty" dynamodbav:"-"`
	Questions        []Question `json:"questions" dynamodbav:"-"`
	AttemptStartedAt int64      `json:"attempt_started_at,omitempty" dynamodbav:"-"`
	DeadlineMS       int64      `json:"deadline_ms,omitempty" dynamodbav:"-"`
}

type Attempt struct {
	PK           string `json:"-" dynamodbav:"pk"`
	SK           string `json:"-" dynamodbav:"sk"`
	StartedAt    int64  `json:"started_at" dynamodbav:"started_at"`
	StartedAtISO string `json:"started_at_iso" dynamodbav:"started_at_iso"`
}

type Submission struct {
	PK              string         `json:"-" dynamodbav:"pk"`
	SK              string         `json:"-" dynamodbav:"sk"`
	Answers         map[string]int `json:"answers" dynamodbav:"answers"`
	Version         int            `json:"version" dynamodbav:"version"`
	RequestID       string         `json:"request_id" dynamodbav:"request_id"`
	UpdatedAt       int64          `json:"updated_at" dynamodbav:"updated_at"`
	UpdatedAtISO    string         `json:"updated_at_iso" dynamodbav:"updated_at_iso"`
	WrittenByRegion string         `json:"written_by_region" dynamodbav:"written_by_region"`
	StudentName     string         `json:"student_name" dynamodbav:"student_name"`
	Score           int            `json:"score" dynamodbav:"score"`
	Total           int            `json:"total" dynamodbav:"total"`
}

type OutboxEntry struct {
	PK          string         `json:"pk" dynamodbav:"pk"`
	SK          string         `json:"sk" dynamodbav:"sk"`
	TargetPK    string         `json:"target_pk" dynamodbav:"target_pk"`
	TargetSK    string         `json:"target_sk" dynamodbav:"target_sk"`
	Version     int            `json:"version" dynamodbav:"version"`
	RequestID   string         `json:"request_id" dynamodbav:"request_id"`
	CreatedAt   int64          `json:"created_at" dynamodbav:"created_at"`
	Answers     map[string]int `json:"answers" dynamodbav:"answers"`
	StudentName string         `json:"student_name" dynamodbav:"student_name"`
	Score       int            `json:"score" dynamodbav:"score"`
	Total       int            `json:"total" dynamodbav:"total"`
	TTL         int64          `json:"ttl" dynamodbav:"ttl"`
}

type SubmissionResult struct {
	Status      string `json:"status"`
	RequestID   string `json:"request_id"`
	Version     int    `json:"version"`
	CommittedAt int64  `json:"committed_at,omitempty"`
	Score       int    `json:"score"`
	Total       int    `json:"total"`
}
