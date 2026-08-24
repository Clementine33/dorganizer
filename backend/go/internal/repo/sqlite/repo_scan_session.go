package sqlite

import (
	"database/sql"
)

// ==================== Scan Session Methods ====================

// CreateScanSession creates a new scan session
func (r *Repository) CreateScanSession(s *ScanSession) error {
	var scopePath interface{}
	if s.ScopePath != nil {
		scopePath = *s.ScopePath
	}
	_, err := r.db.Exec(`
		INSERT INTO scan_sessions (session_id, root_path, scope_path, kind, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.SessionID, s.RootPath, scopePath, s.Kind, s.Status, s.StartedAt.Format(timeFormat))
	return err
}

// GetScanSession retrieves a scan session by ID
func (r *Repository) GetScanSession(sessionID string) (*ScanSession, error) {
	var s ScanSession
	var startedAtStr string
	var finishedAtStr, errorCode, errorMessage, scopePath sql.NullString
	err := r.db.QueryRow(`
		SELECT session_id, root_path, scope_path, kind, status, error_code, error_message, started_at, finished_at
		FROM scan_sessions WHERE session_id = ?
	`, sessionID).Scan(&s.SessionID, &s.RootPath, &scopePath, &s.Kind, &s.Status, &errorCode, &errorMessage, &startedAtStr, &finishedAtStr)
	if err != nil {
		return nil, err
	}
	if scopePath.Valid {
		s.ScopePath = &scopePath.String
	}
	s.ErrorCode = errorCode.String
	s.ErrorMessage = errorMessage.String
	s.StartedAt = parseTimestamp(startedAtStr)
	if finishedAtStr.Valid && finishedAtStr.String != "" {
		s.FinishedAt = parseTimestamp(finishedAtStr.String)
	}
	return &s, nil
}

// UpdateScanSessionStatus updates scan session status
func (r *Repository) UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	finishedAt := "NULL"
	if status == "completed" || status == "failed" || status == "canceled" {
		finishedAt = "datetime('now')"
	}
	if finishedAt == "NULL" {
		_, err := r.db.Exec(`
			UPDATE scan_sessions SET status = ?, error_code = ?, error_message = ? WHERE session_id = ?
		`, status, errorCode, errorMessage, sessionID)
		return err
	}
	_, err := r.db.Exec(`
		UPDATE scan_sessions SET status = ?, error_code = ?, error_message = ?, finished_at = datetime('now') WHERE session_id = ?
	`, status, errorCode, errorMessage, sessionID)
	return err
}

// ListScanSessionsByRoot returns scan sessions for a root
func (r *Repository) ListScanSessionsByRoot(rootPath string) ([]*ScanSession, error) {
	rows, err := r.db.Query(`
		SELECT session_id, root_path, scope_path, kind, status, started_at, finished_at
		FROM scan_sessions WHERE root_path = ? ORDER BY started_at DESC
	`, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*ScanSession
	for rows.Next() {
		var s ScanSession
		var startedAtStr string
		var finishedAtStr, scopePath sql.NullString
		if err := rows.Scan(&s.SessionID, &s.RootPath, &scopePath, &s.Kind, &s.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		if scopePath.Valid {
			s.ScopePath = &scopePath.String
		}
		s.StartedAt = parseTimestamp(startedAtStr)
		if finishedAtStr.Valid && finishedAtStr.String != "" {
			s.FinishedAt = parseTimestamp(finishedAtStr.String)
		}
		sessions = append(sessions, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}
