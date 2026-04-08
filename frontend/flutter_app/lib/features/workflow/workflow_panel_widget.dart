import 'package:flutter/material.dart';
import 'package:grpc/grpc.dart';

import '../../core/path_normalizer.dart';
import '../../gen/onsei/v1/service.pbgrpc.dart';
import 'plan_execution_coordinator.dart';
import 'workflow_state_store.dart';

part 'workflow_panel_models.dart';
part 'workflow_panel_logic.dart';
part 'workflow_panel_presenter.dart';

class WorkflowPanelWidget extends StatefulWidget {
  final ClientChannel channel;
  final String? selectedFolder;
  final Set<String> selectedFolders;
  final Set<String> selectedFiles;
  final Future<PlanOperationsResponse> Function(PlanOperationsRequest request)?
  planOperationsOverride;
  final Stream<JobEvent> Function(ExecutePlanRequest request)?
  executePlanOverride;

  // Task 6: Callbacks for updating error state in main.dart
  final String? selectedRoot;
  final void Function(Set<String> folders)? onClearPlanErrorForFolders;
  final void Function(Set<String> folders)? onClearExecuteErrorForFolders;
  final void Function(
    Set<String> folders,
    String eventId,
    String? code,
    String? message,
  )?
  onApplyPlanErrorToFolders;
  final void Function(
    Set<String> folders,
    String eventId,
    String? code,
    String? message,
  )?
  onApplyExecuteErrorToFolders;

  /// Shared workflow state store for softDelete setting.
  /// If provided, the checkbox updates this store and execute operations read from it.
  final WorkflowStateStore? workflowStateStore;

  /// Shared coordinator for unified refresh->plan->execute flow.
  final PlanExecutionCoordinator? coordinator;

  /// Whether error view is currently visible in folder pane.
  /// Plan error clear events/logs are emitted only when true.
  final bool showErrorView;

  const WorkflowPanelWidget({
    super.key,
    required this.channel,
    required this.selectedFolder,
    required this.selectedFolders,
    required this.selectedFiles,
    this.planOperationsOverride,
    this.executePlanOverride,
    this.selectedRoot,
    this.onClearPlanErrorForFolders,
    this.onClearExecuteErrorForFolders,
    this.onApplyPlanErrorToFolders,
    this.onApplyExecuteErrorToFolders,
    this.workflowStateStore,
    this.coordinator,
    this.showErrorView = false,
  });

  @override
  State<WorkflowPanelWidget> createState() => _WorkflowPanelWidgetState();
}

const Key _softDeleteCheckboxKey = Key('workflow_soft_delete_checkbox');
const Key _slimExcludePruneMatchesCheckboxKey = Key(
  'workflow_slim_exclude_prune_matches_checkbox',
);

class _WorkflowPanelWidgetState extends State<WorkflowPanelWidget>
    with WorkflowPanelLogic {
  VoidCallback? _workflowStoreListener;

  @override
  void initState() {
    super.initState();
    _client = OnseiServiceClient(widget.channel);
    _attachWorkflowStoreListener(widget.workflowStateStore);
  }

  @override
  void didUpdateWidget(covariant WorkflowPanelWidget oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.workflowStateStore != widget.workflowStateStore) {
      _detachWorkflowStoreListener(oldWidget.workflowStateStore);
      _attachWorkflowStoreListener(widget.workflowStateStore);
    }
  }

  @override
  void dispose() {
    _detachWorkflowStoreListener(widget.workflowStateStore);
    super.dispose();
  }

  void _attachWorkflowStoreListener(WorkflowStateStore? store) {
    if (store == null) {
      _workflowStoreListener = null;
      return;
    }
    _workflowStoreListener = () {
      if (mounted) {
        setState(() {});
      }
    };
    store.addListener(_workflowStoreListener!);
  }

  void _detachWorkflowStoreListener(WorkflowStateStore? store) {
    final listener = _workflowStoreListener;
    if (store != null && listener != null) {
      store.removeListener(listener);
    }
    _workflowStoreListener = null;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          color: theme.colorScheme.surfaceContainerHighest,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                'Workflow',
                style: theme.textTheme.labelLarge?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              Row(
                children: [
                  Expanded(
                    child: DropdownButtonFormField<WorkflowOperation>(
                      initialValue: _selectedOperation,
                      isDense: true,
                      isExpanded: true,
                      decoration: InputDecoration(
                        labelText: 'Operation',
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(6),
                        ),
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 8,
                          vertical: 4,
                        ),
                      ),
                      items: [
                        DropdownMenuItem(
                          value: WorkflowOperation.slimModeI,
                          child: Text(
                            _operationLabel(WorkflowOperation.slimModeI),
                          ),
                        ),
                        DropdownMenuItem(
                          value: WorkflowOperation.slimModeII,
                          child: Text(
                            _operationLabel(WorkflowOperation.slimModeII),
                          ),
                        ),
                        DropdownMenuItem(
                          value: WorkflowOperation.pruneWavFlac,
                          child: Text(
                            _operationLabel(WorkflowOperation.pruneWavFlac),
                          ),
                        ),
                        DropdownMenuItem(
                          value: WorkflowOperation.pruneMp3M4a,
                          child: Text(
                            _operationLabel(WorkflowOperation.pruneMp3M4a),
                          ),
                        ),
                        DropdownMenuItem(
                          value: WorkflowOperation.pruneBoth,
                          child: Text(
                            _operationLabel(WorkflowOperation.pruneBoth),
                          ),
                        ),
                      ],
                      onChanged: _isAnyBusy
                          ? null
                          : (value) {
                              if (value != null) {
                                setState(() => _selectedOperation = value);
                              }
                            },
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Task 5: Execute buttons instead of Resume
              LayoutBuilder(
                builder: (context, constraints) {
                  final isNarrow = constraints.maxWidth < 320;

                  final resetButton = TextButton.icon(
                    style: TextButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 8),
                    ),
                    onPressed: _isAnyBusy ? null : _reset,
                    icon: const Icon(Icons.refresh, size: 16),
                    label: const Text('Reset'),
                  );

                  final planButton = FilledButton.tonal(
                    style: FilledButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 4),
                    ),
                    onPressed: _isAnyBusy ? null : _runSelectedOperationPlan,
                    child: const FittedBox(
                      fit: BoxFit.scaleDown,
                      child: Text('Plan'),
                    ),
                  );

                  final executeButton = FilledButton(
                    style: FilledButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 4),
                    ),
                    onPressed: _isAnyBusy || _lastPlanId == null
                        ? null
                        : _executeSelectedOperation,
                    child: const FittedBox(
                      fit: BoxFit.scaleDown,
                      child: Text('Execute'),
                    ),
                  );

                  if (isNarrow) {
                    return Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Align(
                          alignment: Alignment.centerLeft,
                          child: resetButton,
                        ),
                        const SizedBox(height: 4),
                        planButton,
                        const SizedBox(height: 4),
                        executeButton,
                      ],
                    );
                  }

                  return Row(
                    children: [
                      resetButton,
                      const SizedBox(width: 4),
                      Expanded(child: planButton),
                      const SizedBox(width: 4),
                      Expanded(child: executeButton),
                    ],
                  );
                },
              ),
              const SizedBox(height: 8),
              // Task 5: softDelete checkbox
              // Task 2: Use shared store if available
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  LayoutBuilder(
                    builder: (context, constraints) {
                      final isNarrow = constraints.maxWidth < 320;

                      final softDeleteToggle = Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Checkbox(
                            key: _softDeleteCheckboxKey,
                            value:
                                widget.workflowStateStore?.softDelete ??
                                _softDelete,
                            onChanged: _isAnyBusy
                                ? null
                                : (value) {
                                    if (widget.workflowStateStore != null) {
                                      widget.workflowStateStore!.setSoftDelete(
                                        value ?? false,
                                      );
                                    } else {
                                      setState(
                                        () => _softDelete = value ?? false,
                                      );
                                    }
                                  },
                          ),
                          const Text('Soft Delete'),
                        ],
                      );

                      final pruneExclusionToggle = Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Checkbox(
                            key: _slimExcludePruneMatchesCheckboxKey,
                            value:
                                widget
                                    .workflowStateStore
                                    ?.slimMode2PruneMatchedExcluded ??
                                _slimMode2PruneMatchedExcluded,
                            onChanged: _isAnyBusy
                                ? null
                                : (value) {
                                    if (widget.workflowStateStore != null) {
                                      widget.workflowStateStore!
                                          .setSlimMode2PruneMatchedExcluded(
                                            value ?? false,
                                          );
                                    } else {
                                      setState(() {
                                        _slimMode2PruneMatchedExcluded =
                                            value ?? false;
                                      });
                                    }
                                  },
                          ),
                          const Flexible(
                            child: Text(
                              'Slim Exclude Prune Matches',
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        ],
                      );

                      if (isNarrow) {
                        return Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [softDeleteToggle, pruneExclusionToggle],
                        );
                      }

                      return Row(
                        children: [
                          softDeleteToggle,
                          const SizedBox(width: 4),
                          Expanded(child: pruneExclusionToggle),
                        ],
                      );
                    },
                  ),
                  Align(
                    alignment: Alignment.centerRight,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: _phaseColor(_phase).withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(6),
                      ),
                      child: Text(
                        _phaseLabel(_phase),
                        style: theme.textTheme.labelSmall?.copyWith(
                          color: _phaseColor(_phase),
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
        Container(
          margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerLow,
            border: Border.all(color: theme.colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Icon(
                    Icons.checklist,
                    size: 14,
                    color: theme.colorScheme.primary,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      _planScopeText(
                        _lastPlanId,
                        _lastPlanFolderCount,
                        _lastPlanFileCount,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodySmall,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Align(
                alignment: Alignment.centerRight,
                child: Text(
                  _planMetaText(_lastPlanId, _lastPlanSummary),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ],
          ),
        ),
        if (_operations.isNotEmpty)
          SizedBox(
            height: 96,
            child: ListView.builder(
              itemCount: _operations.length,
              itemBuilder: (context, index) {
                final op = _operations[index];
                final relativePath = _getRelativePath(
                  op.sourcePath,
                  selectedRoot: widget.selectedRoot,
                  selectedFolder: widget.selectedFolder,
                );
                return ListTile(
                  dense: true,
                  title: Text(
                    relativePath,
                    overflow: TextOverflow.ellipsis,
                    style: theme.textTheme.bodySmall?.copyWith(
                      fontFamily: 'SarasaUiSC',
                    ),
                  ),
                  subtitle: Text(
                    op.operationType,
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                );
              },
            ),
          ),
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 4, 12, 4),
          child: Row(
            children: [
              Text(
                'Log',
                style: theme.textTheme.labelMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              if (_log.isNotEmpty)
                TextButton(
                  onPressed: () => setState(() => _log.clear()),
                  child: const Text('Clear'),
                ),
            ],
          ),
        ),
        Expanded(
          child: Container(
            margin: const EdgeInsets.fromLTRB(12, 0, 12, 12),
            decoration: BoxDecoration(
              color: const Color(0xFF1E1E2E),
              borderRadius: BorderRadius.circular(8),
            ),
            child: ListView.builder(
              reverse: true,
              padding: const EdgeInsets.all(8),
              itemCount: _log.length,
              itemBuilder: (context, index) {
                final entry = _log[_log.length - 1 - index];
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: 1),
                  child: SelectableText.rich(
                    TextSpan(
                      children: [
                        TextSpan(
                          text: '${_formatTime(entry.timestamp)} ',
                          style: const TextStyle(
                            color: Color(0xFF6E6E9E),
                            fontSize: 11,
                            fontFamily: 'monospace',
                          ),
                        ),
                        TextSpan(
                          text: entry.message,
                          style: TextStyle(
                            color: entry.isError
                                ? const Color(0xFFFF5C5C)
                                : const Color(0xFFCDD6F4),
                            fontSize: 11,
                            fontFamily: 'SarasaUiSC',
                          ),
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}
