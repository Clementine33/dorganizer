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

  group('Task 6: slim/prune planning requires selected folders', () {
    late List<PlanOperationsRequest> planRequests;
    late PlanOperationsResponse planResponse;

    setUp(() {
      planRequests = [];
      planResponse = PlanOperationsResponse()
        ..planId = 'test-plan-folders-required'
        ..operations.add(
          PlannedOperation()
            ..sourcePath = '/test/file.mp3'
            ..operationType = 'slim',
        );
    });

    Future<void> pumpPanelWithFilesOnly(WidgetTester tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: WorkflowPanelWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders: const {}, // No folders
              selectedFiles: {
                '/test/file1.mp3',
                '/test/file2.flac',
              }, // Only files
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

    testWidgets(
      'slim planning does NOT call plan RPC when only files selected',
      (tester) async {
        await pumpPanelWithFilesOnly(tester);

        final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
        final future = state.triggerSlim(SlimMode.modeI) as Future<void>;
        await tester.pump();
        // Dismiss confirmation if it appears (but it shouldn't)
        await tester.runAsync(() async => future);
        await tester.pump();

        // Should NOT have called plan RPC
        expect(planRequests, isEmpty);
      },
    );

    testWidgets('slim planning surfaces error log when only files selected', (
      tester,
    ) async {
      await pumpPanelWithFilesOnly(tester);

      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerSlim(SlimMode.modeI) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();

      // Should surface error in log
      expect(find.textContaining('folder'), findsWidgets);
      expect(state.slimLastError, isNotNull);
    });

    testWidgets(
      'prune planning does NOT call plan RPC when only files selected',
      (tester) async {
        await pumpPanelWithFilesOnly(tester);

        final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
        final future = state.triggerPrune(PruneMode.wavFlac) as Future<void>;
        await tester.pump();
        await tester.runAsync(() async => future);
        await tester.pump();

        // Should NOT have called plan RPC
        expect(planRequests, isEmpty);
      },
    );

    testWidgets('prune planning surfaces error log when only files selected', (
      tester,
    ) async {
      await pumpPanelWithFilesOnly(tester);

      final state = tester.state(find.byType(WorkflowPanelWidget)) as dynamic;
      final future = state.triggerPrune(PruneMode.wavFlac) as Future<void>;
      await tester.pump();
      await tester.runAsync(() async => future);
      await tester.pump();

      // Should surface error in log
      expect(find.textContaining('folder'), findsWidgets);
      expect(state.pruneLastError, isNotNull);
    });
  });
}
