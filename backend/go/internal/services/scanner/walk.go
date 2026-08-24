package scanner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// dirEntryInfoFunc is a seam for testing Info() error paths.
// In production, this is nil and job.d.Info() is called directly.
// Tests can override to inject deterministic errors.
var dirEntryInfoFunc func(fs.DirEntry) (fs.FileInfo, error)

// DirEntry represents a directory entry from walking
type DirEntry struct {
	Path       string
	ParentPath string
	Name       string
	IsDir      bool
	Size       int64
	Mtime      int64
}

// walkDirItem represents a directory to be processed in parallel walk
type walkDirItem struct {
	path string
}

type walkResult struct {
	subdirs []string
	err     error
}

// WalkRootEntriesParallel walks the root directory using bounded parallel directory descent
// with inline metadata collection. It does NOT emit the root itself.
//
// Invariant: Root = parallel directory descent + inline metadata + pipeline
//
// Parameters:
//   - ctx: context for cancellation
//   - rootPath: the root directory to walk
//   - basePath: the base path for parent path normalization
//   - dirConcurrency: number of concurrent directory readers (default 4)
//   - emit: callback for each DirEntry found; returning error cancels the walk
//
// Returns error if walk fails or if emit returns error.
func WalkRootEntriesParallel(ctx context.Context, rootPath, basePath string, dirConcurrency int, emit func(DirEntry) error) error {
	if dirConcurrency <= 0 {
		dirConcurrency = 4 // Default as per plan
	}
	if dirConcurrency > 32 {
		dirConcurrency = 32
	}

	// Context for cancellation on first error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Worker input/output channels.
	// IMPORTANT: only coordinator enqueues jobs; workers only consume jobs and report results.
	jobCh := make(chan walkDirItem, dirConcurrency*2)
	resultCh := make(chan walkResult, dirConcurrency*2)

	var wg sync.WaitGroup

	// Start worker pool
	for i := 0; i < dirConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case dir, ok := <-jobCh:
					if !ok {
						return
					}

					dirEntries, readErr := os.ReadDir(dir.path)
					if readErr != nil {
						select {
						case <-ctx.Done():
							return
						case resultCh <- walkResult{err: readErr}:
						}
						return
					}

					subdirs := make([]string, 0)
					for _, d := range dirEntries {
						select {
						case <-ctx.Done():
							return
						default:
						}

						entryPath := filepath.Join(dir.path, d.Name())
						parentPath := filepath.Dir(entryPath)
						if parentPath == "." {
							parentPath = basePath
						}

						// Inline metadata collection - call Info() directly
						var info fs.FileInfo
						var infoErr error
						if dirEntryInfoFunc != nil {
							info, infoErr = dirEntryInfoFunc(d)
						} else {
							info, infoErr = d.Info()
						}

						if infoErr != nil {
							select {
							case <-ctx.Done():
								return
							case resultCh <- walkResult{err: infoErr}:
							}
							return
						}

						entry := DirEntry{
							Path:       entryPath,
							ParentPath: parentPath,
							Name:       d.Name(),
							IsDir:      d.IsDir(),
							Size:       info.Size(),
							Mtime:      info.ModTime().Unix(),
						}

						if emitErr := emit(entry); emitErr != nil {
							select {
							case <-ctx.Done():
								return
							case resultCh <- walkResult{err: emitErr}:
							}
							return
						}

						if d.IsDir() {
							subdirs = append(subdirs, entryPath)
						}
					}

					select {
					case <-ctx.Done():
						return
					case resultCh <- walkResult{subdirs: subdirs}:
					}
				}
			}
		}()
	}

	// Coordinator loop: owns work accounting and all enqueue operations.
	pendingWork := 1
	queued := []walkDirItem{{path: rootPath}}
	for pendingWork > 0 {
		var sendCh chan walkDirItem
		var next walkDirItem
		if len(queued) > 0 {
			sendCh = jobCh
			next = queued[0]
		}

		select {
		case <-ctx.Done():
			close(jobCh)
			wg.Wait()
			return ctx.Err()
		case sendCh <- next:
			queued = queued[1:]
		case res := <-resultCh:
			pendingWork--

			if res.err != nil {
				cancel()
				close(jobCh)
				wg.Wait()
				return res.err
			}

			if len(res.subdirs) > 0 {
				pendingWork += len(res.subdirs)
				for _, subdir := range res.subdirs {
					queued = append(queued, walkDirItem{path: subdir})
				}
			}
		}
	}

	close(jobCh)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}

	return nil
}

// WalkFolderEntries walks a single folder using os.ReadDir (single-enumerator pattern)
// with inline metadata collection. It does NOT emit the folder itself.
//
// Invariant: Folder = single-enumerator directory discovery + inline metadata + pipeline
//
// Parameters:
//   - ctx: context for cancellation
//   - folderPath: the folder to walk
//   - basePath: the base path for parent path normalization
//   - emit: callback for each DirEntry found; returning error cancels the walk
//
// Returns error if walk fails or if emit returns error.
//
// CONTRACT: This function is streaming - it MUST call emit immediately for each
// entry as it's discovered, without pre-aggregating into []DirEntry.
func WalkFolderEntries(ctx context.Context, folderPath, basePath string, emit func(DirEntry) error) error {
	// Validate context
	if err := ctx.Err(); err != nil {
		return err
	}

	var walk func(string) error
	walk = func(currentPath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		dirEntries, err := os.ReadDir(currentPath)
		if err != nil {
			return err
		}

		for _, d := range dirEntries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			entryPath := filepath.Join(currentPath, d.Name())
			parentPath := filepath.Dir(entryPath)
			if parentPath == "." {
				parentPath = basePath
			}

			// Inline metadata collection - call Info() directly
			var info fs.FileInfo
			var infoErr error
			if dirEntryInfoFunc != nil {
				info, infoErr = dirEntryInfoFunc(d)
			} else {
				info, infoErr = d.Info()
			}

			if infoErr != nil {
				return infoErr
			}

			entry := DirEntry{
				Path:       entryPath,
				ParentPath: parentPath,
				Name:       d.Name(),
				IsDir:      d.IsDir(),
				Size:       info.Size(),
				Mtime:      info.ModTime().Unix(),
			}

			// Emit entry via callback - IMMEDIATE STREAMING
			if emitErr := emit(entry); emitErr != nil {
				return emitErr
			}

			if d.IsDir() {
				if err := walk(entryPath); err != nil {
					return err
				}
			}
		}

		return nil
	}

	return walk(folderPath)
}

// Allowed audio extensions
var AllowedExtensions = map[string]bool{
	".wav":  true,
	".flac": true,
	".mp3":  true,
	".aac":  true,
	".m4a":  true,
}

// FileEntry represents a scanned file
type FileEntry struct {
	Path      string
	PathPosix string
	FileSize  int64
}

// ScanDirectory scans a directory for audio files
func ScanDirectory(rootPath string) ([]FileEntry, error) {
	var entries []FileEntry

	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Check extension
		ext := filepath.Ext(path)
		if !AllowedExtensions[ext] {
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			return nil // Skip files we can't stat
		}

		// Convert path to POSIX format
		posixPath := filepath.ToSlash(path)

		entries = append(entries, FileEntry{
			Path:      path,
			PathPosix: posixPath,
			FileSize:  info.Size(),
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	return entries, nil
}
