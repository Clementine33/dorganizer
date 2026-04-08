/// Represents error state for a single folder during workflow operations
class FolderErrorState {
  /// True if the plan stage produced an error for this folder
  final bool planHasError;

  /// True if the execute stage produced an error for this folder
  final bool executeHasError;

  /// True if any error exists (plan or execute)
  bool get hasError => planHasError || executeHasError;

  /// Last event ID that reported status for this folder
  final String? lastEventId;

  /// Last error code if any
  final String? lastCode;

  /// Last error message if any
  final String? lastMessage;

  /// Last stage that reported status (plan or execute)
  final String? lastStage;

  /// When this state was last updated
  final DateTime updatedAt;

  const FolderErrorState({
    this.planHasError = false,
    this.executeHasError = false,
    this.lastEventId,
    this.lastCode,
    this.lastMessage,
    this.lastStage,
    required this.updatedAt,
  });

  /// Create a copy with updated fields
  FolderErrorState copyWith({
    bool? planHasError,
    bool? executeHasError,
    String? lastEventId,
    String? lastCode,
    String? lastMessage,
    String? lastStage,
    DateTime? updatedAt,
  }) {
    return FolderErrorState(
      planHasError: planHasError ?? this.planHasError,
      executeHasError: executeHasError ?? this.executeHasError,
      lastEventId: lastEventId ?? this.lastEventId,
      lastCode: lastCode ?? this.lastCode,
      lastMessage: lastMessage ?? this.lastMessage,
      lastStage: lastStage ?? this.lastStage,
      updatedAt: updatedAt ?? this.updatedAt,
    );
  }

  /// Create an error state from a plan error
  factory FolderErrorState.planError({
    required String eventId,
    String? code,
    String? message,
    required DateTime updatedAt,
  }) {
    return FolderErrorState(
      planHasError: true,
      executeHasError: false,
      lastEventId: eventId,
      lastCode: code,
      lastMessage: message,
      lastStage: 'plan',
      updatedAt: updatedAt,
    );
  }

  /// Create an error state from an execute error
  factory FolderErrorState.executeError({
    required String eventId,
    String? code,
    String? message,
    required DateTime updatedAt,
  }) {
    return FolderErrorState(
      planHasError: false,
      executeHasError: true,
      lastEventId: eventId,
      lastCode: code,
      lastMessage: message,
      lastStage: 'execute',
      updatedAt: updatedAt,
    );
  }

  /// Create a clear/no-error state
  factory FolderErrorState.clear({
    required DateTime updatedAt,
  }) {
    return FolderErrorState(
      planHasError: false,
      executeHasError: false,
      lastEventId: null,
      lastCode: null,
      lastMessage: null,
      lastStage: null,
      updatedAt: updatedAt,
    );
  }
}
