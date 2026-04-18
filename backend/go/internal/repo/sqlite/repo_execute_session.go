package sqlite

import (
	"database/sql"
)

// ==================== Execute Session Methods ====================

// CreateExecuteSession creates a new execute session
func (r *Repository) CreateExecuteSession(e *ExecuteSession) error {
	_, err := r.db.Exec(`
		INSERT INTO execute_sessions (session_id, plan_id, root_path, status, started_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.SessionID, e.PlanID, e.RootPath, e.Status, e.StartedAt.Format(timeFormat))
	return err
}

// GetExecuteSession retrieves an execute session by ID
func (r *Repository) GetExecuteSession(sessionID string) (*ExecuteSession, error) {
	var e ExecuteSession
	var startedAtStr string
	var finishedAtStr, errorCode, errorMessage sql.NullString
	err := r.db.QueryRow(`
		SELECT session_id, plan_id, root_path, status, started_at, finished_at, error_code, error_message
		FROM execute_sessions WHERE session_id = ?
	`, sessionID).Scan(&e.SessionID, &e.PlanID, &e.RootPath, &e.Status, &startedAtStr, &finishedAtStr, &errorCode, &errorMessage)
	if err != nil {
		return nil, err
	}
	e.ErrorCode = errorCode.String
	e.ErrorMessage = errorMessage.String
	e.StartedAt = parseTimestamp(startedAtStr)
	if finishedAtStr.Valid && finishedAtStr.String != "" {
		e.FinishedAt = parseTimestamp(finishedAtStr.String)
	}
	return &e, nil
}

// UpdateExecuteSessionStatus updates execute session status
func (r *Repository) UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	_, err := r.db.Exec(`
		UPDATE execute_sessions SET status = ?, error_code = ?, error_message = ?, finished_at = datetime('now') WHERE session_id = ?
	`, status, errorCode, errorMessage, sessionID)
	return err
}

// ListExecuteSessionsByPlan returns execute sessions for a plan
func (r *Repository) ListExecuteSessionsByPlan(planID string) ([]*ExecuteSession, error) {
	rows, err := r.db.Query(`
		SELECT session_id, plan_id, root_path, status, started_at, finished_at
		FROM execute_sessions WHERE plan_id = ? ORDER BY started_at DESC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*ExecuteSession
	for rows.Next() {
		var e ExecuteSession
		var startedAtStr string
		var finishedAtStr sql.NullString
		if err := rows.Scan(&e.SessionID, &e.PlanID, &e.RootPath, &e.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		e.StartedAt = parseTimestamp(startedAtStr)
		if finishedAtStr.Valid && finishedAtStr.String != "" {
			e.FinishedAt = parseTimestamp(finishedAtStr.String)
		}
		sessions = append(sessions, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}
