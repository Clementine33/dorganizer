import { describe, expect, it } from 'vitest'
import { rootPathIdentityKey } from './root-path-identity'

// Characterization tests for the exact semantics the library store relied on.
// The function is extracted verbatim from the previous store; this task must
// preserve its behavior, not "improve" it. Notable current quirks:
//   - no trailing-slash stripping (C:\Music\ != c:/music)
//   - UNC paths are recognized as Windows-style but are not lowercased
//     (lowercasing only applies to drive-letter paths)
describe('rootPathIdentityKey', () => {
  it('keeps POSIX case significant', () => {
    expect(rootPathIdentityKey('/Music')).not.toBe(rootPathIdentityKey('/music'))
  })

  it('treats an identical path as equal', () => {
    expect(rootPathIdentityKey('/music')).toBe('/music')
  })

  it('does not strip trailing separators (current semantics)', () => {
    expect(rootPathIdentityKey('/music/')).not.toBe(rootPathIdentityKey('/music'))
  })

  it('folds Windows drive-letter case and slash direction', () => {
    expect(rootPathIdentityKey('C:\\Music')).toBe(rootPathIdentityKey('c:/music'))
  })

  it('keeps trailing slashes significant for Windows drives (current semantics)', () => {
    expect(rootPathIdentityKey('C:\\Music\\')).not.toBe(rootPathIdentityKey('c:/music'))
  })

  it('preserves Windows drive-root structure', () => {
    expect(rootPathIdentityKey('C:/')).toBe(rootPathIdentityKey('c:/'))
    expect(rootPathIdentityKey('C:/')).not.toBe(rootPathIdentityKey('C:/music'))
  })

  it('recognizes UNC paths as Windows-style but preserves host case (current semantics)', () => {
    expect(rootPathIdentityKey('\\\\Server\\Share\\Music')).toBe('//Server/Share/Music')
    expect(rootPathIdentityKey('\\\\Server\\Share\\Music')).not.toBe('//server/share/music')
  })

  it('folds backslash-only Windows drive letters', () => {
    expect(rootPathIdentityKey('D:\\')).toBe(rootPathIdentityKey('d:/'))
  })
})