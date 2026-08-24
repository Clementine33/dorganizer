import 'package:dropdown_search/dropdown_search.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/folders/folder_error_state.dart';
import 'package:onsei_organizer/features/folders/folder_pane_widget.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

class FakeOnseiService extends OnseiServiceBase {
  List<String> nextFolders = [];
  final List<ListFoldersRequest> listFoldersRequests = [];

  void setFolders(List<String> folders) {
    nextFolders = folders;
  }

  @override
  Future<GetConfigResponse> getConfig(
    ServiceCall call,
    GetConfigRequest request,
  ) async {
    return GetConfigResponse();
  }

  @override
  Future<UpdateConfigResponse> updateConfig(
    ServiceCall call,
    UpdateConfigRequest request,
  ) async {
    return UpdateConfigResponse();
  }

  @override
  Stream<JobEvent> scan(ServiceCall call, ScanRequest request) {
    return const Stream.empty();
  }

  @override
  Future<ListFoldersResponse> listFolders(
    ServiceCall call,
    ListFoldersRequest request,
  ) async {
    listFoldersRequests.add(request);
    return ListFoldersResponse()..folders.addAll(nextFolders);
  }

  @override
  Future<ListFilesResponse> listFiles(
    ServiceCall call,
    ListFilesRequest request,
  ) async {
    return ListFilesResponse();
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    ServiceCall call,
    PlanOperationsRequest request,
  ) async {
    return PlanOperationsResponse();
  }

  @override
  Stream<JobEvent> executePlan(ServiceCall call, ExecutePlanRequest request) {
    return const Stream.empty();
  }

  @override
  Future<ListPlansResponse> listPlans(
    ServiceCall call,
    ListPlansRequest request,
  ) async {
    return ListPlansResponse();
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    ServiceCall call,
    RefreshFoldersRequest request,
  ) async {
    return RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

Future<(Server, FakeOnseiService)> createTestServer() async {
  final fakeService = FakeOnseiService();
  final server = Server.create(services: [fakeService]);
  await server.serve(port: 0);
  return (server, fakeService);
}

void main() {
  late Server server;
  late FakeOnseiService fakeService;
  late ClientChannel channel;

  setUp(() async {
    final (s, f) = await createTestServer();
    server = s;
    fakeService = f;
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

  testWidgets('folder pane shows baseline top actions', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: FolderPaneWidget(
            channel: channel,
            selectedFolder: null,
            selectedFolders: {},
            onFolderSelected: (_) {},
          ),
        ),
      ),
    );

    expect(find.text('Select Directory'), findsOneWidget);
    expect(find.text('Scan'), findsOneWidget);
    expect(find.text('Reload'), findsOneWidget);
  });

  testWidgets('error view chip toggles selected state via callback', (
    tester,
  ) async {
    // Since showErrorView is now externally controlled, we need a wrapper
    bool showErrorView = false;

    await tester.pumpWidget(
      StatefulBuilder(
        builder: (context, setState) {
          return MaterialApp(
            home: Scaffold(
              body: FolderPaneWidget(
                channel: channel,
                selectedFolder: null,
                selectedFolders: const {},
                showErrorView: showErrorView,
                onErrorViewToggle: (value) =>
                    setState(() => showErrorView = value),
                onFolderSelected: (_) {},
              ),
            ),
          );
        },
      ),
    );

    final before = tester.widget<FilterChip>(
      find.widgetWithText(FilterChip, 'Error View'),
    );
    expect(before.selected, isFalse);

    await tester.tap(find.widgetWithText(FilterChip, 'Error View'));
    await tester.pump();

    final after = tester.widget<FilterChip>(
      find.widgetWithText(FilterChip, 'Error View'),
    );
    expect(after.selected, isTrue);
  });

  testWidgets('folder pane does not overflow at panel width', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 240,
            child: FolderPaneWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders: const {},
              onFolderSelected: (_) {},
            ),
          ),
        ),
      ),
    );

    final exception = tester.takeException();
    expect(exception, isNull);
  });

  testWidgets('folder selector popup shows a search field after reload', (
    tester,
  ) async {
    final key = GlobalKey<FolderPaneWidgetState>();

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 240,
            child: FolderPaneWidget(
              key: key,
              channel: channel,
              selectedRoot: '/root',
              selectedFolder: null,
              selectedFolders: const {},
              onFolderSelected: (_) {},
            ),
          ),
        ),
      ),
    );

    key.currentState!.injectFoldersForTest(
      ['/root/alpha', '/root/beta'],
      root: '/root',
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byType(DropdownSearch<String>));
    await tester.pumpAndSettle();

    expect(find.byType(TextField), findsWidgets);
  });

  testWidgets('reload keeps the folder selector within the narrow pane', (
    tester,
  ) async {
    final key = GlobalKey<FolderPaneWidgetState>();

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 240,
            child: FolderPaneWidget(
              key: key,
              channel: channel,
              selectedRoot: '/root',
              selectedFolder: null,
              selectedFolders: const {},
              onFolderSelected: (_) {},
            ),
          ),
        ),
      ),
    );

    key.currentState!.injectFoldersForTest(
      ['/root/alpha', '/root/beta'],
      root: '/root',
    );
    await tester.pumpAndSettle();

    expect(find.text('Directory'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('searchable selector returns full folder path on selection', (
    tester,
  ) async {
    final key = GlobalKey<FolderPaneWidgetState>();
    String? selectedPath;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: FolderPaneWidget(
            key: key,
            channel: channel,
            selectedRoot: '/root',
            selectedFolder: null,
            selectedFolders: const {},
            onFolderSelected: (path) => selectedPath = path,
          ),
        ),
      ),
    );

    key.currentState!.injectFoldersForTest(
      ['/root/alpha', '/root/beta'],
      root: '/root',
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byType(DropdownSearch<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('beta').last, warnIfMissed: false);
    await tester.pumpAndSettle();

    expect(selectedPath, '/root/beta');
  });

  testWidgets('long press on folder item syncs dropdown selected value', (
    tester,
  ) async {
    final key = GlobalKey<FolderPaneWidgetState>();
    String? selectedPath;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 240,
            child: FolderPaneWidget(
              key: key,
              channel: channel,
              selectedRoot: '/root',
              selectedFolder: null,
              selectedFolders: const {},
              onFolderSelected: (path) => selectedPath = path,
            ),
          ),
        ),
      ),
    );

    key.currentState!.injectFoldersForTest(
      ['/root/alpha', '/root/beta'],
      root: '/root',
    );
    await tester.pumpAndSettle();

    // injectFoldersForTest calls onFolderSelected with the first folder.
    // Verify starting state.
    expect(selectedPath, '/root/alpha');

    // Long press on the second folder in the list.
    await tester.longPress(find.text('beta').last);
    await tester.pumpAndSettle();

    // onFolderSelected should receive the long-pressed folder.
    expect(selectedPath, '/root/beta');

    // The DropdownSearch widget should now show beta as its selected item.
    // The dropdownBuilder renders the selected item's display name with SarasaUiSC font.
    final dropdownFinder = find.byType(DropdownSearch<String>);
    expect(
      find.descendant(
        of: dropdownFinder,
        matching: find.text('beta'),
      ),
      findsOneWidget,
    );
  });

  // =========================================================================
  // Task 5: Structured Error State Tests
  // =========================================================================

  group('Task 5: Structured error state filtering', () {
    // Unit tests for filtering logic are in folder_error_state_test.dart
    // These tests verify the widget API accepts the new parameters

    testWidgets(
      'FolderPaneWidget accepts errorStateMap and showErrorView parameters',
      (tester) async {
        // This test verifies the widget API accepts the new parameters
        final errorStateMap = <String, FolderErrorState>{
          '/root/folderB': FolderErrorState.planError(
            eventId: 'evt-1',
            message: 'Plan failed',
            updatedAt: DateTime.now(),
          ),
        };

        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: SizedBox(
                width: 240,
                child: FolderPaneWidget(
                  channel: channel,
                  selectedRoot: '/root',
                  selectedFolder: null,
                  selectedFolders: const {},
                  errorStateMap: errorStateMap,
                  showErrorView: true,
                  onFolderSelected: (_) {},
                ),
              ),
            ),
          ),
        );

        // Widget should build without errors
        expect(find.byType(FolderPaneWidget), findsOneWidget);

        // Error View chip should reflect the showErrorView state
        final chip = tester.widget<FilterChip>(
          find.widgetWithText(FilterChip, 'Error View'),
        );
        expect(chip.selected, isTrue);
      },
    );

    testWidgets('FolderPaneWidget accepts onErrorViewToggle callback', (
      tester,
    ) async {
      bool showErrorView = false;

      await tester.pumpWidget(
        StatefulBuilder(
          builder: (context, setState) {
            return MaterialApp(
              home: Scaffold(
                body: FolderPaneWidget(
                  channel: channel,
                  selectedFolder: null,
                  selectedFolders: const {},
                  showErrorView: showErrorView,
                  errorStateMap: {},
                  onErrorViewToggle: (value) =>
                      setState(() => showErrorView = value),
                  onFolderSelected: (_) {},
                ),
              ),
            );
          },
        ),
      );

      // Initial state
      var chip = tester.widget<FilterChip>(
        find.widgetWithText(FilterChip, 'Error View'),
      );
      expect(chip.selected, isFalse);

      // Tap to toggle
      await tester.tap(find.widgetWithText(FilterChip, 'Error View'));
      await tester.pump();

      // Should now be selected
      chip = tester.widget<FilterChip>(
        find.widgetWithText(FilterChip, 'Error View'),
      );
      expect(chip.selected, isTrue);
    });

    testWidgets('toggling Error View does not trigger setState during build', (
      tester,
    ) async {
      fakeService.setFolders(['/root/folderA', '/root/folderB']);

      bool showErrorView = false;
      Set<String> selectedFolders = {'/root/folderA', '/root/folderB'};
      String? selectedFolder = '/root/folderA';

      await tester.pumpWidget(
        StatefulBuilder(
          builder: (context, setState) {
            return MaterialApp(
              home: Scaffold(
                body: FolderPaneWidget(
                  channel: channel,
                  selectedRoot: '/root',
                  selectedFolder: selectedFolder,
                  selectedFolders: selectedFolders,
                  showErrorView: showErrorView,
                  errorStateMap: {
                    '/root/folderB': FolderErrorState.planError(
                      eventId: 'evt-filter',
                      message: 'Only B has error',
                      updatedAt: DateTime.now(),
                    ),
                  },
                  onErrorViewToggle: (value) =>
                      setState(() => showErrorView = value),
                  onFoldersSelectionChanged: (folders) =>
                      setState(() => selectedFolders = folders),
                  onFolderSelected: (path) =>
                      setState(() => selectedFolder = path),
                ),
              ),
            );
          },
        ),
      );

      await tester.tap(find.widgetWithText(OutlinedButton, 'Reload'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 200));

      await tester.tap(find.widgetWithText(FilterChip, 'Error View'));
      await tester.pump();

      expect(tester.takeException(), isNull);
    });

    testWidgets('FilterChip shows correct state when showErrorView is true', (
      tester,
    ) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: FolderPaneWidget(
              channel: channel,
              selectedFolder: null,
              selectedFolders: const {},
              showErrorView: true,
              errorStateMap: {},
              onFolderSelected: (_) {},
            ),
          ),
        ),
      );

      final chip = tester.widget<FilterChip>(
        find.widgetWithText(FilterChip, 'Error View'),
      );
      expect(chip.selected, isTrue);
    });

    test('folder dropdown filter matches display name case-insensitively', () {
      const root = '/root';

      expect(
        folderMatchesDropdownFilter('/root/Alpha', 'alpha', root),
        isTrue,
      );
      expect(
        folderMatchesDropdownFilter('/root/Beta', 'alpha', root),
        isFalse,
      );
      expect(
        folderMatchesDropdownFilter('/root/Nested/Child', 'nested', root),
        isTrue,
      );
    });

    test(
      'Error View uses listFolders∩hasError folders and excludes cleared state',
      () {
        final folders = ['/root/folderA', '/root/folderB', '/root/error_logs'];
        final errorStateMap = <String, FolderErrorState>{
          '/root/folderB': FolderErrorState.executeError(
            eventId: 'evt-1',
            message: 'Execute failed',
            updatedAt: DateTime.now(),
          ),
          '/root/error_logs': FolderErrorState.clear(updatedAt: DateTime.now()),
        };

        final filtered = filterFoldersForErrorView(
          folders,
          errorStateMap,
          true,
        );

        expect(filtered, ['/root/folderB']);
      },
    );

    test('Error View filtering normalizes slash variants', () {
      final folders = [r'\\192.168.0.32\Media\onsei\archive\1\14\A'];
      final errorStateMap = <String, FolderErrorState>{
        '//192.168.0.32/Media/onsei/archive/1/14/A': FolderErrorState.planError(
          eventId: 'evt-unc',
          message: 'UNC slash variant',
          updatedAt: DateTime.now(),
        ),
      };

      final filtered = filterFoldersForErrorView(folders, errorStateMap, true);

      expect(filtered, hasLength(1));
      expect(filtered.single, folders.single);
    });

    test(
      'showErrorView toggling changes filtered subset from same folder list',
      () {
        final allFolders = ['/root/folderA', '/root/folderB'];
        final errorStateMap = <String, FolderErrorState>{
          '/root/folderB': FolderErrorState.planError(
            eventId: 'evt-filter',
            message: 'Only B has error',
            updatedAt: DateTime.now(),
          ),
        };

        final unfiltered = filterFoldersForErrorView(
          allFolders,
          errorStateMap,
          false,
        );
        final filtered = filterFoldersForErrorView(
          allFolders,
          errorStateMap,
          true,
        );

        expect(unfiltered, allFolders);
        expect(filtered, ['/root/folderB']);
      },
    );
  });
}
