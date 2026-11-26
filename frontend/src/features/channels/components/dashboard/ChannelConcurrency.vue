<template>
  <div ref="root" class="inline-flex flex-wrap items-center gap-1" @click.stop @keydown.stop>
    <span>并发</span>
    <template v-if="editing">
      <strong :title="title">{{ active ?? '—' }} /</strong>
      <UiInput
        v-model="draft"
        class="w-20"
        type="number"
        min="1"
        step="1"
        aria-label="渠道分支并发上限"
        :disabled="saving"
        :error="!!error"
        @blur="save(false)"
        @keydown.enter.prevent="save(true)"
        @keydown.esc.prevent="cancel"
      />
      <span v-if="saving" class="text-muted-foreground">保存中…</span>
      <span v-if="error" class="basis-full text-destructive" role="alert">{{ error }}</span>
    </template>
    <button
      v-else
      type="button"
      class="cursor-pointer rounded px-1 hover:bg-muted focus-visible:outline-2 focus-visible:outline-primary"
      :title="`${title} 点击设置分支并发上限${channel.builtinProvider ? '（同步到四个协议）' : ''}。`"
      :aria-label="`设置渠道分支并发上限，当前 ${limit}`"
      @click="edit"
    ><strong>{{ active ?? '—' }} / {{ limit }}</strong></button>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { ApiError, channelApiByType, type ApiTab, type Channel } from '@/api'
import UiInput from '@/shared/ui/field/Input.vue'
import { useFreeChannelsStore } from '@/features/channels/stores/freeChannels'
import { DEFAULT_BRANCH_CONCURRENCY, isPositiveInteger } from '@/features/channels/utils/branchConcurrency'

const props = defineProps<{ channel: Channel; kind: ApiTab; active?: number; title: string }>()
const emit = defineEmits<{ refresh: []; success: [message: string]; error: [message: string] }>()
const free = useFreeChannelsStore()
const root = ref<HTMLElement>()
const limit = ref(props.channel.maxBranchConcurrency ?? DEFAULT_BRANCH_CONCURRENCY)
const draft = ref<string | number | null>(limit.value)
const editing = ref(false)
const saving = ref(false)
const error = ref('')
let submitted: number | undefined

watch(() => props.channel.maxBranchConcurrency, value => {
  limit.value = value ?? DEFAULT_BRANCH_CONCURRENCY
})

async function edit() {
  draft.value = limit.value
  error.value = ''
  submitted = undefined
  editing.value = true
  await nextTick()
  const input = root.value?.querySelector('input')
  input?.focus()
  input?.select()
}

function cancel() {
  if (!saving.value) editing.value = false
}

async function save(explicit: boolean) {
  if (!editing.value || saving.value) return
  if (!isPositiveInteger(draft.value)) {
    error.value = '请输入大于零的整数'
    return
  }
  const value = Number(draft.value)
  if (value === limit.value) {
    editing.value = false
    return
  }
  // Enter 后的失焦、以及失败后的失焦不会再次提交；可修正数字或按 Enter 重试。
  if (!explicit && submitted === value) return
  submitted = value
  saving.value = true
  error.value = ''
  try {
    if (props.channel.builtinProvider) await free.setBranchConcurrency(value)
    else {
      if (!props.channel.id) throw new Error('操作目标缺少稳定渠道 ID，请刷新后重试')
      await channelApiByType(props.kind).updateChannel(props.channel.index, { maxBranchConcurrency: value }, props.channel.id)
    }
    limit.value = value
    editing.value = false
    emit('refresh')
    emit('success', `分支并发上限已设为 ${value}`)
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : String(cause)
    if (cause instanceof ApiError && cause.committed) {
      editing.value = false
      emit('refresh')
      emit('error', `配置已写入，请刷新核对，勿重复提交：${message}`)
    } else {
      error.value = `保存失败：${message}。可修正数字或按 Enter 重试。`
    }
  } finally {
    saving.value = false
  }
}
</script>
