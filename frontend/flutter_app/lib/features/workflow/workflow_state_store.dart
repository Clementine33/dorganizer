import 'package:flutter/foundation.dart';
import 'package:grpc/grpc.dart';
import '../../gen/onsei/v1/service.pbgrpc.dart';

/// Shared state store for workflow and file pane widgets.
///
/// Provides a single source of truth for:
/// - softDelete setting (used by both workflow execute and file pane convert/delete)
/// - refreshFolders API action (routed through store for centralized access)
class WorkflowStateStore extends ChangeNotifier {
  final ClientChannel _channel;
  late final OnseiServiceClient _client;

  bool _softDelete = true;
  bool _slimMode2PruneMatchedExcluded = false;

  WorkflowStateStore({required ClientChannel channel}) : _channel = channel {
    _client = OnseiServiceClient(_channel);
  }

  /// Current soft-delete setting.
  /// When true, files are moved to trash instead of permanently deleted.
  bool get softDelete => _softDelete;

  /// Update the soft-delete setting and notify listeners.
  void setSoftDelete(bool value) {
    if (_softDelete != value) {
      _softDelete = value;
      notifyListeners();
    }
  }

  /// Whether Slim Mode II should exclude entries matched by configured pattern.
  bool get slimMode2PruneMatchedExcluded => _slimMode2PruneMatchedExcluded;

  /// Update Slim Mode II matched-entry exclusion switch and notify listeners.
  void setSlimMode2PruneMatchedExcluded(bool value) {
    if (_slimMode2PruneMatchedExcluded != value) {
      _slimMode2PruneMatchedExcluded = value;
      notifyListeners();
    }
  }

  /// Refresh folder metadata via backend gRPC call.
  ///
  /// Calls the backend RefreshFolders RPC which performs folder-scoped scanning.
  /// Returns the response containing successful folders and any errors.
  Future<RefreshFoldersResponse> refreshFolders({
    required String rootPath,
    required List<String> folderPaths,
  }) async {
    final request = RefreshFoldersRequest()
      ..rootPath = rootPath
      ..folderPaths.addAll(folderPaths);

    return await _client.refreshFolders(request);
  }
}
