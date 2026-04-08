import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/folders/folder_error_state.dart';
import 'package:onsei_organizer/main.dart';

void main() {
  testWidgets('waits for onShutdown before allowing app exit', (tester) async {
    var shutdownCalls = 0;
    final shutdownCompleter = Completer<void>();

    final channel = ClientChannel(
      'localhost',
      port: 1,
      options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
    );

    await tester.pumpWidget(
      OnseiApp(
        channel: channel,
        onShutdown: () async {
          shutdownCalls++;
          await shutdownCompleter.future;
        },
      ),
    );

    final exitFuture = tester.binding.handleRequestAppExit();
    var exitCompleted = false;
    exitFuture.then((_) {
      exitCompleted = true;
    });
    await tester.pump();

    expect(shutdownCalls, 1);
    expect(exitCompleted, isFalse);

    shutdownCompleter.complete();
    final response = await exitFuture;
    expect(exitCompleted, isTrue);
    expect(response.toString().toLowerCase(), contains('exit'));
  });

  testWidgets('calls onShutdown when app is detached/disposed', (tester) async {
    var shutdownCalls = 0;

    final channel = ClientChannel(
      'localhost',
      port: 1,
      options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
    );

    await tester.pumpWidget(
      OnseiApp(
        channel: channel,
        onShutdown: () async {
          shutdownCalls++;
        },
      ),
    );

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.detached);
    await tester.pump();

    expect(shutdownCalls, 1);

    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();

    expect(shutdownCalls, 1);
  });

  // =========================================================================
  // Task 5: Root switch clears error state
  // =========================================================================

  testWidgets(
    'root switch clears previous root error state (prototype behavior)',
    (tester) async {
      final channel = ClientChannel(
        'localhost',
        port: 1,
        options: const ChannelOptions(
          credentials: ChannelCredentials.insecure(),
        ),
      );

      await tester.pumpWidget(
        OnseiApp(channel: channel, onShutdown: () async {}),
      );

      await tester.pump();

      final dynamic appState = tester.state(find.byType(OnseiApp));
      appState.setErrorStateMapForTesting({
        '/root1/folderA': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error in root1',
          updatedAt: DateTime.now(),
        ),
      });

      // Verify initial error state exists
      expect(
        (appState.errorStateMapForTesting as Map).length,
        1,
        reason: 'Initial error state should have 1 entry',
      );

      // Simulate root switch using production handler
      appState.handleRootSelected('/root2');
      await tester.pump();

      // After root switch, root-scoped state should be cleared
      expect(
        (appState.errorStateMapForTesting as Map).isEmpty,
        isTrue,
        reason:
            'Error state should be cleared on root switch (prototype behavior)',
      );

      expect(
        (appState.selectedFoldersForTesting as Set).isEmpty,
        isTrue,
        reason: 'Folder selections should be cleared on root switch',
      );

      expect(
        (appState.selectedFilesForTesting as Set).isEmpty,
        isTrue,
        reason: 'File selections should be cleared on root switch',
      );

      expect(
        appState.showErrorViewForTesting,
        isFalse,
        reason: 'Error View toggle should reset on root switch',
      );
    },
  );
}
