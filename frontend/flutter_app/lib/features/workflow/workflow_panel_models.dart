part of 'workflow_panel_widget.dart';

enum WorkflowPhase { idle, planning, planned, executing, done }

enum SlimMode { modeI, modeII }

enum PruneMode { wavFlac, mp3Aac, both }

enum WorkflowOperation {
  slimModeI,
  slimModeII,
  pruneWavFlac,
  pruneMp3M4a,
  pruneBoth,
}

class _LogEntry {
  final String message;
  final DateTime timestamp;
  final bool isError;

  _LogEntry({
    required this.message,
    required this.timestamp,
    required this.isError,
  });
}

class _EventKey {
  final String rootPath;
  final String folderPath;
  final String eventId;

  const _EventKey(this.rootPath, this.folderPath, this.eventId);

  @override
  bool operator ==(Object other) {
    return other is _EventKey &&
        other.rootPath == rootPath &&
        other.folderPath == folderPath &&
        other.eventId == eventId;
  }

  @override
  int get hashCode => Object.hash(rootPath, folderPath, eventId);
}
