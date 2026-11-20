<template>
  <div class="min-w-0" :aria-busy="loading || undefined">
    <div class="ui-table-scroll overflow-x-auto">
      <table class="w-full border-collapse text-sm">
        <thead class="bg-muted/60 text-left text-xs text-muted-foreground">
          <tr>
            <th v-if="showSelect" class="p-3">
              <input
                type="checkbox"
                aria-label="选择本页全部"
                :checked="allSelected"
                class="accent-primary"
                @change="selectPage(($event.target as HTMLInputElement).checked)"
              />
            </th>
            <th
              v-for="header in headers"
              :key="header.key"
              class="px-3 py-3 font-semibold"
              :aria-sort="sortKey === header.key ? (descending ? 'descending' : 'ascending') : undefined"
            >
              <button
                v-if="header.sortable !== false && !server"
                type="button"
                class="inline-flex items-center gap-1"
                @click="sort(header.key)"
              >
                {{ header.title }}<span v-if="sortKey === header.key">{{ descending ? '↓' : '↑' }}</span></button
              ><span v-else>{{ header.title }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(item, index) in rows"
            :key="String(keyFor(item) ?? index)"
            class="border-t border-border hover:bg-primary/5"
          >
            <td v-if="showSelect" class="p-3">
              <input
                type="checkbox"
                :aria-label="`选择第 ${(currentPage - 1) * size + index + 1} 行`"
                :checked="isSelected(item)"
                class="accent-primary"
                @change="selectOne(item, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td v-for="header in headers" :key="header.key" class="px-3 py-3 align-top">
              <slot :name="`item.${header.key}`" :item="item">{{ field(item, header.key) }}</slot>
            </td>
          </tr>
          <tr v-if="!rows.length">
            <td :colspan="headers.length + (showSelect ? 1 : 0)" class="p-8 text-center text-muted-foreground">
              {{ loading ? '正在加载…' : '暂无数据' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <footer
      class="flex flex-wrap items-center justify-end gap-4 border-t border-border px-3 py-2 text-sm text-muted-foreground"
    >
      <span>共 {{ total }} 条</span
      ><label class="flex items-center gap-2"
        >每页<select
          :value="size"
          class="rounded-control border border-input bg-card px-2 py-1"
          @change="changeSize(Number(($event.target as HTMLSelectElement).value))"
        >
          <option v-for="option in pageOptions" :key="option.value" :value="option.value">{{ option.title }}</option>
        </select></label
      ><Pagination
        :model-value="currentPage"
        :length="Math.ceil(total / Math.max(1, size))"
        :disabled="loading"
        @update:model-value="changePage"
      />
    </footer>
  </div>
</template>
<script setup lang="ts" generic="T extends object, K = unknown">
import { computed, ref, watch } from 'vue'
import Pagination from './Pagination.vue'
interface Header {
  title: string
  key: string
  sortable?: boolean
  align?: string
  width?: string | number
}
const props = withDefaults(
  defineProps<{
    modelValue?: K[]
    headers: readonly Header[]
    items: T[]
    search?: string
    itemValue?: string
    itemKey?: string
    showSelect?: boolean
    itemsPerPage?: number
    itemsPerPageOptions?: readonly (number | { value: number; title: string })[]
    itemsLength?: number
    page?: number
    server?: boolean
    loading?: boolean
    density?: string
  }>(),
  { itemsPerPageOptions: () => [10, 25, 50] }
)
const emit = defineEmits<{
  'update:modelValue': [value: K[]]
  'update:page': [value: number]
  'update:itemsPerPage': [value: number]
}>()
defineSlots<{ [name: string]: (props: { item: T }) => unknown }>()
const localPage = ref(1)
const localSize = ref(props.itemsPerPage ?? 10)
const currentPage = computed(() => props.page ?? localPage.value)
const size = computed(() => (props.server ? (props.itemsPerPage ?? localSize.value) : localSize.value))
const sortKey = ref('')
const descending = ref(false)
const field = (item: T, key: string): unknown =>
  key
    .split('.')
    .reduce<unknown>(
      (value, key) => (value && typeof value === 'object' ? (value as Record<string, unknown>)[key] : undefined),
      item
    )
const keyFor = (item: T) => field(item, props.itemValue ?? props.itemKey ?? 'id') as K
const filtered = computed(() => {
  if (props.server) return props.items
  let result = props.search
    ? props.items.filter(item => JSON.stringify(item).toLocaleLowerCase().includes(props.search!.toLocaleLowerCase()))
    : [...props.items]
  if (sortKey.value)
    result = result.sort((a, b) => {
      const av = field(a, sortKey.value),
        bv = field(b, sortKey.value)
      const order =
        typeof av === 'number' && typeof bv === 'number'
          ? av - bv
          : String(av ?? '').localeCompare(String(bv ?? ''), 'zh-CN', { numeric: true })
      return descending.value ? -order : order
    })
  return result
})
const total = computed(() => (props.server ? (props.itemsLength ?? props.items.length) : filtered.value.length))
const rows = computed(() =>
  props.server || size.value === -1
    ? filtered.value
    : filtered.value.slice((currentPage.value - 1) * size.value, currentPage.value * size.value)
)
const pageOptions = computed(() =>
  props.itemsPerPageOptions.map(option =>
    typeof option === 'number' ? { value: option, title: option === -1 ? '全部' : String(option) } : option
  )
)
const isSelected = (item: T) => props.modelValue?.includes(keyFor(item)) ?? false
const allSelected = computed(() => rows.value.length > 0 && rows.value.every(isSelected))
function selectOne(item: T, on: boolean) {
  const selected = new Set(props.modelValue ?? [])
  if (on) selected.add(keyFor(item))
  else selected.delete(keyFor(item))
  emit('update:modelValue', [...selected])
}
function selectPage(on: boolean) {
  const selected = new Set(props.modelValue ?? [])
  for (const item of rows.value) {
    if (on) selected.add(keyFor(item))
    else selected.delete(keyFor(item))
  }
  emit('update:modelValue', [...selected])
}
function changePage(page: number) {
  localPage.value = page
  emit('update:page', page)
}
function changeSize(value: number) {
  localSize.value = value
  emit('update:itemsPerPage', value)
  changePage(1)
}
function sort(key: string) {
  descending.value = sortKey.value === key ? !descending.value : false
  sortKey.value = key
}
watch(
  () => props.search,
  () => {
    if (!props.server) changePage(1)
  }
)
watch(
  () => [total.value, size.value],
  () => {
    if (!props.server && currentPage.value > Math.max(1, Math.ceil(total.value / Math.max(1, size.value))))
      changePage(1)
  }
)
</script>
