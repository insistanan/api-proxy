<template>
  <div class="agent-config-location">
    <UiIcon size="17" class="agent-config-location__icon">mdi-file-cog-outline</UiIcon>
    <span class="agent-config-location__label">{{ label }}</span>
    <code>{{ path || '路径不可用' }}</code>
    <slot></slot>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
defineProps<{
  label: string
  path?: string
}>()
</script>

<style scoped>
@reference "@/styles/index.css";

.agent-config-location { @apply flex flex-wrap items-center gap-2.5 [padding:10px_14px] [border:1px_solid_var(--border-soft)] rounded-control [background:color-mix(in_srgb,_var(--surface-variant)_50%,_transparent)]; }
.agent-config-location__icon { @apply text-primary; }
.agent-config-location__label { @apply text-muted-foreground [font-size:0.8125rem] whitespace-nowrap; }
.agent-config-location code { @apply [padding:2px_6px] rounded-badge text-foreground font-mono [font-size:0.75rem] [background:color-mix(in_srgb,_var(--primary)_7%,_transparent)] [word-break:break-all]; }
</style>
