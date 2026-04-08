import 'dart:async';
import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:grpc/grpc.dart';
import 'bootstrap/backend_process.dart';
import 'bootstrap/grpc_channel.dart';
import 'core/path_normalizer.dart';
import 'features/folders/folder_pane_widget.dart';
import 'features/folders/folder_error_state.dart';
import 'features/files/file_pane_widget.dart';
import 'features/workflow/plan_execution_coordinator.dart';
import 'features/workflow/workflow_panel_widget.dart';
import 'features/workflow/workflow_state_store.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Start backend process
  final backend = BackendProcess();
  await backend.start();

  // Wait for backend ready signal
  final ready = await backend.waitForReady();
  if (ready == null) {
    debugPrint('Failed to start backend');
    return;
  }

  // Create gRPC channel
  final channel = createGrpcChannel('localhost', ready.port, ready.token);

  runApp(
    OnseiApp(
      channel: channel,
      onShutdown: () async {
        await channel.shutdown();
        await backend.stop();
      },
    ),
  );
}

class OnseiApp extends StatefulWidget {
  final ClientChannel channel;
  final Future<void> Function()? onShutdown;

  const OnseiApp({super.key, required this.channel, this.onShutdown});

  @override
  State<OnseiApp> createState() => _OnseiAppState();
}

class _OnseiAppState extends State<OnseiApp> {
  AppLifecycleListener? _lifecycleListener;
  bool _didShutdown = false;
  String? _selectedRoot;
  String? _selectedFolder;
  Set<String> _selectedFolders = {};
  Set<String> _selectedFiles = {};

  // Task 5: Structured error state for current root only
  Map<String, FolderErrorState> _errorStateMap = {};
  bool _showErrorView = false;

  // Task 2: Shared workflow state store for softDelete and refresh
  late final WorkflowStateStore _workflowStateStore;
  late final PlanExecutionCoordinator _planExecutionCoordinator;

  @visibleForTesting
  void handleRootSelected(String root) {
    setState(() {
      _selectedRoot = root;
      _selectedFolder = null;
      _selectedFolders = {};
      _selectedFiles = {};
      _errorStateMap = {};
      _showErrorView = false;
    });
  }

  @visibleForTesting
  void setErrorStateMapForTesting(Map<String, FolderErrorState> value) {
    setState(() {
      _errorStateMap = Map<String, FolderErrorState>.from(value);
    });
  }

  @visibleForTesting
  Map<String, FolderErrorState> get errorStateMapForTesting => _errorStateMap;

  @visibleForTesting
  Set<String> get selectedFoldersForTesting => _selectedFolders;

  @visibleForTesting
  Set<String> get selectedFilesForTesting => _selectedFiles;

  @visibleForTesting
  bool get showErrorViewForTesting => _showErrorView;

  // Task 6: Reducer callbacks for WorkflowPanelWidget

  @visibleForTesting
  void clearPlanErrorForFolders(Set<String> folders) {
    setState(() {
      for (final folder in folders) {
        final normalizedFolder = normalizePathForComparison(folder);
        if (normalizedFolder.isEmpty) {
          continue;
        }
        final existing = _errorStateMap[normalizedFolder];
        if (existing != null) {
          _errorStateMap[normalizedFolder] = existing.copyWith(
            planHasError: false,
            updatedAt: DateTime.now(),
          );
        }
      }
    });
  }

  @visibleForTesting
  void clearExecuteErrorForFolders(Set<String> folders) {
    setState(() {
      for (final folder in folders) {
        final normalizedFolder = normalizePathForComparison(folder);
        if (normalizedFolder.isEmpty) {
          continue;
        }
        final existing = _errorStateMap[normalizedFolder];
        if (existing != null) {
          _errorStateMap[normalizedFolder] = existing.copyWith(
            executeHasError: false,
            updatedAt: DateTime.now(),
          );
        }
      }
    });
  }

  @visibleForTesting
  void applyPlanErrorToFolders(
    Set<String> folders,
    String eventId,
    String? code,
    String? message,
  ) {
    if (folders.isEmpty) return;
    setState(() {
      final now = DateTime.now();
      for (final folder in folders) {
        final normalizedFolder = normalizePathForComparison(folder);
        if (normalizedFolder.isEmpty) {
          continue;
        }
        _errorStateMap[normalizedFolder] = FolderErrorState.planError(
          eventId: eventId,
          code: code,
          message: message,
          updatedAt: now,
        );
      }
    });
  }

  @visibleForTesting
  void applyExecuteErrorToFolders(
    Set<String> folders,
    String eventId,
    String? code,
    String? message,
  ) {
    if (folders.isEmpty) return;
    setState(() {
      final now = DateTime.now();
      for (final folder in folders) {
        final normalizedFolder = normalizePathForComparison(folder);
        if (normalizedFolder.isEmpty) {
          continue;
        }
        final existing = _errorStateMap[normalizedFolder];
        if (existing != null) {
          _errorStateMap[normalizedFolder] = existing.copyWith(
            executeHasError: true,
            lastEventId: eventId,
            lastCode: code,
            lastMessage: message,
            lastStage: 'execute',
            updatedAt: now,
          );
        } else {
          _errorStateMap[normalizedFolder] = FolderErrorState.executeError(
            eventId: eventId,
            code: code,
            message: message,
            updatedAt: now,
          );
        }
      }
    });
  }

  @override
  void initState() {
    super.initState();
    // Task 2: Create shared workflow state store
    _workflowStateStore = WorkflowStateStore(channel: widget.channel);
    _planExecutionCoordinator = PlanExecutionCoordinator(
      channel: widget.channel,
      store: _workflowStateStore,
    );
    _lifecycleListener = AppLifecycleListener(
      onExitRequested: () async {
        await _triggerShutdown();
        return AppExitResponse.exit;
      },
      onDetach: _triggerShutdown,
    );
  }

  Future<void> _triggerShutdown() async {
    if (_didShutdown) return;
    _didShutdown = true;
    await widget.onShutdown?.call();
  }

  @override
  void dispose() {
    _lifecycleListener?.dispose();
    _workflowStateStore.dispose();
    unawaited(_triggerShutdown());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Onsei Organizer',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF7C3AED),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
        fontFamily: 'Roboto',
      ),
      darkTheme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF7C3AED),
          brightness: Brightness.dark,
        ),
        useMaterial3: true,
        fontFamily: 'Roboto',
      ),
      themeMode: ThemeMode.system,
      home: Scaffold(
        // appBar: AppBar(
        //   title: const Row(
        //     children: [
        //       Icon(Icons.music_note, size: 22),
        //       SizedBox(width: 8),
        //       Text(
        //         'Onsei Organizer',
        //         style: TextStyle(fontWeight: FontWeight.w600, fontSize: 18),
        //       ),
        //     ],
        //   ),
        //   elevation: 0,
        //   scrolledUnderElevation: 1,
        // ),
        body: Row(
          children: [
            // Left panel: Folder browser
            SizedBox(
              width: 240,
              child: Card(
                margin: const EdgeInsets.fromLTRB(8, 8, 4, 8),
                elevation: 1,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                clipBehavior: Clip.antiAlias,
                child: FolderPaneWidget(
                  channel: widget.channel,
                  selectedRoot: _selectedRoot,
                  selectedFolder: _selectedFolder,
                  selectedFolders: _selectedFolders,
                  errorStateMap: _errorStateMap,
                  showErrorView: _showErrorView,
                  onRootSelected: handleRootSelected,
                  onFoldersSelectionChanged: (folders) => setState(() {
                    _selectedFolders = folders;
                  }),
                  onFolderSelected: (path) => setState(() {
                    _selectedFolder = path;
                    _selectedFiles = {};
                  }),
                  onErrorViewToggle: (show) => setState(() {
                    _showErrorView = show;
                  }),
                ),
              ),
            ),
            // Center panel: File list
            Expanded(
              flex: 3,
              child: Card(
                margin: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
                elevation: 1,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                clipBehavior: Clip.antiAlias,
                child: FilePaneWidget(
                  channel: widget.channel,
                  rootPath: _selectedRoot,
                  folderPath: _selectedFolder,
                  selectedPaths: _selectedFiles,
                  onSelectionChanged: (paths) => setState(() {
                    _selectedFiles = paths;
                  }),
                  // Task 2: Pass shared store
                  workflowStateStore: _workflowStateStore,
                  coordinator: _planExecutionCoordinator,
                ),
              ),
            ),
            // Right panel: Workflow
            SizedBox(
              width: 400,
              child: Card(
                margin: const EdgeInsets.fromLTRB(4, 8, 8, 8),
                elevation: 1,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                clipBehavior: Clip.antiAlias,
                child: WorkflowPanelWidget(
                  channel: widget.channel,
                  selectedFolder: _selectedFolder,
                  selectedFolders: _selectedFolders,
                  selectedFiles: _selectedFiles,
                  selectedRoot: _selectedRoot,
                  onClearPlanErrorForFolders: clearPlanErrorForFolders,
                  onClearExecuteErrorForFolders: clearExecuteErrorForFolders,
                  onApplyPlanErrorToFolders: applyPlanErrorToFolders,
                  onApplyExecuteErrorToFolders: applyExecuteErrorToFolders,
                  showErrorView: _showErrorView,
                  // Task 2: Pass shared store
                  workflowStateStore: _workflowStateStore,
                  coordinator: _planExecutionCoordinator,
                ),
              ),
            ),
          ],
        ),
        bottomNavigationBar: Builder(
          builder: (context) => Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              border: Border(
                top: BorderSide(
                  color: Theme.of(context).colorScheme.outlineVariant,
                ),
              ),
              color: Theme.of(context).colorScheme.surfaceContainerLow,
            ),
            child: Row(
              children: [
                // Text(
                //   'Selected: ${_selectedFolders.length} folders, ${_selectedFiles.length} files',
                //   style: Theme.of(context).textTheme.bodySmall,
                // ),
                // const SizedBox(width: 100),
                Expanded(
                  child: Text(
                    _selectedRoot == null
                        ? 'Root: (none)'
                        : 'Root: $_selectedRoot',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(
                      context,
                    ).textTheme.labelSmall?.copyWith(fontFamily: 'SarasaUiSC'),
                  ),
                ),
                const SizedBox(width: 16),
                Text(
                  'By Dekopon',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
