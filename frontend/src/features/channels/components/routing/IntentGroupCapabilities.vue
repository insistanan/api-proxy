<template>
  <div class="capabilities">
    <p class="text-sm text-muted-foreground">选择适合的任务即可参与自动匹配；默认接受所有难度，难度可进一步限制。</p>
    <EmptyState v-if="!pools.length" variant="inline" icon="mdi-swap-horizontal" title="当前协议暂无可配置的渠道分组" description="先在渠道编排中创建分组，再回来设置任务能力。" />
    <section v-for="row in rows" :key="row.poolId" class="capability-row" :aria-label="row.name + '的任务能力'">
      <div class="capability-name"><h4 class="text-sm font-medium">{{ row.name }}</h4><span class="text-xs" :class="row.tasks.length && row.tiers.length ? 'text-primary' : 'text-muted-foreground'">{{ row.tasks.length && row.tiers.length ? '已参与' : row.tasks.length || row.tiers.length ? '配置不完整' : '未参与' }}</span></div>
      <div class="capability-fields">
        <UiSelect searchable creatable :model-value="row.tasks" :items="tasks" item-title="name" item-value="id" :return-object="false" multiple chips closable-chips clearable label="适合的任务" :aria-label="row.name + '适合的任务'" :disabled="disabled" :error-messages="row.tiers.length && !row.tasks.length ? '请选择适合的任务，或清空难度以停用' : []" hide-details="auto" @update:model-value="update(row.poolId, { tasks: $event ?? [] })" />
        <UiSwitch :model-value="isTierLimited(row.tiers)" label="限制处理难度" :aria-label="row.name + '限制处理难度'" :disabled="disabled || !row.tasks.length" hide-details @update:model-value="setTierLimit(row.poolId, !!$event)" />
        <UiSelect v-if="isTierLimited(row.tiers)" :model-value="row.tiers" :items="tiers" multiple chips closable-chips label="允许的难度" :aria-label="row.name + '允许的难度'" :disabled="disabled" hide-details="auto" @update:model-value="update(row.poolId, { tiers: $event ?? [] })" />
        <UiInput v-if="useFeedback" :model-value="row.cost" type="number" min="0" label="相对成本" :aria-label="row.name + '相对成本'" :disabled="disabled || !row.tasks.length && !row.tiers.length" hide-details="auto" @update:model-value="update(row.poolId, { cost: Number($event) })" />
      </div>
    </section>
    <p v-if="pools.length" class="text-xs text-muted-foreground">多个分组都满足时，按渠道分组顺序选择{{ useFeedback ? '，并参考反馈与成本' : '' }}。</p>
  </div>
</template>
<script setup lang="ts">
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed } from 'vue'
import type { IntentGroupCapability, IntentTask } from '@/features/channels/services/intent'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
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
  if (patch.tasks && !patch.tasks.length) { model.value = groups; return }
  if (patch.tasks?.length && !next.tiers.length) next.tiers = tiers.map(item => item.value)
  if (next.tasks.length) groups.push(next)
  model.value = groups
}
function isTierLimited(value: number[]) { return value.length > 0 && value.length < tiers.length }
function setTierLimit(poolId: string, limited: boolean) {
  const current = model.value.find(group => group.poolId === poolId)
  if (!current) return
  update(poolId, { tiers: limited ? [1] : tiers.map(item => item.value) })
}
</script>
<style scoped>
@reference "@/styles/index.css";

.capabilities { @apply grid gap-5; }
.capability-row { @apply grid [grid-template-columns:minmax(100px,_1fr)_minmax(0,_4fr)] items-start gap-5 [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)] pt-5; }
.capability-name { @apply pt-3.5 [overflow-wrap:anywhere]; }
.capability-fields { @apply grid [grid-template-columns:minmax(0,_3fr)_minmax(0,_2fr)] items-start gap-4; }
@media (max-width: 750px) { .capability-row { @apply [grid-template-columns:minmax(0,_1fr)] gap-3; } .capability-name { @apply pt-0; } }
@media (max-width: 550px) { .capability-fields { @apply [grid-template-columns:minmax(0,_1fr)]; } }
</style>
