import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:grpc/grpc.dart';
import '../../gen/onsei/v1/service.pbgrpc.dart';
import '../workflow/workflow_state_store.dart';
import '../workflow/plan_execution_coordinator.dart';

class FilePaneWidget extends StatefulWidget {
  final ClientChannel channel;
  final String? rootPath;
  final String? folderPath;
  final Set<String> selectedPaths;
  final void Function(Set<String> selectedPaths) onSelectionChanged;
  final VoidCallback? onOpenFolder;

  /// Shared workflow state store for softDelete setting.
  /// If provided, execute operations read softDelete from this store.
  final WorkflowStateStore? workflowStateStore;

  /// Coordinator for unified execute flow.
  /// If provided, convert/delete operations use this coordinator.
  final PlanExecutionCoordinator? coordinator;

  const FilePaneWidget({
    super.key,
    required this.channel,
    required this.rootPath,
    required this.folderPath,
    required this.selectedPaths,
    required this.onSelectionChanged,
    this.onOpenFolder,
    this.workflowStateStore,
    this.coordinator,
  });

  @override
  State<FilePaneWidget> createState() => _FilePaneWidgetState();
}

class _FilePaneWidgetState extends State<FilePaneWidget> {
  static const Set<String> _losslessFormats = {'wav', 'flac'};

  late OnseiServiceClient _client;
  List<String> _files = [];
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _client = OnseiServiceClient(widget.channel);
    _loadFiles();
  }

  @override
  void didUpdateWidget(FilePaneWidget oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.folderPath != widget.folderPath) {
      _loadFiles();
    }
  }

  Future<void> _loadFiles() async {
    final path = widget.folderPath;
    if (path == null || path.isEmpty) {
      setState(() {
        _files = [];
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final response = await _client.listFiles(
        ListFilesRequest()..folderPath = path,
      );
      if (mounted) {
        _reconcileSelection(response.files);
        setState(() {
          _files = response.files;
          _loading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = e.toString();
          _loading = false;
        });
      }
    }
  }

  void _toggleFile(String path) {
    final next = Set<String>.from(widget.selectedPaths);
    if (next.contains(path)) {
      next.remove(path);
    } else {
      next.add(path);
    }
    widget.onSelectionChanged(next);
  }

  void _selectAll() {
    widget.onSelectionChanged(_files.toSet());
  }

  void _clearSelection() {
    widget.onSelectionChanged({});
  }

  @visibleForTesting
  Future<void> triggerDelete() async {
    // Bypasses dialog for testing
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    setState(() => _loading = true);

    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: '',
          planType: 'single_delete',
        );

        if (mounted) {
          setState(() => _loading = false);
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Delete completed')));
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Delete failed')),
            );
          }
        }
        if (mounted) {
          setState(() => _loading = false);
        }
        return;
      }

      // Legacy path: direct gRPC calls
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..planType = 'single_delete',
      );

      if (planResponse.operations.isEmpty || planResponse.planId.isEmpty) {
        if (mounted) {
          setState(() => _loading = false);
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Planning failed')));
        }
        return;
      }

      // Execute plan and check for errors
      String? errorMessage;
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        if (event.eventType == 'error') {
          errorMessage = event.message;
        }
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        setState(() => _loading = false);
        if (errorMessage != null) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text(errorMessage)));
        } else {
          widget.onSelectionChanged({});
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Delete completed')));
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() => _loading = false);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Delete failed: $e')));
      }
    }
  }

  @visibleForTesting
  Future<void> triggerConvert(String format) async {
    // Bypasses dialogs for testing
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      return;
    }

    if (!_selectionIsLossless(selected)) {
      return;
    }

    final configuredFormat = await _resolveConfiguredTargetFormat();
    final targetFormat = configuredFormat ?? format;
    if (targetFormat.isEmpty) {
      return;
    }

    setState(() => _loading = true);

    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: targetFormat,
          planType: 'single_convert',
        );

        if (mounted) {
          setState(() => _loading = false);
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Convert completed')));
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Convert failed')),
            );
          }
        }
        return;
      }

      // Legacy path: direct gRPC calls
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..targetFormat = targetFormat
          ..planType = 'single_convert',
      );

      if (planResponse.operations.isEmpty || planResponse.planId.isEmpty) {
        if (mounted) {
          setState(() => _loading = false);
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Planning failed')));
        }
        return;
      }

      String? errorMessage;
      // Read softDelete from shared store if available
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        if (event.eventType == 'error') {
          errorMessage = event.message;
        }
      }

      if (mounted) {
        setState(() => _loading = false);
        if (errorMessage != null) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text(errorMessage)));
        } else {
          widget.onSelectionChanged({});
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Convert completed')));
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  void _reconcileSelection(List<String> refreshedFiles) {
    final current = widget.selectedPaths;
    if (current.isEmpty) {
      return;
    }

    final existing = refreshedFiles.toSet();
    final pruned = current.where(existing.contains).toSet();
    if (pruned.length != current.length) {
      widget.onSelectionChanged(pruned);
    }
  }

  String _getRelativePath(String fullPath) {
    final folderPath = widget.folderPath;
    if (folderPath == null || folderPath.isEmpty) {
      return fullPath;
    }
    // Convert backslashes to forward slashes for consistent handling
    final normalizedFolder = folderPath.replaceAll('\\', '/');
    final normalizedPath = fullPath.replaceAll('\\', '/');
    if (normalizedPath.startsWith(normalizedFolder)) {
      final relative = normalizedPath.substring(normalizedFolder.length);
      // Remove leading slash if present
      return relative.startsWith('/') ? relative.substring(1) : relative;
    }
    return fullPath;
  }

  Future<void> _onDelete() async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Confirm Delete'),
        content: Text(
          'Are you sure you want to delete ${selected.length} file(s)?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            style: TextButton.styleFrom(foregroundColor: Colors.red),
            child: const Text('Delete'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    setState(() => _loading = true);
    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: '',
          planType: 'single_delete',
        );

        if (mounted) {
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Delete completed')));
            _loadFiles();
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Delete failed')),
            );
          }
        }
        return;
      }

      // Legacy path: direct gRPC calls without coordinator
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..planType = 'single_delete',
      );

      if (planResponse.operations.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('No operations to execute')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      if (planResponse.planId.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Planning failed: missing plan ID')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        // Stream events - could show progress
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        widget.onSelectionChanged({});
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Delete completed')));
        _loadFiles();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Delete failed: $e')));
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _openFolder() async {
    if (widget.onOpenFolder != null) {
      widget.onOpenFolder!.call();
      return;
    }
    final path = widget.folderPath;
    if (path == null || path.isEmpty) {
      return;
    }
    try {
      if (Platform.isWindows) {
        await Process.run('explorer', [path]);
      } else if (Platform.isMacOS) {
        await Process.run('open', [path]);
      } else if (Platform.isLinux) {
        await Process.run('xdg-open', [path]);
      }
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Failed to open folder')));
    }
  }

  Future<void> _onConvert() async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    if (!_selectionIsLossless(selected)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Convert only supports lossless source files (.wav/.flac)',
          ),
        ),
      );
      return;
    }

    final format = await _resolveConfiguredTargetFormat();
    if (format == null || format.isEmpty) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Convert failed: encoder config is invalid'),
        ),
      );
      return;
    }

    if (!mounted) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Confirm Convert'),
        content: Text(
          'Convert ${selected.length} file(s) to $format (encoder-configured)?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Convert'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    setState(() => _loading = true);
    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: format,
          planType: 'single_convert',
        );

        if (mounted) {
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Convert completed')));
            _loadFiles();
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Convert failed')),
            );
          }
        }
        if (mounted) {
          setState(() => _loading = false);
        }
        return;
      }

      // Legacy path: direct gRPC calls without coordinator
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..targetFormat = format
          ..planType = 'single_convert',
      );

      if (planResponse.operations.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('No operations to execute')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      if (planResponse.planId.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Planning failed: missing plan ID')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      // Execute the plan
      // Read softDelete from shared store if available
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        widget.onSelectionChanged({});
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Convert completed')));
        _loadFiles();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Convert failed: $e')));
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  bool _selectionIsLossless(Set<String> selected) {
    for (final path in selected) {
      final ext = path.split('.').last.toLowerCase();
      if (!_losslessFormats.contains(ext)) {
        return false;
      }
    }
    return true;
  }

  Future<String?> _resolveConfiguredTargetFormat() async {
    try {
      final response = await _client.getConfig(GetConfigRequest());
      if (response.configJson.isEmpty) {
        return null;
      }

      final decoded = jsonDecode(response.configJson);
      if (decoded is! Map<String, dynamic>) {
        return null;
      }

      final tools = decoded['tools'];
      if (tools is! Map<String, dynamic>) {
        return null;
      }

      final encoderRaw = tools['encoder'];
      if (encoderRaw is! String) {
        return null;
      }

      final encoder = encoderRaw.trim().toLowerCase();
      if (encoder == 'lame') {
        return 'mp3';
      }
      if (encoder == 'qaac') {
        return 'm4a';
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final selectedCount = widget.selectedPaths.length;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Action area
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerLow,
            border: Border(
              bottom: BorderSide(
                color: theme.colorScheme.outlineVariant,
                width: 1,
              ),
            ),
          ),
          child: Wrap(
            spacing: 4,
            children: [
              TextButton.icon(
                onPressed: widget.folderPath == null ? null : _openFolder,
                icon: const Icon(Icons.folder_open, size: 16),
                label: const Text('Open Folder'),
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 4,
                  ),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ),
              TextButton.icon(
                onPressed: _loadFiles,
                icon: const Icon(Icons.refresh, size: 16),
                label: const Text('Rescan'),
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 4,
                  ),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ),
              TextButton.icon(
                onPressed: widget.selectedPaths.isNotEmpty ? _onDelete : null,
                icon: const Icon(Icons.delete, size: 16),
                label: const Text('Delete'),
                style: TextButton.styleFrom(
                  foregroundColor: theme.colorScheme.error,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 4,
                  ),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ),
              TextButton.icon(
                onPressed: widget.selectedPaths.isNotEmpty ? _onConvert : null,
                icon: const Icon(Icons.transform, size: 16),
                label: const Text('Convert'),
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 4,
                  ),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ),
            ],
          ),
        ),
        // Header
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          color: theme.colorScheme.surfaceContainerHighest,
          child: Row(
            children: [
              Expanded(
                child: Text(
                  selectedCount > 0
                      ? 'Files ($selectedCount / ${_files.length} selected)'
                      : 'Files (${_files.length})',
                  style: theme.textTheme.labelLarge?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (_files.isNotEmpty) ...[
                // Checkbox(
                //   tristate: true,
                //   value: _allSelected
                //       ? true
                //       : (_partiallySelected ? null : false),
                //   onChanged: (_) {
                //     if (_allSelected || _partiallySelected) {
                //       _clearSelection();
                //     } else {
                //       _selectAll();
                //     }
                //   },
                // ),
                TextButton(
                  onPressed: _selectAll,
                  style: TextButton.styleFrom(
                    padding: const EdgeInsets.symmetric(horizontal: 6),
                    minimumSize: Size.zero,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: const Text('All', style: TextStyle(fontSize: 12)),
                ),
                TextButton(
                  onPressed: _clearSelection,
                  style: TextButton.styleFrom(
                    padding: const EdgeInsets.symmetric(horizontal: 6),
                    minimumSize: Size.zero,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: const Text('None', style: TextStyle(fontSize: 12)),
                ),
              ],
            ],
          ),
        ),
        // File list
        Expanded(
          child: widget.folderPath == null
              ? Center(
                  child: Text(
                    'Select a folder',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                )
              : _loading
              ? const Center(child: CircularProgressIndicator())
              : _error != null
              ? _ErrorTile(message: _error!, onRetry: _loadFiles)
              : _files.isEmpty
              ? Center(
                  child: Text(
                    'No files found',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                )
              : ListView.builder(
                  itemCount: _files.length,
                  itemBuilder: (context, index) {
                    final file = _files[index];
                    final isSelected = widget.selectedPaths.contains(file);
                    final filename = file.split(RegExp(r'[/\\]')).last;
                    final relativePath = _getRelativePath(file);
                    return CheckboxListTile(
                      value: isSelected,
                      onChanged: (_) => _toggleFile(file),
                      dense: true,
                      title: Text(
                        filename,
                        style: theme.textTheme.bodySmall?.copyWith(
                          fontFamily: 'SarasaUiSC',
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                      subtitle: Text(
                        relativePath,
                        style: theme.textTheme.labelSmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                          fontFamily: 'SarasaUiSC',
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                      secondary: Icon(
                        _iconForFile(filename),
                        size: 18,
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      controlAffinity: ListTileControlAffinity.leading,
                    );
                  },
                ),
        ),
      ],
    );
  }

  IconData _iconForFile(String filename) {
    final ext = filename.split('.').last.toLowerCase();
    switch (ext) {
      case 'mp3':
      case 'flac':
      case 'wav':
      case 'aac':
      case 'm4a':
      case 'ogg':
        return Icons.audiotrack;
      default:
        return Icons.insert_drive_file;
    }
  }
}

class _ErrorTile extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const _ErrorTile({required this.message, required this.onRetry});

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
