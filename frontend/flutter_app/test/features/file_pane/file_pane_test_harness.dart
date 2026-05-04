import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/files/file_pane_widget.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Full-featured fake OnseiService for file pane tests.
/// Records all incoming requests for verification and provides
/// configurable stub responses for PlanOperations, ListFiles,
/// ListFolders, ExecutePlan, RefreshFolders, and GetConfig.
class FakeOnseiService extends OnseiServiceBase {
  final List<PlanOperationsRequest> planRequests = [];
  final List<ExecutePlanRequest> executeRequests = [];
  final List<RefreshFoldersRequest> refreshRequests = [];

  PlanOperationsResponse? nextPlanResponse;
  List<String> nextFiles = [];
  List<FileListEntry> nextEntries = [];
  Stream<JobEvent> Function()? nextExecuteStreamFactory;
  String? nextConfigJson;

  // Call counters
  int refreshCallCount = 0;
  int planCallCount = 0;
  int executeCallCount = 0;

  // Flags tracking whether setFiles / setEntries were explicitly called,
  // so that explicit empty-list requests are honoured instead of
  // falling back to defaults.
  bool _hasSetFiles = false;
  bool _hasSetEntries = false;

  // Default files to return when listFiles is called.
  List<String> defaultFiles = [
    '/test/folder/audio1.mp3',
    '/test/folder/audio2.flac',
  ];

  // Default entries (empty by default; tests opt in via setEntries).
  final List<FileListEntry> defaultEntries = [];

  void reset() {
    planRequests.clear();
    executeRequests.clear();
    refreshRequests.clear();
    nextPlanResponse = null;
    nextFiles = [];
    nextEntries = [];
    nextExecuteStreamFactory = null;
    nextConfigJson = null;
    refreshCallCount = 0;
    planCallCount = 0;
    executeCallCount = 0;
    _hasSetFiles = false;
    _hasSetEntries = false;
    defaultFiles = [
      '/test/folder/audio1.mp3',
      '/test/folder/audio2.flac',
    ];
  }

  void setPlanResponse(PlanOperationsResponse response) {
    nextPlanResponse = response;
  }

  void setFiles(List<String> files) {
    nextFiles = files;
    _hasSetFiles = true;
  }

  void setEntries(List<FileListEntry> entries) {
    nextEntries = entries;
    _hasSetEntries = true;
  }

  void setDefaultFiles(List<String> files) {
    defaultFiles = files;
  }

  void setExecuteStreamFactory(Stream<JobEvent> Function() factory) {
    nextExecuteStreamFactory = factory;
  }

  void setConfigJson(String configJson) {
    nextConfigJson = configJson;
  }

  @override
  Future<GetConfigResponse> getConfig(
    ServiceCall call,
    GetConfigRequest request,
  ) async {
    return GetConfigResponse()
      ..configJson = nextConfigJson ?? '{"tools":{"encoder":"qaac"}}';
  }

  @override
  Future<UpdateConfigResponse> updateConfig(
    ServiceCall call,
    UpdateConfigRequest request,
  ) async {
    return UpdateConfigResponse();
  }

  @override
  Stream<JobEvent> scan(ServiceCall call, ScanRequest request) {
    return const Stream.empty();
  }

  @override
  Future<ListFoldersResponse> listFolders(
    ServiceCall call,
    ListFoldersRequest request,
  ) async {
    return ListFoldersResponse();
  }

  @override
  Future<ListFilesResponse> listFiles(
    ServiceCall call,
    ListFilesRequest request,
  ) async {
    final files = _hasSetFiles ? nextFiles : defaultFiles;
    final entries = _hasSetEntries ? nextEntries : defaultEntries;
    return ListFilesResponse()
      ..files.addAll(files)
      ..entries.addAll(entries);
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    ServiceCall call,
    PlanOperationsRequest request,
  ) async {
    planRequests.add(request);
    planCallCount++;
    if (nextPlanResponse != null) {
      return nextPlanResponse!;
    }
    return PlanOperationsResponse()
      ..planId = 'test-plan-${DateTime.now().millisecondsSinceEpoch}'
      ..operations.add(
        PlannedOperation()
          ..sourcePath = '/test/file.mp3'
          ..targetPath = '/test/output.mp3'
          ..operationType = 'convert',
      );
  }

  @override
  Stream<JobEvent> executePlan(ServiceCall call, ExecutePlanRequest request) {
    executeRequests.add(request);
    executeCallCount++;
    if (nextExecuteStreamFactory != null) {
      return nextExecuteStreamFactory!();
    }
    return Stream.fromIterable([
      JobEvent()
        ..eventType = 'info'
        ..message = 'Started',
      JobEvent()
        ..eventType = 'done'
        ..message = 'Completed',
    ]);
  }

  @override
  Future<ListPlansResponse> listPlans(
    ServiceCall call,
    ListPlansRequest request,
  ) async {
    return ListPlansResponse();
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    ServiceCall call,
    RefreshFoldersRequest request,
  ) async {
    refreshRequests.add(request);
    refreshCallCount++;
    return RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

/// Test harness holding the gRPC server, fake service, and channel
/// for file pane tests.
class FilePaneTestHarness {
  final Server server;
  final FakeOnseiService fakeService;
  final ClientChannel channel;

  FilePaneTestHarness({
    required this.server,
    required this.fakeService,
    required this.channel,
  });

  Future<void> tearDown() async {
    await channel.shutdown();
    await server.shutdown();
  }
}

/// Creates a minimal test harness with a running fake gRPC server.
Future<FilePaneTestHarness> createFilePaneTestHarness() async {
  final fakeService = FakeOnseiService();
  final server = Server.create(services: [fakeService]);
  await server.serve(port: 0);
  final channel = ClientChannel(
    'localhost',
    port: server.port as int,
    options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
  );
  return FilePaneTestHarness(
    server: server,
    fakeService: fakeService,
    channel: channel,
  );
}

/// Pumps until the file pane has finished the gRPC _loadFiles call,
/// alternating between real-event-loop bursts (runAsync) and widget
/// rebuilds (pump). Returns when the loading spinner disappears.
Future<void> settleGrpcLoad(WidgetTester tester) async {
  for (int i = 0; i < 30; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
    if (find.byType(CircularProgressIndicator).evaluate().isEmpty) return;
  }
  await tester.pump();
}

/// Centralised helper that pumps a [FilePaneWidget] into the test harness
/// and waits for the initial gRPC load to settle.
///
/// The returned [FilePaneWidget] Finder can be used to access the widget
/// state for calling trigger methods.
Future<Finder> pumpFilePane(
  WidgetTester tester, {
  required FilePaneTestHarness harness,
  required String rootPath,
  String? folderPath,
  Set<String> selectedPaths = const {},
  void Function(Set<String>)? onSelectionChanged,
  WorkflowStateStore? workflowStateStore,
  FilePaneCoordinator? coordinator,
}) async {
  final paths = Set<String>.from(selectedPaths);
  void handler(Set<String> p) {
    paths.clear();
    paths.addAll(p);
    onSelectionChanged?.call(paths);
  }

  await tester.runAsync(() async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              return FilePaneWidget(
                channel: harness.channel,
                rootPath: rootPath,
                folderPath: folderPath,
                selectedPaths: paths,
                onSelectionChanged: handler,
                workflowStateStore: workflowStateStore,
                coordinator: coordinator,
              );
            },
          ),
        ),
      ),
    );
  });
  await tester.pump();

  if (folderPath != null && folderPath.isNotEmpty) {
    await settleGrpcLoad(tester);
  }

  return find.byType(FilePaneWidget);
}
