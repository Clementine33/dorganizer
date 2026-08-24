part of 'file_pane_widget.dart';

// ---------------------------------------------------------------------------
// _ErrorTile
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// _FilePaneActionBar
// ---------------------------------------------------------------------------

class _FilePaneActionBar extends StatelessWidget {
  final VoidCallback? onOpenFolder;
  final VoidCallback onRescan;
  final VoidCallback? onDelete;
  final VoidCallback? onConvert;

  const _FilePaneActionBar({
    required this.onOpenFolder,
    required this.onRescan,
    required this.onDelete,
    required this.onConvert,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
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
            onPressed: onOpenFolder,
            icon: const Icon(Icons.folder_open, size: 16),
            label: const Text('Open Folder'),
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
          TextButton.icon(
            onPressed: onRescan,
            icon: const Icon(Icons.refresh, size: 16),
            label: const Text('Rescan'),
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
          TextButton.icon(
            onPressed: onDelete,
            icon: const Icon(Icons.delete, size: 16),
            label: const Text('Delete'),
            style: TextButton.styleFrom(
              foregroundColor: theme.colorScheme.error,
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
          TextButton.icon(
            onPressed: onConvert,
            icon: const Icon(Icons.transform, size: 16),
            label: const Text('Convert'),
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// _FilePaneHeader
// ---------------------------------------------------------------------------

class _FilePaneHeader extends StatelessWidget {
  final int selectedCount;
  final int totalCount;
  final bool hasFiles;
  final VoidCallback onSelectAll;
  final VoidCallback onClearSelection;
  final VoidCallback onInvertSelection;

  const _FilePaneHeader({
    required this.selectedCount,
    required this.totalCount,
    required this.hasFiles,
    required this.onSelectAll,
    required this.onClearSelection,
    required this.onInvertSelection,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      color: theme.colorScheme.surfaceContainerHighest,
      child: Row(
        children: [
          Expanded(
            child: Text(
              selectedCount > 0
                  ? 'Files ($selectedCount / $totalCount selected)'
                  : 'Files ($totalCount)',
              style: theme.textTheme.labelLarge?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (hasFiles) ...[
            TextButton(
              onPressed: onSelectAll,
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: const Text('All', style: TextStyle(fontSize: 12)),
            ),
            TextButton(
              onPressed: onClearSelection,
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: const Text('None', style: TextStyle(fontSize: 12)),
            ),
            TextButton(
              onPressed: onInvertSelection,
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: const Text('Invert', style: TextStyle(fontSize: 12)),
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// _FilePaneFileListBody
// ---------------------------------------------------------------------------

class _FilePaneFileListBody extends StatelessWidget {
  final String? folderPath;
  final bool loading;
  final String? error;
  final List<FileEntry> files;
  final Set<String> selectedPaths;
  final ValueChanged<String> onToggleFile;
  final String Function(String) getRelativePath;
  final VoidCallback onRetry;

  const _FilePaneFileListBody({
    required this.folderPath,
    required this.loading,
    required this.error,
    required this.files,
    required this.selectedPaths,
    required this.onToggleFile,
    required this.getRelativePath,
    required this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    if (folderPath == null) {
      return Center(
        child: Text(
          'Select a folder',
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      );
    }

    if (loading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (error != null) {
      return _ErrorTile(message: error!, onRetry: onRetry);
    }

    if (files.isEmpty) {
      return Center(
        child: Text(
          'No files found',
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      );
    }

    return ListView.builder(
      itemCount: files.length,
      itemBuilder: (context, index) {
        final entry = files[index];
        final filePath = entry.path;
        final isSelected = selectedPaths.contains(filePath);
        final filename = filePath.split(RegExp(r'[/\\]')).last;
        final relativePath = getRelativePath(filePath);
        return CheckboxListTile(
          value: isSelected,
          onChanged: (_) => onToggleFile(filePath),
          dense: true,
          title: Row(
            children: [
              Expanded(
                child: Text(
                  filename,
                  style: theme.textTheme.bodySmall?.copyWith(
                    fontFamily: 'SarasaUiSC',
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (entry.showBitrateBadge)
                Container(
                  margin: const EdgeInsets.only(left: 6),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 5,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: theme.colorScheme.secondaryContainer,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    entry.bitrateLabel,
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSecondaryContainer,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
            ],
          ),
          subtitle: Text(
            relativePath,
            style: theme.textTheme.labelSmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
              fontFamily: 'SarasaUiSC',
            ),
            overflow: TextOverflow.ellipsis,
          ),
          controlAffinity: ListTileControlAffinity.leading,
        );
      },
    );
  }
}
