package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const taskSchema = `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  instruction TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  node TEXT NOT NULL,
  workspace TEXT NOT NULL,
  runtime TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  workdir TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  cost_usd REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  change_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tasks_updated ON tasks(updated_at DESC);

CREATE TABLE IF NOT EXISTS task_events (
  task_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload TEXT NOT NULL,
  at INTEGER NOT NULL,
  PRIMARY KEY (task_id, seq)
);

CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  input TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  decided_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  decided_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS approvals_task ON approvals(task_id);
`

// Task statuses.
const (
	TaskQueued   = "queued"
	TaskRunning  = "running"
	TaskWaiting  = "waiting_approval"
	TaskIdle     = "idle" // turn finished; can continue with follow-up input
	TaskFailed   = "failed"
	TaskCanceled = "cancelled"
	TaskDone     = "done" // user closed it
)

// Task is one unit of work handed to an agent.
type Task struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Instruction      string    `json:"instruction"`
	Status           string    `json:"status"`
	Node             string    `json:"node"`
	Workspace        string    `json:"workspace"`
	Runtime          string    `json:"runtime"`
	Model            string    `json:"model"`
	ConversationID   string    `json:"conversation_id"`
	SessionID        string    `json:"session_id"`
	Workdir          string    `json:"workdir"`
	Result           string    `json:"result"`
	Error            string    `json:"error"`
	CostUSD          float64   `json:"cost_usd"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	PendingApprovals int       `json:"pending_approvals"`
}

// TaskEvent is one item of a task's event stream.
type TaskEvent struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	At      time.Time       `json:"at"`
}

// Approval is a tool call waiting for (or decided by) a human.
type Approval struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	TaskTitle string    `json:"task_title,omitempty"`
	Tool      string    `json:"tool"`
	Input     string    `json:"input"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"` // pending | allowed | denied | expired
	DecidedBy string    `json:"decided_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrAlreadyDecided: another device decided first.
var ErrAlreadyDecided = errors.New("已被处理")

func (s *Store) migrateTasks() error {
	_, err := s.db.Exec(taskSchema)
	return err
}

// CreateTask inserts a task.
func (s *Store) CreateTask(ctx context.Context, t Task) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks (id, title, instruction, status, node, workspace, runtime, model, conversation_id, created_at, updated_at, change_seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Instruction, t.Status, t.Node, t.Workspace, t.Runtime, t.Model, t.ConversationID, now, now, s.nextSeq())
	if err == nil {
		s.changed("tasks")
	}
	return err
}

// TaskPatch updates selected fields; nil means unchanged.
type TaskPatch struct {
	Status, SessionID, Workdir, Result, Error *string
	CostUSD                                   *float64
}

func sp(s string) *string { return &s }

// UpdateTask applies a patch.
func (s *Store) UpdateTask(ctx context.Context, id string, p TaskPatch) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.updateTaskLocked(ctx, s.db, id, p)
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func (s *Store) updateTaskLocked(ctx context.Context, ex execer, id string, p TaskPatch) error {
	sets := []string{"updated_at = ?", "change_seq = ?"}
	args := []any{time.Now().UnixMilli(), s.nextSeq()}
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.SessionID != nil {
		add("session_id", *p.SessionID)
	}
	if p.Workdir != nil {
		add("workdir", *p.Workdir)
	}
	if p.Result != nil {
		add("result", *p.Result)
	}
	if p.Error != nil {
		add("error", *p.Error)
	}
	if p.CostUSD != nil {
		add("cost_usd", *p.CostUSD)
	}
	args = append(args, id)
	res, err := ex.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	s.changed("tasks")
	return nil
}

const taskCols = `t.id, t.title, t.instruction, t.status, t.node, t.workspace, t.runtime, t.model, t.conversation_id, t.session_id, t.workdir, t.result, t.error, t.cost_usd, t.created_at, t.updated_at,
  (SELECT COUNT(*) FROM approvals a WHERE a.task_id = t.id AND a.status = 'pending' AND a.expires_at > ?)`

func scanTask(sc interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var created, updated int64
	err := sc.Scan(&t.ID, &t.Title, &t.Instruction, &t.Status, &t.Node, &t.Workspace, &t.Runtime, &t.Model, &t.ConversationID,
		&t.SessionID, &t.Workdir, &t.Result, &t.Error, &t.CostUSD, &created, &updated, &t.PendingApprovals)
	t.CreatedAt, t.UpdatedAt = fromMS(created), fromMS(updated)
	return t, err
}

// GetTask loads one task.
func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.id = ?`, time.Now().UnixMilli(), id))
}

// ListTasks lists tasks, newest activity first. status "" = all, "active" =
// not done/cancelled.
func (s *Store) ListTasks(ctx context.Context, status string, limit int) ([]Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where, args := "1=1", []any{time.Now().UnixMilli()}
	switch status {
	case "":
	case "active":
		where = "t.status NOT IN ('done', 'cancelled')"
	default:
		where = "t.status = ?"
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks t WHERE `+where+` ORDER BY t.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AppendTaskEvent stores one event (seq assigned per task) and returns it.
func (s *Store) AppendTaskEvent(ctx context.Context, taskID, typ string, payload any, at time.Time) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var seq int64
	err = s.db.QueryRowContext(ctx, `INSERT INTO task_events (task_id, seq, type, payload, at)
VALUES (?, (SELECT IFNULL(MAX(seq), 0) + 1 FROM task_events WHERE task_id = ?), ?, ?, ?) RETURNING seq`,
		taskID, taskID, typ, string(b), at.UnixMilli()).Scan(&seq)
	if err == nil {
		s.changed("tasks")
	}
	return seq, err
}

// TaskEvents returns events with seq > after (continuity: clients pass the
// last seq they rendered and append; a gap can't happen).
func (s *Store) TaskEvents(ctx context.Context, taskID string, after int64, limit int) ([]TaskEvent, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, type, payload, at FROM task_events WHERE task_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, taskID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		var p string
		var at int64
		if err := rows.Scan(&e.Seq, &e.Type, &p, &at); err != nil {
			return nil, err
		}
		e.Payload, e.At = json.RawMessage(p), fromMS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateApproval records a pending approval.
func (s *Store) CreateApproval(ctx context.Context, a Approval) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals (id, task_id, tool, input, reason, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.Tool, a.Input, a.Reason, time.Now().UnixMilli(), a.ExpiresAt.UnixMilli())
	if err == nil {
		s.changed("tasks")
	}
	return err
}

// DecideApproval: first decision wins. A later decision gets
// ErrAlreadyDecided plus the approval as actually decided.
func (s *Store) DecideApproval(ctx context.Context, id string, allow bool, by string) (Approval, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	status := "denied"
	if allow {
		status = "allowed"
	}
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_by = ?, decided_at = ?
WHERE id = ? AND status = 'pending' AND expires_at > ?`, status, by, now, id, now)
	if err != nil {
		return Approval{}, err
	}
	n, _ := res.RowsAffected()
	a, err := s.getApproval(ctx, id)
	if err != nil {
		return a, err
	}
	if n == 0 {
		return a, ErrAlreadyDecided
	}
	s.changed("tasks")
	return a, nil
}

// ExpireApproval marks a pending approval expired (node no longer waiting).
func (s *Store) ExpireApproval(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = 'expired' WHERE id = ? AND status = 'pending'`, id)
	return err
}

func (s *Store) getApproval(ctx context.Context, id string) (Approval, error) {
	var a Approval
	var created, expires int64
	err := s.db.QueryRowContext(ctx, `SELECT a.id, a.task_id, IFNULL(t.title, ''), a.tool, a.input, a.reason, a.status, a.decided_by, a.created_at, a.expires_at
FROM approvals a LEFT JOIN tasks t ON t.id = a.task_id WHERE a.id = ?`, id).
		Scan(&a.ID, &a.TaskID, &a.TaskTitle, &a.Tool, &a.Input, &a.Reason, &a.Status, &a.DecidedBy, &created, &expires)
	a.CreatedAt, a.ExpiresAt = fromMS(created), fromMS(expires)
	if a.Status == "pending" && time.Now().After(a.ExpiresAt) {
		a.Status = "expired"
	}
	return a, err
}

// ListApprovals returns approvals; pendingOnly filters to live ones.
func (s *Store) ListApprovals(ctx context.Context, taskID string, pendingOnly bool) ([]Approval, error) {
	conds, args := []string{"1=1"}, []any{}
	if taskID != "" {
		conds, args = append(conds, "a.task_id = ?"), append(args, taskID)
	}
	if pendingOnly {
		conds, args = append(conds, "a.status = 'pending' AND a.expires_at > ?"), append(args, time.Now().UnixMilli())
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM approvals a WHERE `+strings.Join(conds, " AND ")+` ORDER BY a.created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Approval{}
	for _, id := range ids {
		a, err := s.getApproval(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("approval %s: %w", id, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// WorkCounts returns (active tasks, live pending approvals) for badges.
func (s *Store) WorkCounts(ctx context.Context) (int, int, error) {
	var active, pending int
	err := s.db.QueryRowContext(ctx, `SELECT
  (SELECT COUNT(*) FROM tasks WHERE status NOT IN ('done', 'cancelled')),
  (SELECT COUNT(*) FROM approvals WHERE status = 'pending' AND expires_at > ?)`, time.Now().UnixMilli()).Scan(&active, &pending)
	return active, pending, err
}
