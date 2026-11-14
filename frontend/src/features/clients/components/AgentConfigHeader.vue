<template>
  <div class="agent-config-header">
    <PageHeader :title="title" :description="description || findNavigation(route.path)?.description">
      <slot></slot>
    </PageHeader>
  </div>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router'
import PageHeader from '@/shared/components/common/PageHeader.vue'
import { findNavigation } from '@/router/navigation'

defineProps<{
  title: string
  description?: string
}>()

const route = useRoute()
</script>
