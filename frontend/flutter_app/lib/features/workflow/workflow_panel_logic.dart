part of 'workflow_panel_widget.dart';

mixin WorkflowPanelLogic on State<WorkflowPanelWidget> {
  late final OnseiServiceClient _client;
  final List<_LogEntry> _log = [];

  WorkflowPhase _phase = WorkflowPhase.idle;
  WorkflowOperation _selectedOperation = WorkflowOperation.slimModeI;
  List<PlannedOperation> _operations = [];

  bool _slimPlanning = false;
  bool _slimExecuting = false;
  bool _prunePlanning = false;
  bool _pruneExecuting = false;

  // Task 5: Separate plan IDs for slim and prune
  String? _slimPlanId;
  String? _prunePlanId;
  bool _softDelete = true; // Local fallback when no store provided
  bool _slimMode2PruneMatchedExcluded =
      false; // Local fallback when no store provided

  // Task 5: Per-channel state isolation for summary/lastError
  String? _slimSummary;
  String? _pruneSummary;
  String? _slimLastError;
  String? _pruneLastError;
  String? _latestPlannedActionLabel;
  bool? _latestPlannedIsSlim;

  String? _lastPlanId;
  String? _lastPlanSummary;
  int _lastPlanFolderCount = 0;
  int _lastPlanFileCount = 0;

  // Task 6: Event ID deduplication - track seen IDs by structured key.
  final Set<_EventKey> _seenEventIds = <_EventKey>{};

  String _normalizedPath(String value) => normalizePathForComparison(value);

  // Getters for testing
  @visibleForTesting
  WorkflowPhase get phase => _phase;

  @visibleForTesting
  String? get slimPlanId => _slimPlanId;

  @visibleForTesting
  String? get prunePlanId => _prunePlanId;

  /// Returns softDelete from shared store if available, otherwise local state.
  @visibleForTesting
  bool get softDelete => widget.workflowStateStore?.softDelete ?? _softDelete;

  /// Returns Slim Mode II exclusion switch from shared store if available.
  @visibleForTesting
  bool get slimMode2PruneMatchedExcluded =>
      widget.workflowStateStore?.slimMode2PruneMatchedExcluded ??
      _slimMode2PruneMatchedExcluded;

  @visibleForTesting
  String? get slimSummary => _slimSummary;

  @visibleForTesting
  String? get pruneSummary => _pruneSummary;

  @visibleForTesting
  String? get slimLastError => _slimLastError;

  @visibleForTesting
  String? get pruneLastError => _pruneLastError;

  @visibleForTesting
  Future<void> triggerSlim(SlimMode mode) async {
    await _runSlimPlan(mode);
  }

  @visibleForTesting
  Future<void> triggerPrune(PruneMode mode) async {
    await _runPrunePlan(mode);
  }

  @visibleForTesting
  Future<void> triggerExecuteSlim() async {
    await _executePlan(_slimPlanId, 'Slim', isSlim: true);
  }

  @visibleForTesting
  Future<void> triggerExecutePrune() async {
    await _executePlan(_prunePlanId, 'Prune', isSlim: false);
  }

  @visibleForTesting
  Future<void> triggerSelectOperation(WorkflowOperation operation) {
    setState(() => _selectedOperation = operation);
    return Future.value();
  }

  @visibleForTesting
  Future<void> triggerPlanSelectedOperation() async {
    await _runSelectedOperationPlan();
  }

  @visibleForTesting
  Future<void> triggerExecuteSelectedOperation() async {
    await _executeSelectedOperation();
  }

  @visibleForTesting
  Future<void> triggerSoftDelete(bool value) {
    // Update shared store if available, otherwise local state
    if (widget.workflowStateStore != null) {
      widget.workflowStateStore!.setSoftDelete(value);
    } else {
      setState(() => _softDelete = value);
    }
    return Future.value();
  }

  @visibleForTesting
  Future<void> triggerSlimMode2PruneMatchedExcluded(bool value) {
    if (widget.workflowStateStore != null) {
      widget.workflowStateStore!.setSlimMode2PruneMatchedExcluded(value);
    } else {
      setState(() => _slimMode2PruneMatchedExcluded = value);
    }
    return Future.value();
  }

  // Task 6: Check if event_id is duplicate (returns true if already seen)
  bool _isEventDuplicate(String rootPath, String folderPath, String eventId) {
    final key = _EventKey(
      _normalizedPath(rootPath),
      _normalizedPath(folderPath),
      eventId,
    );
    if (_seenEventIds.contains(key)) {
      _addLog('[DEDUP] Skipping duplicate event_id: $eventId for $folderPath');
      return true;
    }
    _seenEventIds.add(key);
    return false;
  }

  // Task 6: Check if root_path matches current root (for delayed event detection)
  bool _isCurrentRoot(String? eventRootPath) {
    final normalizedEventRoot = _normalizedPath(eventRootPath ?? '');
    final normalizedCurrentRoot = _normalizedPath(widget.selectedRoot ?? '');
    if (normalizedEventRoot.isEmpty || normalizedCurrentRoot.isEmpty) {
      return false;
    }
    return normalizedEventRoot == normalizedCurrentRoot;
  }

  // Task 6: Clear planHasError for folders (before plan request)
  void _clearPlanErrorForFolders(Set<String> folders) {
    if (folders.isEmpty) return;
    if (!widget.showErrorView || widget.onClearPlanErrorForFolders == null) {
      return;
    }
    widget.onClearPlanErrorForFolders!(folders);
    _addLog('[REDUCER] Cleared planHasError for ${folders.length} folder(s)');
  }

  // Task 6: Clear executeHasError for folders
  void _clearExecuteErrorForFolders(Set<String> folders) {
    if (folders.isEmpty) return;
    widget.onClearExecuteErrorForFolders?.call(folders);
  }

  // Task 6: Apply plan error to folders
  void _applyPlanError(
    String folderPath,
    String eventId,
    String? code,
    String? message,
    String rootPath,
  ) {
    final normalizedFolderPath = _normalizedPath(folderPath);
    // Req 2: Only apply if folder_path != '' and root_path matches current
    if (normalizedFolderPath.isEmpty) {
      _addLog('[REDUCER] Skipping plan error with empty folder_path');
      return;
    }
    if (!_isCurrentRoot(rootPath)) {
      _addLog(
        '[REDUCER] Skipping plan error for different root: $rootPath (current: ${widget.selectedRoot})',
      );
      return;
    }
    // Req 7: Missing event_id - log-only
    if (eventId.isEmpty) {
      _addLog(
        '[REDUCER] Skipping plan error with missing event_id for $normalizedFolderPath',
      );
      return;
    }
    // Req 6: Deduplicate
    if (_isEventDuplicate(rootPath, normalizedFolderPath, eventId)) {
      return;
    }

    widget.onApplyPlanErrorToFolders?.call(
      {normalizedFolderPath},
      eventId,
      code,
      message,
    );
    _addLog(
      '[REDUCER] Applied plan error to $normalizedFolderPath: $code - $message',
    );
  }

  // Task 6: Apply execute error to folders
  void _applyExecuteError(
    String folderPath,
    String eventId,
    String? code,
    String? message,
    String rootPath,
    String stage,
  ) {
    final normalizedFolderPath = _normalizedPath(folderPath);
    // Req 4: Only stage=execute affects executeHasError
    if (stage != 'execute') {
      _addLog(
        '[REDUCER] Skipping execute error with stage=$stage (expected execute)',
      );
      return;
    }
    if (normalizedFolderPath.isEmpty) {
      _addLog('[REDUCER] Skipping execute error with empty folder_path');
      return;
    }
    if (!_isCurrentRoot(rootPath)) {
      _addLog(
        '[REDUCER] Skipping execute error for different root: $rootPath (current: ${widget.selectedRoot})',
      );
      return;
    }
    // Req 7: Missing event_id - log-only
    if (eventId.isEmpty) {
      _addLog(
        '[REDUCER] Skipping execute error with missing event_id for $normalizedFolderPath',
      );
      return;
    }
    // Req 6: Deduplicate
    if (_isEventDuplicate(rootPath, normalizedFolderPath, eventId)) {
      return;
    }

    widget.onApplyExecuteErrorToFolders?.call(
      {normalizedFolderPath},
      eventId,
      code,
      message,
    );
    _addLog(
      '[REDUCER] Applied execute error to $normalizedFolderPath: $code - $message',
    );
  }

  // Task 6: Handle folder_completed event
  void _handleFolderCompleted(
    String folderPath,
    String eventId,
    String rootPath,
  ) {
    final normalizedFolderPath = _normalizedPath(folderPath);
    if (normalizedFolderPath.isEmpty) {
      _addLog('[REDUCER] Skipping folder_completed with empty folder_path');
      return;
    }
    if (!_isCurrentRoot(rootPath)) {
      _addLog(
        '[REDUCER] Skipping folder_completed for different root: $rootPath',
      );
      return;
    }
    if (eventId.isEmpty) {
      _addLog(
        '[REDUCER] Skipping folder_completed with missing event_id for $normalizedFolderPath',
      );
      return;
    }
    if (_isEventDuplicate(rootPath, normalizedFolderPath, eventId)) {
      return;
    }

    _clearExecuteErrorForFolders({normalizedFolderPath});
  }

  void _addLog(String message, {bool isError = false}) {
    setState(() {
      _log.add(
        _LogEntry(
          message: message,
          timestamp: DateTime.now(),
          isError: isError,
        ),
      );
    });
  }

  Future<bool> _confirm(String title, String content) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: Text(content),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Apply'),
          ),
        ],
      ),
    );
    return ok == true;
  }

  // Task 5: Plan-only method (dropdown triggers this)
  Future<void> _runSlimPlan(SlimMode mode) async {
    final actionLabel = switch (mode) {
      SlimMode.modeI => 'Slim Mode I',
      SlimMode.modeII => 'Slim Mode II',
    };
    final pruneMatchedExcluded = slimMode2PruneMatchedExcluded;
    final target = switch (mode) {
      SlimMode.modeI => 'slim:mode1',
      SlimMode.modeII => 'slim:mode2',
    };
    setState(() => _slimPlanning = true);
    try {
      await _planOnly(
        targetFormat: target,
        actionLabel: actionLabel,
        planType: 'slim',
        pruneMatchedExcluded: pruneMatchedExcluded,
        onPlanCreated: (planId) {
          _slimPlanId = planId;
          _latestPlannedActionLabel = actionLabel;
          _latestPlannedIsSlim = true;
        },
        isSlim: true,
      );
    } finally {
      if (mounted) {
        setState(() => _slimPlanning = false);
      }
    }
  }

  // Task 5: Plan-only method (dropdown triggers this)
  Future<void> _runPrunePlan(PruneMode mode) async {
    final actionLabel = switch (mode) {
      PruneMode.wavFlac => 'Prune wav/flac',
      PruneMode.mp3Aac => 'Prune mp3/m4a',
      PruneMode.both => 'Prune both',
    };
    final target = switch (mode) {
      PruneMode.wavFlac => 'prune:wavflac',
      PruneMode.mp3Aac => 'prune:mp3aac',
      PruneMode.both => 'prune:both',
    };
    setState(() => _prunePlanning = true);
    try {
      await _planOnly(
        targetFormat: target,
        actionLabel: actionLabel,
        planType: 'prune',
        onPlanCreated: (planId) {
          _prunePlanId = planId;
          _latestPlannedActionLabel = actionLabel;
          _latestPlannedIsSlim = false;
        },
        isSlim: false,
      );
    } finally {
      if (mounted) {
        setState(() => _prunePlanning = false);
      }
    }
  }

  // Task 5: Plan-only (no execute)
  Future<void> _planOnly({
    required String targetFormat,
    required String actionLabel,
    required String planType,
    bool pruneMatchedExcluded = false,
    required void Function(String planId) onPlanCreated,
    required bool isSlim,
  }) async {
    final files = widget.selectedFiles.toList();
    final folders = widget.selectedFolders.toList();
    final requiresFolderScope = planType == 'slim' || planType == 'prune';

    if (files.isEmpty && folders.isEmpty) {
      _addLog('No files selected for $actionLabel.', isError: true);
      // Per-channel lastError
      if (isSlim) {
        _slimLastError = 'No files selected';
      } else {
        _pruneLastError = 'No files selected';
      }
      return;
    }

    if (requiresFolderScope && folders.isEmpty) {
      final message = '$actionLabel requires folder selection.';
      _addLog(message, isError: true);
      if (isSlim) {
        _slimLastError = message;
      } else {
        _pruneLastError = message;
      }
      return;
    }

    // Task 6 Req 1: Before sending Plan request, clear planHasError for selected folders
    if (folders.isNotEmpty) {
      _clearPlanErrorForFolders(folders.toSet());
    }

    setState(() {
      _phase = WorkflowPhase.planning;
      _seenEventIds.clear();
    });
    _addLog('Planning $actionLabel...');

    try {
      final PlanOperationsResponse planResponse;
      if (widget.coordinator != null && folders.isNotEmpty) {
        final rootPath = widget.selectedRoot ?? widget.selectedFolder ?? '';
        planResponse = await widget.coordinator!.planOnlyForFolders(
          rootPath: rootPath,
          selectedFolders: folders.toSet(),
          targetFormat: targetFormat,
          planType: planType,
          pruneMatchedExcluded: pruneMatchedExcluded,
        );
      } else {
        final req = PlanOperationsRequest()
          ..targetFormat = targetFormat
          ..planType = planType
          ..pruneMatchedExcluded = pruneMatchedExcluded;
        if (folders.isNotEmpty) {
          req.folderPaths.addAll(folders);
          req.folderPath = folders.first;
        } else {
          req.sourceFiles.addAll(files);
        }

        planResponse = widget.planOperationsOverride != null
            ? await widget.planOperationsOverride!(req)
            : await _client.planOperations(req);
      }

      if (planResponse.planId.isEmpty) {
        _addLog('Planning failed: missing plan_id', isError: true);
        if (isSlim) {
          _slimLastError = 'Missing plan_id';
        } else {
          _pruneLastError = 'Missing plan_id';
        }
        setState(() => _phase = WorkflowPhase.idle);
        return;
      }

      // Task 6: Process plan_errors (Req 2, 7, 9)
      for (final planError in planResponse.planErrors) {
        _applyPlanError(
          planError.folderPath,
          planError.eventId,
          planError.code.isNotEmpty ? planError.code : null,
          planError.message.isNotEmpty ? planError.message : null,
          planError.rootPath,
        );
      }

      // Task 6 Req 3: Process successful_folders - clear planHasError
      if (planResponse.successfulFolders.isNotEmpty) {
        _clearPlanErrorForFolders(planResponse.successfulFolders.toSet());
      }

      final hasBreakdown =
          planResponse.totalCount > 0 || planResponse.summaryReason.isNotEmpty;
      final convertCount = planResponse.operations.where((op) {
        final type = op.operationType.toLowerCase();
        return type == 'convert' || type == 'convert_and_delete';
      }).length;
      final deleteCount = planResponse.operations
          .where((op) => op.operationType.toLowerCase() == 'delete')
          .length;
      final summaryText = hasBreakdown
          ? '$convertCount convert / $deleteCount delete / ${planResponse.totalCount} total'
          : '${planResponse.operations.length} operation(s)';

      // Per-channel state isolation
      if (isSlim) {
        _slimSummary = summaryText;
        _slimLastError = null;
      } else {
        _pruneSummary = summaryText;
        _pruneLastError = null;
      }

      setState(() {
        _operations = planResponse.operations;
        _phase = WorkflowPhase.planned;
        _lastPlanId = planResponse.planId;
        _lastPlanSummary = summaryText;
        _lastPlanFolderCount = folders.length;
        _lastPlanFileCount = files.length;
      });

      onPlanCreated(planResponse.planId);

      _addLog('Plan created: ${planResponse.planId} ($summaryText).');
    } catch (e) {
      _addLog('$actionLabel failed: $e', isError: true);
      if (isSlim) {
        _slimLastError = e.toString();
      } else {
        _pruneLastError = e.toString();
      }
      setState(() => _phase = WorkflowPhase.idle);
    }
  }

  // Task 5: Execute plan with softDelete
  Future<void> _executePlan(
    String? planId,
    String actionLabel, {
    required bool isSlim,
  }) async {
    if (planId == null || planId.isEmpty) {
      _addLog('No plan to execute.', isError: true);
      return;
    }

    final confirmed = await _confirm(
      '$actionLabel Execute Confirmation',
      'Implement $planId ?',
    );
    if (!confirmed) {
      return;
    }

    setState(() {
      _phase = WorkflowPhase.executing;
      _seenEventIds.clear();
      if (isSlim) {
        _slimExecuting = true;
      } else {
        _pruneExecuting = true;
      }
    });
    _addLog('Executing $actionLabel...');

    try {
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeByPlanId(
          planId: planId,
          rootPathForValidation: widget.selectedRoot ?? widget.selectedFolder,
          onEvent: (event) {
            _addLog(
              '[${event.eventType}] ${event.message}',
              isError: event.eventType == 'error',
            );
            _processExecuteEvent(event);
          },
        );

        if (!result.success) {
          _addLog('$actionLabel failed: ${result.errorMessage}', isError: true);
          setState(() => _phase = WorkflowPhase.idle);
          return;
        }

        setState(() => _phase = WorkflowPhase.done);
        _addLog('$actionLabel completed.');
        return;
      }

      final request = ExecutePlanRequest()
        ..planId = planId
        ..softDelete = softDelete;
      final stream = widget.executePlanOverride != null
          ? widget.executePlanOverride!(request)
          : _client.executePlan(request);
      await for (final event in stream) {
        _addLog(
          '[${event.eventType}] ${event.message}',
          isError: event.eventType == 'error',
        );

        // Task 6: Process structured event fields
        _processExecuteEvent(event);
      }

      setState(() => _phase = WorkflowPhase.done);
      _addLog('$actionLabel completed.');
    } catch (e) {
      _addLog('$actionLabel failed: $e', isError: true);
      setState(() => _phase = WorkflowPhase.idle);
    } finally {
      if (mounted) {
        setState(() {
          if (isSlim) {
            _slimExecuting = false;
          } else {
            _pruneExecuting = false;
          }
        });
      }
    }
  }

  // Task 6: Process execute stream events with structured fields
  void _processExecuteEvent(JobEvent event) {
    // Req 9: Handle missing structured fields gracefully
    final eventType = event.eventType;
    final stage = event.stage;
    final folderPath = event.folderPath;
    final rootPath = event.rootPath;
    final eventId = event.eventId;
    final code = event.code.isNotEmpty ? event.code : null;
    final message = event.message.isNotEmpty ? event.message : null;

    // Req 5: Handle folder_completed
    if (eventType == 'folder_completed') {
      _handleFolderCompleted(folderPath, eventId, rootPath);
      return;
    }

    // Req 4: Handle error events - only stage=execute affects executeHasError
    if (eventType == 'error') {
      _applyExecuteError(folderPath, eventId, code, message, rootPath, stage);
      return;
    }

    // Other event types are just logged (already done above)
  }

  void _reset() {
    setState(() {
      _phase = WorkflowPhase.idle;
      _operations = [];
      _slimPlanId = null;
      _prunePlanId = null;
      // Task 5: Clear per-channel state isolation
      _slimSummary = null;
      _pruneSummary = null;
      _slimLastError = null;
      _pruneLastError = null;
      _latestPlannedActionLabel = null;
      _latestPlannedIsSlim = null;
      _lastPlanId = null;
      _lastPlanSummary = null;
      _lastPlanFolderCount = 0;
      _lastPlanFileCount = 0;
      // Task 5: _reset clears workflow state only, not log
      // Task 6: Clear event deduplication state
      _seenEventIds.clear();
    });
  }

  Future<void> _runSelectedOperationPlan() {
    return switch (_selectedOperation) {
      WorkflowOperation.slimModeI => _runSlimPlan(SlimMode.modeI),
      WorkflowOperation.slimModeII => _runSlimPlan(SlimMode.modeII),
      WorkflowOperation.pruneWavFlac => _runPrunePlan(PruneMode.wavFlac),
      WorkflowOperation.pruneMp3M4a => _runPrunePlan(PruneMode.mp3Aac),
      WorkflowOperation.pruneBoth => _runPrunePlan(PruneMode.both),
    };
  }

  Future<void> _executeSelectedOperation() {
    return _executePlan(
      _lastPlanId,
      _latestPlannedActionLabel ?? 'Latest Plan',
      isSlim: _latestPlannedIsSlim ?? true,
    );
  }

  bool get _isSlimBusy => _slimPlanning || _slimExecuting;
  bool get _isPruneBusy => _prunePlanning || _pruneExecuting;
  bool get _isAnyBusy => _isSlimBusy || _isPruneBusy;
}
