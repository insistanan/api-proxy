<template>
  <v-dialog v-model="isOpen" max-width="520" persistent>
    <v-card rounded="lg">
      <v-card-title class="d-flex align-center justify-space-between pa-4">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-application-export</v-icon>
          <span class="text-h6">导入到 CC Switch</span>
          <v-chip v-if="channel" size="small" color="primary" variant="tonal">
            {{ channel.name }}
          </v-chip>
        </div>
        <v-btn icon size="small" variant="text" aria-label="关闭导入弹窗" @click="close">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-divider />

      <v-card-text class="pa-4">
        <p class="text-body-2 text-medium-emphasis mb-4">
          选择该渠道在 CC Switch 中作为哪类应用导入。已按当前渠道池预选，
          <span v-if="serviceTypeDiffers">该渠道上游协议为 <code>{{ channel?.serviceType }}</code>（由代理转换），如需按上游协议导入请改选。</span>
        </p>

        <v-select
          v-model="selectedApp"
          :items="appOptions"
          item-title="title"
          item-value="value"
          label="目标应用类型"
          variant="outlined"
          density="comfortable"
          class="mb-2"
          :disabled="isImporting"
        />
        <p class="text-body-2 text-medium-emphasis mb-4 app-hint">{{ selectedAppHint }}</p>

        <v-alert v-if="errorMessage" type="error" variant="tonal" density="compact" class="mb-4">
          {{ errorMessage }}
        </v-alert>

        <div class="d-flex justify-end ga-2">
          <v-btn variant="text" :disabled="isImporting" @click="close">取消</v-btn>
          <v-btn
            color="primary"
            prepend-icon="mdi-application-export"
            :loading="isImporting"
            @click="confirm"
          >
            {{ ccsConfigured ? '导入' : '复制 CCS 链接' }}
          </v-btn>
        </div>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Channel } from '@/types/channel'
import { buildCcsDeepLink, CCS_APP_BY_KIND, CCS_APP_OPTIONS, type CcsApp } from '@/utils/ccsDeepLink'
import { api } from '@/services/api'
import type { ApiTab } from '@/services/channel-api'

const props = defineProps<{
  modelValue: boolean
  channel: Channel | null
  /** 当前渠道池，决定默认目标 app */
  channelType: ApiTab
  /** 是否已配置 CC Switch 路径：导入 or 复制链接 */
  ccsConfigured: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 已调用 CC Switch（已配置路径） */
  imported: [channelName: string]
  /** 链接已复制到剪贴板（未配置路径） */
  copied: []
  error: [message: string]
}>()

const isOpen = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value)
})

const selectedApp = ref<CcsApp>('claude')
const isImporting = ref(false)
const errorMessage = ref('')

// 每次打开按渠道池重置默认值
watch(() => props.modelValue, (open) => {
  if (open) {
    selectedApp.value = CCS_APP_BY_KIND[props.channelType] ?? 'claude'
    errorMessage.value = ''
  }
})

const appOptions = CCS_APP_OPTIONS
const selectedAppHint = computed(() =>
  appOptions.find(option => option.value === selectedApp.value)?.hint ?? ''
)
const serviceTypeDiffers = computed(() => {
  if (!props.channel) return false
  const appForServiceType: Record<Channel['serviceType'], CcsApp> = {
    claude: 'claude',
    chat: 'claude',
    gemini: 'gemini',
    responses: 'codex',
    openai: 'opencode'
  }
  return appForServiceType[props.channel.serviceType] !== selectedApp.value
})

const close = () => {
  if (!isImporting.value) isOpen.value = false
}

const confirm = async () => {
  if (!props.channel) return
  isImporting.value = true
  errorMessage.value = ''
  const channelName = props.channel.name
  try {
    const link = buildCcsDeepLink(props.channel, selectedApp.value)
    if (props.ccsConfigured) {
      await api.importToCcs(link)
      emit('imported', channelName)
    } else {
      await navigator.clipboard.writeText(link)
      emit('copied')
    }
    isOpen.value = false
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '操作失败'
  } finally {
    isImporting.value = false
  }
}
</script>

<style scoped>
.app-hint {
  min-height: 1.5em;
}
</style>
