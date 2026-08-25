<script setup lang="ts">
import { ref, watch } from 'vue'
import { FolderOpen, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type { CreateLibraryInput, Library } from '@/lib/api/types'
import { useDesktopAdapter } from '@/lib/desktop/desktop-adapter'

const props = defineProps<{
  open: boolean
  library?: Library | null
  saving?: boolean
}>()
const emit = defineEmits<{
  close: []
  save: [value: CreateLibraryInput]
  remove: [id: string]
}>()

const adapter = useDesktopAdapter()
const name = ref('')
const rootPath = ref('')

watch(
  () => [props.open, props.library] as const,
  () => {
    if (!props.open) return
    name.value = props.library?.name ?? ''
    rootPath.value = props.library?.root_path ?? ''
  },
  { immediate: true },
)

async function pickFolder() {
  const picked = await adapter.pickFolder()
  if (picked !== null) rootPath.value = picked
}

function submit() {
  if (!name.value || !rootPath.value) return
  emit('save', { name: name.value, root_path: rootPath.value })
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 grid place-items-center bg-black/65 px-4"
    role="dialog"
    aria-modal="true"
    :aria-label="library ? '编辑媒体库' : '添加媒体库'"
    @click.self="emit('close')"
  >
    <form
      class="w-full max-w-lg rounded-lg border border-border bg-popover p-5 shadow-2xl"
      @submit.prevent="submit"
    >
      <div class="flex items-start gap-4 border-b border-border pb-4">
        <div>
          <p class="font-heading text-base font-semibold">{{ library ? '编辑媒体库' : '添加媒体库' }}</p>
          <p class="mt-1 text-xs text-muted-foreground">
            每个条目指向一个音频根目录。可以添加多个媒体库。
          </p>
        </div>
        <Button class="ml-auto" variant="ghost" size="icon" type="button" aria-label="关闭" @click="emit('close')">
          <X class="size-4" />
        </Button>
      </div>

      <label class="mt-4 block text-xs font-medium" for="library-name">名称</label>
      <input
        id="library-name"
        v-model="name"
        required
        autocomplete="off"
        class="mt-1.5 h-9 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        placeholder="例如：无损音乐库"
      />

      <label class="mt-4 block text-xs font-medium" for="library-root">根目录</label>
      <div class="mt-1.5 flex gap-2">
        <input
          id="library-root"
          v-model="rootPath"
          required
          autocomplete="off"
          spellcheck="false"
          class="h-9 min-w-0 flex-1 rounded-md border border-input bg-background px-3 font-mono text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          placeholder="/home/me/music 或 D:\Music"
        />
        <Button type="button" variant="outline" @click="pickFolder">
          <FolderOpen class="size-4" />
          选择文件夹
        </Button>
      </div>
      <p class="mt-1.5 text-[11px] text-muted-foreground">
        浏览器无法打开系统目录选择器时，请直接粘贴路径。路径会原样发送给后端。
      </p>

      <div class="mt-6 flex items-center gap-2 border-t border-border pt-4">
        <Button
          v-if="library"
          type="button"
          variant="destructive"
          :disabled="saving"
          @click="emit('remove', library.id)"
        >
          删除媒体库
        </Button>
        <Button type="button" variant="ghost" class="ml-auto" @click="emit('close')">取消</Button>
        <Button type="submit" :disabled="saving || !name || !rootPath">
          {{ saving ? '保存中…' : '保存' }}
        </Button>
      </div>
    </form>
  </div>
</template>
