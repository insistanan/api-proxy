<template>
  <header class="sticky top-0 z-40 border-b border-border bg-popover shadow-[0_1px_8px_color-mix(in_srgb,var(--foreground)_3%,transparent)]">
    <div class="mx-auto flex min-h-14 max-w-[1760px] items-center gap-2 px-3 sm:px-5"><slot></slot></div>
    <div v-if="extended" class="mx-auto flex min-h-12 max-w-[1760px] items-center border-t border-border/60 px-2 sm:px-4">
      <slot name="extension"></slot>
    </div>
  </header>
</template>
<script setup lang="ts">
defineProps<{ extended?: boolean }>()
</script>
