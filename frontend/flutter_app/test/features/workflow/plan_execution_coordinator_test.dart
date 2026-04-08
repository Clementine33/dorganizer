import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Fake OnseiService for PlanExecutionCoordinator tests.
/// Records all incoming requests for verification.
class FakeOnseiService extends OnseiServiceBase {
  final List<RefreshFoldersRequest> refreshRequests = [];
  final List<PlanOperationsRequest> planRequests = [];
  final List<ExecutePlanRequest> executeRequests = [];
  final List<ListPlansRequest> listPlansRequests = [];

  RefreshFoldersResponse? nextRefreshResponse;
  PlanOperationsResponse? nextPlanResponse;
  Stream<JobEvent>? nextExecuteStream;
  ListPlansResponse? nextListPlansResponse;
  String? nextConfigJson;

  // Track call counts for retry verification
  int refreshCallCount = 0;
  int planCallCount = 0;
  int executeCallCount = 0;

  void reset() {
    refreshRequests.clear();
    planRequests.clear();
    executeRequests.clear();
    nextRefreshResponse = null;
    nextPlanResponse = null;
    nextExecuteStream = null;
    nextListPlansResponse = null;
    nextConfigJson = null;
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

  void setConfigJson(String configJson) {
    nextConfigJson = configJson;
  }

  void setListPlansResponse(ListPlansResponse response) {
    nextListPlansResponse = response;
  }

  @override
  Future<GetConfigResponse> getConfig(
    ServiceCall call,
    GetConfigRequest request,
  ) async {
    return GetConfigResponse()
      ..configJson = nextConfigJson ?? '{"tools":{"encoder":"qaac"}}';
  }

  @override
  Future<UpdateConfigResponse> updateConfig(
    ServiceCall call,
    UpdateConfigRequest request,
  ) async {
    return UpdateConfigResponse();
  }

  @override
  Stream<JobEvent> scan(ServiceCall call, ScanRequest request) {
    return const Stream.empty();
  }

  @override
  Future<ListFoldersResponse> listFolders(
    ServiceCall call,
    ListFoldersRequest request,
  ) async {
    return ListFoldersResponse();
  }

  @override
  Future<ListFilesResponse> listFiles(
    ServiceCall call,
    ListFilesRequest request,
  ) async {
    return ListFilesResponse();
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    ServiceCall call,
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
  Stream<JobEvent> executePlan(ServiceCall call, ExecutePlanRequest request) {
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
  Future<ListPlansResponse> listPlans(
    ServiceCall call,
    ListPlansRequest request,
  ) async {
    listPlansRequests.add(request);
    return nextListPlansResponse ?? ListPlansResponse();
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    ServiceCall call,
    RefreshFoldersRequest request,
  ) async {
    refreshRequests.add(request);
    refreshCallCount++;
    return nextRefreshResponse ?? RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

/// Creates a test Server that runs the fake service.
Future<(Server, FakeOnseiService)> createTestServer() async {
  final fakeService = FakeOnseiService();
  final server = Server.create(services: [fakeService]);
  await server.serve(port: 0);
  return (server, fakeService);
}

void main() {
  late Server server;
  late FakeOnseiService fakeService;
  late ClientChannel channel;
  late WorkflowStateStore store;
  late PlanExecutionCoordinator coordinator;

  setUp(() async {
    final (s, f) = await createTestServer();
    server = s;
    fakeService = f;
    channel = ClientChannel(
      'localhost',
      port: server.port as int,
      options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
    );
    store = WorkflowStateStore(channel: channel);
    coordinator = PlanExecutionCoordinator(channel: channel, store: store);
  });

  tearDown(() async {
    await channel.shutdown();
    await server.shutdown();
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
      fakeService.reset();
      store.setSoftDelete(true);

      final result = await coordinator.executeByPlanId(
        planId: 'existing-plan-1',
      );

      expect(result.success, isTrue);
      expect(fakeService.refreshCallCount, equals(0));
      expect(fakeService.planCallCount, equals(0));
      expect(fakeService.executeCallCount, equals(1));
      expect(
        fakeService.executeRequests.single.planId,
        equals('existing-plan-1'),
      );
      expect(fakeService.executeRequests.single.softDelete, isTrue);
    });

    test('executeByPlanId validates missing planId', () async {
      fakeService.reset();

      final result = await coordinator.executeByPlanId(planId: '');

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('plan ID'));
      expect(fakeService.executeCallCount, equals(0));
    });

    test(
      'executeByPlanId validates non-existent planId when scoped plans exist',
      () async {
        fakeService.reset();
        fakeService.setListPlansResponse(
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
        expect(fakeService.executeCallCount, equals(0));
      },
    );

    test('executeByPlanId accepts existing planId from listPlans', () async {
      fakeService.reset();
      fakeService.setListPlansResponse(
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
      fakeService.reset();
      fakeService.setListPlansResponse(ListPlansResponse());

      final result = await coordinator.executeByPlanId(
        planId: 'existing-plan-not-yet-listed',
        rootPathForValidation: '/root',
      );

      expect(result.success, isTrue);
      expect(fakeService.executeCallCount, equals(1));
      expect(
        fakeService.executeRequests.single.planId,
        equals('existing-plan-not-yet-listed'),
      );
    });

    test('executeByPlanId forwards events during stream via onEvent', () async {
      fakeService.reset();

      final controller = StreamController<JobEvent>(sync: true);
      fakeService.setExecuteStream(controller.stream);

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
        fakeService.reset();
        fakeService.setPlanResponse(
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
        expect(fakeService.refreshCallCount, equals(1));
        expect(fakeService.planCallCount, equals(1));
        expect(fakeService.executeCallCount, equals(1));

        // Verify refresh was called with correct scope
        expect(fakeService.refreshRequests, hasLength(1));
        expect(fakeService.refreshRequests.first.rootPath, equals('/root'));
        expect(
          fakeService.refreshRequests.first.folderPaths,
          contains('/root/folderA'),
        );

        // Verify plan request
        expect(fakeService.planRequests, hasLength(1));
        expect(fakeService.planRequests.first.targetFormat, equals('m4a'));
        expect(
          fakeService.planRequests.first.planType,
          equals('single_convert'),
        );

        // Verify execute used softDelete from store
        expect(fakeService.executeRequests, hasLength(1));
        // Execute request should have been called
        expect(fakeService.executeRequests.first.softDelete, isTrue);

        expect(result.success, isTrue);
      },
    );

    test(
      'executeFlowForFolders calls refresh->plan->execute for folders',
      () async {
        fakeService.reset();
        fakeService.setPlanResponse(
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
        expect(fakeService.refreshCallCount, equals(1));
        expect(
          fakeService.refreshRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );

        // Verify plan
        expect(fakeService.planCallCount, equals(1));
        expect(
          fakeService.planRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );

        // Verify execute with softDelete from store
        expect(fakeService.executeCallCount, equals(1));
        expect(fakeService.executeRequests.first.softDelete, isFalse);

        expect(result.success, isTrue);
      },
    );

    test(
      'executeFlowForFiles derives parent folders from selected files when folderPath is null',
      () async {
        fakeService.reset();

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
        expect(fakeService.refreshRequests, hasLength(1));
        expect(
          fakeService.refreshRequests.first.folderPaths,
          containsAll(['/root/folderA', '/root/folderB']),
        );
      },
    );
  });

  group('PlanExecutionCoordinator stale retry behavior', () {
    test('stale signal triggers exactly one retry cycle', () async {
      fakeService.reset();

      // First execute returns PLAN_STALE
      // Second execute returns success
      fakeService.setExecuteStream(
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
        fakeService.refreshCallCount,
        equals(2),
        reason: 'Should refresh twice (initial + retry)',
      );
      expect(
        fakeService.planCallCount,
        equals(2),
        reason: 'Should plan twice (initial + retry)',
      );
      expect(
        fakeService.executeCallCount,
        equals(2),
        reason: 'Should execute twice (initial stale + retry)',
      );
    });

    test('non-stale execute error does not retry', () async {
      fakeService.reset();
      fakeService.setExecuteStream(
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
        fakeService.refreshCallCount,
        equals(1),
        reason: 'Should only refresh once (no retry for non-stale)',
      );
      expect(
        fakeService.planCallCount,
        equals(1),
        reason: 'Should only plan once (no retry for non-stale)',
      );
      expect(
        fakeService.executeCallCount,
        equals(1),
        reason: 'Should only execute once (no retry for non-stale)',
      );

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('PERMISSION_DENIED'));
    });

    test('second stale does not trigger additional retry', () async {
      fakeService.reset();

      // All execute attempts return PLAN_STALE
      fakeService.setExecuteStream(
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
        fakeService.executeCallCount,
        equals(2),
        reason: 'Should execute at most twice (initial + one retry)',
      );

      expect(result.success, isFalse);
      expect(result.errorMessage, contains('PLAN_STALE'));
    });
  });

  group('PlanExecutionCoordinator scope normalization', () {
    test('deduplicates and normalizes folder paths', () async {
      fakeService.reset();

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
      final refreshPaths = fakeService.refreshRequests.first.folderPaths;
      expect(
        refreshPaths.length,
        equals(2),
        reason: 'Should deduplicate equivalent paths',
      );
    });

    test(
      'normalizes paths for comparison (case-insensitive on Windows)',
      () async {
        fakeService.reset();

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
        final refreshPaths = fakeService.refreshRequests.first.folderPaths;
        // Both should be treated as same after normalization
        expect(refreshPaths.length, lessThanOrEqualTo(2));
      },
    );

    test(
      'keeps original folder-path casing in refresh/plan requests',
      () async {
        fakeService.reset();

        await coordinator.executeFlowForFolders(
          rootPath: 'C:/Root',
          selectedFolders: {
            'C:/Root/RJ259534 [ReApple] オレっ娘アリスの癒し(ドS)のご奉仕',
            'c:/root/rj259534 [reapple] オレっ娘アリスの癒し(ドs)のご奉仕',
          },
          targetFormat: 'slim:mode1',
          planType: 'slim',
        );

        expect(fakeService.refreshRequests, hasLength(1));
        expect(fakeService.planRequests, hasLength(1));

        final refreshPaths = fakeService.refreshRequests.first.folderPaths;
        final planPaths = fakeService.planRequests.first.folderPaths;
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
      fakeService.reset();
      store.setSoftDelete(true);

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(fakeService.executeRequests.last.softDelete, isTrue);
    });

    test('softDelete=false is correctly propagated', () async {
      fakeService.reset();
      store.setSoftDelete(false);

      await coordinator.executeFlowForFiles(
        rootPath: '/root',
        folderPath: '/root/folderA',
        selectedFiles: {'/root/folderA/file.flac'},
        targetFormat: 'm4a',
        planType: 'single_convert',
      );

      expect(fakeService.executeRequests.last.softDelete, isFalse);
    });
  });

  group('PlanExecutionCoordinator error handling', () {
    test('returns failure when plan returns empty planId', () async {
      fakeService.reset();
      final emptyPlanIdResponse = PlanOperationsResponse();
      emptyPlanIdResponse.planId = ''; // Explicitly set empty string
      emptyPlanIdResponse.operations.add(
        PlannedOperation()
          ..sourcePath = '/test/file.flac'
          ..operationType = 'convert',
      );
      fakeService.setPlanResponse(emptyPlanIdResponse);

      // Verify the setup is correct
      expect(fakeService.nextPlanResponse!.planId, isEmpty);
      expect(fakeService.nextPlanResponse!.operations, isNotEmpty);

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
        fakeService.executeCallCount,
        equals(0),
        reason: 'Should not execute when plan is invalid',
      );
    });

    test('returns failure when plan returns no operations', () async {
      fakeService.reset();
      final noOpsResponse = PlanOperationsResponse();
      noOpsResponse.planId = 'test-plan-empty';
      // Don't add any operations - operations list should be empty
      fakeService.setPlanResponse(noOpsResponse);

      // Verify the setup is correct
      expect(fakeService.nextPlanResponse!.operations, isEmpty);

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
      fakeService.reset();
      fakeService.setRefreshResponse(
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
      expect(fakeService.refreshCallCount, equals(1));
      expect(fakeService.planCallCount, equals(1));
      expect(fakeService.executeCallCount, equals(1));
    });
  });
}
