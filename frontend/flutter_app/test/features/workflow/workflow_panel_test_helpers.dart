import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/plan_execution_coordinator.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Fake OnseiService that records incoming requests for testing.
/// Provides stub responses for PlanOperations, ListFiles, ExecutePlan, and ListPlans.
class FakeOnseiService extends OnseiServiceBase {
  final List<PlanOperationsRequest> planRequests = [];
  final List<ExecutePlanRequest> executeRequests = [];
  final List<ListPlansRequest> listPlansRequests = [];
  PlanOperationsResponse? nextPlanResponse;
  List<String> nextFiles = [];
  Stream<JobEvent>? nextExecuteStream;
  ListPlansResponse? nextListPlansResponse;

  // Default files to return when listFiles is called
  final List<String> defaultFiles = [
    '/test/folder/audio1.mp3',
    '/test/folder/audio2.flac',
  ];

  void setListPlansResponse(ListPlansResponse response) {
    nextListPlansResponse = response;
  }

  void reset() {
    planRequests.clear();
    executeRequests.clear();
    listPlansRequests.clear();
    nextPlanResponse = null;
    nextFiles = [];
    nextExecuteStream = null;
    nextListPlansResponse = null;
  }

  void setPlanResponse(PlanOperationsResponse response) {
    nextPlanResponse = response;
  }

  void setFiles(List<String> files) {
    nextFiles = files;
  }

  void setExecuteStream(Stream<JobEvent> stream) {
    nextExecuteStream = stream;
  }

  @override
  Future<GetConfigResponse> getConfig(
    ServiceCall call,
    GetConfigRequest request,
  ) async {
    return GetConfigResponse();
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
    final files = nextFiles.isNotEmpty ? nextFiles : defaultFiles;
    return ListFilesResponse()..files.addAll(files);
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    ServiceCall call,
    PlanOperationsRequest request,
  ) async {
    planRequests.add(request);
    // Return persisted nextPlanResponse if set, otherwise default
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
    return nextExecuteStream ??
        Stream.fromIterable([
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
    listPlansRequests.add(request);
    if (nextListPlansResponse != null) {
      return nextListPlansResponse!;
    }
    return ListPlansResponse();
  }

  @override
  Future<RefreshFoldersResponse> refreshFolders(
    ServiceCall call,
    RefreshFoldersRequest request,
  ) async {
    return RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

/// Creates a test Server that runs the fake service.
Future<Server> createTestServer() async {
  final server = Server.create(services: [FakeOnseiService()]);
  await server.serve(port: 0);
  return server;
}

class SpyPlanExecutionCoordinator extends PlanExecutionCoordinator {
  int planOnlyForFoldersCalls = 0;
  int executeByPlanIdCalls = 0;
  int executeFlowForFoldersCalls = 0;
  String? lastRootPathForValidation;

  SpyPlanExecutionCoordinator({required super.channel, required super.store});

  @override
  Future<PlanOperationsResponse> planOnlyForFolders({
    required String rootPath,
    required Set<String> selectedFolders,
    required String targetFormat,
    required String planType,
    bool pruneMatchedExcluded = false,
  }) async {
    planOnlyForFoldersCalls += 1;
    return PlanOperationsResponse()
      ..planId = 'workflow-plan-locked-1'
      ..operations.add(
        PlannedOperation()
          ..sourcePath = '/test/audio1.mp3'
          ..operationType = 'slim',
      );
  }

  @override
  Future<PlanExecutionResult> executeByPlanId({
    required String planId,
    String? rootPathForValidation,
    ExecuteEventListener? onEvent,
  }) async {
    executeByPlanIdCalls += 1;
    lastRootPathForValidation = rootPathForValidation;
    final event = JobEvent()
      ..eventType = 'done'
      ..message = 'ok';
    onEvent?.call(event);
    return PlanExecutionResult.success(planId: planId, events: [event]);
  }

  @override
  Future<PlanExecutionResult> executeFlowForFolders({
    required String rootPath,
    required Set<String> selectedFolders,
    required String targetFormat,
    required String planType,
  }) async {
    executeFlowForFoldersCalls += 1;
    return PlanExecutionResult.success(
      planId: 'unexpected',
      events: [
        JobEvent()
          ..eventType = 'done'
          ..message = 'ok',
      ],
    );
  }
}
