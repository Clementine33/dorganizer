// This is a generated file - do not edit.
//
// Generated from onsei/v1/service.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

class GetConfigRequest extends $pb.GeneratedMessage {
  factory GetConfigRequest() => create();

  GetConfigRequest._();

  factory GetConfigRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetConfigRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetConfigRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConfigRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConfigRequest copyWith(void Function(GetConfigRequest) updates) =>
      super.copyWith((message) => updates(message as GetConfigRequest))
          as GetConfigRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetConfigRequest create() => GetConfigRequest._();
  @$core.override
  GetConfigRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetConfigRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetConfigRequest>(create);
  static GetConfigRequest? _defaultInstance;
}

class GetConfigResponse extends $pb.GeneratedMessage {
  factory GetConfigResponse({
    $core.String? configJson,
  }) {
    final result = create();
    if (configJson != null) result.configJson = configJson;
    return result;
  }

  GetConfigResponse._();

  factory GetConfigResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetConfigResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetConfigResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'configJson')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConfigResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConfigResponse copyWith(void Function(GetConfigResponse) updates) =>
      super.copyWith((message) => updates(message as GetConfigResponse))
          as GetConfigResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetConfigResponse create() => GetConfigResponse._();
  @$core.override
  GetConfigResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetConfigResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetConfigResponse>(create);
  static GetConfigResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get configJson => $_getSZ(0);
  @$pb.TagNumber(1)
  set configJson($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasConfigJson() => $_has(0);
  @$pb.TagNumber(1)
  void clearConfigJson() => $_clearField(1);
}

class UpdateConfigRequest extends $pb.GeneratedMessage {
  factory UpdateConfigRequest({
    $core.String? configJson,
  }) {
    final result = create();
    if (configJson != null) result.configJson = configJson;
    return result;
  }

  UpdateConfigRequest._();

  factory UpdateConfigRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory UpdateConfigRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'UpdateConfigRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'configJson')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateConfigRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateConfigRequest copyWith(void Function(UpdateConfigRequest) updates) =>
      super.copyWith((message) => updates(message as UpdateConfigRequest))
          as UpdateConfigRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static UpdateConfigRequest create() => UpdateConfigRequest._();
  @$core.override
  UpdateConfigRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static UpdateConfigRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<UpdateConfigRequest>(create);
  static UpdateConfigRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get configJson => $_getSZ(0);
  @$pb.TagNumber(1)
  set configJson($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasConfigJson() => $_has(0);
  @$pb.TagNumber(1)
  void clearConfigJson() => $_clearField(1);
}

class UpdateConfigResponse extends $pb.GeneratedMessage {
  factory UpdateConfigResponse() => create();

  UpdateConfigResponse._();

  factory UpdateConfigResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory UpdateConfigResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'UpdateConfigResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateConfigResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateConfigResponse copyWith(void Function(UpdateConfigResponse) updates) =>
      super.copyWith((message) => updates(message as UpdateConfigResponse))
          as UpdateConfigResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static UpdateConfigResponse create() => UpdateConfigResponse._();
  @$core.override
  UpdateConfigResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static UpdateConfigResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<UpdateConfigResponse>(create);
  static UpdateConfigResponse? _defaultInstance;
}

class ScanRequest extends $pb.GeneratedMessage {
  factory ScanRequest({
    $core.String? folderPath,
  }) {
    final result = create();
    if (folderPath != null) result.folderPath = folderPath;
    return result;
  }

  ScanRequest._();

  factory ScanRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'folderPath')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanRequest copyWith(void Function(ScanRequest) updates) =>
      super.copyWith((message) => updates(message as ScanRequest))
          as ScanRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanRequest create() => ScanRequest._();
  @$core.override
  ScanRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanRequest>(create);
  static ScanRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get folderPath => $_getSZ(0);
  @$pb.TagNumber(1)
  set folderPath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasFolderPath() => $_has(0);
  @$pb.TagNumber(1)
  void clearFolderPath() => $_clearField(1);
}

class ListFoldersRequest extends $pb.GeneratedMessage {
  factory ListFoldersRequest({
    $core.String? parentPath,
  }) {
    final result = create();
    if (parentPath != null) result.parentPath = parentPath;
    return result;
  }

  ListFoldersRequest._();

  factory ListFoldersRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListFoldersRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListFoldersRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'parentPath')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFoldersRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFoldersRequest copyWith(void Function(ListFoldersRequest) updates) =>
      super.copyWith((message) => updates(message as ListFoldersRequest))
          as ListFoldersRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListFoldersRequest create() => ListFoldersRequest._();
  @$core.override
  ListFoldersRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListFoldersRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListFoldersRequest>(create);
  static ListFoldersRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get parentPath => $_getSZ(0);
  @$pb.TagNumber(1)
  set parentPath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasParentPath() => $_has(0);
  @$pb.TagNumber(1)
  void clearParentPath() => $_clearField(1);
}

class ListFoldersResponse extends $pb.GeneratedMessage {
  factory ListFoldersResponse({
    $core.Iterable<$core.String>? folders,
  }) {
    final result = create();
    if (folders != null) result.folders.addAll(folders);
    return result;
  }

  ListFoldersResponse._();

  factory ListFoldersResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListFoldersResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListFoldersResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'folders')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFoldersResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFoldersResponse copyWith(void Function(ListFoldersResponse) updates) =>
      super.copyWith((message) => updates(message as ListFoldersResponse))
          as ListFoldersResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListFoldersResponse create() => ListFoldersResponse._();
  @$core.override
  ListFoldersResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListFoldersResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListFoldersResponse>(create);
  static ListFoldersResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get folders => $_getList(0);
}

class ListFilesRequest extends $pb.GeneratedMessage {
  factory ListFilesRequest({
    $core.String? folderPath,
  }) {
    final result = create();
    if (folderPath != null) result.folderPath = folderPath;
    return result;
  }

  ListFilesRequest._();

  factory ListFilesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListFilesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListFilesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'folderPath')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFilesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFilesRequest copyWith(void Function(ListFilesRequest) updates) =>
      super.copyWith((message) => updates(message as ListFilesRequest))
          as ListFilesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListFilesRequest create() => ListFilesRequest._();
  @$core.override
  ListFilesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListFilesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListFilesRequest>(create);
  static ListFilesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get folderPath => $_getSZ(0);
  @$pb.TagNumber(1)
  set folderPath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasFolderPath() => $_has(0);
  @$pb.TagNumber(1)
  void clearFolderPath() => $_clearField(1);
}

class ListFilesResponse extends $pb.GeneratedMessage {
  factory ListFilesResponse({
    $core.Iterable<$core.String>? files,
  }) {
    final result = create();
    if (files != null) result.files.addAll(files);
    return result;
  }

  ListFilesResponse._();

  factory ListFilesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListFilesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListFilesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'files')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFilesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListFilesResponse copyWith(void Function(ListFilesResponse) updates) =>
      super.copyWith((message) => updates(message as ListFilesResponse))
          as ListFilesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListFilesResponse create() => ListFilesResponse._();
  @$core.override
  ListFilesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListFilesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListFilesResponse>(create);
  static ListFilesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get files => $_getList(0);
}

class PlanOperationsRequest extends $pb.GeneratedMessage {
  factory PlanOperationsRequest({
    $core.Iterable<$core.String>? sourceFiles,
    $core.String? targetFormat,
    $core.String? folderPath,
    $core.Iterable<$core.String>? folderPaths,
    $core.String? planType,
    $core.bool? pruneMatchedExcluded,
  }) {
    final result = create();
    if (sourceFiles != null) result.sourceFiles.addAll(sourceFiles);
    if (targetFormat != null) result.targetFormat = targetFormat;
    if (folderPath != null) result.folderPath = folderPath;
    if (folderPaths != null) result.folderPaths.addAll(folderPaths);
    if (planType != null) result.planType = planType;
    if (pruneMatchedExcluded != null)
      result.pruneMatchedExcluded = pruneMatchedExcluded;
    return result;
  }

  PlanOperationsRequest._();

  factory PlanOperationsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PlanOperationsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PlanOperationsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'sourceFiles')
    ..aOS(2, _omitFieldNames ? '' : 'targetFormat')
    ..aOS(3, _omitFieldNames ? '' : 'folderPath')
    ..pPS(4, _omitFieldNames ? '' : 'folderPaths')
    ..aOS(5, _omitFieldNames ? '' : 'planType')
    ..aOB(6, _omitFieldNames ? '' : 'pruneMatchedExcluded')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanOperationsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanOperationsRequest copyWith(
          void Function(PlanOperationsRequest) updates) =>
      super.copyWith((message) => updates(message as PlanOperationsRequest))
          as PlanOperationsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PlanOperationsRequest create() => PlanOperationsRequest._();
  @$core.override
  PlanOperationsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PlanOperationsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PlanOperationsRequest>(create);
  static PlanOperationsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get sourceFiles => $_getList(0);

  @$pb.TagNumber(2)
  $core.String get targetFormat => $_getSZ(1);
  @$pb.TagNumber(2)
  set targetFormat($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasTargetFormat() => $_has(1);
  @$pb.TagNumber(2)
  void clearTargetFormat() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get folderPath => $_getSZ(2);
  @$pb.TagNumber(3)
  set folderPath($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasFolderPath() => $_has(2);
  @$pb.TagNumber(3)
  void clearFolderPath() => $_clearField(3);

  @$pb.TagNumber(4)
  $pb.PbList<$core.String> get folderPaths => $_getList(3);

  @$pb.TagNumber(5)
  $core.String get planType => $_getSZ(4);
  @$pb.TagNumber(5)
  set planType($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasPlanType() => $_has(4);
  @$pb.TagNumber(5)
  void clearPlanType() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.bool get pruneMatchedExcluded => $_getBF(5);
  @$pb.TagNumber(6)
  set pruneMatchedExcluded($core.bool value) => $_setBool(5, value);
  @$pb.TagNumber(6)
  $core.bool hasPruneMatchedExcluded() => $_has(5);
  @$pb.TagNumber(6)
  void clearPruneMatchedExcluded() => $_clearField(6);
}

class PlanOperationsResponse extends $pb.GeneratedMessage {
  factory PlanOperationsResponse({
    $core.String? planId,
    $core.Iterable<PlannedOperation>? operations,
    $core.int? totalCount,
    $core.int? actionableCount,
    $core.String? summaryReason,
    $core.Iterable<FolderError>? planErrors,
    $core.Iterable<$core.String>? successfulFolders,
  }) {
    final result = create();
    if (planId != null) result.planId = planId;
    if (operations != null) result.operations.addAll(operations);
    if (totalCount != null) result.totalCount = totalCount;
    if (actionableCount != null) result.actionableCount = actionableCount;
    if (summaryReason != null) result.summaryReason = summaryReason;
    if (planErrors != null) result.planErrors.addAll(planErrors);
    if (successfulFolders != null)
      result.successfulFolders.addAll(successfulFolders);
    return result;
  }

  PlanOperationsResponse._();

  factory PlanOperationsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PlanOperationsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PlanOperationsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'planId')
    ..pPM<PlannedOperation>(2, _omitFieldNames ? '' : 'operations',
        subBuilder: PlannedOperation.create)
    ..aI(3, _omitFieldNames ? '' : 'totalCount')
    ..aI(4, _omitFieldNames ? '' : 'actionableCount')
    ..aOS(6, _omitFieldNames ? '' : 'summaryReason')
    ..pPM<FolderError>(7, _omitFieldNames ? '' : 'planErrors',
        subBuilder: FolderError.create)
    ..pPS(8, _omitFieldNames ? '' : 'successfulFolders')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanOperationsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanOperationsResponse copyWith(
          void Function(PlanOperationsResponse) updates) =>
      super.copyWith((message) => updates(message as PlanOperationsResponse))
          as PlanOperationsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PlanOperationsResponse create() => PlanOperationsResponse._();
  @$core.override
  PlanOperationsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PlanOperationsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PlanOperationsResponse>(create);
  static PlanOperationsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get planId => $_getSZ(0);
  @$pb.TagNumber(1)
  set planId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPlanId() => $_has(0);
  @$pb.TagNumber(1)
  void clearPlanId() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<PlannedOperation> get operations => $_getList(1);

  @$pb.TagNumber(3)
  $core.int get totalCount => $_getIZ(2);
  @$pb.TagNumber(3)
  set totalCount($core.int value) => $_setSignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasTotalCount() => $_has(2);
  @$pb.TagNumber(3)
  void clearTotalCount() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.int get actionableCount => $_getIZ(3);
  @$pb.TagNumber(4)
  set actionableCount($core.int value) => $_setSignedInt32(3, value);
  @$pb.TagNumber(4)
  $core.bool hasActionableCount() => $_has(3);
  @$pb.TagNumber(4)
  void clearActionableCount() => $_clearField(4);

  @$pb.TagNumber(6)
  $core.String get summaryReason => $_getSZ(4);
  @$pb.TagNumber(6)
  set summaryReason($core.String value) => $_setString(4, value);
  @$pb.TagNumber(6)
  $core.bool hasSummaryReason() => $_has(4);
  @$pb.TagNumber(6)
  void clearSummaryReason() => $_clearField(6);

  @$pb.TagNumber(7)
  $pb.PbList<FolderError> get planErrors => $_getList(5);

  @$pb.TagNumber(8)
  $pb.PbList<$core.String> get successfulFolders => $_getList(6);
}

/// FolderError represents a structured error attributed to a specific folder
class FolderError extends $pb.GeneratedMessage {
  factory FolderError({
    $core.String? stage,
    $core.String? code,
    $core.String? message,
    $core.String? folderPath,
    $core.String? planId,
    $core.String? rootPath,
    $core.String? timestamp,
    $core.String? eventId,
  }) {
    final result = create();
    if (stage != null) result.stage = stage;
    if (code != null) result.code = code;
    if (message != null) result.message = message;
    if (folderPath != null) result.folderPath = folderPath;
    if (planId != null) result.planId = planId;
    if (rootPath != null) result.rootPath = rootPath;
    if (timestamp != null) result.timestamp = timestamp;
    if (eventId != null) result.eventId = eventId;
    return result;
  }

  FolderError._();

  factory FolderError.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory FolderError.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'FolderError',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'stage')
    ..aOS(2, _omitFieldNames ? '' : 'code')
    ..aOS(3, _omitFieldNames ? '' : 'message')
    ..aOS(4, _omitFieldNames ? '' : 'folderPath')
    ..aOS(5, _omitFieldNames ? '' : 'planId')
    ..aOS(6, _omitFieldNames ? '' : 'rootPath')
    ..aOS(7, _omitFieldNames ? '' : 'timestamp')
    ..aOS(8, _omitFieldNames ? '' : 'eventId')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  FolderError clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  FolderError copyWith(void Function(FolderError) updates) =>
      super.copyWith((message) => updates(message as FolderError))
          as FolderError;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static FolderError create() => FolderError._();
  @$core.override
  FolderError createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static FolderError getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<FolderError>(create);
  static FolderError? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get stage => $_getSZ(0);
  @$pb.TagNumber(1)
  set stage($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasStage() => $_has(0);
  @$pb.TagNumber(1)
  void clearStage() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get code => $_getSZ(1);
  @$pb.TagNumber(2)
  set code($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCode() => $_has(1);
  @$pb.TagNumber(2)
  void clearCode() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get message => $_getSZ(2);
  @$pb.TagNumber(3)
  set message($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasMessage() => $_has(2);
  @$pb.TagNumber(3)
  void clearMessage() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get folderPath => $_getSZ(3);
  @$pb.TagNumber(4)
  set folderPath($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasFolderPath() => $_has(3);
  @$pb.TagNumber(4)
  void clearFolderPath() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get planId => $_getSZ(4);
  @$pb.TagNumber(5)
  set planId($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasPlanId() => $_has(4);
  @$pb.TagNumber(5)
  void clearPlanId() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get rootPath => $_getSZ(5);
  @$pb.TagNumber(6)
  set rootPath($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasRootPath() => $_has(5);
  @$pb.TagNumber(6)
  void clearRootPath() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get timestamp => $_getSZ(6);
  @$pb.TagNumber(7)
  set timestamp($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasTimestamp() => $_has(6);
  @$pb.TagNumber(7)
  void clearTimestamp() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.String get eventId => $_getSZ(7);
  @$pb.TagNumber(8)
  set eventId($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasEventId() => $_has(7);
  @$pb.TagNumber(8)
  void clearEventId() => $_clearField(8);
}

class PlannedOperation extends $pb.GeneratedMessage {
  factory PlannedOperation({
    $core.String? sourcePath,
    $core.String? targetPath,
    $core.String? operationType,
  }) {
    final result = create();
    if (sourcePath != null) result.sourcePath = sourcePath;
    if (targetPath != null) result.targetPath = targetPath;
    if (operationType != null) result.operationType = operationType;
    return result;
  }

  PlannedOperation._();

  factory PlannedOperation.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PlannedOperation.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PlannedOperation',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'sourcePath')
    ..aOS(2, _omitFieldNames ? '' : 'targetPath')
    ..aOS(3, _omitFieldNames ? '' : 'operationType')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlannedOperation clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlannedOperation copyWith(void Function(PlannedOperation) updates) =>
      super.copyWith((message) => updates(message as PlannedOperation))
          as PlannedOperation;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PlannedOperation create() => PlannedOperation._();
  @$core.override
  PlannedOperation createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PlannedOperation getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PlannedOperation>(create);
  static PlannedOperation? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get sourcePath => $_getSZ(0);
  @$pb.TagNumber(1)
  set sourcePath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSourcePath() => $_has(0);
  @$pb.TagNumber(1)
  void clearSourcePath() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get targetPath => $_getSZ(1);
  @$pb.TagNumber(2)
  set targetPath($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasTargetPath() => $_has(1);
  @$pb.TagNumber(2)
  void clearTargetPath() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get operationType => $_getSZ(2);
  @$pb.TagNumber(3)
  set operationType($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasOperationType() => $_has(2);
  @$pb.TagNumber(3)
  void clearOperationType() => $_clearField(3);
}

class ExecutePlanRequest extends $pb.GeneratedMessage {
  factory ExecutePlanRequest({
    $core.String? planId,
    $core.bool? softDelete,
  }) {
    final result = create();
    if (planId != null) result.planId = planId;
    if (softDelete != null) result.softDelete = softDelete;
    return result;
  }

  ExecutePlanRequest._();

  factory ExecutePlanRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ExecutePlanRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ExecutePlanRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'planId')
    ..aOB(2, _omitFieldNames ? '' : 'softDelete')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExecutePlanRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExecutePlanRequest copyWith(void Function(ExecutePlanRequest) updates) =>
      super.copyWith((message) => updates(message as ExecutePlanRequest))
          as ExecutePlanRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ExecutePlanRequest create() => ExecutePlanRequest._();
  @$core.override
  ExecutePlanRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ExecutePlanRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ExecutePlanRequest>(create);
  static ExecutePlanRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get planId => $_getSZ(0);
  @$pb.TagNumber(1)
  set planId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPlanId() => $_has(0);
  @$pb.TagNumber(1)
  void clearPlanId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.bool get softDelete => $_getBF(1);
  @$pb.TagNumber(2)
  set softDelete($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasSoftDelete() => $_has(1);
  @$pb.TagNumber(2)
  void clearSoftDelete() => $_clearField(2);
}

class ListPlansRequest extends $pb.GeneratedMessage {
  factory ListPlansRequest({
    $core.String? rootPath,
    $core.int? limit,
  }) {
    final result = create();
    if (rootPath != null) result.rootPath = rootPath;
    if (limit != null) result.limit = limit;
    return result;
  }

  ListPlansRequest._();

  factory ListPlansRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListPlansRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListPlansRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'rootPath')
    ..aI(2, _omitFieldNames ? '' : 'limit')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPlansRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPlansRequest copyWith(void Function(ListPlansRequest) updates) =>
      super.copyWith((message) => updates(message as ListPlansRequest))
          as ListPlansRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListPlansRequest create() => ListPlansRequest._();
  @$core.override
  ListPlansRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListPlansRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListPlansRequest>(create);
  static ListPlansRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get rootPath => $_getSZ(0);
  @$pb.TagNumber(1)
  set rootPath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRootPath() => $_has(0);
  @$pb.TagNumber(1)
  void clearRootPath() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get limit => $_getIZ(1);
  @$pb.TagNumber(2)
  set limit($core.int value) => $_setSignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearLimit() => $_clearField(2);
}

class ListPlansResponse extends $pb.GeneratedMessage {
  factory ListPlansResponse({
    $core.Iterable<PlanInfo>? plans,
  }) {
    final result = create();
    if (plans != null) result.plans.addAll(plans);
    return result;
  }

  ListPlansResponse._();

  factory ListPlansResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListPlansResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListPlansResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..pPM<PlanInfo>(1, _omitFieldNames ? '' : 'plans',
        subBuilder: PlanInfo.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPlansResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPlansResponse copyWith(void Function(ListPlansResponse) updates) =>
      super.copyWith((message) => updates(message as ListPlansResponse))
          as ListPlansResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListPlansResponse create() => ListPlansResponse._();
  @$core.override
  ListPlansResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListPlansResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListPlansResponse>(create);
  static ListPlansResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<PlanInfo> get plans => $_getList(0);
}

class RefreshFoldersRequest extends $pb.GeneratedMessage {
  factory RefreshFoldersRequest({
    $core.String? rootPath,
    $core.Iterable<$core.String>? folderPaths,
  }) {
    final result = create();
    if (rootPath != null) result.rootPath = rootPath;
    if (folderPaths != null) result.folderPaths.addAll(folderPaths);
    return result;
  }

  RefreshFoldersRequest._();

  factory RefreshFoldersRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory RefreshFoldersRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'RefreshFoldersRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'rootPath')
    ..pPS(2, _omitFieldNames ? '' : 'folderPaths')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RefreshFoldersRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RefreshFoldersRequest copyWith(
          void Function(RefreshFoldersRequest) updates) =>
      super.copyWith((message) => updates(message as RefreshFoldersRequest))
          as RefreshFoldersRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static RefreshFoldersRequest create() => RefreshFoldersRequest._();
  @$core.override
  RefreshFoldersRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static RefreshFoldersRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<RefreshFoldersRequest>(create);
  static RefreshFoldersRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get rootPath => $_getSZ(0);
  @$pb.TagNumber(1)
  set rootPath($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRootPath() => $_has(0);
  @$pb.TagNumber(1)
  void clearRootPath() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<$core.String> get folderPaths => $_getList(1);
}

class RefreshFoldersResponse extends $pb.GeneratedMessage {
  factory RefreshFoldersResponse({
    $core.Iterable<$core.String>? successfulFolders,
    $core.Iterable<FolderError>? errors,
  }) {
    final result = create();
    if (successfulFolders != null)
      result.successfulFolders.addAll(successfulFolders);
    if (errors != null) result.errors.addAll(errors);
    return result;
  }

  RefreshFoldersResponse._();

  factory RefreshFoldersResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory RefreshFoldersResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'RefreshFoldersResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'successfulFolders')
    ..pPM<FolderError>(2, _omitFieldNames ? '' : 'errors',
        subBuilder: FolderError.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RefreshFoldersResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RefreshFoldersResponse copyWith(
          void Function(RefreshFoldersResponse) updates) =>
      super.copyWith((message) => updates(message as RefreshFoldersResponse))
          as RefreshFoldersResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static RefreshFoldersResponse create() => RefreshFoldersResponse._();
  @$core.override
  RefreshFoldersResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static RefreshFoldersResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<RefreshFoldersResponse>(create);
  static RefreshFoldersResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get successfulFolders => $_getList(0);

  @$pb.TagNumber(2)
  $pb.PbList<FolderError> get errors => $_getList(1);
}

class PlanInfo extends $pb.GeneratedMessage {
  factory PlanInfo({
    $core.String? planId,
    $core.String? rootPath,
    $core.String? planType,
    $core.String? status,
    $core.String? createdAt,
  }) {
    final result = create();
    if (planId != null) result.planId = planId;
    if (rootPath != null) result.rootPath = rootPath;
    if (planType != null) result.planType = planType;
    if (status != null) result.status = status;
    if (createdAt != null) result.createdAt = createdAt;
    return result;
  }

  PlanInfo._();

  factory PlanInfo.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PlanInfo.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PlanInfo',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'planId')
    ..aOS(2, _omitFieldNames ? '' : 'rootPath')
    ..aOS(3, _omitFieldNames ? '' : 'planType')
    ..aOS(4, _omitFieldNames ? '' : 'status')
    ..aOS(5, _omitFieldNames ? '' : 'createdAt')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanInfo clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PlanInfo copyWith(void Function(PlanInfo) updates) =>
      super.copyWith((message) => updates(message as PlanInfo)) as PlanInfo;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PlanInfo create() => PlanInfo._();
  @$core.override
  PlanInfo createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PlanInfo getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<PlanInfo>(create);
  static PlanInfo? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get planId => $_getSZ(0);
  @$pb.TagNumber(1)
  set planId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPlanId() => $_has(0);
  @$pb.TagNumber(1)
  void clearPlanId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get rootPath => $_getSZ(1);
  @$pb.TagNumber(2)
  set rootPath($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasRootPath() => $_has(1);
  @$pb.TagNumber(2)
  void clearRootPath() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get planType => $_getSZ(2);
  @$pb.TagNumber(3)
  set planType($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasPlanType() => $_has(2);
  @$pb.TagNumber(3)
  void clearPlanType() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get status => $_getSZ(3);
  @$pb.TagNumber(4)
  set status($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasStatus() => $_has(3);
  @$pb.TagNumber(4)
  void clearStatus() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get createdAt => $_getSZ(4);
  @$pb.TagNumber(5)
  set createdAt($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasCreatedAt() => $_has(4);
  @$pb.TagNumber(5)
  void clearCreatedAt() => $_clearField(5);
}

class JobEvent extends $pb.GeneratedMessage {
  factory JobEvent({
    $core.String? eventType,
    $core.String? message,
    $core.int? progressPercent,
    $core.String? timestamp,
    $core.String? stage,
    $core.String? code,
    $core.String? folderPath,
    $core.String? planId,
    $core.String? rootPath,
    $core.String? eventId,
    $core.String? itemSourcePath,
    $core.String? itemTargetPath,
    $core.String? correlationId,
  }) {
    final result = create();
    if (eventType != null) result.eventType = eventType;
    if (message != null) result.message = message;
    if (progressPercent != null) result.progressPercent = progressPercent;
    if (timestamp != null) result.timestamp = timestamp;
    if (stage != null) result.stage = stage;
    if (code != null) result.code = code;
    if (folderPath != null) result.folderPath = folderPath;
    if (planId != null) result.planId = planId;
    if (rootPath != null) result.rootPath = rootPath;
    if (eventId != null) result.eventId = eventId;
    if (itemSourcePath != null) result.itemSourcePath = itemSourcePath;
    if (itemTargetPath != null) result.itemTargetPath = itemTargetPath;
    if (correlationId != null) result.correlationId = correlationId;
    return result;
  }

  JobEvent._();

  factory JobEvent.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory JobEvent.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'JobEvent',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'onsei.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'eventType')
    ..aOS(2, _omitFieldNames ? '' : 'message')
    ..aI(3, _omitFieldNames ? '' : 'progressPercent')
    ..aOS(4, _omitFieldNames ? '' : 'timestamp')
    ..aOS(5, _omitFieldNames ? '' : 'stage')
    ..aOS(6, _omitFieldNames ? '' : 'code')
    ..aOS(7, _omitFieldNames ? '' : 'folderPath')
    ..aOS(8, _omitFieldNames ? '' : 'planId')
    ..aOS(9, _omitFieldNames ? '' : 'rootPath')
    ..aOS(10, _omitFieldNames ? '' : 'eventId')
    ..aOS(11, _omitFieldNames ? '' : 'itemSourcePath')
    ..aOS(12, _omitFieldNames ? '' : 'itemTargetPath')
    ..aOS(13, _omitFieldNames ? '' : 'correlationId')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  JobEvent clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  JobEvent copyWith(void Function(JobEvent) updates) =>
      super.copyWith((message) => updates(message as JobEvent)) as JobEvent;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static JobEvent create() => JobEvent._();
  @$core.override
  JobEvent createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static JobEvent getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<JobEvent>(create);
  static JobEvent? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get eventType => $_getSZ(0);
  @$pb.TagNumber(1)
  set eventType($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasEventType() => $_has(0);
  @$pb.TagNumber(1)
  void clearEventType() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get message => $_getSZ(1);
  @$pb.TagNumber(2)
  set message($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMessage() => $_has(1);
  @$pb.TagNumber(2)
  void clearMessage() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get progressPercent => $_getIZ(2);
  @$pb.TagNumber(3)
  set progressPercent($core.int value) => $_setSignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasProgressPercent() => $_has(2);
  @$pb.TagNumber(3)
  void clearProgressPercent() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get timestamp => $_getSZ(3);
  @$pb.TagNumber(4)
  set timestamp($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasTimestamp() => $_has(3);
  @$pb.TagNumber(4)
  void clearTimestamp() => $_clearField(4);

  /// Structured error attribution fields (Task 1)
  @$pb.TagNumber(5)
  $core.String get stage => $_getSZ(4);
  @$pb.TagNumber(5)
  set stage($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasStage() => $_has(4);
  @$pb.TagNumber(5)
  void clearStage() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get code => $_getSZ(5);
  @$pb.TagNumber(6)
  set code($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasCode() => $_has(5);
  @$pb.TagNumber(6)
  void clearCode() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get folderPath => $_getSZ(6);
  @$pb.TagNumber(7)
  set folderPath($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasFolderPath() => $_has(6);
  @$pb.TagNumber(7)
  void clearFolderPath() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.String get planId => $_getSZ(7);
  @$pb.TagNumber(8)
  set planId($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasPlanId() => $_has(7);
  @$pb.TagNumber(8)
  void clearPlanId() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.String get rootPath => $_getSZ(8);
  @$pb.TagNumber(9)
  set rootPath($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasRootPath() => $_has(8);
  @$pb.TagNumber(9)
  void clearRootPath() => $_clearField(9);

  @$pb.TagNumber(10)
  $core.String get eventId => $_getSZ(9);
  @$pb.TagNumber(10)
  set eventId($core.String value) => $_setString(9, value);
  @$pb.TagNumber(10)
  $core.bool hasEventId() => $_has(9);
  @$pb.TagNumber(10)
  void clearEventId() => $_clearField(10);

  @$pb.TagNumber(11)
  $core.String get itemSourcePath => $_getSZ(10);
  @$pb.TagNumber(11)
  set itemSourcePath($core.String value) => $_setString(10, value);
  @$pb.TagNumber(11)
  $core.bool hasItemSourcePath() => $_has(10);
  @$pb.TagNumber(11)
  void clearItemSourcePath() => $_clearField(11);

  @$pb.TagNumber(12)
  $core.String get itemTargetPath => $_getSZ(11);
  @$pb.TagNumber(12)
  set itemTargetPath($core.String value) => $_setString(11, value);
  @$pb.TagNumber(12)
  $core.bool hasItemTargetPath() => $_has(11);
  @$pb.TagNumber(12)
  void clearItemTargetPath() => $_clearField(12);

  @$pb.TagNumber(13)
  $core.String get correlationId => $_getSZ(12);
  @$pb.TagNumber(13)
  set correlationId($core.String value) => $_setString(12, value);
  @$pb.TagNumber(13)
  $core.bool hasCorrelationId() => $_has(12);
  @$pb.TagNumber(13)
  void clearCorrelationId() => $_clearField(13);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
