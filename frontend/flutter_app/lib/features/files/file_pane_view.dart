part of 'file_pane_widget.dart';

class _FilePaneWidgetState extends State<FilePaneWidget> {
  late OnseiServiceClient _client;
  List<FileEntry> _files = [];
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
        final List<FileEntry> entries;
        if (response.files.isNotEmpty) {
          // Files is canonical: build rows from files, overlay bitrate
          // from entries by matching path.
          final bitrateByPath = <String, int>{};
          for (final e in response.entries) {
            bitrateByPath[e.path] = e.bitrate;
          }
          entries = response.files
              .map((f) => FileEntry(path: f, bitrate: bitrateByPath[f] ?? 0))
              .toList();
        } else if (response.entries.isNotEmpty) {
          // No files: use entries directly.
          entries = response.entries
              .map((e) => FileEntry(path: e.path, bitrate: e.bitrate))
              .toList();
        } else {
          entries = [];
        }
        _reconcileSelection(entries);
        setState(() {
          _files = entries;
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
    widget.onSelectionChanged(_files.map((e) => e.path).toSet());
  }

  void _clearSelection() {
    widget.onSelectionChanged({});
  }

  /// Bridges for use by tests. Actual logic lives in [performDelete] extension.
  @visibleForTesting
  Future<void> triggerDelete() async => performDelete();

  /// Bridges for use by tests. Actual logic lives in [performConvert] extension.
  @visibleForTesting
  Future<void> triggerConvert(String format) async => performConvert(format);

  void _reconcileSelection(List<FileEntry> refreshedFiles) {
    final existing = refreshedFiles.map((e) => e.path).toSet();
    final pruned = reconcileSelection(widget.selectedPaths, existing);
    if (pruned.length != widget.selectedPaths.length) {
      widget.onSelectionChanged(pruned);
    }
  }

  String _getRelativePath(String fullPath) {
    return getRelativePath(fullPath, widget.folderPath);
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

  @override
  Widget build(BuildContext context) {
    final selectedCount = widget.selectedPaths.length;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _FilePaneActionBar(
          onOpenFolder: widget.folderPath == null ? null : _openFolder,
          onRescan: _loadFiles,
          onDelete: widget.selectedPaths.isNotEmpty
              ? () => deleteWithConfirmation()
              : null,
          onConvert: widget.selectedPaths.isNotEmpty
              ? () => convertWithConfirmation()
              : null,
        ),
        _FilePaneHeader(
          selectedCount: selectedCount,
          totalCount: _files.length,
          hasFiles: _files.isNotEmpty,
          onSelectAll: _selectAll,
          onClearSelection: _clearSelection,
        ),
        Expanded(
          child: _FilePaneFileListBody(
            folderPath: widget.folderPath,
            loading: _loading,
            error: _error,
            files: _files,
            selectedPaths: widget.selectedPaths,
            onToggleFile: _toggleFile,
            getRelativePath: _getRelativePath,
            onRetry: _loadFiles,
          ),
        ),
      ],
    );
  }

  // ignore: unused_element — referenced only in commented-out secondary icon
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

