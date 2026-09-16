import { mount, type VueWrapper } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import UnitFields from './UnitFields.vue'
import UnitSelect from './UnitSelect.vue'

/** Emits one change on the UnitSelect carrying the given testid. */
function pickCodec(wrapper: VueWrapper, testId: string, value: string) {
  const select = wrapper.findAllComponents(UnitSelect).find((c) => c.vm.$attrs['data-testid'] === testId)
  if (!select) throw new Error(`no unit select ${testId}`)
  select.vm.$emit('change', value)
}

function mountFields(value: unknown): VueWrapper {
  return mount(UnitFields, { props: { unit: 'matched', label: '无音效目标', value, scope: 'common' } })
}

describe('UnitFields', () => {
  it('seeds the displayed bitrate when an encoded codec is picked from 不需要', () => {
    const wrapper = mountFields({})

    pickCodec(wrapper, 'common-matched-encoded', 'aac')

    // A bitrate-less encoded output cannot generate, so a fresh pick is never
    // saved without one.
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      encoded: { codec: 'aac', quality: { kind: 'bitrate', bitrate: 320 } },
    })
  })

  it('keeps the existing bitrate across a codec change and never adds one to lossless', () => {
    const wrapper = mountFields({ encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 192 } } })

    pickCodec(wrapper, 'common-matched-encoded', 'aac')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      encoded: { codec: 'aac', quality: { kind: 'bitrate', bitrate: 192 } },
    })

    pickCodec(wrapper, 'common-matched-lossless', 'wav')
    expect(wrapper.emitted('change')?.[1]?.[0]).toEqual({
      lossless: { codec: 'wav' },
      encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 192 } },
    })
  })

  it('warns that an empty profile removes the partition audio', () => {
    const empty = mountFields({})
    expect(empty.get('[data-testid="empty-profile-warning"]').text()).toContain('该分类下的音频将被移除')

    const declared = mountFields({ lossless: { codec: 'wav' } })
    expect(declared.find('[data-testid="empty-profile-warning"]').exists()).toBe(false)
  })
})
