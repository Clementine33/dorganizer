import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/files/file_pane_widget.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Fake OnseiService that records incoming requests for testing.
/// Provides stub responses for PlanOperations, ListFiles, and ExecutePlan.
/// Uses default files so they're available immediately.
class FakeOnseiService extends OnseiServiceBase {
  final List<PlanOperationsRequest> planRequests = [];
  final List<ExecutePlanRequest> executeRequests = [];
  final List<RefreshFoldersRequest> refreshRequests = [];
  PlanOperationsResponse? nextPlanResponse;
  List<String> nextFiles = [];
  Stream<JobEvent> Function()? nextExecuteStreamFactory;
  String? nextConfigJson;

  // Call counters for coordinator testing
  int refreshCallCount = 0;
  int planCallCount = 0;
  int executeCallCount = 0;

  // Default files to return when listFiles is called
  final List<String> defaultFiles = [
    '/test/folder/audio1.mp3',
    '/test/folder/audio2.flac',
  ];

  void reset() {
    planRequests.clear();
    executeRequests.clear();
    refreshRequests.clear();
    nextPlanResponse = null;
    nextFiles = [];
    nextExecuteStreamFactory = null;
    nextConfigJson = null;
    refreshCallCount = 0;
    planCallCount = 0;
    executeCallCount = 0;
  }

  void setPlanResponse(PlanOperationsResponse response) {
    nextPlanResponse = response;
  }

  void setFiles(List<String> files) {
    nextFiles = files;
  }

  void setExecuteStreamFactory(Stream<JobEvent> Function() factory) {
    nextExecuteStreamFactory = factory;
  }

  void setConfigJson(String configJson) {
    nextConfigJson = configJson;
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
    final files = nextFiles.isNotEmpty ? nextFiles : defaultFiles;
    return ListFilesResponse()..files.addAll(files);
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
          ..sourcePath = '/test/file.mp3'
          ..targetPath = '/test/output.mp3'
          ..operationType = 'convert',
      );
  }

  @override
  Stream<JobEvent> executePlan(ServiceCall call, ExecutePlanRequest request) {
    executeRequests.add(request);
    executeCallCount++;
    if (nextExecuteStreamFactory != null) {
      return nextExecuteStreamFactory!();
    }
    return Stream.fromIterable([
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
    return ListPlansResponse();
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    ServiceCall call,
    RefreshFoldersRequest request,
  ) async {
    refreshRequests.add(request);
    refreshCallCount++;
    return RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

/// Creates a test Server that runs the fake service.
/// Returns the server and the FakeOnseiService instance.
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

  setUp(() async {
    final (s, f) = await createTestServer();
    server = s;
    fakeService = f;
    channel = ClientChannel(
      'localhost',
      port: server.port as int,
      options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
    );
  });

  tearDown(() async {
    await channel.shutdown();
    await server.shutdown();
  });

  testWidgets('file pane shows single-directory action area', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: FilePaneWidget(
            channel: channel,
            rootPath: '/test',
            folderPath: null,
            selectedPaths: const {},
            onSelectionChanged: (_) {},
          ),
        ),
      ),
    );

    expect(find.text('Open Folder'), findsOneWidget);
    expect(find.text('Rescan'), findsAtLeastNWidgets(1));
    expect(find.text('Delete'), findsOneWidget);
    expect(find.text('Convert'), findsOneWidget);
  });

  testWidgets('file delete action sends planType "single_delete"', (
    tester,
  ) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-delete'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.mp3'
            ..operationType = 'delete',
        ),
    );

    final selectedPaths = <String>{'/test/folder/audio1.mp3'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerDelete();
      await tester.pump();

      await future;

      // After planning, also trigger execute to verify plan->execute linkage
      final client = OnseiServiceClient(channel);
      await for (final _ in client.executePlan(
        ExecutePlanRequest()..planId = 'test-plan-delete',
      )) {
        // Consume stream
      }
    });

    expect(fakeService.planRequests, isNotEmpty);
    expect(fakeService.planRequests.last.planType, equals('single_delete'));
    expect(fakeService.planRequests.last.folderPath, equals('/test'));

    // Verify plan->execute linkage via planId
    expect(fakeService.executeRequests, isNotEmpty);
    expect(fakeService.executeRequests.last.planId, equals('test-plan-delete'));
  });

  testWidgets('successful delete clears selected paths', (tester) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-delete-clear'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.mp3'
            ..operationType = 'delete',
        ),
    );

    final selectedPaths = <String>{'/test/folder/audio1.mp3'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerDelete();
      await tester.pump();
      await future;
    });

    expect(selectedPaths, isEmpty);
  });

  testWidgets('direct delete success clears loading state', (tester) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-delete-loading'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.mp3'
            ..operationType = 'delete',
        ),
    );

    final selectedPaths = <String>{'/test/folder/audio1.mp3'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      await state.triggerDelete();
    });
    await tester.pump();

    expect(find.byType(CircularProgressIndicator), findsNothing);
  });

  testWidgets('successful convert clears selected paths', (tester) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-convert-clear'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.flac'
            ..targetPath = '/test/folder/audio1.m4a'
            ..operationType = 'convert',
        ),
    );
    fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

    final selectedPaths = <String>{'/test/folder/audio1.flac'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerConvert('m4a');
      await tester.pump();
      await future;
    });

    expect(selectedPaths, isEmpty);
  });

  testWidgets('file convert action uses configured encoder target', (
    tester,
  ) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-convert'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.flac'
            ..targetPath = '/test/folder/audio1.m4a'
            ..operationType = 'convert',
        ),
    );
    fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

    final selectedPaths = <String>{'/test/folder/audio1.flac'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerConvert('m4a');
      await tester.pump();

      await future;
    });

    expect(fakeService.planRequests, isNotEmpty);
    expect(fakeService.planRequests.last.planType, equals('single_convert'));
    expect(fakeService.planRequests.last.folderPath, equals('/test'));
    expect(fakeService.planRequests.last.targetFormat, equals('m4a'));
    expect(fakeService.executeRequests, isNotEmpty);
    // Without a store, softDelete defaults to true
    expect(fakeService.executeRequests.last.softDelete, isTrue);
  });

  testWidgets('file convert rejects non-lossless source formats', (
    tester,
  ) async {
    fakeService.reset();
    fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

    final selectedPaths = <String>{'/test/folder/audio1.mp3'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerConvert('m4a');
      await tester.pump();

      await future;
    });

    expect(fakeService.planRequests, isEmpty);
    expect(fakeService.executeRequests, isEmpty);
  });

  // P0: Execute error should be visible to user via SnackBar
  testWidgets('executePlan error event shows error message in SnackBar', (
    tester,
  ) async {
    fakeService.reset();
    fakeService.setPlanResponse(
      PlanOperationsResponse()
        ..planId = 'test-plan-error'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/folder/audio1.mp3'
            ..operationType = 'delete',
        ),
    );

    // Return error event from executePlan
    fakeService.setExecuteStreamFactory(
      () => Stream.fromIterable([
        JobEvent()
          ..eventType = 'error'
          ..message = 'PLAN_STALE: plan expired',
      ]),
    );

    final selectedPaths = <String>{'/test/folder/audio1.mp3'};

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: channel,
                rootPath: '/test',
                folderPath: null,
                selectedPaths: selectedPaths,
                onSelectionChanged: (paths) {
                  setState(() {
                    selectedPaths.clear();
                    selectedPaths.addAll(paths);
                  });
                },
              );
            },
          ),
        ),
      ),
    );

    await tester.pump();
    await tester.runAsync(() async {
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      final future = state.triggerDelete();
      await tester.pump();

      await future;
    });

    // Verify error event was received
    expect(fakeService.executeRequests, isNotEmpty);

    // Verify SnackBar shows error message
    await tester.pumpAndSettle();
    expect(find.textContaining('PLAN_STALE'), findsOneWidget);
  });

  // =========================================================================
  // Task 2: Shared WorkflowStateStore for softDelete
  // =========================================================================

  group('Task 2: shared WorkflowStateStore softDelete', () {
    testWidgets('file pane convert uses softDelete from shared store', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-store'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: channel);
      store.setSoftDelete(true); // Set via shared store

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: null,
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      await tester.runAsync(() async {
        final widgetFinder = find.byType(FilePaneWidget);
        final state = tester.state(widgetFinder) as dynamic;

        // ignore: avoid_dynamic_calls
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      // Verify execute plan used softDelete from shared store
      expect(fakeService.executeRequests, isNotEmpty);
      expect(fakeService.executeRequests.last.softDelete, isTrue);
    });

    testWidgets('file pane delete uses softDelete from shared store', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-delete-store'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.mp3'
              ..operationType = 'delete',
          ),
      );

      final store = WorkflowStateStore(channel: channel);
      store.setSoftDelete(true); // Set via shared store

      final selectedPaths = <String>{'/test/folder/audio1.mp3'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: null,
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      await tester.runAsync(() async {
        final widgetFinder = find.byType(FilePaneWidget);
        final state = tester.state(widgetFinder) as dynamic;

        // ignore: avoid_dynamic_calls
        final future = state.triggerDelete();
        await tester.pump();

        await future;
      });

      // Verify execute plan used softDelete from shared store
      expect(fakeService.executeRequests, isNotEmpty);
      expect(fakeService.executeRequests.last.softDelete, isTrue);
    });

    testWidgets('softDelete=false from shared store is passed to execute', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-softdelete-false'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: channel);
      store.setSoftDelete(false);

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: null,
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      await tester.runAsync(() async {
        final widgetFinder = find.byType(FilePaneWidget);
        final state = tester.state(widgetFinder) as dynamic;

        // ignore: avoid_dynamic_calls
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      expect(fakeService.executeRequests, isNotEmpty);
      expect(fakeService.executeRequests.last.softDelete, isFalse);
    });

    testWidgets('changing store softDelete is observed by file pane', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-observed'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: channel);

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: null,
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();

      // Change softDelete via store after widget is built
      store.setSoftDelete(true);

      await tester.runAsync(() async {
        final widgetFinder = find.byType(FilePaneWidget);
        final state = tester.state(widgetFinder) as dynamic;

        // ignore: avoid_dynamic_calls
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      // Should use the updated softDelete value from store
      expect(fakeService.executeRequests, isNotEmpty);
      expect(fakeService.executeRequests.last.softDelete, isTrue);
    });
  });

  // =========================================================================
  // Task 3: PlanExecutionCoordinator integration
  // =========================================================================

  group('Task 3: PlanExecutionCoordinator integration', () {
    testWidgets(
      'coordinator delete failure clears loading state instead of hanging',
      (tester) async {
        fakeService.reset();
        fakeService.setPlanResponse(
          PlanOperationsResponse()..planId = 'coordinator-plan-delete-no-ops',
        );

        final store = WorkflowStateStore(channel: channel);
        final coordinator = PlanExecutionCoordinator(
          channel: channel,
          store: store,
        );

        final selectedPaths = <String>{'/test/folder/audio1.mp3'};

        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: StatefulBuilder(
                builder: (context, setState) {
                  return FilePaneWidget(
                    channel: channel,
                    rootPath: '/test',
                    folderPath: null,
                    selectedPaths: selectedPaths,
                    onSelectionChanged: (paths) {
                      setState(() {
                        selectedPaths.clear();
                        selectedPaths.addAll(paths);
                      });
                    },
                    workflowStateStore: store,
                    coordinator: coordinator,
                  );
                },
              ),
            ),
          ),
        );

        await tester.pump();

        await tester.tap(find.widgetWithText(TextButton, 'Delete').first);
        await tester.pump();

        final dialogDelete = find.descendant(
          of: find.byType(AlertDialog),
          matching: find.widgetWithText(TextButton, 'Delete'),
        );
        await tester.tap(dialogDelete);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 200));

        expect(find.byType(CircularProgressIndicator), findsNothing);
      },
    );

    testWidgets(
      'coordinator convert failure clears loading state instead of hanging',
      (tester) async {
        fakeService.reset();
        fakeService.setPlanResponse(
          PlanOperationsResponse()..planId = 'coordinator-plan-no-ops',
        );
        fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

        final store = WorkflowStateStore(channel: channel);
        final coordinator = PlanExecutionCoordinator(
          channel: channel,
          store: store,
        );

        final selectedPaths = <String>{'/test/folder/audio1.flac'};

        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: StatefulBuilder(
                builder: (context, setState) {
                  return FilePaneWidget(
                    channel: channel,
                    rootPath: '/test',
                    folderPath: null,
                    selectedPaths: selectedPaths,
                    onSelectionChanged: (paths) {
                      setState(() {
                        selectedPaths.clear();
                        selectedPaths.addAll(paths);
                      });
                    },
                    workflowStateStore: store,
                    coordinator: coordinator,
                  );
                },
              ),
            ),
          ),
        );

        await tester.pump();

        await tester.tap(find.widgetWithText(TextButton, 'Convert').first);
        await tester.pump();

        await tester.tap(find.widgetWithText(TextButton, 'Convert'));
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 200));

        expect(find.byType(CircularProgressIndicator), findsNothing);
      },
    );

    testWidgets('file pane uses coordinator for convert when provided', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'coordinator-plan-convert'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: channel);
      store.setSoftDelete(true);

      final coordinator = PlanExecutionCoordinator(
        channel: channel,
        store: store,
      );

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: '/test/folder',
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                  coordinator: coordinator,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      await state.triggerConvert('m4a');
      await tester.pumpAndSettle();

      // Verify coordinator flow was used: refresh -> plan -> execute
      expect(
        fakeService.refreshRequests,
        isNotEmpty,
        reason: 'Coordinator should call refreshFolders',
      );
      expect(
        fakeService.planRequests,
        isNotEmpty,
        reason: 'Coordinator should call planOperations',
      );
      expect(
        fakeService.executeRequests,
        isNotEmpty,
        reason: 'Coordinator should call executePlan',
      );

      // Verify softDelete from store
      expect(fakeService.executeRequests.last.softDelete, isTrue);
    }, skip: true);

    testWidgets('file pane uses coordinator for delete when provided', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'coordinator-plan-delete'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.mp3'
              ..operationType = 'delete',
          ),
      );

      final store = WorkflowStateStore(channel: channel);
      final coordinator = PlanExecutionCoordinator(
        channel: channel,
        store: store,
      );

      final selectedPaths = <String>{'/test/folder/audio1.mp3'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: '/test/folder',
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                  coordinator: coordinator,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      await state.triggerDelete();
      await tester.pumpAndSettle();

      // Verify coordinator flow
      expect(
        fakeService.refreshRequests,
        isNotEmpty,
        reason: 'Coordinator should call refreshFolders',
      );
      expect(fakeService.planRequests.last.planType, equals('single_delete'));
      expect(fakeService.executeRequests, isNotEmpty);
    }, skip: true);

    testWidgets('coordinator retry on PLAN_STALE refreshes and replans', (
      tester,
    ) async {
      fakeService.reset();

      // Execute returns PLAN_STALE
      fakeService.setExecuteStreamFactory(
        () => Stream.fromIterable([
          JobEvent()
            ..eventType = 'error'
            ..code = 'PLAN_STALE'
            ..message = 'Plan expired',
        ]),
      );

      final store = WorkflowStateStore(channel: channel);
      final coordinator = PlanExecutionCoordinator(
        channel: channel,
        store: store,
      );

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: '/test/folder',
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                  coordinator: coordinator,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      await state.triggerConvert('m4a');
      await tester.pumpAndSettle();

      // Stale triggers retry: refresh + plan + execute twice
      expect(
        fakeService.refreshCallCount,
        equals(2),
        reason: 'Should refresh twice on stale retry',
      );
      expect(
        fakeService.planCallCount,
        equals(2),
        reason: 'Should plan twice on stale retry',
      );
    }, skip: true);

    testWidgets('convert still validates lossless source with coordinator', (
      tester,
    ) async {
      fakeService.reset();
      fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: channel);
      final coordinator = PlanExecutionCoordinator(
        channel: channel,
        store: store,
      );

      // Select a non-lossless file (mp3)
      final selectedPaths = <String>{'/test/folder/audio1.mp3'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: channel,
                  rootPath: '/test',
                  folderPath: '/test/folder',
                  selectedPaths: selectedPaths,
                  onSelectionChanged: (paths) {
                    setState(() {
                      selectedPaths.clear();
                      selectedPaths.addAll(paths);
                    });
                  },
                  workflowStateStore: store,
                  coordinator: coordinator,
                );
              },
            ),
          ),
        ),
      );

      await tester.pump();
      final widgetFinder = find.byType(FilePaneWidget);
      final state = tester.state(widgetFinder) as dynamic;

      // ignore: avoid_dynamic_calls
      await state.triggerConvert('m4a');
      await tester.pumpAndSettle();

      // Should NOT call coordinator because validation fails first
      expect(
        fakeService.planRequests,
        isEmpty,
        reason: 'Should not plan for non-lossless source',
      );
      expect(
        fakeService.executeRequests,
        isEmpty,
        reason: 'Should not execute for non-lossless source',
      );
    }, skip: true);
  });
}
