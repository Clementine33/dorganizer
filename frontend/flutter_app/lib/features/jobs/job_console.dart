/// JobEvent represents a job status event
class JobEvent {
  final String jobId;
  final String status; // 'started', 'progress', 'completed', 'failed'
  final String? message;
  final double? progress;

  JobEvent({
    required this.jobId,
    required this.status,
    this.message,
    this.progress,
  });
}

/// JobConsole manages job output and events
class JobConsole {
  final List<JobEvent> events;
  final Map<String, String> jobStatuses;

  JobConsole({
    this.events = const [],
    this.jobStatuses = const {},
  });

  /// Add an event
  JobConsole addEvent(JobEvent event) {
    return JobConsole(
      events: [...events, event],
      jobStatuses: {...jobStatuses, event.jobId: event.status},
    );
  }

  /// Get events for a specific job
  List<JobEvent> getEventsForJob(String jobId) {
    return events.where((e) => e.jobId == jobId).toList();
  }

  /// Check if a job is complete
  bool isJobComplete(String jobId) {
    final status = jobStatuses[jobId];
    return status == 'completed' || status == 'failed';
  }
}
