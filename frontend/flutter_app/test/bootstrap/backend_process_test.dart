import 'dart:async';
import 'dart:io';

import 'package:test/test.dart';
import 'package:onsei_organizer/bootstrap/backend_process.dart';

/// Fake process for testing BackendProcess.stop behavior
class FakeBackendChildProcess implements BackendChildProcess {
  final StreamController<List<int>> _stdoutController =
      StreamController<List<int>>();
  final StreamController<List<int>> _stderrController =
      StreamController<List<int>>();
  final Completer<int> _exitCodeCompleter = Completer<int>();
  bool _killCalled = false;
  bool _stdinClosed = false;

  bool get killCalled => _killCalled;
  bool get stdinClosed => _stdinClosed;

  @override
  Stream<List<int>> get stdout => _stdoutController.stream;

  @override
  Stream<List<int>> get stderr => _stderrController.stream;

  bool get stderrHasListener => _stderrController.hasListener;

  @override
  Future<int> get exitCode => _exitCodeCompleter.future;

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    _killCalled = true;
    // When killed, complete exitCode if not already done
    if (!_exitCodeCompleter.isCompleted) {
      _exitCodeCompleter.complete(-1);
    }
    return true;
  }

  @override
  Future<void> closeStdin() async {
    _stdinClosed = true;
  }

  /// Completes the exitCode future to simulate process exit
  void completeExitCode(int code) {
    if (!_exitCodeCompleter.isCompleted) {
      _exitCodeCompleter.complete(code);
    }
  }

  void dispose() {
    _stdoutController.close();
    _stderrController.close();
  }
}

void main() {
  group('BackendProcess', () {
    test('includes .exe backend candidates on Windows', () {
      final paths = backendCandidatePaths(isWindows: true);

      expect(paths.first, 'bin/onsei-organizer-backend.exe');
      expect(paths, contains('bin/onsei-organizer-backend.exe'));
      expect(paths, contains('../../backend/go/bin/onsei-organizer-backend'));
      expect(
        paths,
        contains('../../backend/go/bin/onsei-organizer-backend.exe'),
      );
    });

    test('parses backend READY handshake line', () {
      const line = 'ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1';
      final parsed = parseReadyLine(line);
      expect(parsed?.port, 51234);
      expect(parsed?.token, 'tok-1');
    });

    test('returns null for invalid line', () {
      const line = 'INVALID LINE';
      final parsed = parseReadyLine(line);
      expect(parsed, isNull);
    });

    test('parses line without version', () {
      const line = 'ONSEI_BACKEND_READY port=8080 token=abc';
      final parsed = parseReadyLine(line);
      expect(parsed, isNull); // version is required
    });

    test('uses .dev_data under project root in debug mode', () async {
      final fakeProcess = FakeBackendChildProcess();
      Map<String, String>? capturedEnv;

      final backend = BackendProcess(
        isDebugMode: true,
        fileExists: (_) async => true,
        processStarter: (executable, arguments, environment) async {
          capturedEnv = environment;
          return fakeProcess;
        },
      );

      await backend.start();

      final expectedDebugDataDir =
          Directory.current.path.endsWith(Platform.pathSeparator)
          ? '${Directory.current.path}.dev_data'
          : '${Directory.current.path}${Platform.pathSeparator}.dev_data';

      expect(capturedEnv, isNotNull);
      expect(capturedEnv!['ONSEI_DATA_DIR'], expectedDebugDataDir);

      fakeProcess.dispose();
    });

    test('uses resolved executable parent in non-debug mode', () async {
      final fakeProcess = FakeBackendChildProcess();
      Map<String, String>? capturedEnv;

      final backend = BackendProcess(
        isDebugMode: false,
        fileExists: (_) async => true,
        processStarter: (executable, arguments, environment) async {
          capturedEnv = environment;
          return fakeProcess;
        },
      );

      await backend.start();

      final expectedReleaseDataDir = File(
        Platform.resolvedExecutable,
      ).parent.path;

      expect(capturedEnv, isNotNull);
      expect(capturedEnv!['ONSEI_DATA_DIR'], expectedReleaseDataDir);

      fakeProcess.dispose();
    });

    test('drains stderr to avoid backend log pipe blocking', () async {
      final fakeProcess = FakeBackendChildProcess();

      final backend = BackendProcess(
        isDebugMode: true,
        fileExists: (_) async => true,
        processStarter: (executable, arguments, environment) async {
          return fakeProcess;
        },
      );

      await backend.start();

      expect(fakeProcess.stderrHasListener, isTrue);

      fakeProcess.dispose();
    });
  });

  group('BackendProcess.stop', () {
    test(
      'closes stdin and waits for exitCode without kill when process exits promptly',
      () async {
        final fakeProcess = FakeBackendChildProcess();

        final backend = BackendProcess(initialProcess: fakeProcess);

        // Simulate the process exiting promptly after stdin close
        fakeProcess.completeExitCode(0);

        await backend.stop();

        expect(
          fakeProcess.stdinClosed,
          isTrue,
          reason: 'stdin should be closed',
        );
        expect(
          fakeProcess.killCalled,
          isFalse,
          reason: 'kill should not be called when process exits promptly',
        );

        fakeProcess.dispose();
      },
    );

    test(
      'force-kills when exitCode does not complete within timeout',
      () async {
        final fakeProcess = FakeBackendChildProcess();

        final backend = BackendProcess(initialProcess: fakeProcess);

        // Do NOT complete exitCode - simulate a hanging process
        // stop() will timeout and call kill

        await backend.stop();

        expect(
          fakeProcess.stdinClosed,
          isTrue,
          reason: 'stdin should be closed',
        );
        expect(
          fakeProcess.killCalled,
          isTrue,
          reason: 'kill should be called when exitCode times out',
        );

        fakeProcess.dispose();
      },
    );
  });
}
