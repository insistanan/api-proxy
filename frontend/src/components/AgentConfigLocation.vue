<template>
  <v-alert type="info" variant="tonal" density="compact" class="agent-config-location">
    <div class="agent-config-location__content">
      <span class="agent-config-location__label">{{ label }}</span>
      <code>{{ path || '路径不可用' }}</code>
      <slot ></slot>
    </div>
  </v-alert>
</template>

<script setup lang="ts">
defineProps<{
  label: string
  path?: string
}>()
</script>
