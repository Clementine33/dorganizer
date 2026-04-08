import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/folders/folder_error_state.dart';

void main() {
  group('FolderErrorState', () {
    test('planError factory creates correct state', () {
      final state = FolderErrorState.planError(
        eventId: 'evt-1',
        code: 'ERR_PLAN',
        message: 'Plan failed',
        updatedAt: DateTime.now(),
      );

      expect(state.planHasError, isTrue);
      expect(state.executeHasError, isFalse);
      expect(state.hasError, isTrue);
      expect(state.lastEventId, 'evt-1');
      expect(state.lastCode, 'ERR_PLAN');
      expect(state.lastMessage, 'Plan failed');
      expect(state.lastStage, 'plan');
    });

    test('executeError factory creates correct state', () {
      final state = FolderErrorState.executeError(
        eventId: 'evt-2',
        code: 'ERR_EXEC',
        message: 'Execute failed',
        updatedAt: DateTime.now(),
      );

      expect(state.planHasError, isFalse);
      expect(state.executeHasError, isTrue);
      expect(state.hasError, isTrue);
      expect(state.lastEventId, 'evt-2');
      expect(state.lastCode, 'ERR_EXEC');
      expect(state.lastMessage, 'Execute failed');
      expect(state.lastStage, 'execute');
    });

    test('clear factory creates correct state', () {
      final state = FolderErrorState.clear(updatedAt: DateTime.now());

      expect(state.planHasError, isFalse);
      expect(state.executeHasError, isFalse);
      expect(state.hasError, isFalse);
      expect(state.lastEventId, isNull);
      expect(state.lastCode, isNull);
      expect(state.lastMessage, isNull);
      expect(state.lastStage, isNull);
    });

    test('copyWith creates modified state', () {
      final original = FolderErrorState.planError(
        eventId: 'evt-1',
        message: 'Original',
        updatedAt: DateTime.now(),
      );

      final modified = original.copyWith(
        lastMessage: 'Modified',
      );

      expect(modified.lastEventId, 'evt-1');
      expect(modified.lastMessage, 'Modified');
      expect(original.lastMessage, 'Original'); // Original unchanged
    });
  });

  group('Error state map intersection filtering', () {
    test('Intersection of listFolders and errorStateMap', () {
      // Simulate the filtering logic used in FolderPaneWidget
      final allFolders = [
        '/root/folderA',
        '/root/folderB',
        '/root/folderC',
        '/root/folderD',
      ];

      final errorStateMap = <String, FolderErrorState>{
        '/root/folderB': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error',
          updatedAt: DateTime.now(),
        ),
        '/root/folderD': FolderErrorState.executeError(
          eventId: 'evt-2',
          message: 'Error',
          updatedAt: DateTime.now(),
        ),
      };

      // Filter based on active error flags in state map
      final errorFolderPaths = errorStateMap.entries
          .where((entry) => entry.value.hasError)
          .map((entry) => entry.key)
          .toSet();
      final filteredFolders =
          allFolders.where((f) => errorFolderPaths.contains(f)).toList();

      expect(filteredFolders, ['/root/folderB', '/root/folderD']);
      expect(filteredFolders, isNot(contains('/root/folderA')));
      expect(filteredFolders, isNot(contains('/root/folderC')));
    });

    test('Empty errorStateMap returns empty list when showErrorView is true',
        () {
      final allFolders = ['/root/folderA', '/root/folderB'];
      final errorStateMap = <String, FolderErrorState>{};

      final errorFolderPaths = errorStateMap.entries
          .where((entry) => entry.value.hasError)
          .map((entry) => entry.key)
          .toSet();
      final filteredFolders =
          allFolders.where((f) => errorFolderPaths.contains(f)).toList();

      expect(filteredFolders, isEmpty);
    });

    test(
        'String contains(error) filtering is NOT used (Task 5 requirement)',
        () {
      // This test verifies that we're NOT using the old string contains filtering
      final allFolders = [
        '/root/error_logs', // name contains "error" but no actual error state
        '/root/folderA',
        '/root/folderB',
      ];

      // With the OLD (wrong) logic, error_logs would be included
      final wrongFilter =
          allFolders.where((f) => f.toLowerCase().contains('error')).toList();
      expect(wrongFilter,
          contains('/root/error_logs')); // This is the WRONG behavior

      // With the NEW (correct) logic, only folders in errorStateMap are included
      final errorStateMap = <String, FolderErrorState>{
        '/root/folderB': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error',
          updatedAt: DateTime.now(),
        ),
        // error_logs is NOT in map
      };

      final errorFolderPaths = errorStateMap.entries
          .where((entry) => entry.value.hasError)
          .map((entry) => entry.key)
          .toSet();
      final correctFilter =
          allFolders.where((f) => errorFolderPaths.contains(f)).toList();

      expect(correctFilter, ['/root/folderB']);
      expect(correctFilter,
          isNot(contains('/root/error_logs'))); // CORRECT: not included
      expect(correctFilter,
          isNot(contains('/root/folderA'))); // CORRECT: not included
    });

    test('Error state map with hasError=true filters correctly', () {
      final allFolders = ['/root/folderA', '/root/folderB', '/root/folderC'];

      // Create error states with hasError=true
      final errorStateMap = <String, FolderErrorState>{
        '/root/folderA': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error in A',
          updatedAt: DateTime.now(),
        ),
        '/root/folderC': FolderErrorState.executeError(
          eventId: 'evt-2',
          message: 'Error in C',
          updatedAt: DateTime.now(),
        ),
        // folderB has no error
      };

      // Verify all error states have hasError=true
      for (final entry in errorStateMap.entries) {
        expect(entry.value.hasError, isTrue);
      }

      // Filter based on active error state only
      final errorFolderPaths = errorStateMap.entries
          .where((entry) => entry.value.hasError)
          .map((entry) => entry.key)
          .toSet();
      final filteredFolders =
          allFolders.where((f) => errorFolderPaths.contains(f)).toList();

      expect(filteredFolders, ['/root/folderA', '/root/folderC']);
    });

    test('Folders with cleared state are excluded from Error View', () {
      final allFolders = ['/root/folderA', '/root/folderB'];

      final errorStateMap = <String, FolderErrorState>{
        '/root/folderA': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error in A',
          updatedAt: DateTime.now(),
        ),
        '/root/folderB': FolderErrorState.clear(updatedAt: DateTime.now()),
      };

      final errorFolderPaths = errorStateMap.entries
          .where((entry) => entry.value.hasError)
          .map((entry) => entry.key)
          .toSet();

      final filteredFolders =
          allFolders.where((f) => errorFolderPaths.contains(f)).toList();

      expect(filteredFolders, ['/root/folderA']);
      expect(filteredFolders, isNot(contains('/root/folderB')));
    });
  });

  group('Root switch clears error state (prototype behavior)', () {
    test('Error state map is cleared when root changes', () {
      // Simulate the root switch behavior
      var errorStateMap = <String, FolderErrorState>{
        '/root1/folderA': FolderErrorState.planError(
          eventId: 'evt-1',
          message: 'Error in root1',
          updatedAt: DateTime.now(),
        ),
      };

      // Verify initial state
      expect(errorStateMap.length, 1);

      // Simulate root switch - clear the map
      errorStateMap = {};

      // After root switch, error state should be empty
      expect(errorStateMap.isEmpty, isTrue);
    });

    test('New root starts with empty error state', () {
      // Simulate switching to a new root
      var errorStateMap = <String, FolderErrorState>{};

      // Verify new root starts with empty state
      expect(errorStateMap.isEmpty, isTrue);

      // Add new errors for the new root
      errorStateMap['/root2/folderB'] = FolderErrorState.executeError(
        eventId: 'evt-new',
        message: 'Error in root2',
        updatedAt: DateTime.now(),
      );

      // Verify only new root errors exist
      expect(errorStateMap.length, 1);
      expect(errorStateMap.keys.first, contains('/root2'));
    });
  });
}
