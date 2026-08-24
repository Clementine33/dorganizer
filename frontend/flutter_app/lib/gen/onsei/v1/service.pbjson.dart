// This is a generated file - do not edit.
//
// Generated from onsei/v1/service.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports
// ignore_for_file: unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

@$core.Deprecated('Use getConfigRequestDescriptor instead')
const GetConfigRequest$json = {
  '1': 'GetConfigRequest',
};

/// Descriptor for `GetConfigRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getConfigRequestDescriptor =
    $convert.base64Decode('ChBHZXRDb25maWdSZXF1ZXN0');

@$core.Deprecated('Use getConfigResponseDescriptor instead')
const GetConfigResponse$json = {
  '1': 'GetConfigResponse',
  '2': [
    {'1': 'config_json', '3': 1, '4': 1, '5': 9, '10': 'configJson'},
  ],
};

/// Descriptor for `GetConfigResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getConfigResponseDescriptor = $convert.base64Decode(
    'ChFHZXRDb25maWdSZXNwb25zZRIfCgtjb25maWdfanNvbhgBIAEoCVIKY29uZmlnSnNvbg==');

@$core.Deprecated('Use updateConfigRequestDescriptor instead')
const UpdateConfigRequest$json = {
  '1': 'UpdateConfigRequest',
  '2': [
    {'1': 'config_json', '3': 1, '4': 1, '5': 9, '10': 'configJson'},
  ],
};

/// Descriptor for `UpdateConfigRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List updateConfigRequestDescriptor = $convert.base64Decode(
    'ChNVcGRhdGVDb25maWdSZXF1ZXN0Eh8KC2NvbmZpZ19qc29uGAEgASgJUgpjb25maWdKc29u');

@$core.Deprecated('Use updateConfigResponseDescriptor instead')
const UpdateConfigResponse$json = {
  '1': 'UpdateConfigResponse',
};

/// Descriptor for `UpdateConfigResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List updateConfigResponseDescriptor =
    $convert.base64Decode('ChRVcGRhdGVDb25maWdSZXNwb25zZQ==');

@$core.Deprecated('Use scanRequestDescriptor instead')
const ScanRequest$json = {
  '1': 'ScanRequest',
  '2': [
    {'1': 'folder_path', '3': 1, '4': 1, '5': 9, '10': 'folderPath'},
  ],
};

/// Descriptor for `ScanRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanRequestDescriptor = $convert.base64Decode(
    'CgtTY2FuUmVxdWVzdBIfCgtmb2xkZXJfcGF0aBgBIAEoCVIKZm9sZGVyUGF0aA==');

@$core.Deprecated('Use listFoldersRequestDescriptor instead')
const ListFoldersRequest$json = {
  '1': 'ListFoldersRequest',
  '2': [
    {'1': 'parent_path', '3': 1, '4': 1, '5': 9, '10': 'parentPath'},
  ],
};

/// Descriptor for `ListFoldersRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listFoldersRequestDescriptor = $convert.base64Decode(
    'ChJMaXN0Rm9sZGVyc1JlcXVlc3QSHwoLcGFyZW50X3BhdGgYASABKAlSCnBhcmVudFBhdGg=');

@$core.Deprecated('Use listFoldersResponseDescriptor instead')
const ListFoldersResponse$json = {
  '1': 'ListFoldersResponse',
  '2': [
    {'1': 'folders', '3': 1, '4': 3, '5': 9, '10': 'folders'},
  ],
};

/// Descriptor for `ListFoldersResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listFoldersResponseDescriptor =
    $convert.base64Decode(
        'ChNMaXN0Rm9sZGVyc1Jlc3BvbnNlEhgKB2ZvbGRlcnMYASADKAlSB2ZvbGRlcnM=');

@$core.Deprecated('Use listFilesRequestDescriptor instead')
const ListFilesRequest$json = {
  '1': 'ListFilesRequest',
  '2': [
    {'1': 'folder_path', '3': 1, '4': 1, '5': 9, '10': 'folderPath'},
  ],
};

/// Descriptor for `ListFilesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listFilesRequestDescriptor = $convert.base64Decode(
    'ChBMaXN0RmlsZXNSZXF1ZXN0Eh8KC2ZvbGRlcl9wYXRoGAEgASgJUgpmb2xkZXJQYXRo');

@$core.Deprecated('Use fileListEntryDescriptor instead')
const FileListEntry$json = {
  '1': 'FileListEntry',
  '2': [
    {'1': 'path', '3': 1, '4': 1, '5': 9, '10': 'path'},
    {'1': 'bitrate', '3': 2, '4': 1, '5': 5, '10': 'bitrate'},
  ],
};

/// Descriptor for `FileListEntry`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List fileListEntryDescriptor = $convert.base64Decode(
    'Cg1GaWxlTGlzdEVudHJ5EhIKBHBhdGgYASABKAlSBHBhdGgSGAoHYml0cmF0ZRgCIAEoBVIHYm'
    'l0cmF0ZQ==');

@$core.Deprecated('Use listFilesResponseDescriptor instead')
const ListFilesResponse$json = {
  '1': 'ListFilesResponse',
  '2': [
    {'1': 'files', '3': 1, '4': 3, '5': 9, '10': 'files'},
    {
      '1': 'entries',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.onsei.v1.FileListEntry',
      '10': 'entries'
    },
  ],
};

/// Descriptor for `ListFilesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listFilesResponseDescriptor = $convert.base64Decode(
    'ChFMaXN0RmlsZXNSZXNwb25zZRIUCgVmaWxlcxgBIAMoCVIFZmlsZXMSMQoHZW50cmllcxgCIA'
    'MoCzIXLm9uc2VpLnYxLkZpbGVMaXN0RW50cnlSB2VudHJpZXM=');

@$core.Deprecated('Use planOperationsRequestDescriptor instead')
const PlanOperationsRequest$json = {
  '1': 'PlanOperationsRequest',
  '2': [
    {'1': 'source_files', '3': 1, '4': 3, '5': 9, '10': 'sourceFiles'},
    {'1': 'target_format', '3': 2, '4': 1, '5': 9, '10': 'targetFormat'},
    {'1': 'folder_path', '3': 3, '4': 1, '5': 9, '10': 'folderPath'},
    {'1': 'folder_paths', '3': 4, '4': 3, '5': 9, '10': 'folderPaths'},
    {'1': 'plan_type', '3': 5, '4': 1, '5': 9, '10': 'planType'},
    {
      '1': 'prune_matched_excluded',
      '3': 6,
      '4': 1,
      '5': 8,
      '10': 'pruneMatchedExcluded'
    },
  ],
};

/// Descriptor for `PlanOperationsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List planOperationsRequestDescriptor = $convert.base64Decode(
    'ChVQbGFuT3BlcmF0aW9uc1JlcXVlc3QSIQoMc291cmNlX2ZpbGVzGAEgAygJUgtzb3VyY2VGaW'
    'xlcxIjCg10YXJnZXRfZm9ybWF0GAIgASgJUgx0YXJnZXRGb3JtYXQSHwoLZm9sZGVyX3BhdGgY'
    'AyABKAlSCmZvbGRlclBhdGgSIQoMZm9sZGVyX3BhdGhzGAQgAygJUgtmb2xkZXJQYXRocxIbCg'
    'lwbGFuX3R5cGUYBSABKAlSCHBsYW5UeXBlEjQKFnBydW5lX21hdGNoZWRfZXhjbHVkZWQYBiAB'
    'KAhSFHBydW5lTWF0Y2hlZEV4Y2x1ZGVk');

@$core.Deprecated('Use planOperationsResponseDescriptor instead')
const PlanOperationsResponse$json = {
  '1': 'PlanOperationsResponse',
  '2': [
    {'1': 'plan_id', '3': 1, '4': 1, '5': 9, '10': 'planId'},
    {
      '1': 'operations',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.onsei.v1.PlannedOperation',
      '10': 'operations'
    },
    {'1': 'total_count', '3': 3, '4': 1, '5': 5, '10': 'totalCount'},
    {'1': 'actionable_count', '3': 4, '4': 1, '5': 5, '10': 'actionableCount'},
    {'1': 'summary_reason', '3': 6, '4': 1, '5': 9, '10': 'summaryReason'},
    {
      '1': 'plan_errors',
      '3': 7,
      '4': 3,
      '5': 11,
      '6': '.onsei.v1.FolderError',
      '10': 'planErrors'
    },
    {
      '1': 'successful_folders',
      '3': 8,
      '4': 3,
      '5': 9,
      '10': 'successfulFolders'
    },
  ],
  '9': [
    {'1': 5, '2': 6},
  ],
  '10': ['keep_count'],
};

/// Descriptor for `PlanOperationsResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List planOperationsResponseDescriptor = $convert.base64Decode(
    'ChZQbGFuT3BlcmF0aW9uc1Jlc3BvbnNlEhcKB3BsYW5faWQYASABKAlSBnBsYW5JZBI6CgpvcG'
    'VyYXRpb25zGAIgAygLMhoub25zZWkudjEuUGxhbm5lZE9wZXJhdGlvblIKb3BlcmF0aW9ucxIf'
    'Cgt0b3RhbF9jb3VudBgDIAEoBVIKdG90YWxDb3VudBIpChBhY3Rpb25hYmxlX2NvdW50GAQgAS'
    'gFUg9hY3Rpb25hYmxlQ291bnQSJQoOc3VtbWFyeV9yZWFzb24YBiABKAlSDXN1bW1hcnlSZWFz'
    'b24SNgoLcGxhbl9lcnJvcnMYByADKAsyFS5vbnNlaS52MS5Gb2xkZXJFcnJvclIKcGxhbkVycm'
    '9ycxItChJzdWNjZXNzZnVsX2ZvbGRlcnMYCCADKAlSEXN1Y2Nlc3NmdWxGb2xkZXJzSgQIBRAG'
    'UgprZWVwX2NvdW50');

@$core.Deprecated('Use folderErrorDescriptor instead')
const FolderError$json = {
  '1': 'FolderError',
  '2': [
    {'1': 'stage', '3': 1, '4': 1, '5': 9, '10': 'stage'},
    {'1': 'code', '3': 2, '4': 1, '5': 9, '10': 'code'},
    {'1': 'message', '3': 3, '4': 1, '5': 9, '10': 'message'},
    {'1': 'folder_path', '3': 4, '4': 1, '5': 9, '10': 'folderPath'},
    {'1': 'plan_id', '3': 5, '4': 1, '5': 9, '10': 'planId'},
    {'1': 'root_path', '3': 6, '4': 1, '5': 9, '10': 'rootPath'},
    {'1': 'timestamp', '3': 7, '4': 1, '5': 9, '10': 'timestamp'},
    {'1': 'event_id', '3': 8, '4': 1, '5': 9, '10': 'eventId'},
  ],
};

/// Descriptor for `FolderError`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List folderErrorDescriptor = $convert.base64Decode(
    'CgtGb2xkZXJFcnJvchIUCgVzdGFnZRgBIAEoCVIFc3RhZ2USEgoEY29kZRgCIAEoCVIEY29kZR'
    'IYCgdtZXNzYWdlGAMgASgJUgdtZXNzYWdlEh8KC2ZvbGRlcl9wYXRoGAQgASgJUgpmb2xkZXJQ'
    'YXRoEhcKB3BsYW5faWQYBSABKAlSBnBsYW5JZBIbCglyb290X3BhdGgYBiABKAlSCHJvb3RQYX'
    'RoEhwKCXRpbWVzdGFtcBgHIAEoCVIJdGltZXN0YW1wEhkKCGV2ZW50X2lkGAggASgJUgdldmVu'
    'dElk');

@$core.Deprecated('Use plannedOperationDescriptor instead')
const PlannedOperation$json = {
  '1': 'PlannedOperation',
  '2': [
    {'1': 'source_path', '3': 1, '4': 1, '5': 9, '10': 'sourcePath'},
    {'1': 'target_path', '3': 2, '4': 1, '5': 9, '10': 'targetPath'},
    {'1': 'operation_type', '3': 3, '4': 1, '5': 9, '10': 'operationType'},
  ],
};

/// Descriptor for `PlannedOperation`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List plannedOperationDescriptor = $convert.base64Decode(
    'ChBQbGFubmVkT3BlcmF0aW9uEh8KC3NvdXJjZV9wYXRoGAEgASgJUgpzb3VyY2VQYXRoEh8KC3'
    'RhcmdldF9wYXRoGAIgASgJUgp0YXJnZXRQYXRoEiUKDm9wZXJhdGlvbl90eXBlGAMgASgJUg1v'
    'cGVyYXRpb25UeXBl');

@$core.Deprecated('Use executePlanRequestDescriptor instead')
const ExecutePlanRequest$json = {
  '1': 'ExecutePlanRequest',
  '2': [
    {'1': 'plan_id', '3': 1, '4': 1, '5': 9, '10': 'planId'},
    {'1': 'soft_delete', '3': 2, '4': 1, '5': 8, '10': 'softDelete'},
  ],
};

/// Descriptor for `ExecutePlanRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List executePlanRequestDescriptor = $convert.base64Decode(
    'ChJFeGVjdXRlUGxhblJlcXVlc3QSFwoHcGxhbl9pZBgBIAEoCVIGcGxhbklkEh8KC3NvZnRfZG'
    'VsZXRlGAIgASgIUgpzb2Z0RGVsZXRl');

@$core.Deprecated('Use listPlansRequestDescriptor instead')
const ListPlansRequest$json = {
  '1': 'ListPlansRequest',
  '2': [
    {'1': 'root_path', '3': 1, '4': 1, '5': 9, '10': 'rootPath'},
    {'1': 'limit', '3': 2, '4': 1, '5': 5, '10': 'limit'},
  ],
};

/// Descriptor for `ListPlansRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listPlansRequestDescriptor = $convert.base64Decode(
    'ChBMaXN0UGxhbnNSZXF1ZXN0EhsKCXJvb3RfcGF0aBgBIAEoCVIIcm9vdFBhdGgSFAoFbGltaX'
    'QYAiABKAVSBWxpbWl0');

@$core.Deprecated('Use listPlansResponseDescriptor instead')
const ListPlansResponse$json = {
  '1': 'ListPlansResponse',
  '2': [
    {
      '1': 'plans',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.onsei.v1.PlanInfo',
      '10': 'plans'
    },
  ],
};

/// Descriptor for `ListPlansResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listPlansResponseDescriptor = $convert.base64Decode(
    'ChFMaXN0UGxhbnNSZXNwb25zZRIoCgVwbGFucxgBIAMoCzISLm9uc2VpLnYxLlBsYW5JbmZvUg'
    'VwbGFucw==');

@$core.Deprecated('Use refreshFoldersRequestDescriptor instead')
const RefreshFoldersRequest$json = {
  '1': 'RefreshFoldersRequest',
  '2': [
    {'1': 'root_path', '3': 1, '4': 1, '5': 9, '10': 'rootPath'},
    {'1': 'folder_paths', '3': 2, '4': 3, '5': 9, '10': 'folderPaths'},
  ],
};

/// Descriptor for `RefreshFoldersRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List refreshFoldersRequestDescriptor = $convert.base64Decode(
    'ChVSZWZyZXNoRm9sZGVyc1JlcXVlc3QSGwoJcm9vdF9wYXRoGAEgASgJUghyb290UGF0aBIhCg'
    'xmb2xkZXJfcGF0aHMYAiADKAlSC2ZvbGRlclBhdGhz');

@$core.Deprecated('Use refreshFoldersResponseDescriptor instead')
const RefreshFoldersResponse$json = {
  '1': 'RefreshFoldersResponse',
  '2': [
    {
      '1': 'successful_folders',
      '3': 1,
      '4': 3,
      '5': 9,
      '10': 'successfulFolders'
    },
    {
      '1': 'errors',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.onsei.v1.FolderError',
      '10': 'errors'
    },
  ],
};

/// Descriptor for `RefreshFoldersResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List refreshFoldersResponseDescriptor = $convert.base64Decode(
    'ChZSZWZyZXNoRm9sZGVyc1Jlc3BvbnNlEi0KEnN1Y2Nlc3NmdWxfZm9sZGVycxgBIAMoCVIRc3'
    'VjY2Vzc2Z1bEZvbGRlcnMSLQoGZXJyb3JzGAIgAygLMhUub25zZWkudjEuRm9sZGVyRXJyb3JS'
    'BmVycm9ycw==');

@$core.Deprecated('Use planInfoDescriptor instead')
const PlanInfo$json = {
  '1': 'PlanInfo',
  '2': [
    {'1': 'plan_id', '3': 1, '4': 1, '5': 9, '10': 'planId'},
    {'1': 'root_path', '3': 2, '4': 1, '5': 9, '10': 'rootPath'},
    {'1': 'plan_type', '3': 3, '4': 1, '5': 9, '10': 'planType'},
    {'1': 'status', '3': 4, '4': 1, '5': 9, '10': 'status'},
    {'1': 'created_at', '3': 5, '4': 1, '5': 9, '10': 'createdAt'},
  ],
};

/// Descriptor for `PlanInfo`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List planInfoDescriptor = $convert.base64Decode(
    'CghQbGFuSW5mbxIXCgdwbGFuX2lkGAEgASgJUgZwbGFuSWQSGwoJcm9vdF9wYXRoGAIgASgJUg'
    'hyb290UGF0aBIbCglwbGFuX3R5cGUYAyABKAlSCHBsYW5UeXBlEhYKBnN0YXR1cxgEIAEoCVIG'
    'c3RhdHVzEh0KCmNyZWF0ZWRfYXQYBSABKAlSCWNyZWF0ZWRBdA==');

@$core.Deprecated('Use jobEventDescriptor instead')
const JobEvent$json = {
  '1': 'JobEvent',
  '2': [
    {'1': 'event_type', '3': 1, '4': 1, '5': 9, '10': 'eventType'},
    {'1': 'message', '3': 2, '4': 1, '5': 9, '10': 'message'},
    {'1': 'progress_percent', '3': 3, '4': 1, '5': 5, '10': 'progressPercent'},
    {'1': 'timestamp', '3': 4, '4': 1, '5': 9, '10': 'timestamp'},
    {'1': 'stage', '3': 5, '4': 1, '5': 9, '10': 'stage'},
    {'1': 'code', '3': 6, '4': 1, '5': 9, '10': 'code'},
    {'1': 'folder_path', '3': 7, '4': 1, '5': 9, '10': 'folderPath'},
    {'1': 'plan_id', '3': 8, '4': 1, '5': 9, '10': 'planId'},
    {'1': 'root_path', '3': 9, '4': 1, '5': 9, '10': 'rootPath'},
    {'1': 'event_id', '3': 10, '4': 1, '5': 9, '10': 'eventId'},
    {'1': 'item_source_path', '3': 11, '4': 1, '5': 9, '10': 'itemSourcePath'},
    {'1': 'item_target_path', '3': 12, '4': 1, '5': 9, '10': 'itemTargetPath'},
    {'1': 'correlation_id', '3': 13, '4': 1, '5': 9, '10': 'correlationId'},
  ],
};

/// Descriptor for `JobEvent`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List jobEventDescriptor = $convert.base64Decode(
    'CghKb2JFdmVudBIdCgpldmVudF90eXBlGAEgASgJUglldmVudFR5cGUSGAoHbWVzc2FnZRgCIA'
    'EoCVIHbWVzc2FnZRIpChBwcm9ncmVzc19wZXJjZW50GAMgASgFUg9wcm9ncmVzc1BlcmNlbnQS'
    'HAoJdGltZXN0YW1wGAQgASgJUgl0aW1lc3RhbXASFAoFc3RhZ2UYBSABKAlSBXN0YWdlEhIKBG'
    'NvZGUYBiABKAlSBGNvZGUSHwoLZm9sZGVyX3BhdGgYByABKAlSCmZvbGRlclBhdGgSFwoHcGxh'
    'bl9pZBgIIAEoCVIGcGxhbklkEhsKCXJvb3RfcGF0aBgJIAEoCVIIcm9vdFBhdGgSGQoIZXZlbn'
    'RfaWQYCiABKAlSB2V2ZW50SWQSKAoQaXRlbV9zb3VyY2VfcGF0aBgLIAEoCVIOaXRlbVNvdXJj'
    'ZVBhdGgSKAoQaXRlbV90YXJnZXRfcGF0aBgMIAEoCVIOaXRlbVRhcmdldFBhdGgSJQoOY29ycm'
    'VsYXRpb25faWQYDSABKAlSDWNvcnJlbGF0aW9uSWQ=');
