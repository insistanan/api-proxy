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

.page-header { @apply flex items-center justify-between gap-5 mb-6 min-w-0; }
.page-header__copy { @apply min-w-0; }
h1 { @apply m-0 text-foreground [font-size:1.625rem] [line-height:1.3] font-semibold [letter-spacing:-0.02em]; }
p { @apply [margin:8px_0_0] [max-width:72ch] text-muted-foreground [font-size:0.875rem] [line-height:1.65]; }
.page-header__actions { @apply flex items-center flex-wrap justify-end gap-2 max-w-full; }
@media (max-width: 700px) { .page-header { @apply items-start flex-col gap-4 mb-5; } .page-header__actions { @apply justify-start w-full; } h1 { @apply [font-size:1.375rem]; } }
</style>
