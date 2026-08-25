import { describe, expect, it } from 'vitest'
import type { TreeNode } from '@/lib/api/types'
import { createTreeModel } from './tree-model'

function dir(name: string, path: string, children: TreeNode[] = []): TreeNode {
  return { name, path, type: 'dir', format: '', bitrate: null, children }
}

function file(name: string, path: string, format = 'flac', bitrate = 920000, size = 12345): TreeNode {
  return { name, path, type: 'file', format, bitrate, size }
}

const rootFixture: TreeNode = dir('albumA', '/music/albumA', [
  file('track1.flac', '/music/albumA/track1.flac'),
  dir('sub2', '/music/albumA/sub2', [file('b.flac', '/music/albumA/sub2/b.flac')]),
  file('track2.flac', '/music/albumA/track2.flac'),
  dir('sub1', '/music/albumA/sub1', [
    file('a.flac', '/music/albumA/sub1/a.flac'),
    dir('deep', '/music/albumA/sub1/deep', [file('c.flac', '/music/albumA/sub1/deep/c.flac')]),
  ]),
])

describe('tree model', () => {
  it('orders children dirs-first while preserving server order within each group', () => {
    const model = createTreeModel(rootFixture)
    expect(model.root.children.map((node) => node.name)).toEqual([
      'sub2',
      'sub1',
      'track1.flac',
      'track2.flac',
    ])
  })

  it('assigns depth and flattens visible nodes depth-first with dirs collapsed by default', () => {
    const model = createTreeModel(rootFixture)
    expect(model.root.depth).toBe(0)
    // Root is expanded by default; nested dirs start collapsed.
    expect(model.getVisibleNodes().map((node) => node.name)).toEqual([
      'albumA',
      'sub2',
      'sub1',
      'track1.flac',
      'track2.flac',
    ])
    expect(model.getVisibleNodes().find((node) => node.name === 'sub1')?.depth).toBe(1)
  })

  it('expands and collapses a directory via toggle', () => {
    const model = createTreeModel(rootFixture)
    model.toggleDir('/music/albumA/sub1')
    expect(model.getVisibleNodes().map((node) => node.name)).toContain('a.flac')
    model.toggleDir('/music/albumA/sub1')
    expect(model.getVisibleNodes().map((node) => node.name)).not.toContain('a.flac')
  })

  it('selecting a directory selects all descendant files and dir state reflects it', () => {
    const model = createTreeModel(rootFixture)

    model.selectDir('/music/albumA/sub1', true)

    expect(model.selectedFilePaths.has('/music/albumA/sub1/a.flac')).toBe(true)
    expect(model.selectedFilePaths.has('/music/albumA/sub1/deep/c.flac')).toBe(true)
    expect(model.selectedFilePaths.has('/music/albumA/track1.flac')).toBe(false)
    expect(model.dirSelection('/music/albumA/sub1')).toBe('checked')
    expect(model.dirSelection('/music/albumA/sub1/deep')).toBe('checked')
    expect(model.dirSelection('/music/albumA')).toBe('indeterminate')

    model.selectDir('/music/albumA/sub1', false)
    expect(model.selectedFilePaths.has('/music/albumA/sub1/a.flac')).toBe(false)
    expect(model.selectedFilePaths.has('/music/albumA/sub1/deep/c.flac')).toBe(false)
    expect(model.dirSelection('/music/albumA/sub1')).toBe('unchecked')
  })

  it('keeps dir state checked when every descendant file is selected individually', () => {
    const model = createTreeModel(rootFixture)
    model.selectFile('/music/albumA/sub2/b.flac', true)
    expect(model.dirSelection('/music/albumA/sub2')).toBe('checked')
    model.selectFile('/music/albumA/sub2/b.flac', false)
    expect(model.dirSelection('/music/albumA/sub2')).toBe('unchecked')
  })

  it('uses server basenames as labels and never synthesizes names from paths', () => {
    const model = createTreeModel(rootFixture)
    expect(model.root.name).toBe('albumA')
    for (const node of model.getVisibleNodes()) {
      expect(node.name).not.toContain('/')
      expect(node.name).not.toContain('\\')
    }
    // Payload paths are echoed verbatim from the server tree.
    expect(model.selectedFilePaths).toBeInstanceOf(Set)
    model.selectFile('/music/albumA/track2.flac', true)
    expect(model.selectedFilePaths.has('/music/albumA/track2.flac')).toBe(true)
  })
})