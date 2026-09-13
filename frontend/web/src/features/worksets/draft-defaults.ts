import type { DesiredProfile, OverrideUnit } from '@/lib/api/types'

/**
 * The values a new conversion draft is seeded with (backend `seedDraft`):
 * relaxed mode, the deployment's literal tags, and WAV + MP3@320 for both
 * partitions. 恢复默认 restores exactly these, so "default" means one thing.
 */
export function defaultDraftValues(defaultTags: string[]): Record<OverrideUnit, unknown> {
  const profile: DesiredProfile = {
    lossless: { codec: 'wav' },
    encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } },
  }
  return {
    mode: 'available_sources',
    classifier_tags: [...defaultTags],
    matched: profile,
    unmatched: profile,
  }
}
