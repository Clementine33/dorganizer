import { describe, expect, it } from 'vitest'
import { router } from './router'

/**
 * The route table as a contract (ADR 0003 §1): the documented addresses are the
 * only ones that resolve, they carry identities rather than names, and the
 * retired long addresses land on the not-found page instead of being guessed at.
 */

const DIR_ID = 'a1b2c3d4e5f60718293a4b5c6d7e8f90'

const DOCUMENTED: Array<[path: string, name: string]> = [
  ['/worksets', 'worksets'],
  ['/worksets/lib-1', 'workbench-overview'],
  [`/worksets/lib-1/f/${DIR_ID}`, 'overview-files'],
  ['/worksets/lib-1/conversion', 'conversion'],
  ['/worksets/lib-1/conversion/m-1', 'conversion-member'],
  ['/worksets/lib-1/conversion/m-1/files', 'conversion-member-files'],
  ['/worksets/lib-1/conversion/m-1/edit', 'conversion-member-edit'],
  ['/worksets/lib-1/conversion/settings', 'conversion-settings'],
  ['/worksets/lib-1/conversion/batch-edit', 'conversion-batch-edit'],
  ['/worksets/lib-1/conversion/execution', 'conversion-execution'],
]

describe('workbench route table', () => {
  it.each(DOCUMENTED)('resolves %s to the %s page', (path, name) => {
    expect(router.resolve(path).name).toBe(name)
  })

  it('reads the identity a path carries as its parameter, never as a name', () => {
    expect(router.resolve(`/worksets/lib-1/f/${DIR_ID}`).params).toMatchObject({
      libraryId: 'lib-1',
      dirId: DIR_ID,
    })
    expect(router.resolve('/worksets/lib-1/conversion/m-1/files').params).toMatchObject({
      libraryId: 'lib-1',
      memberId: 'm-1',
    })
  })

  it('resolves the static pages of the conversion section before a member', () => {
    // `/conversion/settings` could be read as a member named "settings": the
    // static page wins, and a member is only what the record says it is.
    for (const [path, name] of [
      ['/worksets/lib-1/conversion/settings', 'conversion-settings'],
      ['/worksets/lib-1/conversion/execution', 'conversion-execution'],
      ['/worksets/lib-1/conversion/batch-edit', 'conversion-batch-edit'],
    ] as const) {
      expect(router.resolve(path).name).toBe(name)
      expect(router.resolve(path).params.memberId).toBeUndefined()
    }
  })

  it('shows the not-found page for the retired long addresses', () => {
    for (const path of [
      '/worksets/libraries/lib-1',
      '/worksets/libraries/lib-1/files',
      '/worksets/libraries/lib-1/conversion',
      '/worksets/libraries/lib-1/conversion/members/m-1/files',
    ]) {
      expect(router.resolve(path).name, path).toBe('not-found')
    }
  })

  it('shows the not-found page for an address that names no page', () => {
    expect(router.resolve('/nope').name).toBe('not-found')
  })

  it('lets a page read its own parameters from the address it was given', () => {
    const route = router.resolve(`/worksets/lib-1/f/${DIR_ID}?view=plan`)
    expect(route.params.dirId).toBe(DIR_ID)
  })
})
