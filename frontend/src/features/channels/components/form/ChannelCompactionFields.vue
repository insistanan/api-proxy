<template>
  <UiCard variant="outlined" class="p-4 space-y-4">
    <div>
      <h3 class="text-base font-semibold">上下文压缩</h3>
      <p class="text-sm text-muted-foreground mt-1">将独立文本摘要交给便宜模型。原生加密压缩与会话续聊分别处理。</p>
    </div>
    <UiSwitch v-model="model.summaryCapable" label="加入文本摘要池" :disabled="model.summaryPool" />
    <template v-if="model.summaryCapable">
      <p class="text-sm text-muted-foreground">退出常规对话调度，为同入口协议的渠道提供摘要。上游服务类型可以不同。</p>
      <UiInput v-model="model.summaryModel" label="摘要模型（必填）" placeholder="填写上游实际模型 ID" />
    </template>
    <template v-else>
      <UiSwitch v-model="model.summaryPool" label="将独立摘要委托给文本摘要池" />
      <template v-if="model.summaryPool">
        <p class="text-sm text-muted-foreground">客户端使用 X-Api-Proxy-Task: compact 标记已序列化的纯文本摘要。按池内顺序尝试；池为空或全部失败时返回错误。</p>
        <UiSwitch v-model="model.detectClientPrompts" label="识别已适配的客户端摘要模板（pi）" />
        <p class="text-sm text-muted-foreground">严格匹配系统提示词和输入结构；客户端模板变化后需使用任务头。普通“总结”指令不会触发分流。</p>
      </template>
      <template v-if="nativeProtocol">
        <UiInput v-model="model.nativeModel" label="原生压缩模型（可选，留空沿用主模型）" />
        <template v-if="model.nativeModel">
          <UiInput v-model="model.nativeSourceModel" label="已验证的主模型（映射后的精确 ID）" />
          <p class="text-sm text-muted-foreground">仅独立原生压缩可替换；自动压缩继续使用主模型。要求单地址、单密钥、保留签名，不能跨渠道。请验证“主模型 → 压缩模型 → 主模型续聊 → 再压缩”的完整往返。</p>
          <UiSwitch v-model="model.nativeCompatibilityConfirmed" label="已验证此来源与模型对的压缩、加密历史及思考兼容性" />
        </template>
      </template>
    </template>
  </UiCard>
</template>

<script setup lang="ts">
import UiCard from '@/shared/ui/card/Card.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import type { CompactionConfig } from '@/types/channel'

defineProps<{ nativeProtocol: boolean }>()
const model = defineModel<CompactionConfig>({ required: true })
</script>
