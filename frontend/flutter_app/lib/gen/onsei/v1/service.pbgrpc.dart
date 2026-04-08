// This is a generated file - do not edit.
//
// Generated from onsei/v1/service.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:grpc/service_api.dart' as $grpc;
import 'package:protobuf/protobuf.dart' as $pb;

import 'service.pb.dart' as $0;

export 'service.pb.dart';

@$pb.GrpcServiceName('onsei.v1.OnseiService')
class OnseiServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  OnseiServiceClient(super.channel, {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.GetConfigResponse> getConfig(
    $0.GetConfigRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getConfig, request, options: options);
  }

  $grpc.ResponseFuture<$0.UpdateConfigResponse> updateConfig(
    $0.UpdateConfigRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$updateConfig, request, options: options);
  }

  $grpc.ResponseStream<$0.JobEvent> scan(
    $0.ScanRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(_$scan, $async.Stream.fromIterable([request]),
        options: options);
  }

  $grpc.ResponseFuture<$0.ListFoldersResponse> listFolders(
    $0.ListFoldersRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listFolders, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListFilesResponse> listFiles(
    $0.ListFilesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listFiles, request, options: options);
  }

  $grpc.ResponseFuture<$0.PlanOperationsResponse> planOperations(
    $0.PlanOperationsRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$planOperations, request, options: options);
  }

  $grpc.ResponseStream<$0.JobEvent> executePlan(
    $0.ExecutePlanRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(
        _$executePlan, $async.Stream.fromIterable([request]),
        options: options);
  }

  $grpc.ResponseFuture<$0.ListPlansResponse> listPlans(
    $0.ListPlansRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listPlans, request, options: options);
  }

  $grpc.ResponseFuture<$0.RefreshFoldersResponse> refreshFolders(
    $0.RefreshFoldersRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$refreshFolders, request, options: options);
  }

  // method descriptors

  static final _$getConfig =
      $grpc.ClientMethod<$0.GetConfigRequest, $0.GetConfigResponse>(
          '/onsei.v1.OnseiService/GetConfig',
          ($0.GetConfigRequest value) => value.writeToBuffer(),
          $0.GetConfigResponse.fromBuffer);
  static final _$updateConfig =
      $grpc.ClientMethod<$0.UpdateConfigRequest, $0.UpdateConfigResponse>(
          '/onsei.v1.OnseiService/UpdateConfig',
          ($0.UpdateConfigRequest value) => value.writeToBuffer(),
          $0.UpdateConfigResponse.fromBuffer);
  static final _$scan = $grpc.ClientMethod<$0.ScanRequest, $0.JobEvent>(
      '/onsei.v1.OnseiService/Scan',
      ($0.ScanRequest value) => value.writeToBuffer(),
      $0.JobEvent.fromBuffer);
  static final _$listFolders =
      $grpc.ClientMethod<$0.ListFoldersRequest, $0.ListFoldersResponse>(
          '/onsei.v1.OnseiService/ListFolders',
          ($0.ListFoldersRequest value) => value.writeToBuffer(),
          $0.ListFoldersResponse.fromBuffer);
  static final _$listFiles =
      $grpc.ClientMethod<$0.ListFilesRequest, $0.ListFilesResponse>(
          '/onsei.v1.OnseiService/ListFiles',
          ($0.ListFilesRequest value) => value.writeToBuffer(),
          $0.ListFilesResponse.fromBuffer);
  static final _$planOperations =
      $grpc.ClientMethod<$0.PlanOperationsRequest, $0.PlanOperationsResponse>(
          '/onsei.v1.OnseiService/PlanOperations',
          ($0.PlanOperationsRequest value) => value.writeToBuffer(),
          $0.PlanOperationsResponse.fromBuffer);
  static final _$executePlan =
      $grpc.ClientMethod<$0.ExecutePlanRequest, $0.JobEvent>(
          '/onsei.v1.OnseiService/ExecutePlan',
          ($0.ExecutePlanRequest value) => value.writeToBuffer(),
          $0.JobEvent.fromBuffer);
  static final _$listPlans =
      $grpc.ClientMethod<$0.ListPlansRequest, $0.ListPlansResponse>(
          '/onsei.v1.OnseiService/ListPlans',
          ($0.ListPlansRequest value) => value.writeToBuffer(),
          $0.ListPlansResponse.fromBuffer);
  static final _$refreshFolders =
      $grpc.ClientMethod<$0.RefreshFoldersRequest, $0.RefreshFoldersResponse>(
          '/onsei.v1.OnseiService/RefreshFolders',
          ($0.RefreshFoldersRequest value) => value.writeToBuffer(),
          $0.RefreshFoldersResponse.fromBuffer);
}

@$pb.GrpcServiceName('onsei.v1.OnseiService')
abstract class OnseiServiceBase extends $grpc.Service {
  $core.String get $name => 'onsei.v1.OnseiService';

  OnseiServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.GetConfigRequest, $0.GetConfigResponse>(
        'GetConfig',
        getConfig_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetConfigRequest.fromBuffer(value),
        ($0.GetConfigResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.UpdateConfigRequest, $0.UpdateConfigResponse>(
            'UpdateConfig',
            updateConfig_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.UpdateConfigRequest.fromBuffer(value),
            ($0.UpdateConfigResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ScanRequest, $0.JobEvent>(
        'Scan',
        scan_Pre,
        false,
        true,
        ($core.List<$core.int> value) => $0.ScanRequest.fromBuffer(value),
        ($0.JobEvent value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.ListFoldersRequest, $0.ListFoldersResponse>(
            'ListFolders',
            listFolders_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.ListFoldersRequest.fromBuffer(value),
            ($0.ListFoldersResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListFilesRequest, $0.ListFilesResponse>(
        'ListFiles',
        listFiles_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.ListFilesRequest.fromBuffer(value),
        ($0.ListFilesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PlanOperationsRequest,
            $0.PlanOperationsResponse>(
        'PlanOperations',
        planOperations_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.PlanOperationsRequest.fromBuffer(value),
        ($0.PlanOperationsResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ExecutePlanRequest, $0.JobEvent>(
        'ExecutePlan',
        executePlan_Pre,
        false,
        true,
        ($core.List<$core.int> value) =>
            $0.ExecutePlanRequest.fromBuffer(value),
        ($0.JobEvent value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListPlansRequest, $0.ListPlansResponse>(
        'ListPlans',
        listPlans_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.ListPlansRequest.fromBuffer(value),
        ($0.ListPlansResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.RefreshFoldersRequest,
            $0.RefreshFoldersResponse>(
        'RefreshFolders',
        refreshFolders_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.RefreshFoldersRequest.fromBuffer(value),
        ($0.RefreshFoldersResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.GetConfigResponse> getConfig_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetConfigRequest> $request) async {
    return getConfig($call, await $request);
  }

  $async.Future<$0.GetConfigResponse> getConfig(
      $grpc.ServiceCall call, $0.GetConfigRequest request);

  $async.Future<$0.UpdateConfigResponse> updateConfig_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.UpdateConfigRequest> $request) async {
    return updateConfig($call, await $request);
  }

  $async.Future<$0.UpdateConfigResponse> updateConfig(
      $grpc.ServiceCall call, $0.UpdateConfigRequest request);

  $async.Stream<$0.JobEvent> scan_Pre(
      $grpc.ServiceCall $call, $async.Future<$0.ScanRequest> $request) async* {
    yield* scan($call, await $request);
  }

  $async.Stream<$0.JobEvent> scan(
      $grpc.ServiceCall call, $0.ScanRequest request);

  $async.Future<$0.ListFoldersResponse> listFolders_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListFoldersRequest> $request) async {
    return listFolders($call, await $request);
  }

  $async.Future<$0.ListFoldersResponse> listFolders(
      $grpc.ServiceCall call, $0.ListFoldersRequest request);

  $async.Future<$0.ListFilesResponse> listFiles_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListFilesRequest> $request) async {
    return listFiles($call, await $request);
  }

  $async.Future<$0.ListFilesResponse> listFiles(
      $grpc.ServiceCall call, $0.ListFilesRequest request);

  $async.Future<$0.PlanOperationsResponse> planOperations_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.PlanOperationsRequest> $request) async {
    return planOperations($call, await $request);
  }

  $async.Future<$0.PlanOperationsResponse> planOperations(
      $grpc.ServiceCall call, $0.PlanOperationsRequest request);

  $async.Stream<$0.JobEvent> executePlan_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ExecutePlanRequest> $request) async* {
    yield* executePlan($call, await $request);
  }

  $async.Stream<$0.JobEvent> executePlan(
      $grpc.ServiceCall call, $0.ExecutePlanRequest request);

  $async.Future<$0.ListPlansResponse> listPlans_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListPlansRequest> $request) async {
    return listPlans($call, await $request);
  }

  $async.Future<$0.ListPlansResponse> listPlans(
      $grpc.ServiceCall call, $0.ListPlansRequest request);

  $async.Future<$0.RefreshFoldersResponse> refreshFolders_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.RefreshFoldersRequest> $request) async {
    return refreshFolders($call, await $request);
  }

  $async.Future<$0.RefreshFoldersResponse> refreshFolders(
      $grpc.ServiceCall call, $0.RefreshFoldersRequest request);
}
