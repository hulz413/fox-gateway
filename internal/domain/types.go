package domain

import "time"

type JobKind string

const (
	JobKindDirectAnswer JobKind = "direct_answer"
	JobKindReadOnly     JobKind = "read_only"
	JobKindMutation     JobKind = "mutation"
)

type JobStatus string

const (
	JobStatusQueued          JobStatus = "queued"
	JobStatusWaitingApproval JobStatus = "waiting_approval"
	JobStatusApproved        JobStatus = "approved"
	JobStatusRunning         JobStatus = "running"
	JobStatusSucceeded       JobStatus = "succeeded"
	JobStatusFailed          JobStatus = "failed"
	JobStatusRejected        JobStatus = "rejected"
	JobStatusInterrupted     JobStatus = "interrupted"
)

type ApprovalStatus string

const (
	ApprovalStatusPending     ApprovalStatus = "pending"
	ApprovalStatusApproved    ApprovalStatus = "approved"
	ApprovalStatusRejected    ApprovalStatus = "rejected"
	ApprovalStatusExpired     ApprovalStatus = "expired"
	ApprovalStatusInvalidated ApprovalStatus = "invalidated"
)

type Conversation struct {
	ChatID           string
	LastMessageID    string
	LastSenderOpenID string
	LastMessageText  string
	LastIntent       string
	UpdatedAt        time.Time
}

type Job struct {
	ID               string
	ChatID           string
	MessageID        string
	RequesterOpenID  string
	Kind             JobKind
	Status           JobStatus
	RequestText      string
	ResultSummary    string
	ErrorText        string
	ApprovalHash     string
	RequiresApproval bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Approval struct {
	JobID          string
	PayloadJSON    string
	Hash           string
	Status         ApprovalStatus
	RequestedBy    string
	ApproverOpenID string
	DecisionReason string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AuditEvent struct {
	ID        string
	JobID     string
	Kind      string
	ActorID   string
	Payload   string
	CreatedAt time.Time
}

type WorkerSession struct {
	JobID      string
	PID        *int
	Command    string
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
	ExitCode   *int
	Stdout     string
	Stderr     string
	ErrorText  string
}

type LarkMessageEvent struct {
	SenderOpenID string
	ChatID       string
	Text         string
	MessageID    string
}
