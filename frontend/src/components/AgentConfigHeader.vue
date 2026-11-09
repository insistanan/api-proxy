<template>
  <header class="agent-config-header">
    <div class="agent-config-header__copy">
      <h1>{{ title }}</h1>
    </div>
    <div class="agent-config-header__actions">
      <slot ></slot>
    </div>
  </header>
</template>

<script setup lang="ts">
defineProps<{
  title: string
}>()
</script>
