import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/workflow_panel_widget.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

import 'workflow_panel_test_helpers.dart';

const Key _softDeleteCheckboxKey = Key('workflow_soft_delete_checkbox');

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

  group('Workflow panel unified operation controls', () {
    late List<PlanOperationsRequest> planRequests;
    late List<ExecutePlanRequest> executeRequests;
    late PlanOperationsResponse planResponse;

    setUp(() {
      planRequests = [];
      executeRequests = [];
      planResponse = PlanOperationsResponse()
        ..planId = 'default-plan-id'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/file.mp3'
            ..operationType = 'delete',
        );
    });

    Future<void> pumpPanel(
      WidgetTester tester, {
      WorkflowStateStore? workflowStateStore,
    }) async {
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
              executePlanOverride: (request) {
                executeRequests.add(request);
                return Stream.fromIterable([
                  JobEvent()
                    ..eventType = 'done'
                    ..message = 'ok',
                ]);
              },
              workflowStateStore: workflowStateStore,
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

    Future<void> planPrune(WidgetTester tester, PruneMode mode) async {
      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerPrune(mode) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();
    }

    Future<void> selectOperation(
      WidgetTester tester,
      WorkflowOperation operation,
    ) async {
      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      await (state.triggerSelectOperation(operation) as Future<void>);
      await tester.pump();
    }

    Future<void> tapPlan(WidgetTester tester) async {
      await tester.tap(find.widgetWithText(FilledButton, 'Plan'));
      await tester.pumpAndSettle();
    }

    Future<void> tapExecuteAndApply(WidgetTester tester) async {
      await tester.tap(find.widgetWithText(FilledButton, 'Execute'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Apply'));
      await tester.pumpAndSettle();
    }

    testWidgets('dropdown selection does not auto-generate plan', (
      tester,
    ) async {
      await pumpPanel(tester);

      await selectOperation(tester, WorkflowOperation.pruneWavFlac);

      expect(planRequests, isEmpty);
      expect(executeRequests, isEmpty);
    });

    testWidgets('Plan triggers planning for selected slim operation only', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-slim-only'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio1.mp3'
            ..operationType = 'slim',
        );

      await pumpPanel(tester);
      await selectOperation(tester, WorkflowOperation.slimModeI);
      await tapPlan(tester);

      expect(planRequests, isNotEmpty);
      expect(planRequests.last.planType, equals('slim'));
      expect(executeRequests, isEmpty);
    });

    testWidgets('Plan triggers planning for selected prune operation only', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-prune-only'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio2.flac'
            ..operationType = 'prune',
        );

      await pumpPanel(tester);
      await selectOperation(tester, WorkflowOperation.pruneWavFlac);
      await tapPlan(tester);

      expect(planRequests, isNotEmpty);
      expect(planRequests.last.planType, equals('prune'));
      expect(executeRequests, isEmpty);
    });

    testWidgets('Execute uses slim planId for slim selections', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-slim-plan-123'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio1.mp3'
            ..operationType = 'slim',
        );

      await pumpPanel(tester);
      await planSlim(tester, SlimMode.modeI);

      executeRequests.clear();
      await selectOperation(tester, WorkflowOperation.slimModeII);
      await tapExecuteAndApply(tester);

      expect(executeRequests, hasLength(1));
      expect(executeRequests.single.planId, equals('test-slim-plan-123'));
    });

    testWidgets('Execute uses prune planId for prune selections', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-prune-plan-456'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio2.flac'
            ..operationType = 'prune',
        );

      await pumpPanel(tester);
      await planPrune(tester, PruneMode.wavFlac);

      executeRequests.clear();
      await selectOperation(tester, WorkflowOperation.pruneBoth);
      await tester.tap(find.widgetWithText(FilledButton, 'Execute'));
      await tester.pumpAndSettle();
      expect(find.text('Implement test-prune-plan-456 ?'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Apply'));
      await tester.pumpAndSettle();

      expect(executeRequests, hasLength(1));
      expect(executeRequests.single.planId, equals('test-prune-plan-456'));
    });

    testWidgets('Execute disabled when selected operation has no plan', (
      tester,
    ) async {
      await pumpPanel(tester);

      final execute = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, 'Execute'),
      );
      expect(execute.onPressed, isNull);
    });

    testWidgets('Execute always targets latest planned operation label', (
      tester,
    ) async {
      await pumpPanel(tester);

      planResponse = PlanOperationsResponse()
        ..planId = 'slim-plan-1'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio1.mp3'
            ..operationType = 'slim',
        );
      await planSlim(tester, SlimMode.modeI);

      await selectOperation(tester, WorkflowOperation.pruneBoth);
      var execute = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, 'Execute'),
      );
      expect(execute.onPressed, isNotNull);

      planResponse = PlanOperationsResponse()
        ..planId = 'prune-plan-2'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio2.flac'
            ..operationType = 'prune',
        );
      await tapPlan(tester);

      await selectOperation(tester, WorkflowOperation.slimModeI);
      executeRequests.clear();
      await tester.tap(find.widgetWithText(FilledButton, 'Execute'));
      await tester.pumpAndSettle();
      expect(find.text('Implement prune-plan-2 ?'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Apply'));
      await tester.pumpAndSettle();

      expect(executeRequests, hasLength(1));
      expect(executeRequests.single.planId, equals('prune-plan-2'));
    });

    testWidgets(
      'unchecked checkbox passes softDelete=false to ExecutePlanRequest',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-softdelete'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/audio1.mp3'
              ..operationType = 'slim',
          );

        await pumpPanel(tester);
        await planSlim(tester, SlimMode.modeI);

        await tester.tap(find.byKey(_softDeleteCheckboxKey));
        await tester.pumpAndSettle();

        await tapExecuteAndApply(tester);

        expect(executeRequests, hasLength(1));
        expect(executeRequests.single.softDelete, isFalse);
      },
    );

    testWidgets('checkbox UI updates immediately when using shared store', (
      tester,
    ) async {
      final workflowStateStore = WorkflowStateStore(channel: channel);

      await pumpPanel(tester, workflowStateStore: workflowStateStore);

      expect(workflowStateStore.softDelete, isTrue);
      Checkbox checkbox = tester.widget(find.byKey(_softDeleteCheckboxKey));
      expect(checkbox.value, isTrue);

      await tester.tap(find.byKey(_softDeleteCheckboxKey));
      await tester.pump();

      expect(workflowStateStore.softDelete, isFalse);
      checkbox = tester.widget(find.byKey(_softDeleteCheckboxKey));
      expect(checkbox.value, isFalse);
    });

    testWidgets('default softDelete=true is passed to execute', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-softdelete-true'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio1.mp3'
            ..operationType = 'slim',
        );

      await pumpPanel(tester);
      await planSlim(tester, SlimMode.modeI);

      await tapExecuteAndApply(tester);

      expect(executeRequests, hasLength(1));
      expect(executeRequests.single.softDelete, isTrue);
    });

    testWidgets(
      'unchecked checkbox passes softDelete=false for prune execute',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-prune-softdelete'
          ..operations.add(
            PlannedOperation()
              ..sourcePath = '/test/audio2.flac'
              ..operationType = 'prune',
          );

        await pumpPanel(tester);
        await planPrune(tester, PruneMode.wavFlac);

        await tester.tap(find.byKey(_softDeleteCheckboxKey));
        await tester.pumpAndSettle();

        await selectOperation(tester, WorkflowOperation.pruneWavFlac);
        await tapExecuteAndApply(tester);

        expect(executeRequests, hasLength(1));
        expect(executeRequests.single.softDelete, isFalse);
      },
    );

    testWidgets('reset clears workflow state but keeps logs', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-reset-log'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/audio1.mp3'
            ..operationType = 'slim',
        );

      await pumpPanel(tester);
      await planSlim(tester, SlimMode.modeI);

      expect(find.textContaining('Plan created:'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, 'Reset'));
      await tester.pumpAndSettle();
      expect(find.textContaining('Plan created:'), findsOneWidget);

      final execute = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, 'Execute'),
      );
      expect(execute.onPressed, isNull);
    });

    testWidgets('resume action is absent', (tester) async {
      await pumpPanel(tester);
      expect(find.text('Resume'), findsNothing);
      expect(find.text('Resume Plan'), findsNothing);
    });

    testWidgets('workflow status area shows plan-only status text', (
      tester,
    ) async {
      await pumpPanel(tester);

      expect(find.text('No plan yet'), findsOneWidget);
      expect(find.textContaining('folders •'), findsNothing);
      expect(find.textContaining('files'), findsNothing);
    });
  });

  testWidgets('workflow panel exposes unified operation dropdown', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: WorkflowPanelWidget(
            channel: channel,
            selectedFolder: null,
            selectedFolders: const {},
            selectedFiles: const {},
          ),
        ),
      ),
    );

    expect(
      find.byType(DropdownButtonFormField<WorkflowOperation>),
      findsOneWidget,
    );
    expect(find.text('Operation'), findsOneWidget);
  });

  testWidgets('workflow panel does not overflow at narrow width', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 240,
            child: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders: const {},
              selectedFiles: const {},
            ),
          ),
        ),
      ),
    );

    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });
}
