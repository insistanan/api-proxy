<template>
  <div class="capabilities">
    <div class="capability-heading">
      <div><h3 class="text-subtitle-1">分组能力</h3><p class="text-body-2 text-medium-emphasis mt-1">声明各分组适合的任务和难度。自动匹配取两项条件的交集，分组内仍按原渠道顺序调度。</p></div>
      <v-btn variant="tonal" :disabled="disabled" @click="add">添加分组</v-btn>
    </div>
    <v-alert v-if="!model.length" type="info" variant="tonal">还没有能力配置。添加分组后，勾选适合的任务和难度即可，无需编写正则。</v-alert>
    <section v-for="(group, index) in model" :key="index" class="capability-row">
      <div class="capability-heading">
        <span class="text-subtitle-2">{{ pools.find(pool => pool.id === group.poolId)?.name || '选择分组' }}</span>
        <v-btn variant="text" color="error" size="small" :disabled="disabled" @click="model.splice(index, 1)">移除配置</v-btn>
      </div>
      <v-select v-model="group.poolId" :items="pools.filter(pool => pool.id === group.poolId || !model.some(other => other.poolId === pool.id))" item-title="name" item-value="id" label="目标分组" :disabled="disabled" />
      <div class="capability-fields">
        <v-combobox v-model="group.tasks" :items="tasks" item-title="name" item-value="id" :return-object="false" multiple chips closable-chips label="适合的任务" :disabled="disabled" hint="可多选；自定义类别使用英文 ID，并与数据集 label 一致" persistent-hint />
        <v-select v-model="group.tiers" :items="tiers" multiple chips label="允许处理的难度" :disabled="disabled" hint="只选择这个分组能够胜任的难度" persistent-hint />
      </div>
      <v-text-field v-if="useFeedback" v-model.number="group.cost" type="number" min="0" label="相对成本" :disabled="disabled" hint="同能力候选组之间比较，使用相同计价单位" persistent-hint />
    </section>
    <p v-if="model.length" class="text-caption text-medium-emphasis">未开启效果反馈优化时，同时满足条件的分组按配置顺序选择。未配置的分组不参与自动综合匹配。</p>
  </div>
</template>
<script setup lang="ts">
import type { IntentGroupCapability, IntentTask } from '@/services/intent'
defineProps<{ pools: { id: string; name: string }[]; tasks: IntentTask[]; disabled: boolean; useFeedback: boolean }>()
const model = defineModel<IntentGroupCapability[]>({ required: true })
const tiers = [{ title: '简单', value: 0 }, { title: '常规', value: 1 }, { title: '复杂', value: 2 }]
function add() { model.value.push({ poolId: '', tasks: [], tiers: [], cost: 0 }) }
</script>
<style scoped>
.capabilities { display: grid; gap: 24px; }
.capability-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
.capability-row { display: grid; gap: 20px; border-top: 1px solid rgba(var(--v-theme-outline), 0.2); padding-top: 20px; }
.capability-fields { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); gap: 20px; }
@media (max-width: 650px) { .capability-fields { grid-template-columns: minmax(0, 1fr); } .capability-heading { flex-wrap: wrap; } }
</style>
