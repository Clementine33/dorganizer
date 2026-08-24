import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:grpc/grpc.dart';

import '../../gen/onsei/v1/service.pbgrpc.dart';
import '../workflow/plan_execution_coordinator.dart';
import '../workflow/workflow_state_store.dart';
import 'file_pane_models.dart';
import 'file_pane_selection.dart';

part 'file_pane_view.dart';
part 'file_pane_actions.dart';
part 'file_pane_sections.dart';

class FilePaneWidget extends StatefulWidget {
  final ClientChannel channel;
  final String? rootPath;
  final String? folderPath;
  final Set<String> selectedPaths;
  final void Function(Set<String> selectedPaths) onSelectionChanged;
  final VoidCallback? onOpenFolder;

  /// Shared workflow state store for softDelete setting.
  /// If provided, execute operations read softDelete from this store.
  final WorkflowStateStore? workflowStateStore;

  /// Coordinator for unified execute flow.
  /// If provided, convert/delete operations use this coordinator.
  final FilePaneCoordinator? coordinator;

  const FilePaneWidget({
    super.key,
    required this.channel,
    required this.rootPath,
    required this.folderPath,
    required this.selectedPaths,
    required this.onSelectionChanged,
    this.onOpenFolder,
    this.workflowStateStore,
    this.coordinator,
  });

  @override
  State<FilePaneWidget> createState() => _FilePaneWidgetState();
}
