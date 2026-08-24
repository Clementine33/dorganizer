package execute

import (
	"errors"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// ToolsConfig represents the tools configuration for conversion.
type ToolsConfig struct {
	Encoder  string
	QAACPath string
	LAMEPath string
}

// ExecuteConfig represents the execution configuration.
type ExecuteConfig struct {
	MaxIOWorkers           int
	PrecheckConcurrentStat bool
}

// Service handles delete/convert execution.
type Service struct {
	runner *ToolRunner
}

// PlanItemType represents the type of plan item.
type PlanItemType int

const (
	ItemTypeDelete PlanItemType = iota
	ItemTypeConvert
)

// PlanItem represents a single item to execute.
type PlanItem struct {
	Type PlanItemType
	Src  string
	Dst  string

	// Snapshot-based execution fields (P0)
	SourcePath             string
	TargetPath             string
	PreconditionPath       string
	PreconditionContentRev int
	PreconditionSize       int64
	PreconditionMtime      int64
}

// ExecuteRepository is the persistence contract used by ExecuteService.
type ExecuteRepository interface {
	CreateExecuteSession(sessionID, planID, rootPath, status string) error
	UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error
}

type contentRevProvider interface {
	GetEntryContentRev(path string) (int, error)
}

// Plan is a persisted execution plan snapshot.
type Plan struct {
	PlanID     string
	RootPath   string
	Items      []PlanItem
	SoftDelete bool
}

// ExecuteResult is the outcome of executing a plan.
type ExecuteResult struct {
	SessionID string
	PlanID    string
	Status    string
	ErrorCode string
	ErrorMsg  string
	// Task 4: structured error details for event emission
	Stage          string
	Code           string
	ItemSourcePath string
	ItemTargetPath string
	FolderPath     string
	RootPath       string
	// Task 4: folder-level results
	SuccessfulFolderPaths []string
	FailedFolderPaths     []string
}

// toolRunner is the interface for tool execution (allows mocking in tests).
type toolRunner interface {
	Convert(src, dst string) error
	Delete(path string, soft bool) error
}

// EventHandler is called when an event occurs during execution.
type EventHandler interface {
	OnPreconditionFailed(itemIndex int, item PlanItem, err error)
	OnStage1CopyFailed(itemIndex int, item PlanItem, err error)
	OnStage2EncodeFailed(itemIndex int, item PlanItem, err error)
	OnStage3CommitFailed(itemIndex int, item PlanItem, err error)
	OnDeleteFailed(itemIndex int, item PlanItem, err error)
	// OnItemCompleted is called when an item has been fully processed (success or failure).
	// The usecase uses this to track item completion and determine folder lifecycle boundaries.
	OnItemCompleted(itemIndex int, item PlanItem)
}

// ExecuteService performs plan-level execution with precondition validation.
type ExecuteService struct {
	repo         ExecuteRepository
	runner       toolRunner
	toolsConfig  ToolsConfig
	config       ExecuteConfig                        // Execution config like max_io_workers
	scratchRoot  string                               // prototype: SSD scratch directory root
	copyCallback func(itemIndex int, src, dst string) // test hook for observing copy completion
	// Task 4: event callback
	eventHandler EventHandler
}

// MapError maps tool errors to domain error codes.
func MapError(err error) errdomain.DomainErrorCode {
	if err == nil {
		return 0
	}

	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Code
	}

	return 0
}
