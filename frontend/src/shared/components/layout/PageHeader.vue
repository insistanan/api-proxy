<template>
  <header class="page-header">
    <div class="page-header__copy">
      <h1>{{ title }}</h1>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.default" class="page-header__actions"><slot></slot></div>
  </header>
</template>

<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<style scoped>
@reference "@/styles/index.css";

.page-header { @apply flex flex-wrap items-start justify-between gap-5 mb-6 min-w-0 py-1; }
.page-header__copy { @apply min-w-0; }
h1 { @apply m-0 text-foreground text-2xl leading-tight font-semibold tracking-tight; }
p { @apply mt-2 max-w-[72ch] text-muted-foreground text-[13px] leading-6; }
.page-header__actions { @apply flex shrink-0 items-center flex-wrap justify-end gap-2 max-w-full; }
@media (max-width: 700px) { .page-header { @apply items-start flex-col gap-4 mb-5; } .page-header__actions { @apply justify-start w-full; } h1 { @apply [font-size:1.375rem]; } }
</style>
