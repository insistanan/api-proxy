<template>
  <div class="capabilities">
    <p class="text-body-2 text-medium-emphasis">直接设置当前协议已有分组的任务和难度。两项都留空的分组不参与自动匹配。</p>
    <p v-if="!pools.length" class="text-body-2 text-medium-emphasis">当前协议暂无可配置的渠道分组。</p>
    <section v-for="row in rows" :key="row.poolId" class="capability-row" :aria-label="row.name + '的任务能力'">
      <h3 class="capability-name text-subtitle-2">{{ row.name }}</h3>
      <div class="capability-fields">
        <v-combobox :model-value="row.tasks" :items="tasks" item-title="name" item-value="id" :return-object="false" multiple chips closable-chips clearable label="适合的任务" :aria-label="row.name + '适合的任务'" :disabled="disabled" :error-messages="row.tiers.length && !row.tasks.length ? '请选择适合的任务，或清空难度以停用' : []" hide-details="auto" @update:model-value="update(row.poolId, { tasks: $event ?? [] })" />
        <v-select :model-value="row.tiers" :items="tiers" multiple chips closable-chips clearable label="可处理的难度" :aria-label="row.name + '可处理的难度'" :disabled="disabled" :error-messages="row.tasks.length && !row.tiers.length ? '请选择可处理的难度，或清空任务以停用' : []" hide-details="auto" @update:model-value="update(row.poolId, { tiers: $event ?? [] })" />
        <v-text-field v-if="useFeedback" :model-value="row.cost" type="number" min="0" label="相对成本" :aria-label="row.name + '相对成本'" :disabled="disabled || !row.tasks.length && !row.tiers.length" hide-details="auto" @update:model-value="update(row.poolId, { cost: Number($event) })" />
      </div>
    </section>
    <p v-if="pools.length" class="text-caption text-medium-emphasis">任务和难度同时匹配才会选中该组。多个组都满足时，按渠道分组顺序选择{{ useFeedback ? '，并参考反馈与成本' : '' }}。</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { IntentGroupCapability, IntentTask } from '@/services/intent'
const props = defineProps<{ pools: { id: string; name: string }[]; tasks: IntentTask[]; disabled: boolean; useFeedback: boolean }>()
const model = defineModel<IntentGroupCapability[]>({ required: true })
const tiers = [{ title: '简单', value: 0 }, { title: '常规', value: 1 }, { title: '复杂', value: 2 }]
// 列表来自现有渠道分组；能力配置只保存用户填写的内容，不另建分组清单。
const rows = computed(() => props.pools.map(pool => ({
  poolId: pool.id, tasks: [] as string[], tiers: [] as number[], cost: 0,
  ...model.value.find(group => group.poolId === pool.id), name: pool.name
})))
function update(poolId: string, patch: Partial<Pick<IntentGroupCapability, 'tasks' | 'tiers' | 'cost'>>) {
  const current = model.value.find(group => group.poolId === poolId)
  const next = { poolId, tasks: [] as string[], tiers: [] as number[], cost: 0, ...current, ...patch }
  const groups = model.value.filter(group => group.poolId !== poolId)
  if (next.tasks.length || next.tiers.length) groups.push(next)
  model.value = groups
}
</script>
<style scoped>
.capabilities { display: grid; gap: 20px; }
.capability-row { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(0, 4fr); align-items: start; gap: 20px; border-top: 1px solid rgba(var(--v-theme-outline), 0.2); padding-top: 20px; }
.capability-name { padding-top: 14px; overflow-wrap: anywhere; }
.capability-fields { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); align-items: start; gap: 16px; }
@media (max-width: 750px) { .capability-row { grid-template-columns: minmax(0, 1fr); gap: 12px; } .capability-name { padding-top: 0; } }
@media (max-width: 550px) { .capability-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
