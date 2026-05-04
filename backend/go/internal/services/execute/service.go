package execute

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// NewService creates a new execute service.
func NewService(toolsConfig ToolsConfig) *Service {
	return &Service{
		runner: NewToolRunner(toolsConfig),
	}
}

// ExecuteItem executes a single plan item (delete or convert).
func (s *Service) ExecuteItem(item PlanItem, softDelete bool) error {
	switch item.Type {
	case ItemTypeDelete:
		return s.runner.Delete(item.Src, softDelete)
	case ItemTypeConvert:
		return s.runner.Convert(item.Src, item.Dst)
	default:
		return errors.New("unknown item type")
	}
}

// NewExecuteService creates a plan-level execute service.
func NewExecuteService(repo ExecuteRepository, toolsConfig ToolsConfig) *ExecuteService {
	return &ExecuteService{
		repo:        repo,
		runner:      NewToolRunner(toolsConfig),
		toolsConfig: toolsConfig,
		config: ExecuteConfig{ // Conservative defaults
			MaxIOWorkers:           4,
			PrecheckConcurrentStat: false,
		},
		scratchRoot: defaultScratchRoot(),
	}
}

// SetExecuteConfig updates the execution configuration.
func (s *ExecuteService) SetExecuteConfig(cfg ExecuteConfig) {
	if cfg.MaxIOWorkers < 1 {
		cfg.MaxIOWorkers = 4
	}
	s.config = cfg
}

// SetRunner sets a custom tool runner (for testing).
func (s *ExecuteService) SetRunner(r toolRunner) {
	s.runner = r
}

// SetEventHandler sets the event handler for execution events (Task 4).
func (s *ExecuteService) SetEventHandler(h EventHandler) {
	s.eventHandler = h
}

// ExecutePlan validates preconditions then executes plan items.
// Plan-order semantics: items are executed in plan order with batching for converts.
// When a delete is encountered, the current batch of converts is processed first.
// If converts fail, the delete is NOT executed (preserving order semantics).
// Task 4: Implements per-folder fail-fast and structured error events.
func (s *ExecuteService) ExecutePlan(plan *Plan) (*ExecuteResult, error) {
	precheckItemsCount := 0
	if plan != nil {
		precheckItemsCount = len(plan.Items)
	}
	var (
		precheckStatMs        int64
		sqliteBusyLockedCount int
	)
	recordSQLiteBusyLocked := func(err error) {
		if isSQLiteBusyLockedError(err) {
			sqliteBusyLockedCount++
		}
	}
	defer func() {
		log.Printf("execute.precheck_items_count=%d execute.precheck_stat_ms=%d sqlite.busy_locked_count=%d", precheckItemsCount, precheckStatMs, sqliteBusyLockedCount)
	}()

	// Update runner with rootPath for soft delete support
	if plan.RootPath != "" {
		// Preserve injected test doubles: only replace default concrete runner.
		if _, ok := s.runner.(*ToolRunner); ok {
			s.runner = NewToolRunnerWithRoot(s.toolsConfig, plan.RootPath)
		}
	}

	sessionID := uuid.NewString()
	if s.repo != nil {
		if err := s.repo.CreateExecuteSession(sessionID, plan.PlanID, plan.RootPath, "running"); err != nil {
			recordSQLiteBusyLocked(err)
			return nil, fmt.Errorf("create execute session: %w", err)
		}
	}

	// Check if plan contains any convert operations - only validate tools config for convert plans
	hasConvertOp := false
	for _, item := range plan.Items {
		if item.Type == ItemTypeConvert {
			hasConvertOp = true
			break
		}
	}

	// Preflight tools config validation - only when plan contains convert operations
	if hasConvertOp {
		if err := s.validateToolsConfig(); err != nil {
			if s.repo != nil {
				_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "CONFIG_INVALID", err.Error())
			}
			return &ExecuteResult{
				SessionID: sessionID,
				PlanID:    plan.PlanID,
				Status:    "config_invalid",
				ErrorCode: "CONFIG_INVALID",
				ErrorMsg:  err.Error(),
			}, err
		}
		if err := s.validateConvertTargetExtensions(plan); err != nil {
			if s.repo != nil {
				_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "CONFIG_INVALID", err.Error())
			}
			return &ExecuteResult{
				SessionID: sessionID,
				PlanID:    plan.PlanID,
				Status:    "config_invalid",
				ErrorCode: "CONFIG_INVALID",
				ErrorMsg:  err.Error(),
			}, err
		}
	}

	// Per-folder fail-fast tracking (execution logic; outcome owned by usecase).
	failedFolders := make(map[string]bool)
	var firstExecutionErr error
	var preconditionFailed bool
	_ = plan // used below in loop

	// Item-completion callback helper. The usecase owns folder lifecycle;
	// the lower-level service reports per-item completion facts only.
	notifyItemCompleted := func(itemIndex int, item PlanItem) {
		if s.eventHandler != nil {
			s.eventHandler.OnItemCompleted(itemIndex, item)
		}
	}

	// Task 4/5A:
	// - concurrent_stat=true  -> run fail-fast two-phase precheck before execution
	// - concurrent_stat=false -> keep legacy per-item precheck behavior in execution loop
	if s.config.PrecheckConcurrentStat {
		precheckStart := time.Now()
		if plan.RootPath != "" {
			folderFailures := s.precheckPlanByFolderConcurrent(plan)
			precheckStatMs = time.Since(precheckStart).Milliseconds()
			for _, ff := range folderFailures {
				recordSQLiteBusyLocked(ff.err)
				if ff.index >= 0 && ff.index < len(plan.Items) && s.eventHandler != nil {
					s.eventHandler.OnPreconditionFailed(ff.index, plan.Items[ff.index], ff.err)
				}
				if ff.folderPath != "" {
					failedFolders[ff.folderPath] = true
					preconditionFailed = true
					continue
				}

				if s.repo != nil {
					_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXEC_PRECONDITION_FAILED", ff.err.Error())
				}
				errMsg := ff.err.Error()
				if ff.index >= 0 {
					errMsg = fmt.Sprintf("item %d: %v", ff.index, ff.err)
				}
				return &ExecuteResult{
					SessionID: sessionID,
					PlanID:    plan.PlanID,
					Status:    "precondition_failed",
					ErrorCode: "EXEC_PRECONDITION_FAILED",
					ErrorMsg:  errMsg,
				}, ff.err
			}
		} else {
			precheckErr := s.precheckPlan(plan)
			precheckStatMs = time.Since(precheckStart).Milliseconds()
			if precheckErr != nil {
				recordSQLiteBusyLocked(precheckErr.err)
				if precheckErr.index >= 0 && precheckErr.index < len(plan.Items) && s.eventHandler != nil {
					s.eventHandler.OnPreconditionFailed(precheckErr.index, plan.Items[precheckErr.index], precheckErr.err)
				}
				if s.repo != nil {
					_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXEC_PRECONDITION_FAILED", precheckErr.err.Error())
				}
				errMsg := precheckErr.err.Error()
				if precheckErr.index >= 0 {
					errMsg = fmt.Sprintf("item %d: %v", precheckErr.index, precheckErr.err)
				}
				return &ExecuteResult{
					SessionID: sessionID,
					PlanID:    plan.PlanID,
					Status:    "precondition_failed",
					ErrorCode: "EXEC_PRECONDITION_FAILED",
					ErrorMsg:  errMsg,
				}, precheckErr.err
			}
		}
	}

	// Plan-order execution: batch converts, execute delete after each batch completes successfully
	var currentBatch []PlanItem
	var currentBatchIndices []int

	// flushBatch executes the current convert batch and returns error if any
	// Task 4: also tracks folder successes/failures
	flushBatch := func() (*ExecuteResult, error) {
		if len(currentBatch) == 0 {
			return nil, nil
		}
		batchItems := currentBatch
		batchIndices := currentBatchIndices
		result, err := s.executeConvertPoolWithTracking(plan, sessionID, currentBatch, currentBatchIndices, failedFolders, make(map[string]bool))
		if plan.RootPath != "" {
			for i, batchItem := range batchItems {
				notifyItemCompleted(batchIndices[i], batchItem)
			}
		}
		currentBatch = nil
		currentBatchIndices = nil
		return result, err
	}

	// Traverse plan.Items in original order with batching
	for i, item := range plan.Items {
		src := item.SourcePath
		if src == "" {
			src = item.Src
		}
		dst := item.TargetPath
		if dst == "" {
			dst = item.Dst
		}

		// Get folder path for per-folder fail-fast
		folderPath := getFolderForItem(plan.RootPath, item)

		// Skip if folder already failed (per-folder fail-fast)
		if folderPath != "" && failedFolders[folderPath] {
			notifyItemCompleted(i, item)
			continue
		}

		if !s.config.PrecheckConcurrentStat {
			itemPrecheckStart := time.Now()
			if err := s.validatePrecondition(item); err != nil {
				precheckStatMs += time.Since(itemPrecheckStart).Milliseconds()
				recordSQLiteBusyLocked(err)
				if s.eventHandler != nil {
					s.eventHandler.OnPreconditionFailed(i, item, err)
				}
				if folderPath != "" {
					failedFolders[folderPath] = true
					preconditionFailed = true
					notifyItemCompleted(i, item)
					continue
				}
				if s.repo != nil {
					_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXEC_PRECONDITION_FAILED", err.Error())
				}
				return &ExecuteResult{
					SessionID: sessionID,
					PlanID:    plan.PlanID,
					Status:    "precondition_failed",
					ErrorCode: "EXEC_PRECONDITION_FAILED",
					ErrorMsg:  fmt.Sprintf("item %d: %v", i, err),
				}, err
			}
			precheckStatMs += time.Since(itemPrecheckStart).Milliseconds()
		}

		if item.Type == ItemTypeConvert && dst != "" {
			// Add convert to current batch
			currentBatch = append(currentBatch, item)
			currentBatchIndices = append(currentBatchIndices, i)
		} else {
			// Delete encountered: flush current convert batch first
			if result, err := flushBatch(); err != nil {
				recordSQLiteBusyLocked(err)
				if firstExecutionErr == nil {
					firstExecutionErr = err
				}
				if plan.RootPath == "" {
					return result, err
				}
			}

			if folderPath != "" && failedFolders[folderPath] {
				notifyItemCompleted(i, item)
				continue
			}

			// Only execute delete if batch succeeded (err == nil)
			var delErr error
			if item.Type == ItemTypeDelete && plan.SoftDelete {
				if dst == "" {
					delErr = fmt.Errorf("delete item missing persisted target_path")
				} else {
					// Task 2: Deterministic execute-time target conflict check
					// For soft-delete operations, verify target does not exist before moving
					if _, statErr := os.Stat(dst); statErr == nil {
						delErr = fmt.Errorf("TARGET_CONFLICT: destination already exists: %s", dst)
					} else {
						delErr = moveToPersistedTarget(src, dst)
					}
				}
			} else {
				delErr = s.runner.Delete(src, plan.SoftDelete)
			}

			if delErr != nil {
				recordSQLiteBusyLocked(delErr)
				// Task 4: Call event handler callback
				if s.eventHandler != nil {
					s.eventHandler.OnDeleteFailed(i, item, delErr)
				}
				// Mark folder as failed for per-folder fail-fast
				if folderPath != "" {
					failedFolders[folderPath] = true
					notifyItemCompleted(i, item)
				}
				if firstExecutionErr == nil {
					firstExecutionErr = fmt.Errorf("delete item failed: %v", delErr)
				}
				if plan.RootPath == "" || folderPath == "" {
					if s.repo != nil {
						_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXEC_DELETE_FAILED", delErr.Error())
					}
					return &ExecuteResult{
						SessionID: sessionID,
						PlanID:    plan.PlanID,
						Status:    "failed",
						ErrorCode: "EXEC_DELETE_FAILED",
						ErrorMsg:  fmt.Sprintf("delete item failed: %v", delErr),
					}, delErr
				}
				continue
			}

			// Notify usecase that item completed successfully.
			if folderPath != "" {
				notifyItemCompleted(i, item)
			}
		}
	}

	// Flush final batch after all items processed
	if result, err := flushBatch(); err != nil {
		recordSQLiteBusyLocked(err)
		if firstExecutionErr == nil {
			firstExecutionErr = err
		}
		if plan.RootPath == "" {
			return result, err
		}
	}

	if firstExecutionErr != nil {
		if s.repo != nil {
			_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXECUTION_FAILED", firstExecutionErr.Error())
		}
		return &ExecuteResult{
			SessionID: sessionID,
			PlanID:    plan.PlanID,
			Status:    "failed",
			ErrorCode: "EXECUTION_FAILED",
			ErrorMsg:  firstExecutionErr.Error(),
		}, firstExecutionErr
	}

	// Rooted precondition failures: the per-folder fail-fast policy allowed
	// execution to continue across folders, but the overall result must still
	// reflect that precondition validation failed.
	if preconditionFailed {
		precondErr := errors.New("precondition validation failed for one or more items")
		if s.repo != nil {
			_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", "EXEC_PRECONDITION_FAILED", precondErr.Error())
		}
		return &ExecuteResult{
			SessionID: sessionID,
			PlanID:    plan.PlanID,
			Status:    "precondition_failed",
			ErrorCode: "EXEC_PRECONDITION_FAILED",
			ErrorMsg:  precondErr.Error(),
		}, precondErr
	}

	if s.repo != nil {
		if err := s.repo.UpdateExecuteSessionStatus(sessionID, "completed", "", ""); err != nil {
			recordSQLiteBusyLocked(err)
			return nil, err
		}
	}

	return &ExecuteResult{SessionID: sessionID, PlanID: plan.PlanID, Status: "completed"}, nil
}

// runBatchDeletes performs converted-source deletes only after an entire
// convert batch has committed successfully.
func (s *ExecuteService) runBatchDeletes(plan *Plan, items []PlanItem, itemIndices []int) *batchOutcome {
	outcome := &batchOutcome{}
	for i, item := range items {
		src := item.SourcePath
		if src == "" {
			src = item.Src
		}
		if err := s.runner.Delete(src, plan.SoftDelete); err != nil {
			failure := itemFailure{
				itemIndex: itemIndices[i],
				stage:     "delete",
				err:       fmt.Errorf("convert succeeded but failed to delete source: %w", err),
			}
			outcome.firstFailure = &failure
			outcome.failures = append(outcome.failures, failure)
			return outcome
		}
	}
	return outcome
}

// makeFailedResult creates a failed ExecuteResult.
func (s *ExecuteService) makeFailedResult(sessionID, planID, errorCode, errorMsg string) *ExecuteResult {
	if s.repo != nil {
		_ = s.repo.UpdateExecuteSessionStatus(sessionID, "failed", errorCode, errorMsg)
	}
	return &ExecuteResult{
		SessionID: sessionID,
		PlanID:    planID,
		Status:    "failed",
		ErrorCode: errorCode,
		ErrorMsg:  errorMsg,
	}
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "sqlite_locked")
}
