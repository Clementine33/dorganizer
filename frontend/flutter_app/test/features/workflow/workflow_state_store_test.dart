import 'package:flutter_test/flutter_test.dart';
import 'package:grpc/grpc.dart';
import 'package:onsei_organizer/features/workflow/workflow_state_store.dart';
import 'package:onsei_organizer/gen/onsei/v1/service.pbgrpc.dart';

/// Fake OnseiService for WorkflowStateStore tests
class FakeOnseiService extends OnseiServiceBase {
  final List<RefreshFoldersRequest> refreshRequests = [];
  RefreshFoldersResponse? nextRefreshResponse;

  void reset() {
    refreshRequests.clear();
    nextRefreshResponse = null;
  }

  void setRefreshResponse(RefreshFoldersResponse response) {
    nextRefreshResponse = response;
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
    return ListFilesResponse();
  }

  @override
  Future<PlanOperationsResponse> planOperations(
    ServiceCall call,
    PlanOperationsRequest request,
  ) async {
    return PlanOperationsResponse()..planId = 'test-plan';
  }

  @override
  Stream<JobEvent> executePlan(ServiceCall call, ExecutePlanRequest request) {
    return Stream.fromIterable([
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
    return nextRefreshResponse ?? RefreshFoldersResponse()
      ..successfulFolders.addAll(request.folderPaths);
  }
}

/// Creates a test Server that runs the fake service.
Future<(Server, FakeOnseiService)> createTestServer() async {
  final fakeService = FakeOnseiService();
  final server = Server.create(services: [fakeService]);
  await server.serve(port: 0);
  return (server, fakeService);
}

void main() {
  late Server server;
  late FakeOnseiService fakeService;
  late ClientChannel channel;

  setUp(() async {
    final (s, f) = await createTestServer();
    server = s;
    fakeService = f;
    channel = ClientChannel(
      'localhost',
      port: server.port as int,
      options: const ChannelOptions(credentials: ChannelCredentials.insecure()),
    );
  });

  tearDown(() async {
    await channel.shutdown();
    await server.shutdown();
  });

  group('WorkflowStateStore', () {
    test('softDelete defaults to true', () {
      final store = WorkflowStateStore(channel: channel);
      expect(store.softDelete, isTrue);
    });

    test('setSoftDelete updates softDelete value', () {
      final store = WorkflowStateStore(channel: channel);
      expect(store.softDelete, isTrue);

      store.setSoftDelete(false);
      expect(store.softDelete, isFalse);

      store.setSoftDelete(true);
      expect(store.softDelete, isTrue);
    });

    test('setSoftDelete notifies listeners', () {
      final store = WorkflowStateStore(channel: channel);
      bool? notifiedValue;

      store.addListener(() {
        notifiedValue = store.softDelete;
      });

      store.setSoftDelete(false);
      expect(notifiedValue, isFalse);
    });

    test('refreshFolders calls gRPC with correct parameters', () async {
      fakeService.reset();
      fakeService.setRefreshResponse(
        RefreshFoldersResponse()
          ..successfulFolders.addAll(['/root/folderA', '/root/folderB']),
      );

      final store = WorkflowStateStore(channel: channel);
      final response = await store.refreshFolders(
        rootPath: '/root',
        folderPaths: ['/root/folderA', '/root/folderB'],
      );

      expect(fakeService.refreshRequests, hasLength(1));
      expect(fakeService.refreshRequests.first.rootPath, equals('/root'));
      expect(
        fakeService.refreshRequests.first.folderPaths,
        containsAll(['/root/folderA', '/root/folderB']),
      );
      expect(
        response.successfulFolders,
        containsAll(['/root/folderA', '/root/folderB']),
      );
    });

    test('refreshFolders returns errors in response', () async {
      fakeService.reset();
      fakeService.setRefreshResponse(
        RefreshFoldersResponse()
          ..successfulFolders.add('/root/folderA')
          ..errors.add(
            FolderError()
              ..folderPath = '/root/folderB'
              ..code = 'SCAN_ERROR'
              ..message = 'Permission denied',
          ),
      );

      final store = WorkflowStateStore(channel: channel);
      final response = await store.refreshFolders(
        rootPath: '/root',
        folderPaths: ['/root/folderA', '/root/folderB'],
      );

      expect(response.successfulFolders, contains('/root/folderA'));
      expect(response.errors, hasLength(1));
      expect(response.errors.first.folderPath, equals('/root/folderB'));
      expect(response.errors.first.code, equals('SCAN_ERROR'));
    });

    test('softDelete can be observed by multiple listeners', () {
      final store = WorkflowStateStore(channel: channel);
      int callCount1 = 0;
      int callCount2 = 0;

      store.addListener(() => callCount1++);
      store.addListener(() => callCount2++);

      store.setSoftDelete(false);
      expect(callCount1, equals(1));
      expect(callCount2, equals(1));

      store.setSoftDelete(true);
      expect(callCount1, equals(2));
      expect(callCount2, equals(2));
    });
  });

  group('WorkflowStateStore shared state', () {
    test(
      'same store instance used by multiple widgets observes same softDelete',
      () {
        final store = WorkflowStateStore(channel: channel);

        // Simulate multiple widgets observing the same store
        bool? widget1Value;
        bool? widget2Value;

        store.addListener(() {
          widget1Value = store.softDelete;
        });
        store.addListener(() {
          widget2Value = store.softDelete;
        });

        // Change via setSoftDelete
        store.setSoftDelete(false);

        expect(widget1Value, isFalse);
        expect(widget2Value, isFalse);

        // Both should see the same change
        store.setSoftDelete(true);
        expect(widget1Value, isTrue);
        expect(widget2Value, isTrue);
      },
    );
  });
}
