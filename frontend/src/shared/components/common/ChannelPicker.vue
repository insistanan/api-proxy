<template>
  <div class="channel-picker">
    <v-checkbox v-if="allowAuto" :model-value="automatic" label="自动选择渠道" :disabled="disabled" hide-details class="channel-picker-model" @update:model-value="selectAutomatic" />
    <v-select v-model:menu="protocolMenu" :menu-props="menuProps" :model-value="protocol" :items="protocols" label="协议" :disabled="disabled || loading || automatic" @update:model-value="selectProtocol" />
    <v-select v-model:menu="poolMenu" :menu-props="menuProps" :model-value="pool" :items="pools" label="分组" :disabled="disabled || !protocol || automatic" @update:model-value="selectPool" />
    <v-autocomplete v-model:menu="channelMenu" :menu-props="menuProps" :model-value="channel" :items="channels" item-title="name" item-value="id" :label="label" :disabled="disabled || !pool || automatic" :loading="loading" :error-messages="selectionError || error" clearable @update:model-value="selectChannel" />
    <v-combobox v-if="showModel" :model-value="model" :items="selected?.models ?? []" :return-object="false" :label="modelLabel" :disabled="disabled || (!selected && !automatic)" class="channel-picker-model" clearable @update:model-value="selectModel" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ChannelSelectionOption } from '@/types/channel-selection'

const props = withDefaults(defineProps<{ options: ChannelSelectionOption[]; disabled?: boolean; loading?: boolean; error?: string; label?: string; showModel?: boolean; modelLabel?: string; allowAuto?: boolean; menuProps?: Record<string, unknown> }>(), { label: '渠道', modelLabel: '模型', error: '' })
const emit = defineEmits<{ select: [value: string]; selection: [channel: string, model: string]; menu: [value: boolean]; valid: [value: boolean] }>()
const channel = defineModel<string>({ default: '' })
const model = defineModel<string>('selectedModel', { default: '' })
const protocol = ref('')
const pool = ref('')
const automatic = ref(!!props.allowAuto && !channel.value)
const protocolMenu = ref(false)
const poolMenu = ref(false)
const channelMenu = ref(false)
function closeMenus() { protocolMenu.value = poolMenu.value = channelMenu.value = false }
defineExpose({ closeMenus })
watch([protocolMenu, poolMenu, channelMenu], values => emit('menu', values.some(Boolean)))
const poolId = (item: ChannelSelectionOption) => item.poolId || 'default'
const protocolTitles: Record<string, string> = { messages: 'Messages', responses: 'Responses', chat: 'Chat', gemini: 'Gemini', images: 'Images', embeddings: 'Embeddings', decisions: 'Decisions' }
const protocols = computed(() => [...new Set(props.options.map(item => item.kind))].map(value => ({ value, title: protocolTitles[value] ?? value })))
const pools = computed(() => [...new Map(props.options.filter(item => item.kind === protocol.value).map(item => [poolId(item), { value: poolId(item), title: item.poolName || (poolId(item) === 'default' ? '兜底分组' : poolId(item)) }])).values()])
const channels = computed(() => props.options.filter(item => item.kind === protocol.value && poolId(item) === pool.value))
const selected = computed(() => props.options.find(item => item.id === channel.value))
const selectionError = computed(() => channel.value && !selected.value && !props.loading ? '已选渠道当前不可用，请重新选择' : !automatic.value && props.allowAuto && !channel.value ? '请选择渠道，或明确勾选自动选择' : '')
watch([automatic, selected, protocol, pool], () => emit('valid', automatic.value || !!selected.value && selected.value.kind === protocol.value && poolId(selected.value) === pool.value), { immediate: true })
function selectProtocol(value: string | null) {
  protocol.value = value ?? ''
  pool.value = pools.value.length === 1 ? pools.value[0]!.value : ''
  channel.value = ''
  model.value = ''
  emit('selection', '', '')
}
function selectPool(value: string | null) {
  pool.value = value ?? ''
  channel.value = ''
  model.value = ''
  emit('selection', '', '')
}
function selectChannel(value: string | null) {
  const id = value ?? ''
  const selectedModel = props.options.find(option => option.id === id)?.defaultModel ?? ''
  channel.value = id
  if (!id && props.allowAuto) automatic.value = true
  model.value = selectedModel
  emit('selection', id, selectedModel)
  emit('select', id)
}
function selectModel(value: string | null) {
  model.value = value ?? ''
  emit('selection', channel.value, value ?? '')
}
function selectAutomatic(value: boolean | null) {
  automatic.value = !!value
  if (automatic.value) { channel.value = ''; emit('select', '') }
}
watch([() => props.options, channel], () => {
  if (selected.value) {
    automatic.value = false
    protocol.value = selected.value.kind
    pool.value = poolId(selected.value)
  } else if (!protocol.value && protocols.value.length === 1) {
    protocol.value = protocols.value[0]!.value
    if (pools.value.length === 1) pool.value = pools.value[0]!.value
  }
}, { immediate: true })
</script>

<style scoped>
.channel-picker { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 20px; min-width: 0; }
.channel-picker-model { grid-column: 1 / -1; }
@media (max-width: 600px) { .channel-picker { grid-template-columns: minmax(0, 1fr); } }
</style>
