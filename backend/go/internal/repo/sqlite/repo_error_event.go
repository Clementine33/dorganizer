package sqlite

import (
	"database/sql"
)

// ==================== Error Event Methods ====================

// CreateErrorEvent logs an error event
func (r *Repository) CreateErrorEvent(e *ErrorEvent) error {
	retryable := 0
	if e.Retryable {
		retryable = 1
	}
	var path interface{}
	if e.Path != nil {
		path = *e.Path
	}
	result, err := r.db.Exec(`
		INSERT INTO error_events (scope, root_path, path, code, message, retryable)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.Scope, e.RootPath, path, e.Code, e.Message, retryable)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	e.ID = id
	return nil
}

// ListErrorEventsByRoot returns error events for a root
func (r *Repository) ListErrorEventsByRoot(rootPath string) ([]*ErrorEvent, error) {
	rows, err := r.db.Query(`
		SELECT id, scope, root_path, path, code, message, retryable, created_at
		FROM error_events WHERE root_path = ? ORDER BY id DESC
	`, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*ErrorEvent
	for rows.Next() {
		var e ErrorEvent
		var retryable int
		var createdAtStr string
		var path sql.NullString
		if err := rows.Scan(&e.ID, &e.Scope, &e.RootPath, &path, &e.Code, &e.Message, &retryable, &createdAtStr); err != nil {
			return nil, err
		}
		if path.Valid {
			e.Path = &path.String
		}
		e.Retryable = retryable == 1
		e.CreatedAt = parseTimestamp(createdAtStr)
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
