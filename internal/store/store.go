package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"fox-gateway/internal/domain"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			chat_id TEXT PRIMARY KEY,
			last_message_id TEXT NOT NULL,
			last_sender_open_id TEXT NOT NULL,
			last_message_text TEXT NOT NULL,
			last_intent TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			requester_open_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			request_text TEXT NOT NULL,
			result_summary TEXT NOT NULL DEFAULT '',
			error_text TEXT NOT NULL DEFAULT '',
			approval_hash TEXT NOT NULL DEFAULT '',
			requires_approval INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS approvals (
			job_id TEXT PRIMARY KEY,
			payload_json TEXT NOT NULL,
			hash TEXT NOT NULL,
			status TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			approver_open_id TEXT NOT NULL DEFAULT '',
			decision_reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS worker_sessions (
			job_id TEXT PRIMARY KEY,
			pid INTEGER,
			command TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP,
			exit_code INTEGER,
			stdout TEXT NOT NULL DEFAULT '',
			stderr TEXT NOT NULL DEFAULT '',
			error_text TEXT NOT NULL DEFAULT ''
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertConversation(ctx context.Context, c domain.Conversation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(chat_id,last_message_id,last_sender_open_id,last_message_text,last_intent,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(chat_id) DO UPDATE SET
		last_message_id=excluded.last_message_id,
		last_sender_open_id=excluded.last_sender_open_id,
		last_message_text=excluded.last_message_text,
		last_intent=excluded.last_intent,
		updated_at=excluded.updated_at`,
		c.ChatID, c.LastMessageID, c.LastSenderOpenID, c.LastMessageText, c.LastIntent, c.UpdatedAt.UTC())
	return err
}

func (s *Store) CreateJob(ctx context.Context, j domain.Job) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id,chat_id,message_id,requester_open_id,kind,status,request_text,result_summary,error_text,approval_hash,requires_approval,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.ChatID, j.MessageID, j.RequesterOpenID, j.Kind, j.Status, j.RequestText, j.ResultSummary, j.ErrorText, j.ApprovalHash, boolToInt(j.RequiresApproval), j.CreatedAt.UTC(), j.UpdatedAt.UTC())
	return err
}

func (s *Store) UpdateJob(ctx context.Context, j domain.Job) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, result_summary=?, error_text=?, approval_hash=?, requires_approval=?, updated_at=? WHERE id=?`,
		j.Status, j.ResultSummary, j.ErrorText, j.ApprovalHash, boolToInt(j.RequiresApproval), j.UpdatedAt.UTC(), j.ID)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (domain.Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, chat_id, message_id, requester_open_id, kind, status, request_text, result_summary, error_text, approval_hash, requires_approval, created_at, updated_at FROM jobs WHERE id=?`, id)
	return scanJob(row)
}

func (s *Store) FindLatestJobByChat(ctx context.Context, chatID string) (domain.Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, chat_id, message_id, requester_open_id, kind, status, request_text, result_summary, error_text, approval_hash, requires_approval, created_at, updated_at FROM jobs WHERE chat_id=? ORDER BY created_at DESC LIMIT 1`, chatID)
	return scanJob(row)
}

func (s *Store) ListJobsByStatuses(ctx context.Context, statuses ...domain.JobStatus) ([]domain.Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query := `SELECT id, chat_id, message_id, requester_open_id, kind, status, request_text, result_summary, error_text, approval_hash, requires_approval, created_at, updated_at FROM jobs WHERE status IN (`
	args := make([]any, 0, len(statuses))
	for i, status := range statuses {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, status)
	}
	query += `) ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []domain.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) SaveApproval(ctx context.Context, a domain.Approval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals(job_id,payload_json,hash,status,requested_by,approver_open_id,decision_reason,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET payload_json=excluded.payload_json, hash=excluded.hash, status=excluded.status, requested_by=excluded.requested_by, approver_open_id=excluded.approver_open_id, decision_reason=excluded.decision_reason, updated_at=excluded.updated_at`,
		a.JobID, a.PayloadJSON, a.Hash, a.Status, a.RequestedBy, a.ApproverOpenID, a.DecisionReason, a.CreatedAt.UTC(), a.UpdatedAt.UTC())
	return err
}

func (s *Store) GetApproval(ctx context.Context, jobID string) (domain.Approval, error) {
	row := s.db.QueryRowContext(ctx, `SELECT job_id, payload_json, hash, status, requested_by, approver_open_id, decision_reason, created_at, updated_at FROM approvals WHERE job_id=?`, jobID)
	var a domain.Approval
	if err := row.Scan(&a.JobID, &a.PayloadJSON, &a.Hash, &a.Status, &a.RequestedBy, &a.ApproverOpenID, &a.DecisionReason, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

func (s *Store) SaveAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(id,job_id,kind,actor_id,payload,created_at) VALUES(?,?,?,?,?,?)`,
		event.ID, event.JobID, event.Kind, event.ActorID, event.Payload, event.CreatedAt.UTC())
	return err
}

func (s *Store) SaveWorkerSession(ctx context.Context, ws domain.WorkerSession) error {
	var pid any
	if ws.PID != nil {
		pid = *ws.PID
	}
	var exitCode any
	if ws.ExitCode != nil {
		exitCode = *ws.ExitCode
	}
	var finishedAt any
	if ws.FinishedAt != nil {
		finishedAt = ws.FinishedAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO worker_sessions(job_id,pid,command,status,started_at,finished_at,exit_code,stdout,stderr,error_text)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET pid=excluded.pid, command=excluded.command, status=excluded.status, started_at=excluded.started_at, finished_at=excluded.finished_at, exit_code=excluded.exit_code, stdout=excluded.stdout, stderr=excluded.stderr, error_text=excluded.error_text`,
		ws.JobID, pid, ws.Command, ws.Status, ws.StartedAt.UTC(), finishedAt, exitCode, ws.Stdout, ws.Stderr, ws.ErrorText)
	return err
}

func (s *Store) ListActiveWorkerSessions(ctx context.Context) ([]domain.WorkerSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,pid,command,status,started_at,finished_at,exit_code,stdout,stderr,error_text FROM worker_sessions WHERE status IN ('running','starting')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []domain.WorkerSession
	for rows.Next() {
		ws, err := scanWorkerSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, ws)
	}
	return sessions, rows.Err()
}

func (s *Store) InterruptInProgressJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, error_text=CASE WHEN error_text='' THEN 'service restarted while job was running' ELSE error_text END, updated_at=? WHERE status IN (?, ?, ?)`,
		domain.JobStatusInterrupted, time.Now().UTC(), domain.JobStatusRunning, domain.JobStatusQueued, domain.JobStatusApproved)
	return err
}

func (s *Store) MarkWorkerSessionsInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE worker_sessions SET status='interrupted', finished_at=COALESCE(finished_at, ?) WHERE status IN ('running','starting')`, time.Now().UTC())
	return err
}

func scanJob(scanner interface{ Scan(dest ...any) error }) (domain.Job, error) {
	var j domain.Job
	var requiresApproval int
	if err := scanner.Scan(&j.ID, &j.ChatID, &j.MessageID, &j.RequesterOpenID, &j.Kind, &j.Status, &j.RequestText, &j.ResultSummary, &j.ErrorText, &j.ApprovalHash, &requiresApproval, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return domain.Job{}, err
	}
	j.RequiresApproval = requiresApproval == 1
	return j, nil
}

func scanWorkerSession(scanner interface{ Scan(dest ...any) error }) (domain.WorkerSession, error) {
	var ws domain.WorkerSession
	var pid sql.NullInt64
	var finishedAt sql.NullTime
	var exitCode sql.NullInt64
	if err := scanner.Scan(&ws.JobID, &pid, &ws.Command, &ws.Status, &ws.StartedAt, &finishedAt, &exitCode, &ws.Stdout, &ws.Stderr, &ws.ErrorText); err != nil {
		return domain.WorkerSession{}, err
	}
	if pid.Valid {
		value := int(pid.Int64)
		ws.PID = &value
	}
	if finishedAt.Valid {
		value := finishedAt.Time
		ws.FinishedAt = &value
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		ws.ExitCode = &value
	}
	return ws, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
