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

// subtreeFilePredicateSQL matches file rows f at or beneath directory row d at
// a slash boundary, using binary comparison. SQLite LIKE is ASCII
// case-insensitive by default, which would conflate case-distinct POSIX
// siblings (`/music/Rock` vs `/music/rock`), so path identity here must be
// exact and case-sensitive; % and _ are ordinary characters.
const subtreeFilePredicateSQL = `(f.path = d.path OR substr(f.path, 1, length(d.path) + 1) = d.path || '/')`

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

// CreateLibrary inserts a new library. rootPath is stored as the cleaned
// canonical root (POSIX separators, lexical cleaning); uniqueness is enforced
// on the canonical identity key, so equivalent spellings (`/music/.`,
// `C:/Music` vs `c:/music`) conflict.
func (r *Repository) CreateLibrary(name, rootPath string) (*Library, error) {
	rootPath = pathnorm.CleanRootPath(rootPath)
	rootPathKey := pathnorm.RootPathKey(rootPath)
	now := time.Now().Format(timeFormat)
	id := uuid.NewString()
	_, err := r.db.Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, name, rootPath, rootPathKey, now, now)
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

// ErrLibraryHasWorksets is returned when a root-path change is attempted on a
// library that still owns worksets. Workset membership identity is a
// normalized library-relative path, so silently rebinding the root would
// reattach fixed worksets to unrelated content.
var ErrLibraryHasWorksets = errors.New("library has worksets")

// ErrGenerationInProgress is returned when an owned workset has a queued or
// running planning session and an operation that must wait for it (library
// deletion) is attempted.
var ErrGenerationInProgress = errors.New("generation in progress")

// UpdateLibrary updates a library's name and root path and returns the
// updated row. Changing the root invalidates the materialized folder list and
// prior scan state in the same transaction so no stale paths remain attached.
// A root-path change is rejected with ErrLibraryHasWorksets while any workset
// is still linked to the library; name edits stay allowed.
func (r *Repository) UpdateLibrary(id, name, rootPath string) (*Library, error) {
	candidateRoot := pathnorm.CleanRootPath(rootPath)
	rootPathKey := pathnorm.RootPathKey(candidateRoot)
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
	// A root change is judged on the canonical identity key, so a spelling-only
	// edit (e.g. `/music/.` for `/music`) must not invalidate folders or scan
	// state, while `C:/Music` -> `c:/music` is genuinely unchanged on Windows.
	rootChanged := pathnorm.RootPathKey(currentRoot) != rootPathKey
	if rootChanged {
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM worksets WHERE library_id = ?", id).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrLibraryHasWorksets
		}
	}
	query := `UPDATE libraries SET name = ?, root_path = ?, root_path_key = ?, updated_at = ? WHERE id = ?`
	if rootChanged {
		query = `
			UPDATE libraries
			SET name = ?, root_path = ?, root_path_key = ?, updated_at = ?,
			    last_scan_at = NULL, last_scan_status = '', last_scan_error = ''
			WHERE id = ?
		`
	}
	if _, err := tx.Exec(query, name, candidateRoot, rootPathKey, now, id); err != nil {
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

// DeleteLibrary removes a library and orphans its worksets in one transaction.
// It fails with ErrGenerationInProgress while any owned workset has a queued
// or running planning session (the client must cancel first), and with
// ErrLibraryNotFound when the library does not exist. The active-session check
// happens inside the write transaction; generation claim/complete updates
// serialize on the same SQLite writer, so a delete that commits cannot race a
// generation that would start against the deleted library.
func (r *Repository) DeleteLibrary(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM libraries WHERE id = ?", id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrLibraryNotFound
	}

	var active int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM plan_generations g
		JOIN worksets w ON w.id = g.workset_id
		WHERE w.library_id = ? AND g.status IN ('queued','running')
	`, id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrGenerationInProgress
	}

	if _, err := tx.Exec("UPDATE worksets SET library_id = NULL WHERE library_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM libraries WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
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
func scanLibrary(row interface{ Scan(...any) error }) (*Library, error) {
	var l Library
	var createdAtStr, updatedAtStr string
	var lastScanAt sql.NullString
	if err := row.Scan(
		&l.ID,
		&l.Name,
		&l.RootPath,
		&createdAtStr,
		&updatedAtStr,
		&lastScanAt,
		&l.LastScanStatus,
		&l.LastScanError,
	); err != nil {
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
		           AND `+subtreeFilePredicateSQL+`
		           AND `+audioExtCond+`) AS audio_count
		FROM entries d
		WHERE d.is_dir = 1
		  AND d.parent_path = ?
		  AND EXISTS (
		    SELECT 1 FROM entries f
		    WHERE f.is_dir = 0
		      AND `+subtreeFilePredicateSQL+`
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
func scanLibraryFolder(row interface{ Scan(...any) error }) (*LibraryFolder, error) {
	var f LibraryFolder
	var createdAtStr, updatedAtStr string
	if err := row.Scan(
		&f.ID,
		&f.LibraryID,
		&f.Path,
		&f.Name,
		&f.RelativePath,
		&f.AudioFileCount,
		&createdAtStr,
		&updatedAtStr,
	); err != nil {
		return nil, err
	}
	f.CreatedAt = parseTimestamp(createdAtStr)
	f.UpdatedAt = parseTimestamp(updatedAtStr)
	return &f, nil
}

// ListEntriesUnderPath returns entries under a path prefix (including the
// prefix itself) for tree building. Path identity is binary and slash-boundary
// exact, so folder names like "100%_hits" match only their own subtree and
// case-distinct siblings stay distinct.
func (r *Repository) ListEntriesUnderPath(pathPrefix string) ([]EntryRow, error) {
	pathPrefix = pathnorm.NormalizeToPOSIX(pathPrefix)
	rows, err := r.db.Query(`
		SELECT path, parent_path, name, is_dir, size, mtime, bitrate, format
		FROM entries
		WHERE path = ? OR substr(path, 1, length(?) + 1) = ? || '/'
		ORDER BY path
	`, pathPrefix, pathPrefix, pathPrefix)
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
