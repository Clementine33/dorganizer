<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import { useRoute, useRouter } from 'vue-router'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { useLibraryList } from '@/queries/libraries'
import {
  worksetDetailQueryOptions,
  worksetDraftQueryOptions,
  worksetFeedInfiniteQueryOptions,
  worksetRevisionListInfiniteQueryOptions,
} from '@/queries/worksets'
import { useWorksetUiStore } from '@/stores/workset-ui'
import WorksetFeed from '@/features/worksets/WorksetFeed.vue'
import WorksetWorkbench from '@/features/worksets/WorksetWorkbench.vue'
import type { WorksetFeedFilter } from '@/lib/api/types'

const api = useApiClient()
const router = useRouter()
const route = useRoute()
const ui = useWorksetUiStore()

// Feed scope: status bucket + library filter, both server-side.
const feedFilter = computed(() => (route.query.feed as WorksetFeedFilter) || 'all')
const libraryFilter = computed(() => (route.query.library as string) || null)

const { librariesData: libraries } = useLibraryList()

const feedQuery = useInfiniteQuery(
  computed(() => worksetFeedInfiniteQueryOptions(api, { feed: feedFilter.value, libraryId: libraryFilter.value })),
)
const worksets = computed(() => feedQuery.data.value?.pages.flatMap((page) => page.worksets) ?? [])
const hasMore = computed(() => feedQuery.hasNextPage.value)
const feedError = computed(() => feedQuery.error.value as Error | null)
const feedInitialLoading = computed(() => feedQuery.isPending.value && !feedQuery.data.value)

// Selected workset: URL param is authoritative; when absent, auto-select the
// first feed entry (deeplink-friendly: the selection becomes the URL).
const selectedId = computed(() => (route.params.worksetId as string) || null)

watchEffect(() => {
  if (selectedId.value) {
    if (ui.selectedWorksetId !== selectedId.value) ui.selectWorkset(selectedId.value)
    return
  }
  if (!feedQuery.data.value) return
  const first = worksets.value[0]
  if (first) {
    void router.replace({ path: `/worksets/${encodeURIComponent(first.workset_id)}`, query: route.query })
  }
})

const detailQuery = useQuery(computed(() => worksetDetailQueryOptions(api, selectedId.value)))
const draftQuery = useQuery(computed(() => worksetDraftQueryOptions(api, selectedId.value)))
const revisionListQuery = useInfiniteQuery(
  computed(() => worksetRevisionListInfiniteQueryOptions(api, selectedId.value)),
)
const revisionList = computed(() => revisionListQuery.data.value?.pages.flatMap((page) => page.revisions) ?? [])
const hasMoreRevisions = computed(() => revisionListQuery.hasNextPage.value)

function loadEarlierRevisions() {
  void revisionListQuery.fetchNextPage()
}

const selectedWorkset = computed(() => detailQuery.data.value ?? null)

// The selected workset may be excluded by the active filter. Per the product
// decision the right pane keeps its context; show a notice above the feed.
const selectedExcludedByFilter = computed(() => {
  if (!selectedId.value || feedFilter.value === 'all') return false
  return !worksets.value.some((ws) => ws.workset_id === selectedId.value)
})

function selectWorkset(id: string) {
  void router.push({ path: `/worksets/${encodeURIComponent(id)}`, query: route.query })
}

// Filter changes keep the currently selected workset in the URL: the right
// pane never loses its context just because the filter moved on (the feed
// shows an exclusion notice instead of silently switching away).
function setFilter(filter: WorksetFeedFilter) {
  const path = selectedId.value ? `/worksets/${encodeURIComponent(selectedId.value)}` : '/worksets'
  void router.push({ path, query: { ...route.query, feed: filter === 'all' ? undefined : filter } })
}

function setLibraryFilter(libraryId: string | null) {
  const path = selectedId.value ? `/worksets/${encodeURIComponent(selectedId.value)}` : '/worksets'
  void router.push({ path, query: { ...route.query, library: libraryId ?? undefined } })
}

function loadMore() {
  void feedQuery.fetchNextPage()
}

function retryFeed() {
  void feedQuery.refetch()
}
</script>

<template>
  <section class="flex h-full min-w-0 bg-background">
    <WorksetFeed
      :worksets="worksets"
      :libraries="libraries"
      :active-filter="feedFilter"
      :active-library-id="libraryFilter"
      :selected-id="selectedId"
      :selected-excluded-by-filter="selectedExcludedByFilter"
      :has-more="hasMore"
      :loading-more="feedQuery.isFetchingNextPage.value"
      :initial-loading="feedInitialLoading"
      :error="feedError ? errorDetails(feedError).message : null"
      :error-code="feedError ? errorDetails(feedError).code : null"
      @select="selectWorkset"
      @update:active-filter="setFilter"
      @update:active-library-id="setLibraryFilter"
      @load-more="loadMore"
      @retry="retryFeed"
    />

    <div v-if="selectedId" class="min-w-0 flex-1" data-testid="workset-workbench">
      <WorksetWorkbench
        :workset="selectedWorkset"
        :detail-error="detailQuery.error.value as Error | null"
        :detail-loading="detailQuery.isPending.value"
        :draft-query-data="draftQuery.data.value ?? null"
        :revision-list="revisionList"
        :has-more-revisions="hasMoreRevisions"
        :loading-more-revisions="revisionListQuery.isFetchingNextPage.value"
        @load-earlier-revisions="loadEarlierRevisions"
      />
    </div>
    <div v-else class="grid min-w-0 flex-1 place-items-center" data-testid="workset-none-selected">
      <p class="text-xs text-muted-foreground">从左侧选择一个工作集</p>
    </div>
  </section>
</template>
