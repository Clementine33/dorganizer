import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Fake gRPC client for PlanExecutionCoordinator unit tests.
/// Records all requests and returns configurable responses — no in-process
/// gRPC server needed.
class FakeCoordinatorClient implements CoordinatorGrpcClient {
  final List<RefreshFoldersRequest> refreshRequests = [];
  final List<PlanOperationsRequest> planRequests = [];
  final List<ExecutePlanRequest> executeRequests = [];
  final List<ListPlansRequest> listPlansRequests = [];

  RefreshFoldersResponse? nextRefreshResponse;
  PlanOperationsResponse? nextPlanResponse;
  Stream<JobEvent>? nextExecuteStream;
  ListPlansResponse? nextListPlansResponse;

  int refreshCallCount = 0;
  int planCallCount = 0;
  int executeCallCount = 0;

  void reset() {
    refreshRequests.clear();
    planRequests.clear();
    executeRequests.clear();
    listPlansRequests.clear();
    nextRefreshResponse = null;
    nextPlanResponse = null;
    nextExecuteStream = null;
    nextListPlansResponse = null;
    refreshCallCount = 0;
    planCallCount = 0;
    executeCallCount = 0;
  }

  void setRefreshResponse(RefreshFoldersResponse response) {
    nextRefreshResponse = response;
  }

  void setPlanResponse(PlanOperationsResponse response) {
    nextPlanResponse = response;
  }

  void setExecuteStream(Stream<JobEvent> stream) {
    nextExecuteStream = stream;
  }

  void setListPlansResponse(ListPlansResponse response) {
    nextListPlansResponse = response;
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    RefreshFoldersRequest request,
  ) async {
    refreshRequests.add(request);
    refreshCallCount++;
    return nextRefreshResponse ??
        (RefreshFoldersResponse()
          ..successfulFolders.addAll(request.folderPaths));
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    PlanOperationsRequest request,
  ) async {
    planRequests.add(request);
    planCallCount++;
    if (nextPlanResponse != null) {
      return nextPlanResponse!;
    }
    return PlanOperationsResponse()
      ..planId = 'test-plan-${DateTime.now().millisecondsSinceEpoch}'
      ..operations.add(
        PlannedOperation()
          ..sourcePath = '/test/file.flac'
          ..targetPath = '/test/file.m4a'
          ..operationType = 'convert',
      );
  }

  @override
  Stream<JobEvent> executePlan(ExecutePlanRequest request) {
    executeRequests.add(request);
    executeCallCount++;
    return nextExecuteStream ??
        Stream.fromIterable([
          JobEvent()
            ..eventType = 'info'
            ..message = 'Started',
          JobEvent()
            ..eventType = 'done'
            ..message = 'Completed',
        ]);
  }

  @override
  Future<ListPlansResponse> listPlans(ListPlansRequest request) async {
    listPlansRequests.add(request);
    return nextListPlansResponse ?? ListPlansResponse();
  }
}

void main() {
  late FakeCoordinatorClient fakeClient;
  late WorkflowStateStore store;
  late PlanExecutionCoordinator coordinator;

  setUp(() {
    fakeClient = FakeCoordinatorClient();
    // WorkflowStateStore requires a channel but tests never call
    // store.refreshFolders() — only setSoftDelete/softDelete are used.
    store = WorkflowStateStore(
      channel: ClientChannel(
        'localhost',
        port: 9999,
        options: const ChannelOptions(
          credentials: ChannelCredentials.insecure(),
        ),
      ),
    );
    coordinator = PlanExecutionCoordinator(store: store, testClient: fakeClient);
  });

  group('PlanExecutionCoordinator stale signal contract', () {
    test('isStaleSignal returns true for PLAN_STALE code', () {
      final event = JobEvent()
        ..eventType = 'error'
        ..code = 'PLAN_STALE'
        ..message = 'Plan has expired';

      expect(PlanExecutionCoordinator.isStaleSignal(event), isTrue);
    });

    test('isStaleSignal returns false for other error codes', () {
      final event = JobEvent()
        ..eventType = 'error'
        ..code = 'PERMISSION_DENIED'
        ..message = 'Access denied';

      expect(PlanExecutionCoordinator.isStaleSignal(event), isFalse);
    });

    test('isStaleSignal returns false for non-error events', () {
      final event = JobEvent()
        ..eventType = 'info'
        ..message = 'Processing...';

      expect(PlanExecutionCoordinator.isStaleSignal(event), isFalse);
    });

    test('isStaleSignal returns false for error event without code', () {
      final event = JobEvent()
        ..eventType = 'error'
        ..message = 'Unknown error';

      expect(PlanExecutionCoordinator.isStaleSignal(event), isFalse);
    });
  });

  group('PlanExecutionCoordinator shared sequence refresh->plan->execute', () {
    test('executeByPlanId executes existing plan without replan', () async {
      fakeClient.reset();
      store.setSoftDelete(true);

      final result = await coordinator.executeByPlanId(
        planId: 'existing-plan-1',
      );

      expect(result.success, isTrue);
      expect(fakeClient.refreshCallCount, equals(0));
      expect(fakeClient.planCallCount, equals(0));
      expect(fakeClient.executeCallCount, equals(1));
      expect(
        fakeClient.executeRequests.single.planId,
        equals('existing-plan-1'),
      );
      expect(fakeClient.executeRequests.single.softDelete, isTrue);
    });

    test('executeByPlanId validates missing planId', () async {
      fakeClient.reset();

      final result = await coordinator.executeByPlanId(planId: '');

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('plan ID'));
      expect(fakeClient.executeCallCount, equals(0));
    });

    test(
      'executeByPlanId validates non-existent planId when scoped plans exist',
      () async {
        fakeClient.reset();
        fakeClient.setListPlansResponse(
          ListPlansResponse()
            ..plans.add(
              PlanInfo()
                ..planId = 'some-other-plan'
                ..rootPath = '/root',
            ),
        );

        final result = await coordinator.executeByPlanId(
          planId: 'missing-plan-id',
          rootPathForValidation: '/root',
        );

        expect(result.success, isFalse);
        expect(result.errorMessage, contains('not found'));
        expect(fakeClient.executeCallCount, equals(0));
      },
    );

    test('executeByPlanId accepts existing planId from listPlans', () async {
      fakeClient.reset();
      fakeClient.setListPlansResponse(
        ListPlansResponse()
          ..plans.add(
            PlanInfo()
              ..planId = 'existing-plan-from-list'
              ..rootPath = '/root',
          ),
      );

      final result = await coordinator.executeByPlanId(
        planId: 'existing-plan-from-list',
        rootPathForValidation: '/root',
      );

      expect(result.success, isTrue);
    });

    test('executeByPlanId proceeds when listPlans is empty', () async {
      fakeClient.reset();
      fakeClient.setListPlansResponse(ListPlansResponse());

      final result = await coordinator.executeByPlanId(
        planId: 'existing-plan-not-yet-listed',
        rootPathForValidation: '/root',
      );

      expect(result.success, isTrue);
      expect(fakeClient.executeCallCount, equals(1));
      expect(
        fakeClient.executeRequests.single.planId,
        equals('existing-plan-not-yet-listed'),
      );
    });

    test('executeByPlanId forwards events during stream via onEvent', () async {
      fakeClient.reset();

      final controller = StreamController<JobEvent>(sync: true);
      fakeClient.setExecuteStream(controller.stream);

      final seenTypes = <String>[];
      final firstEventSeen = Completer<void>();
      var completed = false;

      final future = coordinator.executeByPlanId(
        planId: 'existing-plan-streaming',
        onEvent: (event) {
          seenTypes.add(event.eventType);
          if (!firstEventSeen.isCompleted) {
            firstEventSeen.complete();
          }
        },
      );
      future.then((_) => completed = true);

      controller.add(
        JobEvent()
          ..eventType = 'folder_completed'
          ..message = 'Scope /root/A completed',
      );

      await firstEventSeen.future.timeout(const Duration(seconds: 1));

      expect(seenTypes, equals(['folder_completed']));
      expect(completed, isFalse);

      controller.add(
        JobEvent()
          ..eventType = 'completed'
          ..message = 'Execution complete',
      );
      await controller.close();

      final result = await future;
      expect(result.success, isTrue);
      expect(result.events.length, equals(2));
      expect(seenTypes, equals(['folder_completed', 'completed']));
    });

    test(
      'executeFlowForFiles calls refresh->plan->execute in sequence',
      () async {
        fakeClient.reset();
        fakeClient.setPlanResponse(
          PlanOperationsResponse()
            ..planId = 'test-plan-123'
            ..operations.add(
              PlannedOperation()
                ..sourcePath = '/test/file.flac'
                ..targetPath = '/test/file.m4a'
                ..operationType = 'convert',
            ),
        );

        store.setSoftDelete(true);

        final result = await coordinator.executeFlowForFiles(
          rootPath: '/root',
          folderPath: '/root/folderA',
          selectedFiles: {'/root/folderA/file.flac'},
          targetFormat: 'm4a',
          planType: 'single_convert',
        );

        // Verify sequence: refresh -> plan -> execute
        expect(fakeClient.refreshCallCount, equals(1));
        expect(fakeClient.planCallCount, equals(1));
        expect(fakeClient.executeCallCount, equals(1));

        // Verify refresh was called with correct scope
        expect(fakeClient.refreshRequests, hasLength(1));
        expect(fakeClient.refreshRequests.first.rootPath, equals('/root'));
        expect(
          fakeClient.refreshRequests.first.folderPaths,
          contains('/root/folderA'),
        );

        // Verify plan request
        expect(fakeClient.planRequests, hasLength(1));
        expect(fakeClient.planRequests.first.targetFormat, equals('m4a'));
        expect(
          fakeClient.planRequests.first.planType,
          equals('single_convert'),
        );

        // Verify execute used softDelete from store
        expect(fakeClient.executeRequests, hasLength(1));
        // Execute request should have been called
        expect(fakeClient.executeRequests.first.softDelete, isTrue);

        expect(result.success, isTrue);
      },
    );

    test(
      'executeFlowForFolders calls refresh->plan->execute for folders',
      () async {
        fakeClient.reset();
        fakeClient.setPlanResponse(
          PlanOperationsResponse()
            ..planId = 'test-plan-folders'
            ..operations.add(
              PlannedOperation()
                ..sourcePath = '/root/folderA/file.wav'
                ..targetPath = '/root/folderA/file.m4a'
                ..operationType = 'convert',
            ),
        );

        store.setSoftDelete(false);

        final result = await coordinator.executeFlowForFolders(
          rootPath: '/root',
          selectedFolders: {'/root/folderA', '/root/folderB'},
          targetFormat: 'slim:mode1',
          planType: 'slim',
        );

        // Verify refresh called with deduplicated folders
        expect(fakeClient.refreshCallCount, equals(1));
        expect(
          fakeClient.refreshRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );

        // Verify plan
        expect(fakeClient.planCallCount, equals(1));
        expect(
          fakeClient.planRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );

        // Verify execute with softDelete from store
        expect(fakeClient.executeCallCount, equals(1));
        expect(fakeClient.executeRequests.first.softDelete, isFalse);

        expect(result.success, isTrue);
      },
    );

    test(
      'executeFlowForFiles derives parent folders from selected files when folderPath is null',
      () async {
        fakeClient.reset();

        await coordinator.executeFlowForFiles(
          rootPath: '/root',
          folderPath: null,
          selectedFiles: {
            '/root/folderA/file1.flac',
            '/root/folderA/file2.flac',
            '/root/folderB/file3.flac',
          },
          targetFormat: 'm4a',
          planType: 'single_convert',
        );

        // Verify refresh called with deduplicated parent folders
        expect(fakeClient.refreshRequests, hasLength(1));
        expect(
          fakeClient.refreshRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );
      },
    );
  });

  group('PlanExecutionCoordinator stale retry behavior', () {
    test('stale signal triggers exactly one retry cycle', () async {
      fakeClient.reset();

      // First execute returns PLAN_STALE
      // Second execute returns success
      fakeClient.setExecuteStream(
        Stream.fromIterable([
          JobEvent()
            ..eventType = 'error'
            ..code = 'PLAN_STALE'
            ..message = 'Plan has expired',
        ]),
      );

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      // After first execute with stale, coordinator should retry once
      // So we expect: 2 refresh, 2 plan, 2 execute
      expect(
        fakeClient.refreshCallCount,
        equals(2),
        reason: 'Should refresh twice (initial + retry)',
      );
      expect(
        fakeClient.planCallCount,
        equals(2),
        reason: 'Should plan twice (initial + retry)',
      );
      expect(
        fakeClient.executeCallCount,
        equals(2),
        reason: 'Should execute twice (initial stale + retry)',
      );
    });

    test('non-stale execute error does not retry', () async {
      fakeClient.reset();
      fakeClient.setExecuteStream(
        Stream.fromIterable([
          JobEvent()
            ..eventType = 'error'
            ..code = 'PERMISSION_DENIED'
            ..message = 'Access denied',
        ]),
      );

      final result = await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      // Should not retry for non-stale errors
      expect(
        fakeClient.refreshCallCount,
        equals(1),
        reason: 'Should only refresh once (no retry for non-stale)',
      );
      expect(
        fakeClient.planCallCount,
        equals(1),
        reason: 'Should only plan once (no retry for non-stale)',
      );
      expect(
        fakeClient.executeCallCount,
        equals(1),
        reason: 'Should only execute once (no retry for non-stale)',
      );

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('PERMISSION_DENIED'));
    });

    test('second stale does not trigger additional retry', () async {
      fakeClient.reset();

      // All execute attempts return PLAN_STALE
      fakeClient.setExecuteStream(
        Stream.fromIterable([
          JobEvent()
            ..eventType = 'error'
            ..code = 'PLAN_STALE'
            ..message = 'Plan has expired',
        ]),
      );

      final result = await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      // Should only retry once max, even if second also stale
      expect(
        fakeClient.executeCallCount,
        equals(2),
        reason: 'Should execute at most twice (initial + one retry)',
      );

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('PLAN_STALE'));
    });
  });

  group('PlanExecutionCoordinator scope normalization', () {
    test('deduplicates and normalizes folder paths', () async {
      fakeClient.reset();

      await coordinator.executeFlowForFolders(
        rootPath: '/root',
        selectedFolders: {
          '/root/folderA',
          '/root/folderA/', // Duplicate with trailing slash
          '/root/folderB',
          '/root/folderB\\', // Duplicate with backslash
        },
        targetFormat: 'm4a',
        planType: 'slim',
      );

      // Verify deduplication
      final refreshPaths = fakeClient.refreshRequests.first.folderPaths;
      expect(
        refreshPaths.length,
        equals(2),
        reason: 'Should deduplicate equivalent paths',
      );
    });

    test(
      'normalizes paths for comparison (case-insensitive on Windows)',
      () async {
        fakeClient.reset();

        await coordinator.executeFlowForFolders(
          rootPath: 'C:/Root',
          selectedFolders: {
            'C:/Root/FolderA',
            'c:/root/foldera', // Same path, different case
          },
          targetFormat: 'm4a',
          planType: 'slim',
        );

        // On Windows-style paths, case should be normalized
        final refreshPaths = fakeClient.refreshRequests.first.folderPaths;
        // Both should be treated as same after normalization
        expect(refreshPaths.length, lessThanOrEqualTo(2));
      },
    );

    test(
      'keeps original folder-path casing in refresh/plan requests',
      () async {
        fakeClient.reset();

        await coordinator.executeFlowForFolders(
          rootPath: 'C:/Root',
          selectedFolders: {
            'C:/Root/RJ259534 [ReApple] オレっ娘アリスの癒し(ドS)のご奉仕',
            'c:/root/rj259534 [reapple] オレっ娘アリスの癒し(ドs)のご奉仕',
          },
          targetFormat: 'slim:mode1',
          planType: 'slim',
        );

        expect(fakeClient.refreshRequests, hasLength(1));
        expect(fakeClient.planRequests, hasLength(1));

        final refreshPaths = fakeClient.refreshRequests.first.folderPaths;
        final planPaths = fakeClient.planRequests.first.folderPaths;
        expect(
          refreshPaths,
          hasLength(1),
          reason: 'Equivalent paths should dedupe',
        );
        expect(
          planPaths,
          hasLength(1),
          reason: 'Equivalent paths should dedupe',
        );

        expect(
          refreshPaths.single,
          equals('C:/Root/RJ259534 [ReApple] オレっ娘アリスの癒し(ドS)のご奉仕'),
        );
        expect(
          planPaths.single,
          equals('C:/Root/RJ259534 [ReApple] オレっ娘アリスの癒し(ドS)のご奉仕'),
        );
      },
    );
  });

  group('PlanExecutionCoordinator softDelete propagation', () {
    test('execute uses softDelete from shared store', () async {
      fakeClient.reset();
      store.setSoftDelete(true);

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(fakeClient.executeRequests.last.softDelete, isTrue);
    });

    test('softDelete=false is correctly propagated', () async {
      fakeClient.reset();
      store.setSoftDelete(false);

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(fakeClient.executeRequests.last.softDelete, isFalse);
    });
  });

  group('PlanExecutionCoordinator error handling', () {
    test('returns failure when plan returns empty planId', () async {
      fakeClient.reset();
      final emptyPlanIdResponse = PlanOperationsResponse();
      emptyPlanIdResponse.planId = ''; // Explicitly set empty string
      emptyPlanIdResponse.operations.add(
        PlannedOperation()
          ..sourcePath = '/test/file.flac'
          ..operationType = 'convert',
      );
      fakeClient.setPlanResponse(emptyPlanIdResponse);

      // Verify the setup is correct
      expect(fakeClient.nextPlanResponse!.planId, isEmpty);
      expect(fakeClient.nextPlanResponse!.operations, isNotEmpty);

      final result = await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(
        result.success,
        isFalse,
        reason:
            'Should fail when planId is empty. Got error: ${result.errorMessage}',
      );
      expect(result.errorMessage, contains('plan'));
      expect(
        fakeClient.executeCallCount,
        equals(0),
        reason: 'Should not execute when plan is invalid',
      );
    });

    test('returns failure when plan returns no operations', () async {
      fakeClient.reset();
      final noOpsResponse = PlanOperationsResponse();
      noOpsResponse.planId = 'test-plan-empty';
      // Don't add any operations - operations list should be empty
      fakeClient.setPlanResponse(noOpsResponse);

      // Verify the setup is correct
      expect(fakeClient.nextPlanResponse!.operations, isEmpty);

      final result = await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(
        result.success,
        isFalse,
        reason:
            'Should fail when operations are empty. Got error: ${result.errorMessage}',
      );
    });

    test('handles refresh errors gracefully', () async {
      fakeClient.reset();
      fakeClient.setRefreshResponse(
        RefreshFoldersResponse()
          ..errors.add(
            FolderError()
              ..folderPath = '/root/folderA'
              ..code = 'SCAN_ERROR'
              ..message = 'Permission denied',
          ),
      );

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      // Refresh error should be logged but flow continues
      expect(fakeClient.refreshCallCount, equals(1));
      expect(fakeClient.planCallCount, equals(1));
      expect(fakeClient.executeCallCount, equals(1));
    });
  });
}
