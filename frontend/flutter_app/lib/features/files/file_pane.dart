/// FileInfo represents a scanned audio file
class FileInfo {
  final String path;
  final String name;
  final int size;
  final String? format;

  FileInfo({
    required this.path,
    required this.name,
    required this.size,
    this.format,
  });
}

/// FilePane displays the file listing UI
class FilePane {
  final List<FileInfo> files;
  final Set<String> selectedPaths;

  FilePane({
    this.files = const [],
    this.selectedPaths = const {},
  });

  /// Get selected files
  List<FileInfo> get selectedFiles {
    return files.where((f) => selectedPaths.contains(f.path)).toList();
  }

  /// Toggle file selection
  FilePane toggleSelection(String path) {
    final newSelection = Set<String>.from(selectedPaths);
    if (newSelection.contains(path)) {
      newSelection.remove(path);
    } else {
      newSelection.add(path);
    }
    return FilePane(files: files, selectedPaths: newSelection);
  }

  /// Select all files
  FilePane selectAll() {
    return FilePane(
      files: files,
      selectedPaths: files.map((f) => f.path).toSet(),
    );
  }
}
