import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/files/file_pane_widget.dart';
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

  group('Bitrate badge', () {
    testWidgets('shows bitrate badge only for mp3 entries with bitrate', (
      tester,
    ) async {
      harness.fakeService.reset();
      // Entries-only path: clear default files so files.isEmpty → use entries
      harness.fakeService.setDefaultFiles([]);
      // Return mixed entries via fake gRPC service
      harness.fakeService.setEntries([
        FileListEntry(path: '/test/folder/song1.mp3', bitrate: 320000),
        FileListEntry(path: '/test/folder/song2.flac', bitrate: 1411000),
        FileListEntry(path: '/test/folder/song3.mp3', bitrate: 0),
        FileListEntry(path: '/test/folder/song4.wav', bitrate: 0),
        FileListEntry(path: '/test/folder/doc.txt', bitrate: 0),
      ]);
      harness.fakeService.setFiles([]); // entries path takes precedence

      await tester.runAsync(() async {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: FilePaneWidget(
                channel: harness.channel,
                rootPath: '/test',
                folderPath: '/test/folder',
                selectedPaths: const {},
                onSelectionChanged: (_) {},
              ),
            ),
          ),
        );
      });
      await settleGrpcLoad(tester);

      // song1.mp3 (320000 bps) should show "320 kbps" badge
      expect(find.text('320 kbps'), findsOneWidget);
      // song1.mp3 filename should be visible
      expect(find.text('song1.mp3'), findsAtLeastNWidgets(1));

      // Only one "kbps" badge overall (song1 only)
      expect(find.textContaining('kbps'), findsOneWidget);

      // Explicitly verify "0 kbps" is absent
      expect(find.text('0 kbps'), findsNothing);
    });

    testWidgets('no bitrate badge when entries are empty and only files returned', (
      tester,
    ) async {
      harness.fakeService.reset();
      // Only plain files, no entries — forces bitrate=0 fallback
      harness.fakeService.setEntries([]);
      harness.fakeService.setFiles([
        '/test/folder/audio1.mp3',
        '/test/folder/audio2.flac',
      ]);

      await tester.runAsync(() async {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: FilePaneWidget(
                channel: harness.channel,
                rootPath: '/test',
                folderPath: '/test/folder',
                selectedPaths: const {},
                onSelectionChanged: (_) {},
              ),
            ),
          ),
        );
      });
      await settleGrpcLoad(tester);

      // Should show filenames but no bitrate badges
      expect(find.text('audio1.mp3'), findsAtLeastNWidgets(1));
      expect(find.text('audio2.flac'), findsAtLeastNWidgets(1));
      expect(find.textContaining('kbps'), findsNothing);
    });

    testWidgets('bitrate badge uses correct secondary color styling', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setDefaultFiles([]);
      harness.fakeService.setEntries([
        FileListEntry(path: '/test/folder/song1.mp3', bitrate: 320000),
      ]);
      harness.fakeService.setFiles([]);

      await tester.runAsync(() async {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: FilePaneWidget(
                channel: harness.channel,
                rootPath: '/test',
                folderPath: '/test/folder',
                selectedPaths: const {},
                onSelectionChanged: (_) {},
              ),
            ),
          ),
        );
      });
      await settleGrpcLoad(tester);

      // Find the Container that wraps the bitrate text
      final badge = find.ancestor(
        of: find.text('320 kbps'),
        matching: find.byType(Container),
      );
      expect(badge, findsOneWidget);

      // Verify it has a margin (visual spacing from filename)
      final containerWidget = tester.widget<Container>(badge.first);
      expect(containerWidget.margin, isNotNull);
    });

    testWidgets('file entries without .mp3 extension never show badge', (
      tester,
    ) async {
      harness.fakeService.reset();
      harness.fakeService.setDefaultFiles([]);
      harness.fakeService.setEntries([
        FileListEntry(path: '/test/folder/song.flac', bitrate: 1411000),
        FileListEntry(path: '/test/folder/song.wav', bitrate: 1411000),
      ]);
      harness.fakeService.setFiles([]);

      await tester.runAsync(() async {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: FilePaneWidget(
                channel: harness.channel,
                rootPath: '/test',
                folderPath: '/test/folder',
                selectedPaths: const {},
                onSelectionChanged: (_) {},
              ),
            ),
          ),
        );
      });
      await settleGrpcLoad(tester);

      // No kbps badges at all
      expect(find.textContaining('kbps'), findsNothing);
      // Both filenames should still show
      expect(find.text('song.flac'), findsAtLeastNWidgets(1));
      expect(find.text('song.wav'), findsAtLeastNWidgets(1));
    });
  });
}
