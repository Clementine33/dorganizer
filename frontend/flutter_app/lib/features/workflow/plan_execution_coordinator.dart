import 'package:flutter/foundation.dart';
import 'package:grpc/grpc.dart';
import '../../core/path_normalizer.dart';
import '../../gen/onsei/v1/service.pbgrpc.dart';
import 'workflow_state_store.dart';

typedef ExecuteEventListener = void Function(JobEvent event);

/// Result of a plan execution flow.
class PlanExecutionResult {
  final bool success;
  final String? errorMessage;
  final String? planId;
  final List<JobEvent> events;

  const PlanExecutionResult({
    required this.success,
    this.errorMessage,
    this.planId,
    this.events = const [],
  });

  factory PlanExecutionResult.success({
    String? planId,
    List<JobEvent>? events,
  }) {
    return PlanExecutionResult(
      success: true,
      planId: planId,
      events: events ?? [],
    );
  }

  factory PlanExecutionResult.failure(String message) {
    return PlanExecutionResult(success: false, errorMessage: message);
  }
}

/// Narrow widget-facing coordinator contract.
///
/// Keeps FilePaneWidget decoupled from the concrete coordinator implementation
/// so widget tests can use a simple mock without dragging in gRPC or real
/// refresh/plan/execute machinery.
abstract class FilePaneCoordinator {
  Future<PlanExecutionResult> executeFlowForFiles({
    required String rootPath,
    required String? folderPath,
    required Set<String> selectedFiles,
    required String targetFormat,
    required String planType,
  });
}

/// Internal gRPC client seam so tests can substitute a fake client
/// instead of starting an in-process gRPC server.
@visibleForTesting
abstract class CoordinatorGrpcClient {
  Future<RefreshFoldersResponse> refreshFolders(RefreshFoldersRequest request);
  Future<PlanOperationsResponse> planOperations(PlanOperationsRequest request);
  Stream<JobEvent> executePlan(ExecutePlanRequest request);
  Future<ListPlansResponse> listPlans(ListPlansRequest request);
}

class _RealCoordinatorClient implements CoordinatorGrpcClient {
  final OnseiServiceClient _client;
  _RealCoordinatorClient(this._client);

  @override
  Future<RefreshFoldersResponse> refreshFolders(RefreshFoldersRequest request) =>
      _client.refreshFolders(request);

  @override
  Future<PlanOperationsResponse> planOperations(PlanOperationsRequest request) =>
      _client.planOperations(request);

  @override
  Stream<JobEvent> executePlan(ExecutePlanRequest request) =>
      _client.executePlan(request);

  @override
  Future<ListPlansResponse> listPlans(ListPlansRequest request) =>
      _client.listPlans(request);
}

/// Coordinator for unified refresh->plan->execute flow.
///
/// Provides a single shared execution path used by both WorkflowPanelWidget
/// and FilePaneWidget. Ensures consistent:
/// - Folder refresh before planning
/// - Shared softDelete from WorkflowStateStore
/// - Stale plan retry behavior (exactly once on PLAN_STALE signal)
class PlanExecutionCoordinator implements FilePaneCoordinator {
  final WorkflowStateStore _store;
  late final CoordinatorGrpcClient _client;

  /// The structured error code that signals a stale plan requiring retry.
  /// This is the explicit stale signal contract - no free-form text matching.
  static const String staleSignalCode = 'PLAN_STALE';

  PlanExecutionCoordinator({
    ClientChannel? channel,
    required WorkflowStateStore store,
    @visibleForTesting CoordinatorGrpcClient? testClient,
  }) : _store = store {
    if (testClient != null) {
      _client = testClient;
    } else {
      if (channel == null) {
        throw ArgumentError(
          'channel is required when testClient is not provided',
        );
      }
      _client = _RealCoordinatorClient(OnseiServiceClient(channel));
    }
  }

  /// Returns true if the JobEvent represents an explicit stale signal.
  /// Uses structured field comparison, not free-form text matching.
  static bool isStaleSignal(JobEvent event) {
    return event.eventType == 'error' && event.code == staleSignalCode;
  }

  /// Returns plans visible for validation in a root scope.
  /// Extracted for test override.
  @visibleForTesting
  Future<ListPlansResponse> listPlansForValidation({required String rootPath}) {
    return _client.listPlans(
      ListPlansRequest()
        ..rootPath = rootPath
        ..limit = 500,
    );
  }

  /// Refresh + plan for workflow channel, returns the plan response only.
  /// This keeps plan generation and execution separated for planId-only execute.
  Future<PlanOperationsResponse> planOnlyForFolders({
    required String rootPath,
    required Set<String> selectedFolders,
    required String targetFormat,
    required String planType,
    bool pruneMatchedExcluded = false,
  }) async {
    final folderPaths = _dedupePathsPreservingBusinessPath(selectedFolders);

    if (folderPaths.isNotEmpty) {
      try {
        await _client.refreshFolders(
          RefreshFoldersRequest()
            ..rootPath = rootPath
            ..folderPaths.addAll(folderPaths),
        );
      } catch (_) {
        // Keep planning flow resilient to refresh transient failures.
      }
    }

    final planRequest = PlanOperationsRequest()
      ..targetFormat = targetFormat
      ..planType = planType
      ..pruneMatchedExcluded = pruneMatchedExcluded;

    if (folderPaths.isNotEmpty) {
      planRequest.folderPaths.addAll(folderPaths);
      planRequest.folderPath = folderPaths.first;
    }

    return _client.planOperations(planRequest);
  }

  /// Execute a previously created plan ID only.
  /// No refresh/replan is performed in this path.
  Future<PlanExecutionResult> executeByPlanId({
    required String planId,
    String? rootPathForValidation,
    ExecuteEventListener? onEvent,
  }) async {
    if (planId.trim().isEmpty) {
      return PlanExecutionResult.failure('Execution failed: missing plan ID');
    }

    if (rootPathForValidation != null &&
        rootPathForValidation.trim().isNotEmpty) {
      try {
        final plans = await listPlansForValidation(
          rootPath: rootPathForValidation,
        );
        final found = plans.plans.any((p) => p.planId == planId);
        // listPlans can be temporarily empty right after planning due to
        // backend visibility timing. Treat empty results as inconclusive and
        // let executePlan perform authoritative plan-id validation.
        if (!found && plans.plans.isNotEmpty) {
          return PlanExecutionResult.failure(
            'Execution failed: plan ID not found in current root scope',
          );
        }
      } catch (e) {
        return PlanExecutionResult.failure('Plan validation failed: $e');
      }
    }

    return _executeExistingPlan(planId: planId, onEvent: onEvent);
  }

  /// Execute the full flow for a set of selected files.
  ///
  /// Flow:
  /// 1. Derive refresh scope from folderPath or parent folders of selected files
  /// 2. Call refreshFolders
  /// 3. Call planOperations
  /// 4. Call executePlan with shared softDelete
  /// 5. If execute returns PLAN_STALE, retry exactly once
  @override
  Future<PlanExecutionResult> executeFlowForFiles({
    required String rootPath,
    required String? folderPath,
    required Set<String> selectedFiles,
    required String targetFormat,
    required String planType,
  }) async {
    // Derive refresh scope
    final folderPaths = _deriveFolderScopeFromFileSelection(
      folderPath: folderPath,
      selectedFiles: selectedFiles,
    );

    return _executeFlowInternal(
      rootPath: rootPath,
      folderPaths: folderPaths,
      sourceFiles: selectedFiles.toList(),
      targetFormat: targetFormat,
      planType: planType,
    );
  }

  /// Execute the full flow for a set of selected folders.
  ///
  /// Flow:
  /// 1. Normalize and dedupe folder paths
  /// 2. Call refreshFolders
  /// 3. Call planOperations
  /// 4. Call executePlan with shared softDelete
  /// 5. If execute returns PLAN_STALE, retry exactly once
  Future<PlanExecutionResult> executeFlowForFolders({
    required String rootPath,
    required Set<String> selectedFolders,
    required String targetFormat,
    required String planType,
  }) async {
    // Normalize and dedupe folder paths
    final folderPaths = _dedupePathsPreservingBusinessPath(selectedFolders);

    return _executeFlowInternal(
      rootPath: rootPath,
      folderPaths: folderPaths,
      sourceFiles: const [],
      targetFormat: targetFormat,
      planType: planType,
    );
  }

  /// Derive folder paths from file selection.
  /// Uses folderPath if provided; otherwise extracts parent folders from file paths.
  List<String> _deriveFolderScopeFromFileSelection({
    required String? folderPath,
    required Set<String> selectedFiles,
  }) {
    if (folderPath != null && folderPath.isNotEmpty) {
      return _dedupePathsPreservingBusinessPath({folderPath});
    }

    // Extract parent directories from file paths
    final parentDirs = <String>{};
    for (final filePath in selectedFiles) {
      final lastForward = filePath.lastIndexOf('/');
      final lastBack = filePath.lastIndexOf(r'\');
      final splitIndex = lastForward > lastBack ? lastForward : lastBack;
      if (splitIndex > 0) {
        parentDirs.add(filePath.substring(0, splitIndex));
      }
    }

    return _dedupePathsPreservingBusinessPath(parentDirs);
  }

  /// Dedupe with normalized comparison keys while preserving business path casing.
  List<String> _dedupePathsPreservingBusinessPath(Set<String> paths) {
    final seen = <String>{};
    final result = <String>[];

    for (final path in paths) {
      final businessPath = _sanitizeBusinessPath(path);
      final normalizedKey = normalizePathForComparison(businessPath);
      if (normalizedKey.isNotEmpty && !seen.contains(normalizedKey)) {
        seen.add(normalizedKey);
        result.add(businessPath);
      }
    }

    return result;
  }

  String _sanitizeBusinessPath(String path) {
    var value = path.trim();
    while (value.length > 1 && (value.endsWith('/') || value.endsWith(r'\'))) {
      value = value.substring(0, value.length - 1);
    }
    return value;
  }

  Future<PlanExecutionResult> _executeExistingPlan({
    required String planId,
    ExecuteEventListener? onEvent,
  }) async {
    final executeRequest = ExecutePlanRequest()
      ..planId = planId
      ..softDelete = _store.softDelete;

    final events = <JobEvent>[];
    String? errorMessage;

    try {
      await for (final event in _client.executePlan(executeRequest)) {
        events.add(event);
        onEvent?.call(event);
        if (event.eventType == 'error') {
          errorMessage = event.message;
          if (event.code.isNotEmpty) {
            errorMessage = '${event.code}: $errorMessage';
          }
        }
      }
    } catch (e) {
      return PlanExecutionResult.failure('Execution failed: $e');
    }

    if (errorMessage != null) {
      return PlanExecutionResult.failure(errorMessage);
    }

    return PlanExecutionResult.success(planId: planId, events: events);
  }

  /// Internal execution flow with optional retry on stale.
  Future<PlanExecutionResult> _executeFlowInternal({
    required String rootPath,
    required List<String> folderPaths,
    required List<String> sourceFiles,
    required String targetFormat,
    required String planType,
    bool isRetry = false,
  }) async {
    // Step 1: Refresh folders
    if (folderPaths.isNotEmpty) {
      try {
        await _client.refreshFolders(
          RefreshFoldersRequest()
            ..rootPath = rootPath
            ..folderPaths.addAll(folderPaths),
        );
      } catch (e) {
        // Log but continue - refresh errors shouldn't block planning
        // The plan operation will surface any issues
      }
    }

    // Step 2: Plan operations
    final planRequest = PlanOperationsRequest()
      ..targetFormat = targetFormat
      ..planType = planType;

    if (folderPaths.isNotEmpty) {
      planRequest.folderPaths.addAll(folderPaths);
      planRequest.folderPath = folderPaths.first;
    }

    if (sourceFiles.isNotEmpty) {
      planRequest.sourceFiles.addAll(sourceFiles);
    }

    PlanOperationsResponse planResponse;
    try {
      planResponse = await _client.planOperations(planRequest);
    } catch (e) {
      return PlanExecutionResult.failure('Planning failed: $e');
    }

    if (planResponse.planId.isEmpty) {
      return PlanExecutionResult.failure('Planning failed: missing plan ID');
    }

    if (planResponse.operations.isEmpty) {
      return PlanExecutionResult.failure('No operations to execute');
    }

    // Step 3: Execute plan with shared softDelete
    final executeRequest = ExecutePlanRequest()
      ..planId = planResponse.planId
      ..softDelete = _store.softDelete;

    final events = <JobEvent>[];
    bool foundStale = false;
    String? errorMessage;

    try {
      await for (final event in _client.executePlan(executeRequest)) {
        events.add(event);
        if (isStaleSignal(event)) {
          foundStale = true;
        }
        if (event.eventType == 'error') {
          errorMessage = event.message;
          if (event.code.isNotEmpty) {
            errorMessage = '${event.code}: $errorMessage';
          }
        }
      }
    } catch (e) {
      return PlanExecutionResult.failure('Execution failed: $e');
    }

    // Step 4: Retry exactly once on stale signal
    if (foundStale && !isRetry) {
      return _executeFlowInternal(
        rootPath: rootPath,
        folderPaths: folderPaths,
        sourceFiles: sourceFiles,
        targetFormat: targetFormat,
        planType: planType,
        isRetry: true, // Mark as retry to prevent infinite loop
      );
    }

    if (foundStale && isRetry) {
      // Second attempt also returned stale - fail
      return PlanExecutionResult.failure(
        '$staleSignalCode: Plan remains stale after retry',
      );
    }

    if (errorMessage != null) {
      return PlanExecutionResult.failure(errorMessage);
    }

    return PlanExecutionResult.success(
      planId: planResponse.planId,
      events: events,
    );
  }
}
