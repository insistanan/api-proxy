import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { datasetsApi } from '@/features/extensions/datasets/services/datasets'
import type { DatasetSummary } from '@/types'

export const useDatasetsStore = defineStore('datasets', () => {
  const items = shallowRef<DatasetSummary[]>([])
  let revision = 0
  async function load() {
    const current = ++revision
    const value = await datasetsApi.list()
    if (revision === current) items.value = value
  }
  return { items, load }
})
