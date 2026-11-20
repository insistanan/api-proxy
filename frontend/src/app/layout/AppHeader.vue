<template>
  <header class="sticky top-0 z-40 border-b border-border bg-popover">
    <div class="flex min-h-14 items-center gap-2 px-2 sm:px-4"><slot></slot></div>
    <div v-if="extended" class="flex min-h-12 items-center border-t border-border px-2 sm:px-4">
      <slot name="extension"></slot>
    </div>
  </header>
</template>
<script setup lang="ts">
defineProps<{ extended?: boolean }>()
</script>
