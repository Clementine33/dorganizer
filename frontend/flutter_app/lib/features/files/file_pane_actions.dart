// ignore_for_file: invalid_use_of_protected_member
// setState is @protected but is safely called here from an extension defined
// in the same library as _FilePaneWidgetState (which extends State).
part of 'file_pane_widget.dart';

/// Extension on [_FilePaneWidgetState] containing action logic
/// extracted from the view for separation of concerns.
extension _FilePaneActions on _FilePaneWidgetState {
  // -----------------------------------------------------------------------
  // triggerDelete – @visibleForTesting entrypoint (no confirmation dialog)
  // -----------------------------------------------------------------------

  Future<void> performDelete() async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    setState(() => _loading = true);

    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: '',
          planType: 'single_delete',
        );

        if (mounted) {
          setState(() => _loading = false);
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Delete completed')));
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Delete failed')),
            );
          }
        }
        if (mounted) {
          setState(() => _loading = false);
        }
        return;
      }

      // Legacy path: direct gRPC calls
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..planType = 'single_delete',
      );

      if (planResponse.operations.isEmpty || planResponse.planId.isEmpty) {
        if (mounted) {
          setState(() => _loading = false);
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Planning failed')));
        }
        return;
      }

      // Execute plan and check for errors
      String? errorMessage;
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        if (event.eventType == 'error') {
          errorMessage = event.message;
        }
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        setState(() => _loading = false);
        if (errorMessage != null) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text(errorMessage)));
        } else {
          widget.onSelectionChanged({});
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Delete completed')));
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() => _loading = false);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Delete failed: $e')));
      }
    }
  }

  // -----------------------------------------------------------------------
  // triggerConvert – @visibleForTesting entrypoint (no confirmation dialog)
  // -----------------------------------------------------------------------

  Future<void> performConvert(String format) async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      return;
    }

    if (!_selectionIsLossless(selected)) {
      return;
    }

    final configuredFormat = await _resolveConfiguredTargetFormat();
    final targetFormat = configuredFormat ?? format;
    if (targetFormat.isEmpty) {
      return;
    }

    setState(() => _loading = true);

    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: targetFormat,
          planType: 'single_convert',
        );

        if (mounted) {
          setState(() => _loading = false);
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Convert completed')));
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Convert failed')),
            );
          }
        }
        return;
      }

      // Legacy path: direct gRPC calls
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..targetFormat = targetFormat
          ..planType = 'single_convert',
      );

      if (planResponse.operations.isEmpty || planResponse.planId.isEmpty) {
        if (mounted) {
          setState(() => _loading = false);
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Planning failed')));
        }
        return;
      }

      String? errorMessage;
      // Read softDelete from shared store if available
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        if (event.eventType == 'error') {
          errorMessage = event.message;
        }
      }

      if (mounted) {
        setState(() => _loading = false);
        if (errorMessage != null) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text(errorMessage)));
        } else {
          widget.onSelectionChanged({});
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('Convert completed')));
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  // -----------------------------------------------------------------------
  // UI action: Delete with confirmation dialog
  // -----------------------------------------------------------------------

  Future<void> deleteWithConfirmation() async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Confirm Delete'),
        content: Text(
          'Are you sure you want to delete ${selected.length} file(s)?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            style: TextButton.styleFrom(foregroundColor: Colors.red),
            child: const Text('Delete'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    setState(() => _loading = true);
    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: '',
          planType: 'single_delete',
        );

        if (mounted) {
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Delete completed')));
            _loadFiles();
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Delete failed')),
            );
          }
        }
        if (mounted) {
          setState(() => _loading = false);
        }
        return;
      }

      // Legacy path: direct gRPC calls without coordinator
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..planType = 'single_delete',
      );

      if (planResponse.operations.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('No operations to execute')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      if (planResponse.planId.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Planning failed: missing plan ID')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        // Stream events - could show progress
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        widget.onSelectionChanged({});
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Delete completed')));
        _loadFiles();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Delete failed: $e')));
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  // -----------------------------------------------------------------------
  // UI action: Convert with validation and confirmation dialog
  // -----------------------------------------------------------------------

  Future<void> convertWithConfirmation() async {
    final selected = widget.selectedPaths;
    if (selected.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('No files selected')));
      return;
    }

    if (!_selectionIsLossless(selected)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Convert only supports lossless source files (.wav/.flac)',
          ),
        ),
      );
      return;
    }

    final format = await _resolveConfiguredTargetFormat();
    if (format == null || format.isEmpty) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Convert failed: encoder config is invalid'),
        ),
      );
      return;
    }

    if (!mounted) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Confirm Convert'),
        content: Text(
          'Convert ${selected.length} file(s) to $format (encoder-configured)?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Convert'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    setState(() => _loading = true);
    try {
      // Use coordinator if available
      if (widget.coordinator != null) {
        final result = await widget.coordinator!.executeFlowForFiles(
          rootPath: widget.rootPath ?? widget.folderPath ?? '',
          folderPath: widget.folderPath,
          selectedFiles: selected,
          targetFormat: format,
          planType: 'single_convert',
        );

        if (mounted) {
          if (result.success) {
            widget.onSelectionChanged({});
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Convert completed')));
            _loadFiles();
          } else {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(result.errorMessage ?? 'Convert failed')),
            );
          }
        }
        if (mounted) {
          setState(() => _loading = false);
        }
        return;
      }

      // Legacy path: direct gRPC calls without coordinator
      final planResponse = await _client.planOperations(
        PlanOperationsRequest()
          ..sourceFiles.addAll(selected)
          ..folderPath =
              (widget.rootPath != null && widget.rootPath!.isNotEmpty)
              ? widget.rootPath!
              : (widget.folderPath ?? '')
          ..targetFormat = format
          ..planType = 'single_convert',
      );

      if (planResponse.operations.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('No operations to execute')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      if (planResponse.planId.isEmpty) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Planning failed: missing plan ID')),
          );
          setState(() => _loading = false);
        }
        return;
      }

      // Execute the plan
      // Read softDelete from shared store if available
      await for (final event in _client.executePlan(
        ExecutePlanRequest()
          ..planId = planResponse.planId
          ..softDelete = widget.workflowStateStore?.softDelete ?? true,
      )) {
        debugPrint('Job event: ${event.eventType} - ${event.message}');
      }

      if (mounted) {
        widget.onSelectionChanged({});
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Convert completed')));
        _loadFiles();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Convert failed: $e')));
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  // -----------------------------------------------------------------------
  // Helpers
  // -----------------------------------------------------------------------

  bool _selectionIsLossless(Set<String> selected) {
    return selectionIsLossless(selected);
  }

  Future<String?> _resolveConfiguredTargetFormat() async {
    try {
      final response = await _client.getConfig(GetConfigRequest());
      if (response.configJson.isEmpty) {
        return null;
      }

      final decoded = jsonDecode(response.configJson);
      if (decoded is! Map<String, dynamic>) {
        return null;
      }

      final tools = decoded['tools'];
      if (tools is! Map<String, dynamic>) {
        return null;
      }

      final encoderRaw = tools['encoder'];
      if (encoderRaw is! String) {
        return null;
      }

      final encoder = encoderRaw.trim().toLowerCase();
      if (encoder == 'lame') {
        return 'mp3';
      }
      if (encoder == 'qaac') {
        return 'm4a';
      }
      return null;
    } catch (_) {
      return null;
    }
  }
}
