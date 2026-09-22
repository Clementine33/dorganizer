package conversion

import (
	"context"
	"encoding/json"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// bitrateAnalyzer probes the missing MP3/AAC bitrates of a planning root with
// ffprobe and persists them on the scanned entries. It is part of planning: the
// probed rate is the first entry of the encoded-lane gate — a file measuring its
// target is skipped however it was made — so the facts it reads must be as
// exact as the inventory can make them.
type bitrateAnalyzer struct {
	inventory   Inventory
	ffprobePath string
}

func newBitrateAnalyzer(inv Inventory, ffprobePath string) *bitrateAnalyzer {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	return &bitrateAnalyzer{inventory: inv, ffprobePath: ffprobePath}
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
	updates := make([]inventory.BitrateUpdate, 0, len(idx))
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
				updates = append(updates, inventory.BitrateUpdate{Path: entries[i].PathPosix, BitrateKbps: bitrate})
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

	// Nothing probed means nothing to write: a root whose files all still fail
	// to probe leaves its stored rates as they were.
	if len(updates) == 0 {
		return nil
	}

	// The write shape (per row, or chunked in one transaction) and its retry
	// on a locked database belong to the adapter that owns the table.
	return a.inventory.UpdateEntryBitrates(updates, batchUpdate)
}

func (a *bitrateAnalyzer) probeBitrate(ctx context.Context, pathPosix string) (int64, error) {
	// The stream's own bit rate is the exact fact (MP3, AAC). Ogg/Opus declares
	// none, so the container's average is read as the fallback: for the CBR the
	// executor writes, that average sits a little above the target, which is
	// what the satisfaction comparison can use. A format that reports neither
	// stays unknown, and an unknown bitrate is never assumed adequate.
	//nolint:gosec // Tool path comes from local config; arguments are passed directly without a shell.
	data, err := exec.CommandContext(ctx, a.ffprobePath, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=bit_rate:format=bit_rate", "-of", "json",
		"-i", filepath.FromSlash(pathPosix)).Output()
	if err != nil {
		return 0, err
	}
	var result struct {
		Streams []struct {
			Bitrate string `json:"bit_rate"`
		} `json:"streams"`
		Format struct {
			Bitrate string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	if len(result.Streams) != 1 {
		return 0, nil
	}
	if result.Streams[0].Bitrate != "" {
		return strconv.ParseInt(result.Streams[0].Bitrate, 10, 64)
	}
	if result.Format.Bitrate == "" {
		return 0, nil
	}
	return strconv.ParseInt(result.Format.Bitrate, 10, 64)
}

// selectScopedProbeCandidates names the encoded containers whose bitrate is
// worth asking ffprobe for: the targets the planner compares a stored file
// against.
func selectScopedProbeCandidates(entries []reconcile.AudioEntry) []int {
	idx := make([]int, 0, len(entries))
	for i := range entries {
		if entries[i].Bitrate > 0 {
			continue
		}
		switch strings.ToLower(path.Ext(entries[i].PathPosix)) {
		case ".mp3", ".aac", ".m4a", ".opus":
			idx = append(idx, i)
		}
	}
	return idx
}
