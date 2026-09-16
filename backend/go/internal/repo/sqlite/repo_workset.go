package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ==================== Workset aggregates ====================

// ErrWorksetNotFound is returned when a workset cannot be found.
var ErrWorksetNotFound = errors.New("workset not found")

// ErrWorksetIdemConflict is returned when a workset create collides with an
// existing creation idempotency key.
var ErrWorksetIdemConflict = errors.New("workset idempotency key conflict")

// ErrVersionConflict is returned when an If-Match version precondition fails
// on a workset or operation mutation.
var ErrVersionConflict = errors.New("version conflict")

// ErrOperationNotFound is returned when a workset operation row is absent.
var ErrOperationNotFound = errors.New("operation not found")

// Workset is the persisted aggregate row. Version is the metadata concurrency
// counter: only renames advance it. Draft and generation state
// live on the operation (workset_operations).
type Workset struct {
	ID              string
	Title           string
	LibraryID       string // "" when orphaned (library deleted)
	RootPath        string // snapshot: library root at creation
	RootPathKey     string // snapshot: canonical root identity at creation
	Version         int
	CreationIdemKey string // "" when not set
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// WorksetMember is one ordered album-folder member. MemberID is the stable
// identity (assigned at creation, never regenerated); RelPath is the durable
// normalized library-relative path snapshot; MemberIndex is ordering only.
type WorksetMember struct {
	WorksetID   string
	MemberID    string
	MemberIndex int
	RelPath     string
	FolderID    string
	FolderPath  string
	FolderName  string
}

// ==================== Workset CRUD ====================

const worksetColumns = `id, title, library_id, root_path, root_path_key, version, creation_idem_key, created_at, updated_at`

func scanWorkset(scanner interface{ Scan(...any) error }) (*Workset, error) {
	var w Workset
	var libraryID, creationKey sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&w.ID,
		&w.Title,
		&libraryID,
		&w.RootPath,
		&w.RootPathKey,
		&w.Version,
		&creationKey,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	w.LibraryID = libraryID.String
	w.CreationIdemKey = creationKey.String
	w.CreatedAt = parseTimestamp(createdAt)
	w.UpdatedAt = parseTimestamp(updatedAt)
	return &w, nil
}

// GetWorkset retrieves a workset by id.
func (r *Repository) GetWorkset(id string) (*Workset, error) {
	row := r.db.QueryRow(`SELECT `+worksetColumns+` FROM worksets WHERE id = ?`, id)
	w, err := scanWorkset(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorksetNotFound
		}
		return nil, err
	}
	return w, nil
}

// GetWorksetByCreationIdemKey retrieves the workset owned by a creation
// idempotency key. Returns nil when no key match exists.
func (r *Repository) GetWorksetByCreationIdemKey(key string) (*Workset, error) {
	row := r.db.QueryRow(`SELECT `+worksetColumns+` FROM worksets WHERE creation_idem_key = ?`, key)
	w, err := scanWorkset(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return w, nil
}

// ClearExpiredWorksetIdemKey releases an expired creation idempotency key so a
// retried create transparently makes a new workset. The row itself is never
// deleted.
func (r *Repository) ClearExpiredWorksetIdemKey(id string, cutoff time.Time) error {
	_, err := r.db.Exec(`
		UPDATE worksets SET creation_idem_key = NULL
		WHERE id = ? AND julianday(created_at) < julianday(?)
	`, id, cutoff.Format(timeFormat))
	return err
}

// CreateWorkset inserts a workset with its ordered members, one operation plus
// seeded draft per registered task, all in one transaction. A workset without
// its operations is never visible. It fails with ErrWorksetIdemConflict when
// the creation key is already owned by another workset.
func (r *Repository) CreateWorkset(
	w *Workset,
	members []WorksetMember,
	operations []Operation,
	drafts []OperationDraft,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create workset tx: %w", err)
	}
	defer tx.Rollback()

	if err := insertWorkset(tx, w, members); err != nil {
		return err
	}
	for _, operation := range operations {
		if err := insertOperation(tx, operation); err != nil {
			return err
		}
	}
	for _, draft := range drafts {
		if err := upsertOperationDraft(tx, draft); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertWorksetTx is the transaction-scoped form of the workset insert
// (members included); callers add their own operation rows.
func InsertWorksetTx(tx *sql.Tx, w *Workset, members []WorksetMember) error {
	return insertWorkset(tx, w, members)
}

func insertWorkset(tx *sql.Tx, w *Workset, members []WorksetMember) error {
	var libID, idemKey any
	if w.LibraryID != "" {
		libID = w.LibraryID
	}
	if w.CreationIdemKey != "" {
		idemKey = w.CreationIdemKey
	}
	if _, err := tx.Exec(`
		INSERT INTO worksets (id, title, library_id, root_path, root_path_key, version, creation_idem_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, w.ID, w.Title, libID, w.RootPath, w.RootPathKey, w.Version, idemKey, w.CreatedAt.Format(timeFormat), w.UpdatedAt.Format(timeFormat)); err != nil {
		if isUniqueConstraintError(err) {
			return ErrWorksetIdemConflict
		}
		return fmt.Errorf("insert workset: %w", err)
	}
	for _, m := range members {
		if _, err := tx.Exec(`
			INSERT INTO workset_members (workset_id, member_id, member_index, rel_path, folder_id, folder_path, folder_name)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, m.WorksetID, m.MemberID, m.MemberIndex, m.RelPath, m.FolderID, m.FolderPath, m.FolderName); err != nil {
			return fmt.Errorf("insert workset member: %w", err)
		}
	}
	return nil
}

// ListWorksets lists worksets newest-first with keyset pagination on
// (updated_at, id). includeOrphaned=false excludes orphaned worksets. When
// libraryID is non-empty only worksets owned by that library are returned.
// The result set has at most limit rows; the caller derives the next cursor
// from the last row.
func (r *Repository) ListWorksets(
	cursorUpdatedAt string,
	cursorID string,
	limit int,
	libraryID string,
	includeOrphaned bool,
) ([]*Workset, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + worksetColumns + ` FROM worksets`
	var args []any
	var conds []string
	if libraryID != "" {
		conds = append(conds, "library_id = ?")
		args = append(args, libraryID)
	} else if !includeOrphaned {
		conds = append(conds, "library_id IS NOT NULL")
	}
	if cursorUpdatedAt != "" || cursorID != "" {
		conds = append(
			conds,
			`(julianday(updated_at) < julianday(?) OR (julianday(updated_at) = julianday(?) AND id < ?))`,
		)
		args = append(args, cursorUpdatedAt, cursorUpdatedAt, cursorID)
	}
	if len(conds) > 0 {
		query += " WHERE " + joinConds(conds)
	}
	query += ` ORDER BY julianday(updated_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Workset
	for rows.Next() {
		w, err := scanWorkset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func joinConds(conds []string) string {
	out := conds[0]
	var outSb238 strings.Builder
	for _, c := range conds[1:] {
		outSb238.WriteString(" AND " + c)
	}
	out += outSb238.String()
	return out
}

// ListWorksetMembers returns the ordered members of a workset.
func (r *Repository) ListWorksetMembers(worksetID string) ([]*WorksetMember, error) {
	rows, err := r.db.Query(`
		SELECT workset_id, member_id, member_index, rel_path, folder_id, folder_path, folder_name
		FROM workset_members WHERE workset_id = ? ORDER BY member_index
	`, worksetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*WorksetMember
	for rows.Next() {
		var m WorksetMember
		if err := rows.Scan(
			&m.WorksetID,
			&m.MemberID,
			&m.MemberIndex,
			&m.RelPath,
			&m.FolderID,
			&m.FolderPath,
			&m.FolderName,
		); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// RenameWorkset updates the title with the metadata version guard. Returns
// ErrWorksetNotFound when the row is absent and ErrVersionConflict when the
// If-Match version is stale.
func (r *Repository) RenameWorkset(id, title string, expectedVersion int, now time.Time) error {
	result, err := r.db.Exec(`
		UPDATE worksets SET title = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?
	`, title, now.Format(timeFormat), id, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return r.worksetVersionGuardResult(id)
	}
	return nil
}

// worksetVersionGuardResult distinguishes a stale If-Match from a missing
// workset.
func (r *Repository) worksetVersionGuardResult(id string) error {
	var n int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM worksets WHERE id = ?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrWorksetNotFound
	}
	return ErrVersionConflict
}

// ==================== Library coordination ====================

// CountWorksetsForLibrary returns the number of worksets linked to a library.
func (r *Repository) CountWorksetsForLibrary(libraryID string) (int, error) {
	var n int
	err := r.db.QueryRow("SELECT COUNT(*) FROM worksets WHERE library_id = ?", libraryID).Scan(&n)
	return n, err
}

// LibraryRootPath returns the root path of a library.
func (r *Repository) LibraryRootPath(libraryID string) (string, error) {
	var p string
	err := r.db.QueryRow("SELECT root_path FROM libraries WHERE id = ?", libraryID).Scan(&p)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrLibraryNotFound
		}
		return "", err
	}
	return p, nil
}
