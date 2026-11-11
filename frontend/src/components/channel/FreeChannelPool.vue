<template>
  <div class="free-pool">
    <header class="d-flex align-center justify-space-between ga-3 flex-wrap">
      <div>
        <h3 class="text-subtitle-1 font-weight-bold">免费分组 · Kilo</h3>
        <p class="text-caption text-medium-emphasis">{{ protocolHint }} · 四个协议共享配置</p>
      </div>
      <v-switch
        :model-value="free.config?.kilo.enabled ?? false" label="启用"
        color="primary" hide-details density="compact" inset
        :disabled="!free.config || free.busy || loading" :loading="free.busy"
        @update:model-value="setEnabled"
      />
    </header>
    <p class="text-body-2 mt-3">免注册、免 Key。仅接收 <code>free/</code> 模型请求，不转入付费兜底分组。</p>
    <p class="text-caption text-medium-emphasis mt-1">可用性根据真实请求统计；协议转换能力取决于上游模型。Kilo 可能记录请求内容。</p>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-3" role="alert">{{ error }}</v-alert>
    <v-alert v-if="notice" type="success" variant="tonal" density="compact" class="mt-3" role="status">{{ notice }}</v-alert>
    <div class="d-flex align-center ga-2 flex-wrap mt-3">
      <v-btn size="small" variant="tonal" :disabled="loading || !free.config" :loading="free.busy" @click="syncModels">同步免费模型</v-btn>
      <v-btn size="small" variant="text" :loading="loading" :disabled="free.busy" @click="load">刷新配置</v-btn>
      <v-btn v-if="channel?.id" size="small" variant="text" :aria-expanded="showAvailability" @click="showAvailability = !showAvailability">模型观测</v-btn>
    </div>
    <template v-if="free.config">
      <p class="text-caption text-medium-emphasis mt-3">{{ free.config.kilo.models.length }} 个模型 · {{ syncTime }} · 同步只读取目录，不发送推理请求</p>
      <v-text-field v-if="free.config.kilo.models.length > 5" v-model="search" label="搜索免费模型" density="compact" hide-details class="mt-2" />
      <ul class="free-models mt-2" aria-label="免费模型调用名称">
        <li v-for="model in visibleModels" :key="model">
          <code>{{ `free/${model}` }}</code>
          <v-btn icon="mdi-content-copy" size="x-small" variant="text" :aria-label="`复制 free/${model}`" @click="copyModel(model)" />
        </li>
      </ul>
      <p v-if="!visibleModels.length" class="text-caption text-medium-emphasis">没有匹配的模型。</p>
    </template>
    <ModelAvailabilityPanel v-if="showAvailability && channel?.id" class="mt-3" :channel-type="channelType" :channels="channels" :fixed-channel-id="channel.id" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import { ApiError } from '@/services/http'
import type { Channel } from '@/types/channel'
import { useFreeChannelsStore } from '@/stores/freeChannels'
import { useChannelStore } from '@/stores/channel'
import ModelAvailabilityPanel from './ModelAvailabilityPanel.vue'

const props = defineProps<{ channelType: ApiTab; channels: Channel[] }>()
const free = useFreeChannelsStore()
const channelStore = useChannelStore()
const loading = ref(false)
const error = ref('')
const notice = ref('')
const search = ref('')
const showAvailability = ref(false)
const channel = computed(() => props.channels.find(item => item.builtinProvider === 'kilo'))
const protocolHint = computed(() => props.channelType === 'chat' ? 'Chat 原生转发' : `${props.channelType === 'messages' ? 'Messages' : props.channelType === 'responses' ? 'Responses' : 'Gemini'} ↔ Chat 转换`)
const visibleModels = computed(() => (free.config?.kilo.models ?? []).filter(model => model.toLowerCase().includes(search.value.trim().toLowerCase())))
const syncTime = computed(() => free.config?.kilo.syncedAt ? `上次同步 ${new Date(free.config.kilo.syncedAt).toLocaleString()}` : '初始内置清单，尚未同步')
const message = (cause: unknown) => cause instanceof Error ? cause.message : String(cause)

async function load() {
  loading.value = true
  error.value = ''
  try { await free.refresh() } catch (cause) { error.value = `加载免费分组失败：${message(cause)}` }
  finally { loading.value = false }
}

async function update(action: () => Promise<void>, success: string) {
  error.value = ''
  notice.value = ''
  try {
    await action()
    notice.value = success
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.committed
      ? `配置已保存，但后续处理失败，请刷新查看实际状态：${cause.message}`
      : `更新失败，原配置保留：${message(cause)}`
    return
  }
  const results = await Promise.allSettled((['messages', 'responses', 'chat', 'gemini'] as const).map(type => channelStore.refreshChannels(type)))
  if (results.some(result => result.status === 'rejected')) error.value = '配置已保存，但渠道列表刷新失败，请刷新页面查看实际状态。'
}

function setEnabled(value: boolean | null) {
  if (value === null) return
  void update(() => free.setEnabled(value), value ? 'Kilo 已在四个协议下启用' : 'Kilo 已在四个协议下停用')
}
function syncModels() { void update(() => free.syncModels(), '免费模型清单已同步') }
async function copyModel(model: string) {
  try { await navigator.clipboard.writeText(`free/${model}`); notice.value = '模型名称已复制' }
  catch (cause) { error.value = `复制失败，请手动选择模型名称：${message(cause)}` }
}
onMounted(() => void load())
</script>

<style scoped>
.free-pool { padding: 16px 18px; min-width: 0; }
.free-models { padding: 0; list-style: none; max-height: 220px; overflow: auto; }
.free-models li { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding-block: 3px; }
.free-models code { overflow-wrap: anywhere; user-select: text; }
</style>
