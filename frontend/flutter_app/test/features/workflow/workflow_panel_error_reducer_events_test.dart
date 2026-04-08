import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/workflow_panel_widget.dart';
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

  group('Task 6: plan_errors and successful_folders handling', () {
    late List<PlanOperationsRequest> planRequests;
    late PlanOperationsResponse planResponse;
    late List<String> actionOrder;
    Set<String>? clearedPlanErrorFolders;
    Set<String>? appliedPlanErrorFolders;

    setUp(() {
      planRequests = [];
      planResponse = PlanOperationsResponse()..planId = 'test-plan-task6';
      actionOrder = [];
      clearedPlanErrorFolders = null;
      appliedPlanErrorFolders = null;
    });

    Future<void> pumpPanel(
      WidgetTester tester, {
      String? selectedRoot,
      Set<String>? selectedFolders,
      bool showErrorView = true,
    }) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders:
                  selectedFolders ?? {'/root/folderA', '/root/folderB'},
              selectedFiles: const {},
              selectedRoot: selectedRoot ?? '/root',
              showErrorView: showErrorView,
              planOperationsOverride: (request) async {
                actionOrder.add('plan');
                planRequests.add(request);
                return planResponse;
              },
              executePlanOverride: (request) {
                return Stream.fromIterable([
                  JobEvent()
                    ..eventType = 'done'
                    ..message = 'ok',
                ]);
              },
              onClearPlanErrorForFolders: (folders) {
                actionOrder.add('clear-plan');
                clearedPlanErrorFolders = folders;
              },
              onClearExecuteErrorForFolders: (folders) {},
              onApplyPlanErrorToFolders: (folders, eventId, code, message) {
                appliedPlanErrorFolders = folders;
              },
              onApplyExecuteErrorToFolders:
                  (folders, eventId, code, message) {},
            ),
          ),
        ),
      );
      await tester.pump();
    }

    Future<void> planSlim(WidgetTester tester) async {
      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerSlim(SlimMode.modeI) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();
    }

    testWidgets(
      'clears planHasError for selected folders before plan request',
      (tester) async {
        await pumpPanel(tester);

        await planSlim(tester);

        expect(clearedPlanErrorFolders, isNotNull);
        expect(clearedPlanErrorFolders!, contains('/root/folderA'));
        expect(clearedPlanErrorFolders!, contains('/root/folderB'));
        expect(actionOrder, isNotEmpty);
        expect(actionOrder.first, 'clear-plan');
        expect(actionOrder, contains('plan'));
        expect(
          actionOrder.indexOf('clear-plan'),
          lessThan(actionOrder.indexOf('plan')),
        );
      },
    );

    testWidgets('does not clear planHasError when error view is hidden', (
      tester,
    ) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders: {'/root/folderA', '/root/folderB'},
              selectedFiles: const {},
              selectedRoot: '/root',
              showErrorView: false,
              planOperationsOverride: (request) async {
                return planResponse;
              },
              executePlanOverride: (request) {
                return Stream.fromIterable([JobEvent()..eventType = 'done']);
              },
              onClearPlanErrorForFolders: (folders) {
                clearedPlanErrorFolders = folders;
              },
            ),
          ),
        ),
      );
      await tester.pump();

      await planSlim(tester);

      expect(clearedPlanErrorFolders, isNull);
    });

    testWidgets(
      'applies plan_errors only when folder_path non-empty and root_path matches',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-errors'
          ..planErrors.addAll([
            FolderError()
              ..eventId = 'evt-1'
              ..folderPath = '/root/folderA'
              ..rootPath = '/root'
              ..stage = 'plan'
              ..code = 'ERR001'
              ..message = 'Planning failed',
          ]);

        await pumpPanel(tester, selectedRoot: '/root');
        await planSlim(tester);

        expect(appliedPlanErrorFolders, isNotNull);
        expect(appliedPlanErrorFolders!, contains('/root/folderA'));
      },
    );

    testWidgets('ignores plan_error with empty folder_path', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-empty-folder'
        ..planErrors.addAll([
          FolderError()
            ..eventId = 'evt-2'
            ..folderPath = ''
            ..rootPath = '/root'
            ..stage = 'plan',
        ]);

      await pumpPanel(tester, selectedRoot: '/root');
      await planSlim(tester);

      expect(appliedPlanErrorFolders, isNull);
    });

    testWidgets('ignores plan_error with mismatched root_path', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-wrong-root'
        ..planErrors.addAll([
          FolderError()
            ..eventId = 'evt-3'
            ..folderPath = '/other/folderA'
            ..rootPath =
                '/other' // Different from selected root
            ..stage = 'plan',
        ]);

      await pumpPanel(tester, selectedRoot: '/root');
      await planSlim(tester);

      expect(appliedPlanErrorFolders, isNull);
    });

    testWidgets('treats equivalent UNC roots as same root', (tester) async {
      const selectedRoot = r'\\192.168.0.32\Media\onsei\archive\1\14';
      const selectedFolder = r'\\192.168.0.32\Media\onsei\archive\1\14\A';

      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-unc-root-equivalent'
        ..planErrors.addAll([
          FolderError()
            ..eventId = 'evt-unc-1'
            ..folderPath = '//192.168.0.32/Media/onsei/archive/1/14/A'
            ..rootPath = '//192.168.0.32/Media/onsei/archive/1/14'
            ..stage = 'plan'
            ..code = 'ERR_UNC'
            ..message = 'UNC equivalent root should match',
        ]);

      await pumpPanel(
        tester,
        selectedRoot: selectedRoot,
        selectedFolders: {selectedFolder},
      );
      await planSlim(tester);

      expect(appliedPlanErrorFolders, isNotNull);
      expect(
        appliedPlanErrorFolders!.single.replaceAll('\\', '/'),
        endsWith('/a'),
      );
    });

    testWidgets('clears planHasError for successful_folders', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-success'
        ..successfulFolders.addAll(['/root/folderA', '/root/folderB']);

      await pumpPanel(tester, selectedRoot: '/root');
      await planSlim(tester);

      expect(clearedPlanErrorFolders, isNotNull);
      expect(clearedPlanErrorFolders!, contains('/root/folderA'));
      expect(clearedPlanErrorFolders!, contains('/root/folderB'));
    });

    testWidgets(
      'handles 0-op success (empty operations) in successful_folders',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-0op'
          ..operations.clear()
          ..successfulFolders.addAll(['/root/folderA']);

        await pumpPanel(tester, selectedRoot: '/root');
        await planSlim(tester);

        expect(clearedPlanErrorFolders, contains('/root/folderA'));
      },
    );

    testWidgets('ignores plan_error with missing event_id', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-no-eventid'
        ..planErrors.addAll([
          FolderError()
            ..folderPath = '/root/folderA'
            ..rootPath = '/root'
            ..stage = 'plan'
            ..code = 'ERR'
            ..message = 'No event ID',
        ]);

      await pumpPanel(tester, selectedRoot: '/root');
      await planSlim(tester);

      expect(appliedPlanErrorFolders, isNull);
    });

    testWidgets('handles missing structured fields gracefully', (tester) async {
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-mixed-version';

      await pumpPanel(tester, selectedRoot: '/root');
      await planSlim(tester);

      expect(clearedPlanErrorFolders, isNotNull);
    });
  });

  group('Task 6: execute stream event handling', () {
    late List<ExecutePlanRequest> executeRequests;
    late PlanOperationsResponse planResponse;
    Set<String>? clearedExecuteErrorFolders;
    Set<String>? appliedExecuteErrorFolders;
    int clearExecuteCalls = 0;
    int applyExecuteCalls = 0;
    late void Function(String root) switchRoot;

    setUp(() {
      executeRequests = [];
      planResponse = PlanOperationsResponse()..planId = 'test-plan-exec';
      clearedExecuteErrorFolders = null;
      appliedExecuteErrorFolders = null;
      clearExecuteCalls = 0;
      applyExecuteCalls = 0;
      switchRoot = (_) {};
    });

    Future<void> pumpPanel(
      WidgetTester tester, {
      String? selectedRoot,
      Set<String>? selectedFolders,
      Stream<JobEvent>? executeStream,
    }) async {
      var selectedRootState = selectedRoot ?? '/root';

      await tester.pumpWidget(
        MaterialApp(
          home: StatefulBuilder(
            builder: (context, setParentState) {
              switchRoot = (root) {
                setParentState(() => selectedRootState = root);
              };

              return Scaffold(
                body: WorkflowPanelWidget(
                  channel: channel,
                  selectedFolder: null,
                  selectedFolders: selectedFolders ?? {'/root/folderA'},
                  selectedFiles: const {},
                  selectedRoot: selectedRootState,
                  planOperationsOverride: (request) async {
                    return planResponse;
                  },
                  executePlanOverride: (request) {
                    executeRequests.add(request);
                    return executeStream ??
                        Stream.fromIterable([
                          JobEvent()
                            ..eventType = 'done'
                            ..message = 'ok',
                        ]);
                  },
                  onClearPlanErrorForFolders: (folders) {},
                  onClearExecuteErrorForFolders: (folders) {
                    clearExecuteCalls += 1;
                    clearedExecuteErrorFolders = folders;
                  },
                  onApplyExecuteErrorToFolders:
                      (folders, eventId, code, message) {
                        applyExecuteCalls += 1;
                        appliedExecuteErrorFolders = folders;
                      },
                ),
              );
            },
          ),
        ),
      );
      await tester.pump();
    }

    Future<void> planAndExecute(
      WidgetTester tester, {
      Stream<JobEvent>? executeStream,
    }) async {
      var state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      var future = state.triggerSlim(SlimMode.modeI) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();

      clearedExecuteErrorFolders = null;
      appliedExecuteErrorFolders = null;

      await tester.tap(find.widgetWithText(FilledButton, 'Execute'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Apply'));
      await tester.pumpAndSettle();
    }

    testWidgets('execute error with stage=execute applies executeHasError', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()..planId = 'test-plan-exec-err';
      final executeStream = Stream.fromIterable([
        JobEvent()
          ..eventType = 'error'
          ..stage = 'execute'
          ..eventId = 'exec-evt-1'
          ..folderPath = '/root/folderA'
          ..rootPath = '/root'
          ..code = 'EXEC_ERR'
          ..message = 'Execution failed',
      ]);

      await pumpPanel(tester, executeStream: executeStream);
      await planAndExecute(tester);

      expect(appliedExecuteErrorFolders, isNotNull);
      expect(appliedExecuteErrorFolders!, contains('/root/folderA'));
    });

    testWidgets(
      'execute error with stage=plan does NOT affect executeHasError',
      (tester) async {
        planResponse = PlanOperationsResponse()
          ..planId = 'test-plan-stage-plan';
        final executeStream = Stream.fromIterable([
          JobEvent()
            ..eventType = 'error'
            ..stage = 'plan'
            ..eventId = 'exec-evt-2'
            ..folderPath = '/root/folderA'
            ..rootPath = '/root'
            ..code = 'PLAN_ERR',
        ]);

        await pumpPanel(tester, executeStream: executeStream);
        await planAndExecute(tester);

        expect(appliedExecuteErrorFolders, isNull);
      },
    );

    testWidgets(
      'folder_completed clears executeHasError without reducer log noise',
      (tester) async {
        planResponse = PlanOperationsResponse()..planId = 'test-plan-complete';
        final executeStream = Stream.fromIterable([
          JobEvent()
            ..eventType = 'folder_completed'
            ..eventId = 'exec-evt-3'
            ..folderPath = '/root/folderA'
            ..rootPath = '/root',
        ]);

        await pumpPanel(tester, executeStream: executeStream);
        await planAndExecute(tester);

        expect(clearedExecuteErrorFolders, isNotNull);
        expect(clearedExecuteErrorFolders!, contains('/root/folderA'));
        expect(find.textContaining('[REDUCER]'), findsNothing);
      },
    );

    testWidgets('duplicate event_id does not re-apply reducer', (tester) async {
      planResponse = PlanOperationsResponse()..planId = 'test-plan-dup';
      appliedExecuteErrorFolders = null;

      final executeStream = Stream.fromIterable([
        JobEvent()
          ..eventType = 'error'
          ..stage = 'execute'
          ..eventId = 'dup-evt-id'
          ..folderPath = '/root/folderA'
          ..rootPath = '/root'
          ..code = 'ERR1',
        JobEvent()
          ..eventType = 'error'
          ..stage = 'execute'
          ..eventId = 'dup-evt-id'
          ..folderPath = '/root/folderA'
          ..rootPath = '/root'
          ..code = 'ERR2',
      ]);

      await pumpPanel(tester, executeStream: executeStream);
      await planAndExecute(tester);

      expect(appliedExecuteErrorFolders, isNotNull);
      expect(applyExecuteCalls, 1);
    });

    testWidgets('execute event with missing event_id is ignored', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()..planId = 'test-plan-no-id';
      final executeStream = Stream.fromIterable([
        JobEvent()
          ..eventType = 'error'
          ..stage = 'execute'
          ..folderPath = '/root/folderA'
          ..rootPath = '/root'
          ..code = 'ERR',
      ]);

      await pumpPanel(tester, executeStream: executeStream);
      await planAndExecute(tester);

      expect(appliedExecuteErrorFolders, isNull);
    });

    testWidgets('old-root delayed execute event is ignored after root switch', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()..planId = 'test-plan-old-root';
      final executeStream = Stream.fromIterable([
        JobEvent()
          ..eventType = 'error'
          ..stage = 'execute'
          ..eventId = 'old-root-evt'
          ..folderPath = '/root/folderA'
          ..rootPath = '/root'
          ..code = 'ERR',
      ]);

      await pumpPanel(
        tester,
        selectedRoot: '/root',
        executeStream: executeStream,
      );

      switchRoot('/new-root');
      await tester.pump();

      await planAndExecute(tester);

      expect(appliedExecuteErrorFolders, isNull);
      expect(applyExecuteCalls, 0);
    });

    testWidgets('execute event with missing structured fields does not crash', (
      tester,
    ) async {
      planResponse = PlanOperationsResponse()..planId = 'test-plan-mixed';
      final executeStream = Stream.fromIterable([
        JobEvent()
          ..eventType = 'info'
          ..message = 'Started',
        JobEvent()
          ..eventType = 'done'
          ..message = 'Completed',
      ]);

      await pumpPanel(tester, executeStream: executeStream);
      await planAndExecute(tester);

      expect(tester.takeException(), isNull);
      expect(appliedExecuteErrorFolders, isNull);
      expect(clearedExecuteErrorFolders, isNull);
      expect(applyExecuteCalls, 0);
      expect(clearExecuteCalls, 0);
    });
  });
}
