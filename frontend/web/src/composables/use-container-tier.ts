import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

/** Workbench container tiers of the design layout table (§7.3). */
export type ContainerTier = 'narrow' | 'mid' | 'wide'

/**
 * Container breakpoints in CSS pixels. These are the single breakpoint
 * definition shared by the CSS container queries and the interaction switch:
 * the CSS decides sizes and visibility at the same boundaries at which the
 * carrier changes from inline detail to modal sheet to full page (F13).
 */
export const CONTAINER_BREAKPOINTS = { mid: 641, wide: 1101 } as const

export function tierForWidth(width: number): ContainerTier {
  if (width >= CONTAINER_BREAKPOINTS.wide) return 'wide'
  if (width >= CONTAINER_BREAKPOINTS.mid) return 'mid'
  return 'narrow'
}

/**
 * Observes the real available width of the workbench container — never the
 * window — and reports the tier whose interaction model applies. Only the
 * carrier decision is observed in script; sizes and visibility stay in CSS.
 */
export function useContainerTier(target: Ref<HTMLElement | null>) {
  const tier = ref<ContainerTier>('wide')
  const width = ref(Number.POSITIVE_INFINITY)
  let observer: ResizeObserver | undefined

  function measure() {
    const el = target.value
    if (!el) return
    const measured = el.clientWidth
    width.value = measured
    tier.value = tierForWidth(measured)
  }

  onMounted(() => {
    measure()
    if (typeof ResizeObserver === 'undefined') return
    observer = new ResizeObserver(measure)
    if (target.value) observer.observe(target.value)
  })
  onBeforeUnmount(() => observer?.disconnect())

  return { tier, width, measure }
}
