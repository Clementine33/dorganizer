package scanner

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	pipelineBatchSize      = 1000
	taskQueueSize          = 100 // Buffered channel for backpressure
	defaultRootConcurrency = 4
)

// Repository interface for scanner
type Repository interface {
	WriteStagingEntries(sessionID string, entries []StagingEntry) error
	MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error)
	CreateScanSession(session *ScanSession) error
	UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error
}

// ScanSession represents a scan session
type ScanSession struct {
	SessionID    string
	RootPath     string
	ScopePath    *string
	Kind         string
	Status       string
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// StagingEntry represents an entry to be written to staging
type StagingEntry struct {
	SessionID  string
	Path       string
	RootPath   string
	ParentPath string
	Name       string
	IsDir      bool
	Size       int64
	Mtime      int64
	Format     string
}

func detectFormatFromPath(path string, isDir bool) string {
	if isDir {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			return strings.TrimSpace(mt[:i])
		}
		return mt
	}
	// Fallbacks for common audio extensions not always present in MIME db.
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg":
		return "audio/ogg"
	default:
		return ""
	}
}

// ScannerService handles filesystem scanning
type ScannerService struct {
	repo            Repository
	rootConcurrency int
}

// NewScannerService creates a new scanner service
func NewScannerService(repo Repository) *ScannerService {
	return &ScannerService{
		repo:            repo,
		rootConcurrency: defaultRootConcurrency,
	}
}

// ScanRoot performs a full scan of the given root path using
// parallel directory descent + inline metadata + staging pipeline.
//
// Invariant: Root = parallel directory descent + inline metadata + pipeline
func (s *ScannerService) ScanRoot(rootPath string) (string, error) {
	sessionID := uuid.New().String()

	// Normalize root_path to POSIX for consistent storage and matching
	normalizedRootPath := filepath.ToSlash(rootPath)

	// Create scan session
	session := &ScanSession{
		SessionID: sessionID,
		RootPath:  normalizedRootPath,
		Kind:      "full",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if s.repo != nil {
		if err := s.repo.CreateScanSession(session); err != nil {
			return "", fmt.Errorf("create scan session: %w", err)
		}
	}

	// Run pipeline: walk + batch write to staging
	//
	// CONTRACT:
	// - producer emits to entryCh (buffered with backpressure)
	// - consumer batches and writes (batchSize=1000)
	// - producer closes entryCh when done
	// - consumer exits on entryCh close or ctx cancellation
	// - first error from either triggers ctx.cancel and stops both
	// - after waiting both, if any error, cleanup staging and mark failed
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entryCh := make(chan StagingEntry, taskQueueSize)
	var producerErr, consumerErr error
	var wg sync.WaitGroup

	// Producer: walk and emit to channel
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(entryCh) // Only close when producer exits

		emitFn := func(entry DirEntry) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case entryCh <- DirEntryToStagingEntry(entry, sessionID, normalizedRootPath):
				return nil
			}
		}

		if err := WalkRootEntriesParallel(ctx, rootPath, rootPath, s.rootConcurrency, emitFn); err != nil {
			producerErr = err
			cancel()
		}
	}()

	// Consumer: batch writes from channel
	var cleanupFn func(string) error
	if pipeline, ok := s.repo.(pipelineRepo); ok {
		cleanupFn = pipeline.CleanupStagingSession
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		batch := make([]StagingEntry, 0, pipelineBatchSize)

		for entry := range entryCh {
			batch = append(batch, entry)

			if len(batch) >= pipelineBatchSize {
				// Flush batch
				if s.repo != nil {
					if pipeline, ok := s.repo.(pipelineRepo); ok {
						if err := pipeline.WriteStagingBatch(sessionID, batch); err != nil {
							consumerErr = err
							cancel()
							return
						}
					} else {
						consumerErr = fmt.Errorf("repo does not support pipeline batch writing")
						cancel()
						return
					}
				}
				batch = batch[:0] // Clear batch for reuse
			}
		}

		// Flush final batch
		if len(batch) > 0 && s.repo != nil {
			if pipeline, ok := s.repo.(pipelineRepo); ok {
				if err := pipeline.WriteStagingBatch(sessionID, batch); err != nil {
					consumerErr = err
				}
			}
		}
	}()

	// Wait for both producer and consumer to finish
	wg.Wait()

	// Check for errors from either
	if producerErr != nil || consumerErr != nil {
		// Try to cleanup staging on failure
		// Note: Partial persistence is expected - cleanup is best-effort
		var cleanupFailure error
		if cleanupFn != nil {
			cleanupFailure = cleanupFn(sessionID)
		}

		// Update session status to failed
		if s.repo != nil {
			if producerErr != nil {
				s.repo.UpdateScanSessionStatus(sessionID, "failed", "SCAN_WALK_FAILED", producerErr.Error())
			} else {
				s.repo.UpdateScanSessionStatus(sessionID, "failed", "STAGING_WRITE_FAILED", consumerErr.Error())
			}
		}

		// Return primary error (not cleanup failure)
		_ = cleanupFailure // We could log this, but primary error is what's expected
		if producerErr != nil {
			return "", fmt.Errorf("walk directory: %w", producerErr)
		}
		return "", fmt.Errorf("write staging: %w", consumerErr)
	}

	// Both succeeded - proceed to merge
	if s.repo != nil {
		// Transition to merging status before merge
		if err := s.repo.UpdateScanSessionStatus(sessionID, "merging", "", ""); err != nil {
			return "", fmt.Errorf("update session status to merging: %w", err)
		}

		// Merge into main catalog
		_, err := s.repo.MergeStaging(sessionID, normalizedRootPath, nil)
		if err != nil {
			s.repo.UpdateScanSessionStatus(sessionID, "failed", "MERGE_FAILED", err.Error())
			return "", fmt.Errorf("merge staging: %w", err)
		}

		if err := s.repo.UpdateScanSessionStatus(sessionID, "completed", "", ""); err != nil {
			return "", fmt.Errorf("update session status to completed: %w", err)
		}
	}

	return sessionID, nil
}

// ScanFolder performs a scoped scan of a single folder using
// single-enumerator directory discovery + inline metadata + staging pipeline.
//
// Invariant: Folder = single-enumerator directory discovery + inline metadata + pipeline
func (s *ScannerService) ScanFolder(folderPath, rootPath string) (string, error) {
	sessionID := uuid.New().String()

	// Normalize paths to POSIX for consistent storage and matching
	normalizedRootPath := filepath.ToSlash(rootPath)
	scopePath := filepath.ToSlash(folderPath)
	session := &ScanSession{
		SessionID: sessionID,
		RootPath:  normalizedRootPath,
		ScopePath: &scopePath,
		Kind:      "folder",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if s.repo != nil {
		if err := s.repo.CreateScanSession(session); err != nil {
			return "", fmt.Errorf("create scan session: %w", err)
		}
	}

	// Run pipeline: walk + batch write to staging
	//
	// CONTRACT (Folder-specific):
	// - producer emits to entryCh (buffered with backpressure)
	// - Must NOT pre-aggregate into []DirEntry - must stream immediately
	// - producer is the ONLY close(entryCh) caller
	// - writer triggers cancel() on first error
	// - producer respects ctx.Done() and stops emitting
	// - main waits for both before merge decision
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entryCh := make(chan StagingEntry, taskQueueSize)
	var producerErr, consumerErr error
	var wg sync.WaitGroup

	// Producer: walk and emit to channel (STREAMING, NO PRE-AGGREGATION)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(entryCh) // Producer owns close

		emitFn := func(entry DirEntry) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case entryCh <- DirEntryToStagingEntry(entry, sessionID, normalizedRootPath):
				return nil
			}
		}

		if err := WalkFolderEntries(ctx, folderPath, rootPath, emitFn); err != nil {
			producerErr = err
			cancel()
		}
	}()

	// Consumer: batch writes from channel
	var cleanupFn func(string) error
	if pipeline, ok := s.repo.(pipelineRepo); ok {
		cleanupFn = pipeline.CleanupStagingSession
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		batch := make([]StagingEntry, 0, pipelineBatchSize)

		for entry := range entryCh {
			batch = append(batch, entry)

			if len(batch) >= pipelineBatchSize {
				if s.repo != nil {
					if pipeline, ok := s.repo.(pipelineRepo); ok {
						if err := pipeline.WriteStagingBatch(sessionID, batch); err != nil {
							consumerErr = fmt.Errorf("write staging batch: %w", err)
							cancel()
							return
						}
					} else {
						consumerErr = fmt.Errorf("repo does not support pipeline batch writing")
						cancel()
						return
					}
				}
				batch = batch[:0] // Clear for reuse
			}
		}

		// Flush final batch
		if len(batch) > 0 && s.repo != nil {
			if pipeline, ok := s.repo.(pipelineRepo); ok {
				if err := pipeline.WriteStagingBatch(sessionID, batch); err != nil {
					consumerErr = fmt.Errorf("write final staging batch: %w", err)
				}
			}
		}
	}()

	// Wait for both producer and consumer
	wg.Wait()

	// Check for errors
	if producerErr != nil || consumerErr != nil {
		// Cleanup staging on failure
		var cleanupFailure error
		if cleanupFn != nil {
			cleanupFailure = cleanupFn(sessionID)
		}

		// Update session status
		if s.repo != nil {
			if producerErr != nil {
				s.repo.UpdateScanSessionStatus(sessionID, "failed", "SCAN_WALK_FAILED", producerErr.Error())
			} else {
				s.repo.UpdateScanSessionStatus(sessionID, "failed", "STAGING_WRITE_FAILED", consumerErr.Error())
			}
		}

		_ = cleanupFailure // Best effort cleanup
		if producerErr != nil {
			return "", producerErr
		}
		return "", consumerErr
	}

	// Both succeeded - merge
	if s.repo != nil {
		if err := s.repo.UpdateScanSessionStatus(sessionID, "merging", "", ""); err != nil {
			return "", err
		}

		_, err := s.repo.MergeStaging(sessionID, normalizedRootPath, nil)
		if err != nil {
			s.repo.UpdateScanSessionStatus(sessionID, "failed", "MERGE_FAILED", err.Error())
			return "", err
		}

		if err := s.repo.UpdateScanSessionStatus(sessionID, "completed", "", ""); err != nil {
			return "", err
		}
	}

	return sessionID, nil
}

// DirEntryToStagingEntry converts a DirEntry to StagingEntry with format detection
func DirEntryToStagingEntry(entry DirEntry, sessionID, rootPath string) StagingEntry {
	return StagingEntry{
		SessionID:  sessionID,
		Path:       filepath.ToSlash(entry.Path),
		RootPath:   filepath.ToSlash(rootPath),
		ParentPath: filepath.ToSlash(entry.ParentPath),
		Name:       entry.Name,
		IsDir:      entry.IsDir,
		Size:       entry.Size,
		Mtime:      entry.Mtime,
		Format:     detectFormatFromPath(entry.Path, entry.IsDir),
	}
}
