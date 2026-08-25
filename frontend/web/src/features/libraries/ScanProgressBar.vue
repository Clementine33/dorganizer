<script setup lang="ts">
import type { ScanStatus } from '@/stores/scan'

defineProps<{
  status: ScanStatus
  filesScanned: number
  dirsScanned: number
  errorCode?: string | null
  errorMessage?: string | null
}>()
</script>

<template>
  <div
    v-if="status !== 'idle'"
    class="border-b border-border bg-muted/25 px-5 py-2.5"
    :class="status === 'error' ? 'text-destructive' : ''"
    role="status"
    aria-live="polite"
  >
    <div class="flex items-center gap-3 text-xs">
      <span class="font-medium">
        <template v-if="status === 'scanning'">正在扫描</template>
        <template v-else-if="status === 'completed'">扫描完成</template>
        <template v-else-if="status === 'cancelled'">已取消</template>
        <template v-else>扫描失败</template>
      </span>
      <span v-if="status === 'scanning' || status === 'completed'" class="font-mono text-muted-foreground">
        {{ filesScanned }} 个文件 · {{ dirsScanned }} 个目录
      </span>
      <span v-if="status === 'error'" class="truncate">
        {{ errorCode }}<template v-if="errorCode && errorMessage"> · </template>{{ errorMessage }}
      </span>
    </div>
    <div v-if="status === 'scanning'" class="mt-2 h-0.5 overflow-hidden bg-border" role="progressbar" aria-label="扫描进行中">
      <div class="scan-progress h-full w-1/3 bg-[var(--ring)]" />
    </div>
  </div>
</template>

<style scoped>
.scan-progress {
  animation: scan-traverse 1.25s ease-in-out infinite alternate;
}
@keyframes scan-traverse {
  from { transform: translateX(-100%); }
  to { transform: translateX(300%); }
}
@media (prefers-reduced-motion: reduce) {
  .scan-progress { animation: none; width: 100%; opacity: 0.65; }
}
</style>
