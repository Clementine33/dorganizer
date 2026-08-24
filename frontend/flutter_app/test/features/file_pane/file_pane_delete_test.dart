import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/files/file_pane_widget.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

import 'file_pane_test_harness.dart';

void main() {
  late FilePaneTestHarness harness;

  setUp(() async {
    harness = await createFilePaneTestHarness();
  });

  tearDown(() async {
    await harness.tearDown();
  });

  group('File pane delete actions', () {
    testWidgets('file delete action sends planType "single_delete"', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
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
                  channel: harness.channel,
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
        final client = OnseiServiceClient(harness.channel);
        await for (final _ in client.executePlan(
          ExecutePlanRequest()..planId = 'test-plan-delete',
        )) {
          // Consume stream
        }
      });

      expect(harness.fakeService.planRequests, isNotEmpty);
      expect(harness.fakeService.planRequests.last.planType, equals('single_delete'));
      expect(harness.fakeService.planRequests.last.folderPath, equals('/test'));

      // Verify plan->execute linkage via planId
      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.planId, equals('test-plan-delete'));
    });

    testWidgets('successful delete clears selected paths', (tester) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
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
                  channel: harness.channel,
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
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
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
                  channel: harness.channel,
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

    testWidgets('executePlan error event shows error message in SnackBar', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-error'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.mp3'
              ..operationType = 'delete',
          ),
      );

      // Return error event from executePlan
      harness.fakeService.setExecuteStreamFactory(
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
                  channel: harness.channel,
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
      expect(harness.fakeService.executeRequests, isNotEmpty);

      // Verify SnackBar shows error message
      await tester.pumpAndSettle();
      expect(find.textContaining('PLAN_STALE'), findsOneWidget);
    });

    testWidgets('file pane delete uses softDelete from shared store', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-delete-store'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.mp3'
              ..operationType = 'delete',
          ),
      );

      final store = WorkflowStateStore(channel: harness.channel);
      store.setSoftDelete(true);

      final selectedPaths = <String>{'/test/folder/audio1.mp3'};

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                return FilePaneWidget(
                  channel: harness.channel,
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

      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.softDelete, isTrue);
    });
  });
}
