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

  group('File pane convert actions', () {
    testWidgets('successful convert clears selected paths', (tester) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-clear'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

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
        final future = state.triggerConvert('m4a');
        await tester.pump();
        await future;
      });

      expect(selectedPaths, isEmpty);
    });

    testWidgets('file convert action uses configured encoder target', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

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
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      expect(harness.fakeService.planRequests, isNotEmpty);
      expect(harness.fakeService.planRequests.last.planType, equals('single_convert'));
      expect(harness.fakeService.planRequests.last.folderPath, equals('/test'));
      expect(harness.fakeService.planRequests.last.targetFormat, equals('m4a'));
      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.softDelete, isTrue);
    });

    testWidgets('file convert rejects non-lossless source formats', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

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
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      expect(harness.fakeService.planRequests, isEmpty);
      expect(harness.fakeService.executeRequests, isEmpty);
    });

    testWidgets('file pane convert uses softDelete from shared store', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-store'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: harness.channel);
      store.setSoftDelete(true);

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

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
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.softDelete, isTrue);
    });

    testWidgets('softDelete=false from shared store is passed to execute', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-softdelete-false'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: harness.channel);
      store.setSoftDelete(false);

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

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
        final future = state.triggerConvert('m4a');
        await tester.pump();

        await future;
      });

      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.softDelete, isFalse);
    });

    testWidgets('changing store softDelete is observed by file pane', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setPlanResponse(
        PlanOperationsResponse()
          ..planId = 'test-plan-convert-observed'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/folder/audio1.flac'
              ..targetPath = '/test/folder/audio1.m4a'
              ..operationType = 'convert',
          ),
      );
      harness.fakeService.setConfigJson('{"tools":{"encoder":"qaac"}}');

      final store = WorkflowStateStore(channel: harness.channel);

      final selectedPaths = <String>{'/test/folder/audio1.flac'};

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

      expect(harness.fakeService.executeRequests, isNotEmpty);
      expect(harness.fakeService.executeRequests.last.softDelete, isTrue);
    });
  });
}
