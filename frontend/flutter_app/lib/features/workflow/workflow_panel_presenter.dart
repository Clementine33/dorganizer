part of 'workflow_panel_widget.dart';

String _operationLabel(WorkflowOperation operation) {
  return switch (operation) {
    WorkflowOperation.slimModeI => 'Slim Mode I: Delete Losslsess',
    WorkflowOperation.slimModeII => 'Slim Mode II: Smart Convert',
    WorkflowOperation.pruneWavFlac => 'Prune wav/flac',
    WorkflowOperation.pruneMp3M4a => 'Prune mp3/m4a',
    WorkflowOperation.pruneBoth => 'Prune both',
  };
}

String _getRelativePath(
  String fullPath, {
  required String? selectedRoot,
  required String? selectedFolder,
}) {
  final rootBase = selectedRoot ?? selectedFolder ?? '';
  if (rootBase.isEmpty) return fullPath;
  final normalizedRoot = rootBase.replaceAll('\\', '/');
  final normalizedPath = fullPath.replaceAll('\\', '/');
  if (normalizedPath.startsWith(normalizedRoot)) {
    final rel = normalizedPath.substring(normalizedRoot.length);
    final stripped = rel.startsWith('/') ? rel.substring(1) : rel;
    if (stripped.isEmpty) return fullPath;

    // Shorten the first path segment (the immediate child folder).
    final segments = stripped.split('/');
    final firstSeg = segments.first;
    final rjMatch = RegExp(r'RJ\d{6,8}').firstMatch(firstSeg);
    final shortSeg = rjMatch != null
        ? rjMatch.group(0)!
        : firstSeg.substring(0, firstSeg.length.clamp(0, 10));

    segments[0] = shortSeg;
    return segments.join('/');
  }
  return fullPath;
}

String _planScopeText(
  String? lastPlanId,
  int lastPlanFolderCount,
  int lastPlanFileCount,
) {
  if (lastPlanId == null) {
    return 'No plan yet';
  }
  if (lastPlanFolderCount > 0) {
    return '$lastPlanFolderCount folder(s)';
  }
  if (lastPlanFileCount > 0) {
    return '$lastPlanFileCount file(s)';
  }
  return '0 item(s)';
}

String _planMetaText(String? lastPlanId, String? lastPlanSummary) {
  if (lastPlanId == null) {
    return 'Run Plan to create plan';
  }
  if (lastPlanSummary == null || lastPlanSummary.isEmpty) {
    return lastPlanId;
  }
  return '$lastPlanId ($lastPlanSummary)';
}

String _phaseLabel(WorkflowPhase phase) {
  return switch (phase) {
    WorkflowPhase.idle => 'Idle',
    WorkflowPhase.planning => 'Planning...',
    WorkflowPhase.planned => 'Planned',
    WorkflowPhase.executing => 'Executing...',
    WorkflowPhase.done => 'Done',
  };
}

Color _phaseColor(WorkflowPhase phase) {
  return switch (phase) {
    WorkflowPhase.idle => Colors.grey,
    WorkflowPhase.planning => Colors.orange,
    WorkflowPhase.planned => Colors.teal,
    WorkflowPhase.executing => Colors.blue,
    WorkflowPhase.done => Colors.green,
  };
}

String _formatTime(DateTime t) =>
    '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}:${t.second.toString().padLeft(2, '0')}';
