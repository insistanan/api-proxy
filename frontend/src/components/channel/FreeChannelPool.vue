<template>
  <div class="free-pool">
    <div class="free-pool-content">
      <header class="free-pool-header">
        <slot name="activity"></slot>
        <div>
          <div class="free-pool-title">
            <h3 class="text-subtitle-1 font-weight-bold">免费分组</h3>
            <v-chip size="x-small" variant="tonal">Kilo</v-chip>
            <span v-if="free.config" class="text-caption text-medium-emphasis">{{ free.config.kilo.models.length }} 个模型</span>
          </div>
          <div class="d-flex align-center text-caption text-medium-emphasis">
            自动选择：<code>free</code>
            <v-btn icon="mdi-content-copy" size="x-small" variant="text" aria-label="复制免费分组 free" @click="copyModel()" />
          </div>
        </div>
        <div class="free-pool-actions">
          <v-btn size="small" variant="text" :disabled="loading || !free.config" :loading="free.busy" @click="syncModels">同步模型</v-btn>
          <v-switch
            :model-value="free.config?.kilo.enabled ?? false" label="启用"
            color="primary" hide-details density="compact" inset
            :disabled="!free.config || free.busy || loading" :loading="free.busy || loading"
            @update:model-value="setEnabled"
          />
        </div>
      </header>
      <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-3" role="alert">
        {{ error }}
        <template #append><v-btn size="small" variant="text" :loading="loading" :disabled="free.busy" @click="load">刷新配置</v-btn></template>
      </v-alert>
      <template v-if="free.config">
        <v-text-field v-if="free.config.kilo.models.length > 5" v-model="search" label="搜索免费模型" prepend-inner-icon="mdi-magnify" density="compact" hide-details clearable class="mt-3" />
        <ul class="free-models" aria-label="免费模型调用名称">
          <li v-for="model in visibleModels" :key="model">
            <code>{{ `free/${model}` }}</code>
            <v-btn icon="mdi-content-copy" size="small" variant="text" :aria-label="`复制 free/${model}`" @click="copyModel(model)" />
          </li>
        </ul>
        <p v-if="!visibleModels.length" class="text-caption text-medium-emphasis py-2">没有匹配的模型。</p>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '@/services/http'
import { useFreeChannelsStore } from '@/stores/freeChannels'
import { useChannelStore } from '@/stores/channel'
import { useToast } from '@/composables/useToast'

const free = useFreeChannelsStore()
const channelStore = useChannelStore()
const { showToast } = useToast()
const loading = ref(false)
const error = ref('')
const search = ref<string | null>('')
const visibleModels = computed(() => (free.config?.kilo.models ?? []).filter(model => model.toLowerCase().includes((search.value ?? '').trim().toLowerCase())))
const message = (cause: unknown) => cause instanceof Error ? cause.message : String(cause)

async function load() {
  loading.value = true
  error.value = ''
  try { await free.refresh() } catch (cause) { error.value = `加载免费分组失败：${message(cause)}` }
  finally { loading.value = false }
}

async function update(action: () => Promise<void>, success: string) {
  error.value = ''
  try {
    await action()
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.committed
      ? `配置已保存，但后续处理失败，请刷新查看实际状态：${cause.message}`
      : `更新失败，原配置保留：${message(cause)}`
    return
  }
  const results = await Promise.allSettled((['messages', 'responses', 'chat', 'gemini'] as const).map(type => channelStore.refreshChannels(type)))
  if (results.some(result => result.status === 'rejected')) error.value = '配置已保存，但渠道列表刷新失败，请刷新页面查看实际状态。'
  else showToast(success, 'success')
}

function setEnabled(value: boolean | null) {
  if (value === null) return
  void update(() => free.setEnabled(value), value ? 'Kilo 已在四个协议下启用' : 'Kilo 已在四个协议下停用')
}
function syncModels() { void update(() => free.syncModels(), '免费模型清单已同步') }
async function copyModel(model?: string) {
  try { await navigator.clipboard.writeText(model ? `free/${model}` : 'free'); showToast('模型名称已复制', 'success') }
  catch (cause) { error.value = `复制失败，请手动选择模型名称：${message(cause)}` }
}
onMounted(() => void load())
</script>

<style scoped>
.free-pool { position: relative; isolation: isolate; overflow: hidden; border-radius: inherit; padding: 12px 18px; min-width: 0; }
.free-pool-content { position: relative; z-index: 1; }
.free-pool-header, .free-pool-title, .free-pool-actions { display: flex; align-items: center; gap: 8px; }
.free-pool-header { position: relative; justify-content: space-between; flex-wrap: wrap; }
.free-pool-header > div { position: relative; z-index: 1; }
.free-pool-title { flex-wrap: wrap; }
.free-pool-actions { margin-left: auto; }
.free-pool-actions .v-switch { flex: none; margin-left: 4px; }
.free-models { padding: 0; margin-top: 8px; list-style: none; max-height: 220px; overflow: auto; border-top: 1px solid rgba(var(--v-theme-outline), 0.18); }
.free-models li { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 40px; padding-block: 2px; }
.free-models li + li { border-top: 1px solid rgba(var(--v-theme-outline), 0.12); }
.free-models code { overflow-wrap: anywhere; user-select: text; font-size: 0.8125rem; }
.free-models .v-btn { flex-shrink: 0; }
@media (max-width: 600px) {
  .free-pool { padding: 12px; }
}
</style>
