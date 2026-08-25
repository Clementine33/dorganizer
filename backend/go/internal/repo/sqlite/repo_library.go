package sqlite

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// ==================== Types ====================

// Library represents a user-facing music library.
type Library struct {
	ID             string
	Name           string
	RootPath       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastScanAt     *time.Time
	LastScanStatus string
	LastScanError  string
}

// LibraryFolder represents a direct child folder of a library root that
// contains at least one audio file somewhere beneath it.
type LibraryFolder struct {
	ID             string
	LibraryID      string
	Path           string
	Name           string
	RelativePath   string
	AudioFileCount int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EntryRow is a row from the entries table used for building folder trees.
type EntryRow struct {
	Path       string
	ParentPath string
	Name       string
	IsDir      bool
	Size       int64
	Mtime      int64
	Bitrate    *int32
	Format     string
}

// ==================== Sentinels ====================

var (
	// ErrLibraryExists is returned when a library root path already exists.
	ErrLibraryExists = errors.New("library already exists")
	// ErrLibraryNotFound is returned when a library cannot be found.
	ErrLibraryNotFound = errors.New("library not found")
	// ErrLibraryFolderNotFound is returned when a library folder cannot be found.
	ErrLibraryFolderNotFound = errors.New("library folder not found")
)

// audioExtCond is a SQL predicate matching file entries whose name has one of
// the recognized audio extensions (case-insensitive).
const audioExtCond = `(lower(name) LIKE '%.mp3' OR lower(name) LIKE '%.flac' OR lower(name) LIKE '%.wav' OR lower(name) LIKE '%.m4a' OR lower(name) LIKE '%.aac' OR lower(name) LIKE '%.ogg')`

// escapeLikeLiteral escapes LIKE wildcard characters in a literal so it can be
// used as a safe prefix pattern with ESCAPE '\'. The backslash is escaped
// first so the subsequent %/_ escapes cannot be re-interpreted.
func escapeLikeLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// escapedPathPrefixSQL is a SQL expression producing an escaped LIKE prefix
// for the entries column d.path followed by the wildcard '/%'. Directory
// names may contain % or _ characters, which must not act as patterns, hence
// ESCAPE '\' is required at each LIKE that uses it.
const escapedPathPrefixSQL = `(replace(replace(replace(d.path, '\', '\\'), '%', '\%'), '_', '\_') || '/%')`

// isUniqueConstraintError reports whether err is a SQLite UNIQUE constraint
// violation.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "constraint") && strings.Contains(errStr, "unique")
}

// ==================== Library CRUD ====================

// CreateLibrary inserts a new library. rootPath is stored POSIX-normalized.
func (r *Repository) CreateLibrary(name, rootPath string) (*Library, error) {
	rootPath = pathnorm.NormalizeToPOSIX(rootPath)
	now := time.Now().Format(timeFormat)
	id := uuid.NewString()
	_, err := r.db.Exec(`
		INSERT INTO libraries (id, name, root_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, name, rootPath, now, now)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, ErrLibraryExists
		}
		return nil, err
	}
	return r.GetLibrary(id)
}

// ListLibraries returns all libraries ordered by created_at (newest first).
func (r *Repository) ListLibraries() ([]*Library, error) {
	rows, err := r.db.Query(`
		SELECT id, name, root_path, created_at, updated_at, last_scan_at, last_scan_status, last_scan_error
		FROM libraries ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var libraries []*Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		libraries = append(libraries, lib)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return libraries, nil
}

// GetLibrary retrieves a library by id.
func (r *Repository) GetLibrary(id string) (*Library, error) {
	row := r.db.QueryRow(`
		SELECT id, name, root_path, created_at, updated_at, last_scan_at, last_scan_status, last_scan_error
		FROM libraries WHERE id = ?
	`, id)
	lib, err := scanLibrary(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLibraryNotFound
		}
		return nil, err
	}
	return lib, nil
}

// UpdateLibrary updates a library's name and root path and returns the
// updated row. Changing the root invalidates the materialized folder list and
// prior scan state in the same transaction so no stale paths remain attached.
func (r *Repository) UpdateLibrary(id, name, rootPath string) (*Library, error) {
	rootPath = pathnorm.NormalizeToPOSIX(rootPath)
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var currentRoot string
	if err := tx.QueryRow("SELECT root_path FROM libraries WHERE id = ?", id).Scan(&currentRoot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLibraryNotFound
		}
		return nil, err
	}

	now := time.Now().Format(timeFormat)
	rootChanged := currentRoot != rootPath
	query := `UPDATE libraries SET name = ?, root_path = ?, updated_at = ? WHERE id = ?`
	if rootChanged {
		query = `
			UPDATE libraries
			SET name = ?, root_path = ?, updated_at = ?,
			    last_scan_at = NULL, last_scan_status = '', last_scan_error = ''
			WHERE id = ?
		`
	}
	if _, err := tx.Exec(query, name, rootPath, now, id); err != nil {
		if isUniqueConstraintError(err) {
			return nil, ErrLibraryExists
		}
		return nil, err
	}
	if rootChanged {
		if _, err := tx.Exec("DELETE FROM library_folders WHERE library_id = ?", id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetLibrary(id)
}

// DeleteLibrary removes a library; library_folders rows cascade via FK.
func (r *Repository) DeleteLibrary(id string) error {
	result, err := r.db.Exec("DELETE FROM libraries WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrLibraryNotFound
	}
	return nil
}

// UpdateLibraryScanState records the outcome of a library scan.
func (r *Repository) UpdateLibraryScanState(id, status, errMsg string, finishedAt time.Time) error {
	_, err := r.db.Exec(`
		UPDATE libraries SET last_scan_status = ?, last_scan_error = ?, last_scan_at = ?
		WHERE id = ?
	`, status, errMsg, finishedAt.Format(timeFormat), id)
	return err
}

// scanLibrary decodes a library row from a row or rows-based scanner.
func scanLibrary(row interface{ Scan(...interface{}) error }) (*Library, error) {
	var l Library
	var createdAtStr, updatedAtStr string
	var lastScanAt sql.NullString
	if err := row.Scan(&l.ID, &l.Name, &l.RootPath, &createdAtStr, &updatedAtStr, &lastScanAt, &l.LastScanStatus, &l.LastScanError); err != nil {
		return nil, err
	}
	l.CreatedAt = parseTimestamp(createdAtStr)
	l.UpdatedAt = parseTimestamp(updatedAtStr)
	if lastScanAt.Valid && lastScanAt.String != "" {
		t := parseTimestamp(lastScanAt.String)
		l.LastScanAt = &t
	}
	return &l, nil
}

// ==================== Library Folders ====================

// ReplaceLibraryFolders rebuilds the direct-child folder list for a library
// from the entries table in a single transaction. A folder is kept only if it
// contains at least one audio file anywhere beneath it. Returns the number of
// folders written.
func (r *Repository) ReplaceLibraryFolders(libraryID, rootPath string) (int, error) {
	rootPath = pathnorm.NormalizeToPOSIX(rootPath)
	if len(rootPath) > 1 {
		rootPath = strings.TrimRight(rootPath, "/")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM library_folders WHERE library_id = ?", libraryID); err != nil {
		return 0, err
	}

	rows, err := tx.Query(`
		SELECT d.path, d.name,
		       (SELECT COUNT(*) FROM entries f
		         WHERE f.is_dir = 0
		           AND f.path LIKE `+escapedPathPrefixSQL+` ESCAPE '\'
		           AND `+audioExtCond+`) AS audio_count
		FROM entries d
		WHERE d.is_dir = 1
		  AND d.parent_path = ?
		  AND EXISTS (
		    SELECT 1 FROM entries f
		    WHERE f.is_dir = 0
		      AND f.path LIKE `+escapedPathPrefixSQL+` ESCAPE '\'
		      AND `+audioExtCond+`
		  )
		ORDER BY d.path
	`, rootPath)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type folderCandidate struct {
		path  string
		name  string
		audio int
	}
	var dirs []folderCandidate
	for rows.Next() {
		var d folderCandidate
		if err := rows.Scan(&d.path, &d.name, &d.audio); err != nil {
			return 0, err
		}
		dirs = append(dirs, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	prefix := rootPath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	now := time.Now().Format(timeFormat)
	count := 0
	for _, d := range dirs {
		name := d.name
		if name == "" {
			if idx := strings.LastIndex(d.path, "/"); idx >= 0 {
				name = d.path[idx+1:]
			}
		}
		rel := strings.TrimPrefix(d.path, prefix)
		if _, err := tx.Exec(`
			INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, uuid.NewString(), libraryID, d.path, name, rel, d.audio, now, now); err != nil {
			return 0, err
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// ListLibraryFolders returns the folders of a library ordered by path.
func (r *Repository) ListLibraryFolders(libraryID string) ([]*LibraryFolder, error) {
	rows, err := r.db.Query(`
		SELECT id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at
		FROM library_folders WHERE library_id = ? ORDER BY path
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*LibraryFolder
	for rows.Next() {
		f, err := scanLibraryFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return folders, nil
}

// GetLibraryFolder retrieves a single folder within a library.
func (r *Repository) GetLibraryFolder(libraryID, folderID string) (*LibraryFolder, error) {
	row := r.db.QueryRow(`
		SELECT id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at
		FROM library_folders WHERE library_id = ? AND id = ?
	`, libraryID, folderID)
	f, err := scanLibraryFolder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLibraryFolderNotFound
		}
		return nil, err
	}
	return f, nil
}

// scanLibraryFolder decodes a library_folders row from a row or rows-based
// scanner.
func scanLibraryFolder(row interface{ Scan(...interface{}) error }) (*LibraryFolder, error) {
	var f LibraryFolder
	var createdAtStr, updatedAtStr string
	if err := row.Scan(&f.ID, &f.LibraryID, &f.Path, &f.Name, &f.RelativePath, &f.AudioFileCount, &createdAtStr, &updatedAtStr); err != nil {
		return nil, err
	}
	f.CreatedAt = parseTimestamp(createdAtStr)
	f.UpdatedAt = parseTimestamp(updatedAtStr)
	return &f, nil
}

// ListEntriesUnderPath returns entries under a path prefix (including the
// prefix itself) for tree building. Wildcard characters in the prefix are
// escaped so folder names like "100%_hits" match only their own subtree.
func (r *Repository) ListEntriesUnderPath(pathPrefix string) ([]EntryRow, error) {
	pathPrefix = pathnorm.NormalizeToPOSIX(pathPrefix)
	pattern := escapeLikeLiteral(pathPrefix) + "/%"
	rows, err := r.db.Query(`
		SELECT path, parent_path, name, is_dir, size, mtime, bitrate, format
		FROM entries
		WHERE path = ? OR path LIKE ? ESCAPE '\'
		ORDER BY path
	`, pathPrefix, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []EntryRow
	for rows.Next() {
		var e EntryRow
		var isDir int
		var bitrate sql.NullInt32
		var format sql.NullString
		if err := rows.Scan(&e.Path, &e.ParentPath, &e.Name, &isDir, &e.Size, &e.Mtime, &bitrate, &format); err != nil {
			return nil, err
		}
		e.IsDir = isDir == 1
		if bitrate.Valid {
			v := bitrate.Int32
			e.Bitrate = &v
		}
		e.Format = format.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
