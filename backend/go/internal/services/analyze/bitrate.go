package analyze

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dmulholl/mp3lib"
)

const bitrateUpdateBatchSize = 100

func probeBitrate(pathPosix string) (int64, error) {
	f, err := os.Open(filepath.FromSlash(pathPosix))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	frame := mp3lib.NextFrame(f)
	if frame == nil {
		return 0, nil
	}
	if mp3lib.IsXingHeader(frame) || mp3lib.IsVbriHeader(frame) {
		frame = mp3lib.NextFrame(f)
		if frame == nil {
			return 0, nil
		}
	}
	if frame.BitRate <= 0 {
		return 0, nil
	}
	return int64(frame.BitRate), nil
}

func (a *Analyzer) enrichMissingMP3Bitrate(entries []Entry, batchUpdate bool) error {
	idx := selectScopedProbeCandidates(entries)
	if len(idx) == 0 {
		return nil
	}

	workers := 4
	if len(idx) < workers {
		workers = len(idx)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	updates := make([]bitrateUpdate, 0, len(idx))
	var updatesMu sync.Mutex
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				bitrate, err := probeBitrate(entries[i].PathPosix)
				if err != nil || bitrate <= 0 {
					continue
				}
				entries[i].Bitrate = bitrate
				updatesMu.Lock()
				updates = append(updates, bitrateUpdate{pathPosix: entries[i].PathPosix, bitrate: bitrate})
				updatesMu.Unlock()
			}
		}()
	}

	for _, i := range idx {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if err := a.persistBitrateUpdates(updates, batchUpdate); err != nil {
		return err
	}

	return nil
}

type bitrateUpdate struct {
	pathPosix string
	bitrate   int64
}

func selectScopedProbeCandidates(entries []Entry) []int {
	idx := make([]int, 0, len(entries))
	for i := range entries {
		if strings.ToLower(path.Ext(entries[i].PathPosix)) == ".mp3" && entries[i].Bitrate <= 0 {
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
		end := start + chunkSize
		if end > len(updates) {
			end = len(updates)
		}
		chunks = append(chunks, updates[start:end])
	}

	return chunks
}

func (a *Analyzer) persistBitrateUpdates(updates []bitrateUpdate, batchUpdate bool) error {
	if !batchUpdate {
		for _, update := range updates {
			if _, err := a.repo.DB().Exec("UPDATE entries SET bitrate = ?, updated_at = datetime('now') WHERE path = ?", update.bitrate, update.pathPosix); err != nil {
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

func buildBatchBitrateUpdateQuery(chunk []bitrateUpdate) (string, []interface{}) {
	var b strings.Builder
	b.Grow(128 + len(chunk)*32)

	b.WriteString("UPDATE entries SET bitrate = CASE path")
	args := make([]interface{}, 0, len(chunk)*3)
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
