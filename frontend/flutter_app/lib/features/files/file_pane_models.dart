/// View model for file list entries.
class FileEntry {
  final String path;
  final int bitrate; // in bits per second

  const FileEntry({required this.path, this.bitrate = 0});

  /// Bitrate label like "320 kbps", or empty string if bitrate <= 0.
  String get bitrateLabel =>
      bitrate > 0 ? '${(bitrate / 1000).round()} kbps' : '';

  /// Whether to show the bitrate badge (only for .mp3 with positive bitrate).
  bool get showBitrateBadge =>
      bitrate > 0 && path.toLowerCase().endsWith('.mp3');
}
