import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

const _backendBasePaths = [
  'bin/onsei-organizer-backend',
  '../../backend/go/bin/onsei-organizer-backend',
];

List<String> backendCandidatePaths({required bool isWindows}) {
  if (!isWindows) {
    return List<String>.from(_backendBasePaths);
  }

  return [
    for (final path in _backendBasePaths) '$path.exe',
    ..._backendBasePaths,
  ];
}

/// ReadySignal represents the backend ready handshake
class ReadySignal {
  final int port;
  final String token;
  final String version;

  ReadySignal({required this.port, required this.token, required this.version});
}

/// BackendProcess manages the backend subprocess
class BackendProcess {
  BackendChildProcess? _process;
  final _readyCompleter = Completer<ReadySignal?>();
  final bool _isDebugMode;

  final Future<bool> Function(String path) _fileExists;

  final Future<BackendChildProcess> Function(
    String executable,
    List<String> arguments,
    Map<String, String> environment,
  )
  _processStarter;

  BackendProcess({
    Future<BackendChildProcess> Function(
      String executable,
      List<String> arguments,
      Map<String, String> environment,
    )?
    processStarter,
    bool? isDebugMode,
    Future<bool> Function(String path)? fileExists,
    BackendChildProcess? initialProcess,
  }) : _processStarter = processStarter ?? _defaultProcessStarter,
       _isDebugMode = isDebugMode ?? kDebugMode,
       _fileExists = fileExists ?? _defaultFileExists,
       _process = initialProcess;

  /// start launches the backend process
  Future<void> start() async {
    // Look for backend in common locations
    final backendPaths = backendCandidatePaths(isWindows: Platform.isWindows);

    String? backendPath;
    for (final path in backendPaths) {
      if (await _fileExists(path)) {
        backendPath = path;
        break;
      }
    }

    if (backendPath == null) {
      // Try to build or use a mock for testing
      _readyCompleter.complete(null);
      return;
    }

    _process = await _processStarter(
      backendPath,
      ['serve'],
      {
        'ONSEI_BACKEND': '1',
        'ONSEI_DATA_DIR': _resolveDataDir(isDebugMode: _isDebugMode),
      },
    );

    // Listen for ready line
    _process!.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_handleLine);

    // Drain stderr continuously to avoid backend log pipe backpressure.
    _process!.stderr.listen((_) {});
  }

  void _handleLine(String line) {
    if (line.startsWith('ONSEI_BACKEND_READY')) {
      final parsed = parseReadyLine(line);
      _readyCompleter.complete(parsed);
    }
  }

  /// waitForReady returns the ready signal when backend is ready
  Future<ReadySignal?> waitForReady() async {
    return _readyCompleter.future;
  }

  /// stop terminates the backend process
  Future<void> stop() async {
    final process = _process;
    _process = null;
    if (process == null) {
      return;
    }

    try {
      await process.closeStdin();
    } catch (_) {
      // Ignore stdin close failure and continue waiting/forcing shutdown.
    }

    try {
      await process.exitCode.timeout(const Duration(seconds: 5));
      return;
    } on TimeoutException {
      process.kill();
      await process.exitCode;
    }
  }
}

String _resolveDataDir({required bool isDebugMode}) {
  if (isDebugMode) {
    return _joinPath(Directory.current.path, '.dev_data');
  }

  return File(Platform.resolvedExecutable).parent.path;
}

String _joinPath(String base, String child) {
  final separator = Platform.pathSeparator;
  if (base.endsWith(separator)) {
    return '$base$child';
  }
  return '$base$separator$child';
}

Future<bool> _defaultFileExists(String path) async {
  return File(path).exists();
}

Future<BackendChildProcess> _defaultProcessStarter(
  String executable,
  List<String> arguments,
  Map<String, String> environment,
) async {
  final process = await Process.start(
    executable,
    arguments,
    environment: environment,
  );
  return _ProcessAdapter(process);
}

abstract class BackendChildProcess {
  Stream<List<int>> get stdout;

  Stream<List<int>> get stderr;

  Future<int> get exitCode;

  bool kill([ProcessSignal signal = ProcessSignal.sigterm]);

  Future<void> closeStdin();
}

class _ProcessAdapter implements BackendChildProcess {
  final Process _process;

  _ProcessAdapter(this._process);

  @override
  Stream<List<int>> get stdout => _process.stdout;

  @override
  Stream<List<int>> get stderr => _process.stderr;

  @override
  Future<int> get exitCode => _process.exitCode;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    return _process.kill(signal);
  }

  @override
  Future<void> closeStdin() async {
    await _process.stdin.close();
  }
}

/// parseReadyLine parses the backend ready handshake line
/// Format: ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1
ReadySignal? parseReadyLine(String line) {
  if (!line.startsWith('ONSEI_BACKEND_READY ')) {
    return null;
  }

  final rest = line.substring('ONSEI_BACKEND_READY '.length);
  final parts = rest.split(' ');

  int? port;
  String? token;
  String? version;

  for (final part in parts) {
    final kv = part.split('=');
    if (kv.length != 2) continue;

    switch (kv[0]) {
      case 'port':
        port = int.tryParse(kv[1]);
        break;
      case 'token':
        token = kv[1];
        break;
      case 'version':
        version = kv[1];
        break;
    }
  }

  if (port == null || token == null || version == null) {
    return null;
  }

  return ReadySignal(port: port, token: token, version: version);
}
