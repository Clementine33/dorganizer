import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:grpc/grpc.dart';

import '../../core/path_normalizer.dart';
import '../../gen/onsei/v1/service.pbgrpc.dart';
import 'folder_error_state.dart';

@visibleForTesting
List<String> filterFoldersForErrorView(
  List<String> folders,
  Map<String, FolderErrorState> errorStateMap,
  bool showErrorView,
) {
  if (!showErrorView) {
    return folders;
  }

  final errorFolderPaths = errorStateMap.entries
      .where((entry) => entry.value.hasError)
      .map((entry) => normalizePathForComparison(entry.key))
      .where((path) => path.isNotEmpty)
      .toSet();
  return folders
      .where(
        (folder) =>
            errorFolderPaths.contains(normalizePathForComparison(folder)),
      )
      .toList();
}

class FolderPaneWidget extends StatefulWidget {
  final ClientChannel channel;
  final String? selectedRoot;
  final String? selectedFolder;
  final Set<String> selectedFolders;
  final void Function(String path) onFolderSelected;
  final void Function(String rootPath)? onRootSelected;
  final void Function(Set<String> folders)? onFoldersSelectionChanged;
  final void Function(bool showErrorView)? onErrorViewToggle;

  /// Structured error state map for current root (read-only).
  /// Keys are folder paths, values contain error details.
  final Map<String, FolderErrorState> errorStateMap;

  /// Whether Error View mode is active (controlled by parent).
  final bool showErrorView;

  const FolderPaneWidget({
    super.key,
    required this.channel,
    required this.selectedFolder,
    required this.selectedFolders,
    required this.onFolderSelected,
    this.selectedRoot,
    this.onRootSelected,
    this.onFoldersSelectionChanged,
    this.onErrorViewToggle,
    this.errorStateMap = const {},
    this.showErrorView = false,
  });

  @override
  State<FolderPaneWidget> createState() => _FolderPaneWidgetState();
}

class _FolderPaneWidgetState extends State<FolderPaneWidget> {
  late final OnseiServiceClient _client;

  List<String> _folders = [];
  List<String> _allFolders = [];
  bool _loading = false;
  String? _error;
  String? _dropdownFolder;
  String? _selectedRoot;

  @override
  void initState() {
    super.initState();
    _client = OnseiServiceClient(widget.channel);
    _selectedRoot = widget.selectedRoot;
  }

  @override
  void didUpdateWidget(covariant FolderPaneWidget oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.selectedRoot != oldWidget.selectedRoot) {
      _selectedRoot = widget.selectedRoot;
    }

    final errorViewToggled = widget.showErrorView != oldWidget.showErrorView;
    final errorMapChanged = !mapEquals(
      widget.errorStateMap,
      oldWidget.errorStateMap,
    );
    if ((errorViewToggled || errorMapChanged) && _allFolders.isNotEmpty) {
      _recomputeFilteredFoldersFromLoadedState(deferParentNotifications: true);
    }
  }

  Future<void> _selectDirectory() async {
    final path = await getDirectoryPath();
    if (path == null || path.isEmpty) {
      return;
    }
    setState(() {
      _selectedRoot = path;
      _folders = [];
      _allFolders = [];
      _dropdownFolder = null;
      _error = null;
    });
    widget.onRootSelected?.call(path);
  }

  Future<void> _scan() async {
    final root = _selectedRoot;
    if (root == null || root.isEmpty) {
      return;
    }

    setState(() {
      _loading = true;
      _error = null;
    });

    try {
      final stream = _client.scan(ScanRequest()..folderPath = root);
      await for (final _ in stream) {}
      await _refreshFolders();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  Future<void> _refreshFolders() async {
    final root = _selectedRoot;
    if (root == null || root.isEmpty) {
      return;
    }

    setState(() {
      _loading = true;
      _error = null;
    });

    try {
      final response = await _client.listFolders(
        ListFoldersRequest()..parentPath = root,
      );

      final allFolders = response.folders.toList();
      final nextFolders = filterFoldersForErrorView(
        allFolders,
        widget.errorStateMap,
        widget.showErrorView,
      );

      final previousSelection = widget.selectedFolders;
      final preserved = previousSelection.where(nextFolders.contains).toSet();

      if (mounted) {
        setState(() {
          _allFolders = allFolders;
          _folders = nextFolders;
          _dropdownFolder = nextFolders.contains(_dropdownFolder)
              ? _dropdownFolder
              : (nextFolders.isNotEmpty ? nextFolders.first : null);
          _loading = false;
        });
      }

      widget.onFoldersSelectionChanged?.call(preserved);
      if (_dropdownFolder != null) {
        widget.onFolderSelected(_dropdownFolder!);
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  void _toggleErrorView() {
    widget.onErrorViewToggle?.call(!widget.showErrorView);
  }

  void _recomputeFilteredFoldersFromLoadedState({
    bool deferParentNotifications = false,
  }) {
    final nextFolders = filterFoldersForErrorView(
      _allFolders,
      widget.errorStateMap,
      widget.showErrorView,
    );
    final preserved = widget.selectedFolders
        .where(nextFolders.contains)
        .toSet();
    final nextDropdown = nextFolders.contains(_dropdownFolder)
        ? _dropdownFolder
        : (nextFolders.isNotEmpty ? nextFolders.first : null);

    setState(() {
      _folders = nextFolders;
      _dropdownFolder = nextDropdown;
    });

    void notifyParent() {
      if (!mounted) return;
      widget.onFoldersSelectionChanged?.call(preserved);
      if (nextDropdown != null) {
        widget.onFolderSelected(nextDropdown);
      }
    }

    if (deferParentNotifications) {
      WidgetsBinding.instance.addPostFrameCallback((_) => notifyParent());
    } else {
      notifyParent();
    }
  }

  void _setFolderSelected(String folder, bool selected) {
    final next = Set<String>.from(widget.selectedFolders);
    if (selected) {
      next.add(folder);
    } else {
      next.remove(folder);
    }
    widget.onFoldersSelectionChanged?.call(next);
  }

  void _selectAllFolders() {
    widget.onFoldersSelectionChanged?.call(_folders.toSet());
  }

  void _clearFolderSelection() {
    widget.onFoldersSelectionChanged?.call({});
  }

  // bool get _allFoldersSelected =>
  //     _folders.isNotEmpty && widget.selectedFolders.length == _folders.length;

  // bool get _partiallySelected =>
  //     widget.selectedFolders.isNotEmpty && !_allFoldersSelected;

  String _folderDisplayName(String folder) {
    final normalizedFolder = folder.replaceAll('\\', '/');
    final folderSegments = normalizedFolder
        .split('/')
        .where((segment) => segment.isNotEmpty)
        .toList();

    final root = _selectedRoot;
    if (root == null || root.isEmpty) {
      return folderSegments.isNotEmpty ? folderSegments.last : folder;
    }

    final normalizedRoot = root
        .replaceAll('\\', '/')
        .replaceAll(RegExp(r'/+$'), '');
    final rootPrefix = '$normalizedRoot/';
    if (normalizedFolder.startsWith(rootPrefix)) {
      final relative = normalizedFolder.substring(rootPrefix.length);
      final relativeSegments = relative
          .split('/')
          .where((segment) => segment.isNotEmpty)
          .toList();
      if (relativeSegments.isNotEmpty) {
        return relativeSegments.first;
      }
    }

    return folderSegments.isNotEmpty ? folderSegments.last : folder;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Container(
        //   padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        //   color: theme.colorScheme.surfaceContainerHighest,
        //   child: Row(
        //     children: [
        //       Expanded(
        //         child: Text(
        //           'Folders',
        //           maxLines: 1,
        //           overflow: TextOverflow.ellipsis,
        //           style: theme.textTheme.labelLarge?.copyWith(
        //             color: theme.colorScheme.onSurfaceVariant,
        //             fontWeight: FontWeight.w600,
        //           ),
        //         ),
        //       ),
        //       TextButton.icon(
        //         onPressed: () {
        //           ScaffoldMessenger.of(context).showSnackBar(
        //             const SnackBar(
        //               content: Text('Settings not implemented yet'),
        //             ),
        //           );
        //         },
        //         icon: const Icon(Icons.settings, size: 16),
        //         label: const Text('Settings'),
        //       ),
        //     ],
        //   ),
        // ),
        Padding(
          padding: const EdgeInsets.fromLTRB(8, 8, 8, 4),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              FilledButton.tonal(
                onPressed: _selectDirectory,
                child: const Text('Select Directory'),
              ),
              FilledButton(
                onPressed: _selectedRoot == null || _loading ? null : _scan,
                child: const Text('Scan'),
              ),
              OutlinedButton(
                onPressed: _selectedRoot == null || _loading
                    ? null
                    : _refreshFolders,
                child: const Text('Reload'),
              ),
              FilterChip(
                label: const Text('Error View'),
                selected: widget.showErrorView,
                onSelected: (_) => _toggleErrorView(),
              ),
            ],
          ),
        ),
        // Container(
        //   margin: const EdgeInsets.fromLTRB(8, 0, 8, 8),
        //   padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        //   decoration: BoxDecoration(
        //     border: Border.all(color: theme.colorScheme.outlineVariant),
        //     borderRadius: BorderRadius.circular(8),
        //   ),
        //   child: Text(
        //     _selectedRoot == null || _selectedRoot!.isEmpty
        //         ? 'No directory selected'
        //         : _selectedRoot!,
        //     maxLines: 2,
        //     overflow: TextOverflow.ellipsis,
        //     style: theme.textTheme.bodySmall?.copyWith(
        //       fontFamily: 'SarasaUiSC',
        //     ),
        //   ),
        // ),
        if (_folders.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
            child: DropdownButtonFormField<String>(
              initialValue: _dropdownFolder,
              isExpanded: true,
              decoration: const InputDecoration(
                labelText: 'Directory',
                border: OutlineInputBorder(),
                isDense: true,
              ),
              items: _folders
                  .map(
                    (folder) => DropdownMenuItem<String>(
                      value: folder,
                      child: Text(
                        style: TextStyle(fontFamily: 'SarasaUiSC'),
                        _folderDisplayName(folder),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  )
                  .toList(),
              onChanged: (value) {
                if (value == null) return;
                setState(() {
                  _dropdownFolder = value;
                });
                widget.onFolderSelected(value);
              },
            ),
          ),
        Padding(
          padding: const EdgeInsets.fromLTRB(8, 0, 8, 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  // Checkbox(
                  //   tristate: true,
                  //   value: _allFoldersSelected
                  //       ? true
                  //       : (_partiallySelected ? null : false),
                  //   onChanged: (_) {
                  //     if (_allFoldersSelected || _partiallySelected) {
                  //       _clearFolderSelection();
                  //     } else {
                  //       _selectAllFolders();
                  //     }
                  //   },
                  // ),
                  Expanded(
                    child: Text(
                      'Selected ${widget.selectedFolders.length}/${_folders.length}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.labelSmall,
                    ),
                  ),
                ],
              ),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: _selectAllFolders,
                    child: const Text('All'),
                  ),
                  TextButton(
                    onPressed: _clearFolderSelection,
                    child: const Text('None'),
                  ),
                ],
              ),
            ],
          ),
        ),
        Expanded(
          child: _loading
              ? const Center(child: CircularProgressIndicator())
              : _error != null
              ? _ErrorView(message: _error!, onRetry: _refreshFolders)
              : _folders.isEmpty
              ? Center(
                  child: Text(
                    _selectedRoot == null
                        ? 'Select a directory'
                        : 'No folders found',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                )
              : ListView.builder(
                  itemCount: _folders.length,
                  itemBuilder: (context, index) {
                    final folder = _folders[index];
                    final isSelected = widget.selectedFolders.contains(folder);
                    final isPrimary = folder == widget.selectedFolder;
                    return GestureDetector(
                      onLongPress: () => widget.onFolderSelected(folder),
                      child: CheckboxListTile(
                        value: isSelected,
                        dense: true,
                        secondary: Icon(
                          isPrimary ? Icons.folder_open : Icons.folder,
                          size: 18,
                          color: isPrimary
                              ? theme.colorScheme.primary
                              : theme.colorScheme.onSurfaceVariant,
                        ),
                        title: Text(
                          folder.split(RegExp(r'[/\\]')).last,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall?.copyWith(
                            fontFamily: 'SarasaUiSC',
                          ),
                        ),
                        subtitle: isPrimary
                            ? Text(
                                'Current',
                                style: theme.textTheme.labelSmall?.copyWith(
                                  color: theme.colorScheme.primary,
                                ),
                              )
                            : null,
                        onChanged: (checked) =>
                            _setFolderSelected(folder, checked ?? false),
                        controlAffinity: ListTileControlAffinity.leading,
                      ),
                    );
                  },
                ),
        ),
      ],
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const _ErrorView({required this.message, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 28),
          const SizedBox(height: 8),
          Text(
            message,
            style: const TextStyle(fontSize: 12, color: Colors.redAccent),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),
          TextButton(onPressed: onRetry, child: const Text('Retry')),
        ],
      ),
    );
  }
}
