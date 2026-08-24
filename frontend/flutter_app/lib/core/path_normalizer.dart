String normalizePathForComparison(String path) {
  var normalized = path.trim();
  if (normalized.isEmpty) {
    return '';
  }

  normalized = normalized.replaceAll('\\', '/');

  final hadUncPrefix = normalized.startsWith('//');
  final hadSingleRootPrefix = !hadUncPrefix && normalized.startsWith('/');

  normalized = normalized.replaceAll(RegExp(r'/+'), '/');

  if (hadUncPrefix) {
    normalized = normalized.replaceFirst(RegExp(r'^/+'), '');
    normalized = '//$normalized';
  } else if (hadSingleRootPrefix) {
    normalized = normalized.replaceFirst(RegExp(r'^/+'), '');
    normalized = '/$normalized';
  }

  if (normalized.length > 1) {
    if (hadUncPrefix) {
      normalized = normalized.replaceFirst(RegExp(r'/+$'), '');
    } else {
      normalized = normalized.replaceFirst(RegExp(r'(?<!^)/+$'), '');
    }
  }

  final isWindowsStyle =
      hadUncPrefix || RegExp(r'^[A-Za-z]:/').hasMatch(normalized);
  if (isWindowsStyle) {
    normalized = normalized.toLowerCase();
  }

  return normalized;
}
