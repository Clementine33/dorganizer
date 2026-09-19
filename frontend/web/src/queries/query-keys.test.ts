import { describe, expect, it } from 'vitest'
import { queryKeys } from './query-keys'

describe('queryKeys', () => {
  it('keeps libraries list shape stable', () => {
    expect(queryKeys.libraries.list()).toEqual(['libraries', 'list'])
  })

  it('isolates the overview listing by library and root identity', () => {
    expect(queryKeys.libraries.dirs('lib-a', 'root-a')).toEqual(['libraries', 'dirs', 'lib-a', 'root-a'])
    expect(queryKeys.libraries.dirs('lib-a', 'root-b')).not.toEqual(
      queryKeys.libraries.dirs('lib-a', 'root-a'),
    )
    expect(queryKeys.libraries.dirs('lib-b', 'root-a')).not.toEqual(
      queryKeys.libraries.dirs('lib-a', 'root-a'),
    )
  })

  it('isolates member trees by library, root identity and member path', () => {
    const key = queryKeys.libraries.memberTree('lib-a', 'root-a', 'albumA')
    expect(key).toEqual(['libraries', 'member-trees', 'lib-a', 'root-a', 'albumA'])
    expect(queryKeys.libraries.memberTree('lib-a', 'root-a', 'albumB')).not.toEqual(key)
    expect(queryKeys.libraries.memberTree('lib-a', 'root-b', 'albumA')).not.toEqual(key)
    expect(queryKeys.libraries.memberTree('lib-b', 'root-a', 'albumA')).not.toEqual(key)
  })

  it('supports prefix invalidation over the listing and trees of one library', () => {
    expect(queryKeys.libraries.dirs('lib-a', 'r').slice(0, 3)).toEqual(
      queryKeys.libraries.dirsPrefix('lib-a'),
    )
    expect(queryKeys.libraries.memberTree('lib-a', 'r', 'albumA').slice(0, 3)).toEqual(
      queryKeys.libraries.memberTreesPrefix('lib-a'),
    )
  })

  it('addresses a library\'s current record by the pair, not by a record id', () => {
    expect(queryKeys.worksets.current('lib-a', 'conversion')).toEqual(['worksets', 'current', 'lib-a', 'conversion'])
    expect(queryKeys.worksets.current('lib-b', 'conversion')).not.toEqual(
      queryKeys.worksets.current('lib-a', 'conversion'),
    )
  })

  it('isolates plan lists by library and limit', () => {
    expect(queryKeys.plans.list('lib-a', 100)).toEqual(['plans', 'list', 'lib-a', 100])
    expect(queryKeys.plans.list('lib-a', 50)).not.toEqual(queryKeys.plans.list('lib-a', 100))
    expect(queryKeys.plans.list('lib-b', 100)).not.toEqual(queryKeys.plans.list('lib-a', 100))
  })

  it('keeps plan detail keyed by plan ID', () => {
    expect(queryKeys.plans.detail('plan-1')).toEqual(['plans', 'detail', 'plan-1'])
    expect(queryKeys.plans.detail('plan-2')).not.toEqual(queryKeys.plans.detail('plan-1'))
  })
})