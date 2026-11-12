<template>
  <div class="mt-5">
    <v-switch v-model="model.enabled" label="工具调用语义检测" color="warning" :disabled="!dangerousEnabled" hide-details />
    <p class="text-body-2 text-medium-emphasis mb-4">只检查准备交付的执行工具参数。语义相似仅提供线索，安全样本不会覆盖已有危险命令规则。</p>
    <template v-if="model.enabled">
      <v-select v-model="model.mode" :items="modes" label="语义命中后的处理" />
      <v-alert v-if="model.mode !== 'observe' && !gateEnabled" type="warning" variant="tonal" class="mb-4">请先启用 AI 闸门，核验不能由确定性条件判断的相似操作。</v-alert>
      <DatasetPicker v-model="model.datasetIds" kind="safety" />
      <EmbeddingProfileFields v-model="model.embedding" />
      <div class="d-flex flex-wrap ga-3">
        <v-text-field v-model.number="model.threshold" type="number" min="0" max="1" step="0.01" label="风险召回相似度阈值" />
        <v-text-field v-model.number="model.margin" type="number" min="0" max="1" step="0.01" label="直接阻断所需正反例差值" />
      </div>
      <v-select v-if="model.mode !== 'observe'" v-model="model.failurePolicy" :items="failures" label="向量计算或索引不可用时" />
      <p class="text-caption text-medium-emphasis">观察模式只记录命中和故障。直接阻断需同时满足相似度、正反例差值及已批准样本的核验正则，其余候选交给 AI 闸门。规则开关仍以上方勾选项为准。</p>
    </template>
  </div>
</template>
<script setup lang="ts">
import type { SemanticSafetyConfig } from '@/services/datasets'
import DatasetPicker from '@/components/channel/DatasetPicker.vue'
import EmbeddingProfileFields from '@/components/channel/EmbeddingProfileFields.vue'
defineProps<{ dangerousEnabled: boolean; gateEnabled: boolean }>()
const model = defineModel<SemanticSafetyConfig>({ required: true })
const modes = [{ title: '观察并记录', value: 'observe' }, { title: '交给 AI 闸门核验', value: 'review' }, { title: '满足确定性条件时直接阻断', value: 'block' }]
const failures = [{ title: '明确阻止工具交付', value: 'block' }, { title: '交给 AI 闸门检查完整命令', value: 'review' }]
</script>
