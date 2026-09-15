package plan

import (
	"context"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

const bitrateUpdateBatchSize = 100

// bitratePersistRetryLimit is the maximum number of retry attempts for
// SQLITE_BUSY/SQLITE_LOCKED errors during bitrate persistence.
const bitratePersistRetryLimit = 3

// bitratePersistRetryBase is the base delay for retry backoff.
const bitratePersistRetryBase = 50 * time.Millisecond

// bitrateAnalyzer probes the missing MP3/AAC bitrates of a planning root with
// ffprobe and persists them on the scanned entries. It is part of planning:
// the reconcile compares observed bitrates against the desired output specs,
// so the facts it reads must be as exact as the inventory can make them.
type bitrateAnalyzer struct {
	repo        *sqlite.Repository
	ffprobePath string
}

func newBitrateAnalyzer(repo *sqlite.Repository, ffprobePath string) *bitrateAnalyzer {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	return &bitrateAnalyzer{repo: repo, ffprobePath: ffprobePath}
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked")
}

// enrichMissing probes the entries whose bitrate is unknown and persists the
// probed values on the entries table.
func (a *bitrateAnalyzer) enrichMissing(ctx context.Context, entries []reconcile.AudioEntry, batchUpdate bool) error {
	idx := selectScopedProbeCandidates(entries)
	if len(idx) == 0 {
		return nil
	}

	workers := min(len(idx), 4)

	jobs := make(chan int)
	var wg sync.WaitGroup
	updates := make([]bitrateUpdate, 0, len(idx))
	var updatesMu sync.Mutex
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				bitrate, err := a.probeBitrate(ctx, entries[i].PathPosix)
				if err != nil || bitrate <= 0 {
					continue
				}
				entries[i].Bitrate = bitrate
				updatesMu.Lock()
				updates = append(updates, bitrateUpdate{pathPosix: entries[i].PathPosix, bitrate: bitrate})
				updatesMu.Unlock()
			}
		})
	}

	for _, i := range idx {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := a.persistBitrateUpdates(updates, batchUpdate); err != nil {
		return err
	}

	return nil
}

func (a *bitrateAnalyzer) probeBitrate(ctx context.Context, pathPosix string) (int64, error) {
	//nolint:gosec // Tool path comes from local config; arguments are passed directly without a shell.
	data, err := exec.CommandContext(ctx, a.ffprobePath, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=bit_rate", "-of", "json", "-i", filepath.FromSlash(pathPosix)).Output()
	if err != nil {
		return 0, err
	}
	var result struct {
		Streams []struct {
			Bitrate string `json:"bit_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	if len(result.Streams) != 1 {
		return 0, nil
	}
	return strconv.ParseInt(result.Streams[0].Bitrate, 10, 64)
}

type bitrateUpdate struct {
	pathPosix string
	bitrate   int64
}

func selectScopedProbeCandidates(entries []reconcile.AudioEntry) []int {
	idx := make([]int, 0, len(entries))
	for i := range entries {
		if entries[i].Bitrate > 0 {
			continue
		}
		switch strings.ToLower(path.Ext(entries[i].PathPosix)) {
		case ".mp3", ".aac", ".m4a":
			idx = append(idx, i)
		}
	}
	return idx
}

func chunkBitrateUpdates(updates []bitrateUpdate, chunkSize int) [][]bitrateUpdate {
	if chunkSize <= 0 || len(updates) == 0 {
		return nil
	}

	chunks := make([][]bitrateUpdate, 0, (len(updates)+chunkSize-1)/chunkSize)
	for start := 0; start < len(updates); start += chunkSize {
		end := min(start+chunkSize, len(updates))
		chunks = append(chunks, updates[start:end])
	}

	return chunks
}

func (a *bitrateAnalyzer) persistBitrateUpdates(updates []bitrateUpdate, batchUpdate bool) error {
	if len(updates) == 0 {
		return nil
	}

	// Serialize DB writes across concurrent planner goroutines sharing the
	// same Repository. This prevents SQLITE_BUSY when multiple root goroutines
	// persist bitrate updates concurrently.
	a.repo.BitrateWriteMu.Lock()
	defer a.repo.BitrateWriteMu.Unlock()

	var lastErr error
	for attempt := 0; attempt <= bitratePersistRetryLimit; attempt++ {
		if attempt > 0 {
			// Linear backoff with small jitter.
			delay := bitratePersistRetryBase * time.Duration(attempt)
			jitter := time.Duration(rand.Int63n(int64(bitratePersistRetryBase)))
			time.Sleep(delay + jitter)
		}

		err := a.persistBitrateUpdatesOnce(updates, batchUpdate)
		if err == nil {
			return nil
		}
		if !isSQLiteBusyLockedError(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (a *bitrateAnalyzer) persistBitrateUpdatesOnce(updates []bitrateUpdate, batchUpdate bool) error {
	if !batchUpdate {
		for _, update := range updates {
			if _, err := a.repo.DB().
				Exec("UPDATE entries SET bitrate = ?, updated_at = datetime('now') WHERE path = ?", update.bitrate, update.pathPosix); err != nil {
				return err
			}
		}
		return nil
	}

	chunks := chunkBitrateUpdates(updates, bitrateUpdateBatchSize)
	if len(chunks) == 0 {
		return nil
	}

	tx, err := a.repo.DB().Begin()
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, chunk := range chunks {
		query, args := buildBatchBitrateUpdateQuery(chunk)
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true

	return nil
}

func buildBatchBitrateUpdateQuery(chunk []bitrateUpdate) (string, []any) {
	var b strings.Builder
	b.Grow(128 + len(chunk)*32)

	b.WriteString("UPDATE entries SET bitrate = CASE path")
	args := make([]any, 0, len(chunk)*3)
	for _, u := range chunk {
		b.WriteString(" WHEN ? THEN ?")
		args = append(args, u.pathPosix, u.bitrate)
	}
	b.WriteString(" ELSE bitrate END, updated_at = datetime('now') WHERE path IN (")
	for i, u := range chunk {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args = append(args, u.pathPosix)
	}
	b.WriteByte(')')

	return b.String(), args
}
