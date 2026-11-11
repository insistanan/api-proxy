<template>
  <PageHeader :title="title" :description="description || findNavigation(route.path)?.description">
    <slot></slot>
  </PageHeader>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router'
import PageHeader from './common/PageHeader.vue'
import { findNavigation } from '@/router/navigation'

defineProps<{
  title: string
  description?: string
}>()

const route = useRoute()
</script>
