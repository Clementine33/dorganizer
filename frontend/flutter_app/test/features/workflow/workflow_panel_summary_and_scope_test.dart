import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/workflow_panel_widget.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

import 'workflow_panel_test_helpers.dart';

void main() {
  late Server server;
  late ClientChannel channel;

  setUp(() async {
    server = await createTestServer();
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

  group('Task 7: summary text includes actionable vs total/keep breakdown', () {
    late List<PlanOperationsRequest> planRequests;
    late PlanOperationsResponse planResponse;

    setUp(() {
      planRequests = [];
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-summary-breakdown'
        ..operations.addAll([
          PlannedOperation()
            ..sourcePath = '/test/file1.mp3'
            ..operationType = 'slim',
          PlannedOperation()
            ..sourcePath = '/test/file2.flac'
            ..operationType = 'slim',
        ]);
    });

    Future<void> pumpPanel(WidgetTester tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: '/test/folder',
              selectedFolders: {'/test/folder'},
              selectedFiles: const {},
              planOperationsOverride: (request) async {
                planRequests.add(request);
                return planResponse;
              },
            ),
          ),
        ),
      );
      await tester.pump();
    }

    Future<void> planSlim(WidgetTester tester, SlimMode mode) async {
      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerSlim(mode) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();
    }

    testWidgets(
      'summary shows convert/delete/total breakdown when response has breakdown fields',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-actionable'
          ..operations.addAll([
            PlannedOperation()
              ..sourcePath = '/test/file1.mp3'
              ..operationType = 'convert',
            PlannedOperation()
              ..sourcePath = '/test/file2.flac'
              ..operationType = 'delete',
            PlannedOperation()
              ..sourcePath = '/test/file3.m4a'
              ..operationType = 'convert_and_delete',
          ])
          ..totalCount = 10
          ..actionableCount = 3
          ..summaryReason = 'ACTIONABLE';

        await pumpPanel(tester);
        await planSlim(tester, SlimMode.modeI);

        final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
        expect(state.slimSummary, equals('2 convert / 1 delete / 10 total'));
      },
    );

    testWidgets(
      'log message includes convert/delete/total breakdown when response has summary fields',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-log-breakdown'
          ..operations.addAll([
            PlannedOperation()
              ..sourcePath = '/test/file1.mp3'
              ..operationType = 'convert',
            PlannedOperation()
              ..sourcePath = '/test/file2.flac'
              ..operationType = 'delete',
          ])
          ..totalCount = 10
          ..actionableCount = 2
          ..summaryReason = 'ACTIONABLE';

        await pumpPanel(tester);
        await planSlim(tester, SlimMode.modeI);

        expect(
          find.textContaining('1 convert / 1 delete / 10 total'),
          findsWidgets,
        );
      },
    );

    testWidgets(
      'summary falls back to operation count when breakdown fields absent',
      (tester) async {
        // Response without new summary fields
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-no-breakdown'
          ..operations.addAll([
            PlannedOperation()
              ..sourcePath = '/test/file1.mp3'
              ..operationType = 'slim',
          ]);

        await pumpPanel(tester);
        await planSlim(tester, SlimMode.modeI);

        // Should still work with just operation count
        final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
        expect(state.slimSummary, contains('1'));
      },
    );
  });

  group('Task 5: plan-only dropdown + separate execute buttons', () {
    late List<ExecutePlanRequest> executeRequests;

    setUp(() {
      executeRequests = [];
    });

    Future<void> planSlim(WidgetTester tester, SlimMode mode) async {
      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerSlim(mode) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();
    }

    Future<void> tapExecuteAndApply(WidgetTester tester) async {
      await tester.tap(find.widgetWithText(FilledButton, 'Execute'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Apply'));
      await tester.pumpAndSettle();
    }

    testWidgets('coordinator execute uses existing planId only', (
      tester,
    ) async {
      final store = WorkflowStateStore(channel: channel);
      final spyCoordinator = SpyPlanExecutionCoordinator(
        channel: channel,
        store: store,
      );

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: '/test/folder',
              selectedFolders: {'/test/folder'},
              selectedFiles: const {},
              executePlanOverride: (request) {
                executeRequests.add(request);
                return Stream.fromIterable([
                  JobEvent()
                    ..eventType = 'done'
                    ..message = 'ok',
                ]);
              },
              workflowStateStore: store,
              coordinator: spyCoordinator,
            ),
          ),
        ),
      );
      await tester.pump();

      await planSlim(tester, SlimMode.modeI);

      await tapExecuteAndApply(tester);

      expect(spyCoordinator.planOnlyForFoldersCalls, equals(1));
      expect(spyCoordinator.executeByPlanIdCalls, equals(1));
      expect(spyCoordinator.executeFlowForFoldersCalls, equals(0));
      expect(executeRequests, isEmpty);
    });

    testWidgets(
      'coordinator execute validates against selectedRoot scope before selectedFolder',
      (tester) async {
        final store = WorkflowStateStore(channel: channel);
        final spyCoordinator = SpyPlanExecutionCoordinator(
          channel: channel,
          store: store,
        );

        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: WorkflowPanelWidget(
                channel: channel,
                selectedFolder: '/root/folderA',
                selectedFolders: {'/root/folderA'},
                selectedFiles: const {},
                selectedRoot: '/root',
                workflowStateStore: store,
                coordinator: spyCoordinator,
              ),
            ),
          ),
        );
        await tester.pump();

        await planSlim(tester, SlimMode.modeI);

        await tapExecuteAndApply(tester);

        expect(spyCoordinator.executeByPlanIdCalls, equals(1));
        expect(
          spyCoordinator.lastRootPathForValidation,
          equals('/root'), // selectedRoot takes priority over selectedFolder
        );
      },
    );
  });
}
