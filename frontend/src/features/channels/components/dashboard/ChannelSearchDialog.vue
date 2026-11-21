<template>
  <AppDialog v-model="show" title="搜索渠道" size="md" :busy="navigating">
    <div class="space-y-4">
      <div class="field-surface flex min-h-11 items-center gap-2 border-input px-3">
        <UiIcon icon="mdi-magnify" :size="18" class="text-muted-foreground" />
        <input
          v-model="query"
          autofocus
          autocomplete="off"
          aria-label="搜索渠道、模型、协议或分组"
          placeholder="输入渠道、模型、协议或分组"
          class="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none! placeholder:text-muted-foreground"
          role="combobox"
          aria-autocomplete="list"
          :aria-expanded="!!visibleResults.length"
          :aria-controls="resultsId"
          :aria-activedescendant="visibleResults[activeIndex] ? `${resultsId}-${activeIndex}` : undefined"
          @keydown.down.prevent="moveSelection(1)"
          @keydown.up.prevent="moveSelection(-1)"
          @keydown.enter="handleEnter"
        />
      </div>
      <div class="flex items-center justify-between gap-3 text-xs text-muted-foreground" role="status" aria-live="polite">
        <span>{{ loading ? '正在读取各协议渠道…' : `${filteredResults.length} 个匹配渠道` }}</span>
        <UiButton size="x-small" variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadChannels">刷新</UiButton>
      </div>
      <UiAlert v-if="loadErrors.length" type="warning" variant="tonal">
        <p v-for="failure in loadErrors" :key="failure.protocol" class="text-xs leading-6">{{ failure.protocol }}：{{ failure.message }}</p>
      </UiAlert>
      <div v-if="navigationError" class="text-sm text-destructive" role="alert">{{ navigationError }}</div>
      <div :id="resultsId" ref="resultsRoot" class="search-results" role="listbox" aria-label="渠道搜索结果" :aria-busy="loading">
        <button
          v-for="(result, index) in visibleResults"
          :id="`${resultsId}-${index}`"
          :key="`${result.protocol}-${result.id}`"
          type="button"
          role="option"
          tabindex="-1"
          :aria-selected="activeIndex === index"
          :disabled="navigating"
          class="search-result"
          :class="{ 'is-active': activeIndex === index }"
          @pointermove="activeIndex = index"
          @focus="activeIndex = index"
          @click="locateChannel(result)"
        >
          <span class="search-result-icon"><UiIcon icon="mdi-server-network" :size="20" /></span>
          <span class="min-w-0 flex-1">
            <span class="flex flex-wrap items-center gap-2">
              <span class="break-words text-sm font-medium">{{ result.name }}</span>
              <span v-if="result.location" class="rounded-badge bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{{ result.location }}</span>
            </span>
            <span class="mt-1 block break-words text-xs text-muted-foreground">{{ result.protocolLabel }} <span aria-hidden="true">/</span> {{ result.poolName }}</span>
            <span v-if="result.models.length" class="mt-1 block truncate text-xs text-muted-foreground" :title="result.models.join('、')">{{ result.models.join(' · ') }}</span>
          </span>
          <UiIcon icon="mdi-chevron-right" :size="18" class="shrink-0 text-muted-foreground" />
        </button>
        <p v-if="!loading && !visibleResults.length" class="px-4 py-8 text-center text-sm text-muted-foreground">{{ loadErrors.length ? '已读取的协议中没有匹配渠道' : '没有匹配渠道' }}</p>
      </div>
      <div v-if="visibleResults.length" class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>↑ ↓ 选择 · Enter 定位</span>
        <UiButton v-if="visibleResults.length < filteredResults.length" size="x-small" variant="text" @click="visibleLimit += 40">显示更多</UiButton>
      </div>
    </div>
  </AppDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { isNavigationFailure, NavigationFailureType, useRouter } from 'vue-router'
import { api, channelApiByType } from '@/api'
import { API_TABS, API_TAB_LABELS } from '@/features/channels/services/channel-api'
import { channelSearchResults, filterChannelSearch, type ChannelSearchResult } from '@/features/channels/utils/channelSearch'
import { useChannelStore } from '@/features/channels/stores/channel'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'

const show = defineModel<boolean>({ required: true })
const router = useRouter()
const channelStore = useChannelStore()
const query = ref('')
const loading = ref(false)
const navigating = ref(false)
const navigationError = ref('')
const results = ref<ChannelSearchResult[]>([])
const loadErrors = ref<{ protocol: string; message: string }[]>([])
const resultsId = useId()
const resultsRoot = ref<HTMLElement>()
const activeIndex = ref(0)
const visibleLimit = ref(40)
const filteredResults = computed(() => filterChannelSearch(results.value, query.value))
const visibleResults = computed(() => filteredResults.value.slice(0, visibleLimit.value))
let revision = 0

async function loadChannels() {
  if (loading.value) return
  const request = ++revision
  loading.value = true
  loadErrors.value = []
  results.value = []
  activeIndex.value = 0
  const responses = await Promise.allSettled(API_TABS.map(async protocol => {
    const [channels, pools] = await Promise.all([channelApiByType(protocol).getChannels(), api.getChannelPools(protocol)])
    return channelSearchResults(protocol, channels.channels, pools.pools)
  }))
  if (request !== revision) return
  responses.forEach((response, index) => {
    if (response.status === 'fulfilled') results.value.push(...response.value)
    else loadErrors.value.push({
      protocol: API_TAB_LABELS[API_TABS[index]!],
      message: response.reason instanceof Error ? response.reason.message : '读取渠道失败'
    })
  })
  loading.value = false
}

async function moveSelection(direction: number) {
  const length = visibleResults.value.length
  if (!length) return
  activeIndex.value = (activeIndex.value + direction + length) % length
  await nextTick()
  resultsRoot.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
}

function handleEnter(event: KeyboardEvent) {
  if (event.isComposing) return
  const result = visibleResults.value[activeIndex.value]
  if (!result) return
  event.preventDefault()
  void locateChannel(result)
}

async function locateChannel(result: ChannelSearchResult) {
  if (navigating.value) return
  navigating.value = true
  navigationError.value = ''
  try {
    const failure = await router.push({ name: 'channels', params: { type: result.protocol } })
    if (failure && !isNavigationFailure(failure, NavigationFailureType.duplicated)) throw new Error('页面跳转未完成，请重试')
    show.value = false
    channelStore.locateRequest = { protocol: result.protocol, id: result.id }
  } catch (error) {
    navigationError.value = error instanceof Error ? error.message : '定位渠道失败'
  } finally {
    navigating.value = false
  }
}

watch(query, () => { activeIndex.value = 0; visibleLimit.value = 40 })
onMounted(() => { void loadChannels() })
onUnmounted(() => { revision++ })
</script>

<style scoped>
@reference "@/styles/index.css";

.search-results { @apply max-h-[min(26rem,48dvh)] overflow-y-auto overscroll-contain rounded-control border border-border; }
.search-result { @apply flex w-full items-center gap-3 px-3 py-3 text-left transition-colors disabled:opacity-60; }
.search-result + .search-result { @apply border-t border-border/60; }
.search-result.is-active { @apply bg-primary/8; }
.search-result-icon { @apply flex size-9 shrink-0 items-center justify-center rounded-control bg-muted text-muted-foreground; }
.search-result.is-active .search-result-icon { @apply bg-primary/10 text-primary; }
</style>
