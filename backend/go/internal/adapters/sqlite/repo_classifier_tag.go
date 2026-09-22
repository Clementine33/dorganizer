package sqlite

import (
	"errors"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/conversion"
)

// Tags returns all custom tags from the global library ordered
// alphabetically by tag (case-insensitive).
func (r *Repository) Tags() ([]conversion.ClassifierTag, error) {
	rows, err := r.db.Query(`
		SELECT id, tag, normalized_tag, COALESCE(created_at, '')
		FROM classifier_tag_library
		ORDER BY tag COLLATE NOCASE ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []conversion.ClassifierTag
	for rows.Next() {
		var row conversion.ClassifierTag
		var createdAt string
		if err := rows.Scan(&row.ID, &row.Tag, &row.NormalizedTag, &createdAt); err != nil {
			return nil, err
		}
		row.CreatedAt = parseTimestamp(createdAt)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// AddTag adds a single custom tag to the library idempotently.
// If the tag (case-insensitively trimmed) already exists, it is a no-op.
func (r *Repository) AddTag(tag string) (*conversion.ClassifierTag, error) {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return nil, errors.New("tag cannot be empty")
	}
	norm := strings.ToLower(trimmed)
	now := time.Now().Format(timeFormat)

	_, err := r.db.Exec(`
		INSERT INTO classifier_tag_library (tag, normalized_tag, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(normalized_tag) DO NOTHING
	`, trimmed, norm, now)
	if err != nil {
		return nil, err
	}

	var row conversion.ClassifierTag
	var createdAt string
	err = r.db.QueryRow(`
		SELECT id, tag, normalized_tag, COALESCE(created_at, '')
		FROM classifier_tag_library
		WHERE normalized_tag = ?
	`, norm).Scan(&row.ID, &row.Tag, &row.NormalizedTag, &createdAt)
	if err != nil {
		return nil, err
	}
	row.CreatedAt = parseTimestamp(createdAt)
	return &row, nil
}

// DeleteTag removes a custom tag by ID from the global library.
func (r *Repository) DeleteTag(id int64) error {
	res, err := r.db.Exec(`DELETE FROM classifier_tag_library WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("tag not found")
	}
	return nil
}
