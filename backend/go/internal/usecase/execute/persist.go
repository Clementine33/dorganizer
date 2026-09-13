package execute

import (
	"database/sql"
	"log"
	"path/filepath"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// persistExecuteErrorGlobal persists an execute-level error with no folder attribution.
func (s *serviceImpl) persistExecuteErrorGlobal(code, message string) {
	if s.repo == nil {
		return
	}
	if err := s.repo.CreateErrorEvent(&sqlite.ErrorEvent{
		Scope:     "execute",
		RootPath:  "",
		Code:      code,
		Message:   message,
		Retryable: false,
	}); err != nil {
		log.Printf("warning: failed to persist execute error event: %v", err)
	}
}

// executeRepoAdapter adapts sqlite.Repository to services/execute.ExecuteRepository.
type executeRepoAdapter struct {
	repo *sqlite.Repository
}

func newExecuteRepoAdapter(repo *sqlite.Repository) *executeRepoAdapter {
	return &executeRepoAdapter{repo: repo}
}

func (a *executeRepoAdapter) CreateExecuteSession(sessionID, planID, rootPath, status string) error {
	return a.repo.CreateExecuteSession(&sqlite.ExecuteSession{
		SessionID: sessionID,
		PlanID:    planID,
		RootPath:  toSlash(rootPath),
		Status:    status,
	})
}

func (a *executeRepoAdapter) UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	return a.repo.UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage)
}

// GetEntryContentRev reads the content_rev for a path from the entries table.
// Returns 0 if the entry is not found.
func (a *executeRepoAdapter) GetEntryContentRev(path string) (int, error) {
	var contentRev int
	err := a.repo.DB().
		QueryRow("SELECT COALESCE(content_rev, 0) FROM entries WHERE path = ?", filepath.ToSlash(path)).
		Scan(&contentRev)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return contentRev, err
}

func toSlash(p string) string {
	if p == "" {
		return ""
	}
	// Strip trailing separators and use forward slashes.
	for len(p) > 0 && (p[len(p)-1] == '/' || p[len(p)-1] == '\\') {
		p = p[:len(p)-1]
	}
	s := ""
	var sSb90 strings.Builder
	for _, r := range p {
		if r == '\\' {
			sSb90.WriteRune('/')
		} else {
			sSb90.WriteRune(r)
		}
	}
	s += sSb90.String()
	return s
}
