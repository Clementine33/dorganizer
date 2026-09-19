<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <!-- The retired long addresses land here. Nothing is guessed: the workbench
       says the address names no page and offers the one entry it has. -->
  <section class="grid min-h-0 flex-1 place-items-center px-6 text-center" data-testid="not-found">
    <div class="max-w-sm">
      <h1 class="font-heading text-sm font-semibold">这个地址不存在</h1>
      <p class="mt-1 text-xs leading-5 text-[var(--text-muted)]">
        工作台的地址只包含 ID。旧的长地址已经停用，不会自动跳到别的页面。
      </p>
      <RouterLink
        to="/worksets"
        data-testid="not-found-home"
        class="mt-3 inline-block rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
      >
        返回工作集
      </RouterLink>
    </div>
  </section>
</template>
