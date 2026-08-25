<script setup lang="ts">
import { ref } from 'vue'
import { ChevronDown, ChevronRight, ArrowRightLeft } from '@lucide/vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { PlanOperation } from '@/lib/api/types'

const props = defineProps<{
  operations: PlanOperation[]
  successfulFolders: string[]
}>()

const expanded = ref(new Set<string>(props.successfulFolders.length ? props.successfulFolders : ['']))

function toggleGroup(folder: string): void {
  if (expanded.value.has(folder)) expanded.value.delete(folder)
  else expanded.value.add(folder)
}

/**
 * Groups operations under the backend-reported successful folder paths by
 * prefix match — matching against authoritative data, never building or
 * rewriting paths. A folder matches only when the operation path is the
 * folder itself or continues with a path separator ("/" or "\"), so sibling
 * prefixes never group under the wrong folder. Operations outside every
 * reported folder fall into the remaining group.
 */
function groupOperations(): { folder: string; operations: PlanOperation[] }[] {
  const groups = new Map<string, PlanOperation[]>()
  for (const operation of props.operations) {
    const folder =
      props.successfulFolders
        .filter(
          (f) =>
            f &&
            (operation.source_path === f ||
              operation.source_path.startsWith(f + '/') ||
              operation.source_path.startsWith(f + '\\')),
        )
        .sort((a, b) => b.length - a.length)[0] ?? ''
    if (!groups.has(folder)) groups.set(folder, [])
    groups.get(folder)!.push(operation)
  }
  return [...groups.entries()].map(([folder, operations]) => ({ folder, operations }))
}

function operationLabel(type: string): { label: string; destructive: boolean } {
  if (type === 'delete') return { label: '删除', destructive: true }
  if (type === 'convert') return { label: '转换', destructive: false }
  return { label: type, destructive: false }
}
</script>

<template>
  <Card size="sm">
    <CardHeader class="border-b border-border">
      <CardTitle class="flex items-center gap-2">
        <ArrowRightLeft class="size-4 text-muted-foreground" />
        计划操作
        <span class="ml-auto font-mono text-[11px] font-normal text-muted-foreground">
          {{ props.operations.length }} 项
        </span>
      </CardTitle>
    </CardHeader>
    <CardContent class="flex flex-col gap-1 p-2">
      <div v-for="group in groupOperations()" :key="group.folder" data-testid="operation-group">
        <button
          type="button"
          class="flex min-h-9 w-full items-center gap-2 rounded-md px-2 text-left hover:bg-accent/45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          :aria-expanded="expanded.has(group.folder)"
          @click="toggleGroup(group.folder)"
        >
          <ChevronDown v-if="expanded.has(group.folder)" class="size-3.5 text-muted-foreground" />
          <ChevronRight v-else class="size-3.5 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate font-heading text-xs font-semibold">
            {{ group.folder || '其他' }}
          </span>
          <span class="shrink-0 font-mono text-[10px] text-muted-foreground">{{ group.operations.length }}</span>
        </button>
        <div v-if="expanded.has(group.folder)" class="mb-1">
          <div
            v-for="operation in group.operations"
            :key="operation.source_path"
            data-testid="operation-row"
            class="flex min-h-9 items-center gap-3 border-l border-border px-3"
          >
            <span
              class="shrink-0 rounded border px-1.5 py-0.5 font-mono text-[10px]"
              :class="
                operationLabel(operation.type).destructive
                  ? 'border-destructive/40 text-destructive'
                  : 'border-[var(--brand)]/40 text-[var(--brand)]'
              "
            >
              {{ operationLabel(operation.type).label }}
            </span>
            <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-foreground/90">
              {{ operation.source_path }}
            </span>
            <span v-if="operation.target_path" class="hidden shrink-0 font-mono text-[11px] text-muted-foreground sm:inline">
              → {{ operation.target_path }}
            </span>
          </div>
        </div>
      </div>
    </CardContent>
  </Card>
</template>