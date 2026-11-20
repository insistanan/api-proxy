<template>
  <div class="free-pool">
    <div class="free-pool-content">
      <header class="free-pool-header">
        <slot name="activity"></slot>
        <div>
          <div class="free-pool-title">
            <h3 class="text-base font-medium font-bold">免费分组</h3>
            <UiBadge size="x-small" variant="tonal">Kilo</UiBadge>
            <span v-if="free.config" class="text-xs text-muted-foreground">{{ free.config.kilo.models.length }} 个模型</span>
            <slot name="summary"></slot>
          </div>
          <div class="flex items-center text-xs text-muted-foreground">
            自动选择：<code>free</code>
            <UiButton icon="mdi-content-copy" size="x-small" variant="text" aria-label="复制免费分组 free" @click="copyModel()" />
          </div>
        </div>
        <div class="free-pool-actions">
          <UiButton size="small" variant="text" :disabled="loading || !free.config" :loading="free.busy" @click="syncModels">同步模型</UiButton>
          <UiSwitch
            :model-value="free.config?.kilo.enabled ?? false" label="启用"
            color="primary" hide-details density="compact" inset
            :disabled="!free.config || free.busy || loading" :loading="free.busy || loading"
            @update:model-value="setEnabled"
          />
        </div>
      </header>
      <UiAlert v-if="error" type="error" variant="tonal" density="compact" class="mt-3" role="alert">
        {{ error }}
        <template #append><UiButton size="small" variant="text" :loading="loading" :disabled="free.busy" @click="load">刷新配置</UiButton></template>
      </UiAlert>
      <template v-if="free.config">
        <UiInput v-if="free.config.kilo.models.length > 5" v-model="search" label="搜索免费模型" prepend-inner-icon="mdi-magnify" density="compact" hide-details clearable class="mt-3" />
        <ul class="free-models" aria-label="免费模型调用名称">
          <li v-for="model in visibleModels" :key="model">
            <code>{{ `free/${model}` }}</code>
            <UiButton icon="mdi-content-copy" size="x-small" variant="text" :aria-label="`复制 free/${model}`" @click="copyModel(model)" />
          </li>
        </ul>
        <p v-if="!visibleModels.length" class="text-xs text-muted-foreground py-2">没有匹配的模型。</p>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '@/shared/services/http'
import { useFreeChannelsStore } from '@/features/channels/stores/freeChannels'
import { useChannelStore } from '@/features/channels/stores/channel'
import { INTENT_PROTOCOLS } from '@/features/channels/services/intent'
import { useToast } from '@/shared/composables/useToast'
import { useClipboard } from '@/shared/composables/useClipboard'

const free = useFreeChannelsStore()
const channelStore = useChannelStore()
const { showToast } = useToast()
const { copyText } = useClipboard()
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
  const results = await Promise.allSettled(INTENT_PROTOCOLS.map(type => channelStore.refreshChannels(type)))
  if (results.some(result => result.status === 'rejected')) error.value = '配置已保存，但渠道列表刷新失败，请刷新页面查看实际状态。'
  else showToast(success, 'success')
}

function setEnabled(value: boolean | null) {
  if (value === null) return
  void update(() => free.setEnabled(value), value ? 'Kilo 已在四个协议下启用' : 'Kilo 已在四个协议下停用')
}
function syncModels() { void update(() => free.syncModels(), '免费模型清单已同步') }
async function copyModel(model?: string) {
  if (!await copyText(model ? `free/${model}` : 'free', '模型名称已复制')) {
    error.value = '复制失败，请手动选择模型名称'
  }
}
onMounted(() => void load())
</script>

<style scoped>
@reference "@/styles/index.css";

.free-pool { @apply relative [isolation:isolate] overflow-hidden [border-radius:inherit] [padding:8px_12px] min-w-0; }
.free-pool-content { @apply relative [z-index:1]; }
.free-pool-header, .free-pool-title, .free-pool-actions { @apply flex items-center gap-2; }
.free-pool-header { @apply relative justify-between flex-wrap; }
.free-pool-header > div { @apply relative [z-index:1]; }
.free-pool-title { @apply flex-wrap; }
.free-pool-actions { @apply ml-auto; }
.free-pool-actions .ui-switch { @apply [flex:none] ml-1; }
.free-models { @apply p-0 mt-2 [list-style:none] max-h-39.5 overflow-auto [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)]; }
.free-models li { @apply flex items-center justify-between gap-2 min-h-7.5 py-0.5; }
.free-models li + li { @apply [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_12%,_transparent)]; }
.free-models code { @apply [overflow-wrap:anywhere] [user-select:text] [font-size:0.8125rem]; }
.free-models .ui-button { @apply shrink-0; }
@media (max-width: 600px) {
  .free-pool { @apply p-3; }
}
</style>
