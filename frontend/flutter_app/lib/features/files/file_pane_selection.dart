/// Checks if all paths in [selected] have a lossless extension (wav, flac).
bool selectionIsLossless(Set<String> selected) {
  const losslessFormats = {'wav', 'flac'};
  for (final path in selected) {
    final ext = path.split('.').last.toLowerCase();
    if (!losslessFormats.contains(ext)) {
      return false;
    }
  }
  return true;
}

/// Computes a relative path of [fullPath] against [folderPath].
String getRelativePath(String fullPath, String? folderPath) {
  if (folderPath == null || folderPath.isEmpty) {
    return fullPath;
  }
  // Normalize to forward slashes, strip trailing slash for boundary check
  final normalizedFolder = folderPath.replaceAll('\\', '/');
  final folder = normalizedFolder.endsWith('/')
      ? normalizedFolder.substring(0, normalizedFolder.length - 1)
      : normalizedFolder;
  final normalizedPath = fullPath.replaceAll('\\', '/');
  // Boundary-aware match: folder must be followed by '/' or be the entire path,
  // to avoid prefix collisions (e.g. /music matching /music2/...)
  if (normalizedPath.startsWith(folder) &&
      (normalizedPath.length == folder.length ||
       normalizedPath[folder.length] == '/')) {
    final relative = normalizedPath.substring(folder.length);
    return relative.startsWith('/') ? relative.substring(1) : relative;
  }
  return fullPath;
}

/// Prunes [current] to only include paths that exist in [existing].
/// Returns a new set if pruned, or the original set if unchanged.
Set<String> reconcileSelection(Set<String> current, Set<String> existing) {
  if (current.isEmpty) return current;
  final pruned = current.where(existing.contains).toSet();
  return pruned.length != current.length ? pruned : current;
}
