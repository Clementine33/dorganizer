import { describe, expect, it } from 'vitest'
import { queryKeys } from './query-keys'

describe('queryKeys', () => {
  it('keeps libraries list shape stable', () => {
    expect(queryKeys.libraries.list()).toEqual(['libraries', 'list'])
  })

  it('isolates folders by library and root identity', () => {
    expect(queryKeys.libraries.folders('lib-a', 'root-a')).toEqual(['libraries', 'folders', 'lib-a', 'root-a'])
    expect(queryKeys.libraries.folders('lib-a', 'root-b')).not.toEqual(
      queryKeys.libraries.folders('lib-a', 'root-a'),
    )
    expect(queryKeys.libraries.folders('lib-b', 'root-a')).not.toEqual(
      queryKeys.libraries.folders('lib-a', 'root-a'),
    )
  })

  it('isolates trees by library, root identity and folder', () => {
    const key = queryKeys.libraries.tree('lib-a', 'root-a', 'folder-1')
    expect(key).toEqual(['libraries', 'folder-trees', 'lib-a', 'root-a', 'folder-1'])
    expect(queryKeys.libraries.tree('lib-a', 'root-a', 'folder-2')).not.toEqual(key)
    expect(queryKeys.libraries.tree('lib-a', 'root-b', 'folder-1')).not.toEqual(key)
    expect(queryKeys.libraries.tree('lib-b', 'root-a', 'folder-1')).not.toEqual(key)
  })

  it('supports prefix invalidation over folders and trees of one library', () => {
    expect(queryKeys.libraries.folders('lib-a', 'r').slice(0, 3)).toEqual(
      queryKeys.libraries.foldersPrefix('lib-a'),
    )
    expect(queryKeys.libraries.tree('lib-a', 'r', 'f').slice(0, 3)).toEqual(
      queryKeys.libraries.treesPrefix('lib-a'),
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