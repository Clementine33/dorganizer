import 'package:flutter_test/flutter_test.dart';
import 'package:onsei_organizer/features/files/file_pane_selection.dart';

void main() {
  group('selectionIsLossless', () {
    test('returns true for all wav/flac paths', () {
      const paths = {
        '/music/track1.wav',
        '/music/track2.flac',
      };
      expect(selectionIsLossless(paths), isTrue);
    });

    test('returns true for single lossless path', () {
      const paths = {'/music/track1.wav'};
      expect(selectionIsLossless(paths), isTrue);
    });

    test('returns false if any path is mp3', () {
      const paths = {
        '/music/track1.wav',
        '/music/track2.mp3',
      };
      expect(selectionIsLossless(paths), isFalse);
    });

    test('returns false for non-audio extension', () {
      const paths = {'/music/notes.txt'};
      expect(selectionIsLossless(paths), isFalse);
    });

    test('returns true for empty set', () {
      const Set<String> paths = {};
      expect(selectionIsLossless(paths), isTrue);
    });

    test('handles mixed case extensions', () {
      const paths = {'/music/track1.WAV', '/music/track2.Flac'};
      expect(selectionIsLossless(paths), isTrue);
    });
  });

  group('getRelativePath', () {
    test('returns fullPath when folderPath is null', () {
      expect(
        getRelativePath('/music/album/track.mp3', null),
        equals('/music/album/track.mp3'),
      );
    });

    test('returns fullPath when folderPath is empty', () {
      expect(
        getRelativePath('/music/album/track.mp3', ''),
        equals('/music/album/track.mp3'),
      );
    });

    test('strips folder prefix', () {
      expect(
        getRelativePath('/music/album/track.mp3', '/music'),
        equals('album/track.mp3'),
      );
    });

    test('strips folder prefix with trailing slash', () {
      expect(
        getRelativePath('/music/album/track.mp3', '/music/'),
        equals('album/track.mp3'),
      );
    });

    test('returns fullPath when not under folder', () {
      expect(
        getRelativePath('/other/album/track.mp3', '/music'),
        equals('/other/album/track.mp3'),
      );
    });

    test('handles Windows backslash paths', () {
      expect(
        getRelativePath(r'C:\music\album\track.mp3', r'C:\music'),
        equals('album/track.mp3'),
      );
    });

    test('handles mixed slash paths', () {
      expect(
        getRelativePath(r'C:\music/album\track.mp3', r'C:\music'),
        equals('album/track.mp3'),
      );
    });

    test('does not strip prefix that is a partial directory name match', () {
      // /music must not match /music2/... as a prefix
      expect(
        getRelativePath('/music2/album/track.mp3', '/music'),
        equals('/music2/album/track.mp3'),
      );
    });

    test('does not strip prefix on partial match with trailing slash', () {
      expect(
        getRelativePath('/music2/album/track.mp3', '/music/'),
        equals('/music2/album/track.mp3'),
      );
    });
  });

  group('reconcileSelection', () {
    test('returns same set when all paths exist', () {
      const current = {'/a.mp3', '/b.flac'};
      const existing = {'/a.mp3', '/b.flac', '/c.wav'};
      final result = reconcileSelection(current, existing);
      expect(result, equals(current));
    });

    test('prunes paths not in existing set', () {
      const current = {'/a.mp3', '/b.flac', '/d.txt'};
      const existing = {'/a.mp3', '/b.flac'};
      final result = reconcileSelection(current, existing);
      expect(result, equals({'/a.mp3', '/b.flac'}));
    });

    test('returns same set when current is empty', () {
      const Set<String> current = {};
      const existing = {'/a.mp3'};
      final result = reconcileSelection(current, existing);
      expect(result, same(current));
    });

    test('returns same set when existing is empty', () {
      const current = {'/a.mp3'};
      const Set<String> existing = {};
      final result = reconcileSelection(current, existing);
      expect(result, isEmpty);
    });

    test('returns same set when current is subset of existing', () {
      const current = {'/a.mp3'};
      const existing = {'/a.mp3', '/b.flac'};
      final result = reconcileSelection(current, existing);
      expect(result, equals(current));
    });
  });
}
