<template>
  <UiCard variant="outlined" class="p-4 space-y-4">
    <div>
      <h3 class="text-base font-semibold">上下文压缩</h3>
      <p class="text-sm text-muted-foreground mt-1">可将“压缩旧对话”这类纯文本摘要任务分流到便宜模型，主对话不受影响。</p>
    </div>

    <div class="flex flex-col items-start gap-3">
      <UiSwitch v-model="model.summaryCapable" label="加入文本摘要池" :disabled="model.summaryPool" />

      <template v-if="model.summaryCapable">
        <p class="text-sm text-muted-foreground">作为跨协议共享的摘要专用渠道，退出常规对话调度。</p>
        <div class="w-full">
          <UiSelect
            v-model="model.summaryModel"
            searchable
            creatable
            label="摘要模型 *"
            placeholder="点击拉取或输入上游实际模型 ID"
            :items="modelOptions"
            item-title="title"
            item-value="value"
            :return-object="false"
            :loading="modelsLoading"
            variant="outlined"
            density="comfortable"
            @focus="emit('fetch-models')"
            @update:menu="(open: boolean) => { if (open) emit('fetch-models') }"
          />
          <p v-if="modelsError" class="text-destructive text-xs mt-1" role="alert">{{ modelsError }}</p>
        </div>
      </template>

      <template v-else>
        <UiSwitch v-model="model.summaryPool" label="把独立文本摘要交给文本摘要池" />
        <template v-if="model.summaryPool">
          <p class="text-sm text-muted-foreground">
            默认识别带请求头 <code>X-Api-Proxy-Task: compact</code> 的纯文本摘要请求；按摘要池顺序尝试，池为空或全部失败时直接报错，不回退主模型。
          </p>
          <UiSwitch v-model="model.detectClientPrompts" label="自动识别客户端压缩摘要请求" />
          <p class="text-sm text-muted-foreground">
            无须改请求头，自动识别 OpenCode、pi 等客户端发出的纯文本历史压缩请求（需满足无工具调用、系统提示词明确为对话摘要任务）；普通聊天里的“总结一下”不会误触发。
          </p>
        </template>
      </template>
    </div>

    <template v-if="!model.summaryCapable && nativeProtocol">
      <hr class="border-0 border-t border-border" />
      <div class="space-y-3">
        <UiInput v-model="model.nativeModel" label="原生压缩模型（可选，留空沿用主模型）" />
        <template v-if="model.nativeModel">
          <UiInput v-model="model.nativeSourceModel" label="已验证的主模型（映射后的精确 ID）" />
          <p class="text-sm text-muted-foreground">仅替换独立的原生压缩请求；要求单地址、单密钥且同渠道，请先验证压缩后能正常续聊。</p>
          <UiSwitch v-model="model.nativeCompatibilityConfirmed" label="已确认此主模型与压缩模型可兼容续聊" />
        </template>
      </div>
    </template>
  </UiCard>
</template>

<script setup lang="ts">
import UiCard from '@/shared/ui/card/Card.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import type { CompactionConfig } from '@/types/channel'

const emit = defineEmits<{ 'fetch-models': [] }>()
defineProps<{
  nativeProtocol: boolean
  modelOptions: Array<{ title: string; value: string }>
  modelsLoading: boolean
  modelsError: string
}>()
const model = defineModel<CompactionConfig>({ required: true })
</script>
