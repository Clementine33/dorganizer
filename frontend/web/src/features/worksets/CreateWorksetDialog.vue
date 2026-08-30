<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type { Folder } from '@/lib/api/types'

const props = defineProps<{
  open: boolean
  folders: Folder[]
  libraryName: string
  saving: boolean
}>()

const emit = defineEmits<{
  close: []
  submit: [{ title: string }]
}>()

// Title starts as a sensible default but is fully editable; empty input is
// rejected by the disabled state rather than an error banner (the backend
// would reject it with INVALID_TITLE anyway).
const title = ref('')
const touched = ref(false)

watch(
  () => props.open,
  (open) => {
    if (open) {
      title.value = `${props.libraryName} 工作集`
      touched.value = false
    }
  },
)

const effectiveTitle = computed(() => title.value.trim())
const canSubmit = computed(() => effectiveTitle.value.length > 0 && !props.saving)
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 grid place-items-center bg-black/50 px-4"
    role="dialog"
    aria-modal="true"
    aria-label="创建工作集"
    data-testid="create-workset-dialog"
    @click.self="emit('close')"
  >
    <div class="w-full max-w-md rounded-lg border border-border bg-card p-5 shadow-lg">
      <div class="flex items-start justify-between gap-3">
        <div>
          <h2 class="font-heading text-base font-semibold tracking-tight">创建工作集</h2>
          <p class="mt-0.5 text-[11px] text-muted-foreground">
            将选中的专辑批次纳入一个工作集，随后配置 Workflow 并生成不可变计划版本。
          </p>
        </div>
        <Button variant="ghost" size="icon" aria-label="关闭" data-testid="close-workset-dialog" @click="emit('close')">
          <X class="size-4" />
        </Button>
      </div>

      <label class="mt-4 block">
        <span class="text-xs font-medium">工作集标题</span>
        <input
          v-model="title"
          data-testid="workset-title-input"
          type="text"
          maxlength="120"
          class="mt-1 h-9 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          placeholder="例如：夏季整理"
          @input="touched = true"
        />
      </label>

      <div class="mt-4">
        <p class="text-xs font-medium">纳入的专辑批次（{{ folders.length }}）</p>
        <ul
          data-testid="workset-folder-review"
          class="mt-1 max-h-44 overflow-auto rounded-md border border-border bg-background/60 p-2"
        >
          <li
            v-for="folder in folders"
            :key="folder.id"
            class="flex items-center justify-between gap-3 rounded px-1.5 py-1 text-xs"
          >
            <span class="truncate font-medium">{{ folder.name }}</span>
            <span class="shrink-0 font-mono text-[10px] text-muted-foreground">{{ folder.audio_file_count }} 个音频文件</span>
          </li>
        </ul>
      </div>

      <div class="mt-5 flex items-center justify-end gap-2">
        <Button variant="ghost" size="sm" :disabled="saving" @click="emit('close')">取消</Button>
        <Button
          data-testid="confirm-create-workset"
          size="sm"
          :disabled="!canSubmit"
          @click="emit('submit', { title: effectiveTitle })"
        >
          {{ saving ? '创建中…' : '创建工作集' }}
        </Button>
      </div>
    </div>
  </div>
</template>
