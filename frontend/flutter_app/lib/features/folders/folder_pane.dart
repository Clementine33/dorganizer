/// FolderPane displays the folder selection and listing UI
class FolderPane {
  final String? selectedPath;
  final List<String> folders;

  FolderPane({
    this.selectedPath,
    this.folders = const [],
  });

  /// Select a folder
  FolderPane selectFolder(String path) {
    return FolderPane(
      selectedPath: path,
      folders: folders,
    );
  }

  /// Add a folder to the list
  FolderPane addFolder(String path) {
    return FolderPane(
      selectedPath: selectedPath,
      folders: [...folders, path],
    );
  }
}
