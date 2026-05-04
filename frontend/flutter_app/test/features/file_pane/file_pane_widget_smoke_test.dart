import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/files/file_pane_widget.dart';

import 'file_pane_test_harness.dart';

void main() {
  late FilePaneTestHarness harness;

  setUp(() async {
    harness = await createFilePaneTestHarness();
  });

  tearDown(() async {
    await harness.tearDown();
  });

  testWidgets('file pane shows single-directory action area', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: FilePaneWidget(
            channel: harness.channel,
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
}
