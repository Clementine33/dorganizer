import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';

import 'file_pane_test_harness.dart';

/// Shallow mock coordinator for FilePane widget tests.
/// Records calls and returns a configurable result — no gRPC needed.
class MockFilePaneCoordinator implements FilePaneCoordinator {
  PlanExecutionResult nextResult = PlanExecutionResult.success();
  final calls = <({
    String planType,
    String targetFormat,
    Set<String> selectedFiles,
  })>[];

  @override
  Future<PlanExecutionResult> executeFlowForFiles({
    required String rootPath,
    required String? folderPath,
    required Set<String> selectedFiles,
    required String targetFormat,
    required String planType,
  }) async {
    calls.add((
      planType: planType,
      targetFormat: targetFormat,
      selectedFiles: {...selectedFiles},
    ));
    return nextResult;
  }
}

void main() {
  late FilePaneTestHarness harness;

  setUp(() async {
    harness = await createFilePaneTestHarness();
  });

  tearDown(() async {
    await harness.tearDown();
  });

  group('FilePane coordinator (mock)', () {
    testWidgets('convert dispatches selected files to coordinator', (
      tester,
    ) async {
      final coordinator = MockFilePaneCoordinator()
        ..nextResult = PlanExecutionResult.success();

      final selectedPaths = {'/test/folder/audio2.flac'};
      final cleared = <Set<String>>[];
      final wf = await pumpFilePane(
        tester,
        harness: harness,
        rootPath: '/test',
        folderPath: '/test/folder',
        selectedPaths: selectedPaths,
        coordinator: coordinator,
        onSelectionChanged: (p) => cleared.add(Set.from(p)),
      );

      final state = tester.state(wf) as dynamic;
      await tester.runAsync(() async {
        await state.triggerConvert('m4a');
      });
      await tester.pump();

      expect(coordinator.calls, hasLength(1));
      expect(coordinator.calls.single.planType, 'single_convert');
      expect(coordinator.calls.single.targetFormat, 'm4a');
      expect(
        coordinator.calls.single.selectedFiles,
        contains('/test/folder/audio2.flac'),
      );
    });

    testWidgets('delete dispatches selected files to coordinator', (
      tester,
    ) async {
      final coordinator = MockFilePaneCoordinator()
        ..nextResult = PlanExecutionResult.success();

      final selectedPaths = {'/test/folder/audio1.mp3'};
      final wf = await pumpFilePane(
        tester,
        harness: harness,
        rootPath: '/test',
        folderPath: '/test/folder',
        selectedPaths: selectedPaths,
        coordinator: coordinator,
      );

      final state = tester.state(wf) as dynamic;
      await tester.runAsync(() async {
        await state.triggerDelete();
      });
      await tester.pump();

      expect(coordinator.calls, hasLength(1));
      expect(coordinator.calls.single.planType, 'single_delete');
      expect(coordinator.calls.single.targetFormat, '');
    });

    testWidgets(
      'non-lossless convert does not dispatch to coordinator',
      (tester) async {
        final coordinator = MockFilePaneCoordinator()
          ..nextResult = PlanExecutionResult.success();

        // mp3 is not lossless -> convert should be rejected before coordinator
        final selectedPaths = {'/test/folder/audio1.mp3'};
        final wf = await pumpFilePane(
          tester,
          harness: harness,
          rootPath: '/test',
          folderPath: '/test/folder',
          selectedPaths: selectedPaths,
          coordinator: coordinator,
        );

        final state = tester.state(wf) as dynamic;
        await tester.runAsync(() async {
          await state.triggerConvert('m4a');
        });
        await tester.pump();

        expect(
          coordinator.calls,
          isEmpty,
          reason: 'Should not dispatch non-lossless source to coordinator',
        );
      },
    );

    testWidgets('success clears selection', (tester) async {
      final coordinator = MockFilePaneCoordinator()
        ..nextResult = PlanExecutionResult.success();

      final selectedPaths = {'/test/folder/audio2.flac'};
      final cleared = <Set<String>>[];
      final wf = await pumpFilePane(
        tester,
        harness: harness,
        rootPath: '/test',
        folderPath: '/test/folder',
        selectedPaths: selectedPaths,
        coordinator: coordinator,
        onSelectionChanged: (p) => cleared.add(Set.from(p)),
      );

      final state = tester.state(wf) as dynamic;
      await tester.runAsync(() async {
        await state.triggerConvert('m4a');
      });
      await tester.pump();

      expect(coordinator.calls, hasLength(1));
      expect(
        cleared.last,
        isEmpty,
        reason: 'Selection should be cleared on success',
      );
      expect(
        find.byType(CircularProgressIndicator),
        findsNothing,
        reason: 'Loading spinner should be cleared',
      );
    });

    testWidgets('failure clears loading and shows error message', (
      tester,
    ) async {
      final coordinator = MockFilePaneCoordinator()
        ..nextResult = PlanExecutionResult.failure('No operations to execute');

      final selectedPaths = {'/test/folder/audio2.flac'};
      final wf = await pumpFilePane(
        tester,
        harness: harness,
        rootPath: '/test',
        folderPath: '/test/folder',
        selectedPaths: selectedPaths,
        coordinator: coordinator,
      );

      final state = tester.state(wf) as dynamic;
      await tester.runAsync(() async {
        await state.triggerConvert('m4a');
      });
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));

      expect(
        find.byType(CircularProgressIndicator),
        findsNothing,
        reason: 'Loading spinner should be cleared on failure',
      );
      expect(
        find.text('No operations to execute'),
        findsOneWidget,
        reason: 'Error message should be shown',
      );
    });
  });

  // ---------------------------------------------------------------------------
  // RED: Harness explicit empty semantics (Bug B)
  // ---------------------------------------------------------------------------

  group('RED: Harness explicit empty semantics (Bug B)', () {
    testWidgets(
      'setFiles([]) produces empty file list without falling back to defaults',
      (tester) async {
        harness.fakeService.reset();
        // Explicitly request empty files
        harness.fakeService.setFiles([]);
        // Use defaultFiles that are non-empty to prove the fallback bug
        harness.fakeService.setDefaultFiles([
          '/test/folder/audio1.mp3',
          '/test/folder/audio2.flac',
        ]);

        await pumpFilePane(
          tester,
          harness: harness,
          rootPath: '/test',
          folderPath: '/test/folder',
        );

        // The file pane should show "No files found" when files list is empty.
        expect(
          find.text('No files found'),
          findsOneWidget,
          reason:
              'setFiles([]) should produce empty file list, not fall back to defaults',
        );

        // Conversely, the default audio files should NOT be present
        expect(
          find.text('audio1.mp3'),
          findsNothing,
          reason:
              'Default fallback files should not appear when setFiles([]) is used',
        );
      },
    );

    testWidgets(
      'setEntries([]) produces empty entries list without falling back to defaults',
      (tester) async {
        harness.fakeService.reset();
        // Explicitly request empty entries
        harness.fakeService.setEntries([]);
        harness.fakeService.setDefaultFiles([]);
        // Set empty files so entries path is used
        harness.fakeService.setFiles([]);

        await pumpFilePane(
          tester,
          harness: harness,
          rootPath: '/test',
          folderPath: '/test/folder',
        );

        // BUG: the harness listFiles uses `nextEntries.isNotEmpty ? nextEntries : defaultEntries`
        // but defaultEntries is empty, so this happens to work for entries but
        // not when defaultEntries is populated. The core issue is the fallback
        // logic itself.
        expect(
          find.text('No files found'),
          findsOneWidget,
          reason:
              'setEntries([]) should produce empty entries list, not fall back to defaults',
        );
      },
    );
  });
}
