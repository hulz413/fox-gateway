package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"fox-gateway/internal/domain"
	"fox-gateway/internal/registry"
)

const (
	registeredUserStatusActive  = "active"
	bootstrapPairingModeChatKey = "chat_key"
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
			claude_session_id TEXT NOT NULL DEFAULT '',
			session_generation INTEGER NOT NULL DEFAULT 0,
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
			actor_open_id TEXT NOT NULL DEFAULT '',
			decision_reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS registered_users (
			open_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			registered_via TEXT NOT NULL,
			status TEXT NOT NULL,
			registered_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS bootstrap_pairing (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			mode TEXT NOT NULL,
			pairing_key TEXT NOT NULL,
			issued_at TIMESTAMP NOT NULL,
			consumed_at TIMESTAMP,
			initialized_by_open_id TEXT NOT NULL DEFAULT ''
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
	return s.ensureConversationColumns(ctx)
}

func (s *Store) ensureConversationColumns(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "conversations")
	if err != nil {
		return err
	}
	if !columns["claude_session_id"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN claude_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !columns["session_generation"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN session_generation INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (s *Store) UpsertConversation(ctx context.Context, c domain.Conversation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(chat_id,last_message_id,last_sender_open_id,last_message_text,last_intent,claude_session_id,session_generation,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(chat_id) DO UPDATE SET
		last_message_id=excluded.last_message_id,
		last_sender_open_id=excluded.last_sender_open_id,
		last_message_text=excluded.last_message_text,
		last_intent=excluded.last_intent,
		claude_session_id=excluded.claude_session_id,
		session_generation=excluded.session_generation,
		updated_at=excluded.updated_at`,
		c.ChatID, c.LastMessageID, c.LastSenderOpenID, c.LastMessageText, c.LastIntent, c.ClaudeSessionID, c.SessionGeneration, c.UpdatedAt.UTC())
	return err
}

func (s *Store) GetConversation(ctx context.Context, chatID string) (domain.Conversation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT chat_id, last_message_id, last_sender_open_id, last_message_text, last_intent, claude_session_id, session_generation, updated_at FROM conversations WHERE chat_id=?`, chatID)
	return scanConversation(row)
}

func (s *Store) UpdateConversationSession(ctx context.Context, chatID, sessionID string, expectedGeneration int64, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET claude_session_id=?, updated_at=? WHERE chat_id=? AND session_generation=?`, sessionID, updatedAt.UTC(), chatID, expectedGeneration)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ClearConversationSession(ctx context.Context, chatID string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET claude_session_id='', session_generation=session_generation+1, updated_at=? WHERE chat_id=?`, updatedAt.UTC(), chatID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
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

func (s *Store) EnsureBootstrapPairing(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureBootstrapPairingTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BootstrapMessage(ctx context.Context) (string, bool, error) {
	if err := s.EnsureBootstrapPairing(ctx); err != nil {
		return "", false, err
	}
	var users int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM registered_users WHERE status=?`, registeredUserStatusActive).Scan(&users); err != nil {
		return "", false, err
	}
	if users > 0 {
		return "", false, nil
	}
	var key string
	var consumedAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT pairing_key, consumed_at FROM bootstrap_pairing WHERE id=1`).Scan(&key, &consumedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if consumedAt.Valid || strings.TrimSpace(key) == "" {
		return "", false, nil
	}
	return fmt.Sprintf("%s %s", registry.RegisterCommandPrefix, key), true, nil
}

func (s *Store) HasRegisteredUser(ctx context.Context, openID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM registered_users WHERE status=? AND lower(trim(open_id))=lower(trim(?))`, registeredUserStatusActive, openID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) RegisterFirstUserWithBootstrap(ctx context.Context, openID, chatID, key string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := ensureBootstrapPairingTx(ctx, tx); err != nil {
		return false, err
	}
	var duplicateCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM registered_users WHERE status=? AND lower(trim(open_id))=lower(trim(?))`, registeredUserStatusActive, openID).Scan(&duplicateCount); err != nil {
		return false, err
	}
	if duplicateCount > 0 {
		return false, nil
	}
	var userCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM registered_users WHERE status=?`, registeredUserStatusActive).Scan(&userCount); err != nil {
		return false, err
	}
	if userCount > 0 {
		return false, fmt.Errorf("bootstrap registration is closed")
	}
	var pairingKey string
	var consumedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT pairing_key, consumed_at FROM bootstrap_pairing WHERE id=1`).Scan(&pairingKey, &consumedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("bootstrap registration is unavailable")
		}
		return false, err
	}
	if strings.TrimSpace(pairingKey) == "" || consumedAt.Valid {
		return false, fmt.Errorf("bootstrap registration is unavailable")
	}
	if strings.TrimSpace(key) != strings.TrimSpace(pairingKey) {
		return false, fmt.Errorf("invalid registration key")
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO registered_users(open_id,chat_id,registered_via,status,registered_at) VALUES(?,?,?,?,?)`, openID, chatID, "feishu_message", registeredUserStatusActive, now); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bootstrap_pairing SET consumed_at=?, initialized_by_open_id=? WHERE id=1`, now, openID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func ensureBootstrapPairingTx(ctx context.Context, tx *sql.Tx) error {
	var userCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM registered_users WHERE status=?`, registeredUserStatusActive).Scan(&userCount); err != nil {
		return err
	}
	if userCount > 0 {
		return nil
	}
	var pairingKey string
	var consumedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT pairing_key, consumed_at FROM bootstrap_pairing WHERE id=1`).Scan(&pairingKey, &consumedAt); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO bootstrap_pairing(id,mode,pairing_key,issued_at,initialized_by_open_id) VALUES(1,?,?,?,'')`, bootstrapPairingModeChatKey, registry.RandomHex(16), time.Now().UTC())
		return err
	}
	if strings.TrimSpace(pairingKey) != "" && !consumedAt.Valid {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE bootstrap_pairing SET mode=?, pairing_key=?, issued_at=?, consumed_at=NULL, initialized_by_open_id='' WHERE id=1`, bootstrapPairingModeChatKey, registry.RandomHex(16), time.Now().UTC())
	return err
}

func (s *Store) SaveApproval(ctx context.Context, a domain.Approval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals(job_id,payload_json,hash,status,requested_by,actor_open_id,decision_reason,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET payload_json=excluded.payload_json, hash=excluded.hash, status=excluded.status, requested_by=excluded.requested_by, actor_open_id=excluded.actor_open_id, decision_reason=excluded.decision_reason, updated_at=excluded.updated_at`,
		a.JobID, a.PayloadJSON, a.Hash, a.Status, a.RequestedBy, a.ActorOpenID, a.DecisionReason, a.CreatedAt.UTC(), a.UpdatedAt.UTC())
	return err
}

func (s *Store) GetApproval(ctx context.Context, jobID string) (domain.Approval, error) {
	row := s.db.QueryRowContext(ctx, `SELECT job_id, payload_json, hash, status, requested_by, actor_open_id, decision_reason, created_at, updated_at FROM approvals WHERE job_id=?`, jobID)
	var a domain.Approval
	if err := row.Scan(&a.JobID, &a.PayloadJSON, &a.Hash, &a.Status, &a.RequestedBy, &a.ActorOpenID, &a.DecisionReason, &a.CreatedAt, &a.UpdatedAt); err != nil {
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

func scanConversation(scanner interface{ Scan(dest ...any) error }) (domain.Conversation, error) {
	var c domain.Conversation
	if err := scanner.Scan(&c.ChatID, &c.LastMessageID, &c.LastSenderOpenID, &c.LastMessageText, &c.LastIntent, &c.ClaudeSessionID, &c.SessionGeneration, &c.UpdatedAt); err != nil {
		return domain.Conversation{}, err
	}
	return c, nil
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
