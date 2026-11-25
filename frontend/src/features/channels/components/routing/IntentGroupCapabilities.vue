<template>
  <div class="capabilities">
    <EmptyState v-if="!pools.length" variant="inline" icon="mdi-swap-horizontal" title="当前协议暂无可配置的渠道分组" description="先在渠道编排中创建分组，再回来设置任务能力。" />
    <div v-else class="capability-table-wrap">
      <table class="capability-table" aria-label="分组能力配置">
        <thead>
          <tr><th scope="col" class="capability-group-column">渠道分组</th><th scope="col">适合的任务</th><th scope="col" class="capability-tier-column">处理难度</th><th v-if="useFeedback" scope="col" class="capability-cost-column">相对成本</th></tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.poolId" :aria-label="row.name + '的任务能力'">
            <th scope="row" class="capability-name">
              <span class="block text-sm font-medium">{{ row.name }}</span>
              <span class="capability-status" :class="row.tasks.length && row.tiers.length ? 'text-primary' : row.tasks.length || row.tiers.length ? 'text-warning' : 'text-muted-foreground'">
                <span class="size-1.5 shrink-0 rounded-full bg-current" aria-hidden="true"></span>
                {{ row.tasks.length && row.tiers.length ? '已参与' : row.tasks.length || row.tiers.length ? '配置不完整' : '未参与' }}
              </span>
            </th>
            <td>
              <span class="capability-mobile-label">适合的任务</span>
              <UiSelect searchable :creatable="allowCustom" :model-value="row.tasks" :items="tasks" item-title="name" item-value="id" :return-object="false" multiple chips inline-chips closable-chips clearable placeholder="选择任务…" :aria-label="row.name + '适合的任务'" :disabled="disabled" :error-messages="row.tiers.length && !row.tasks.length ? '请选择适合的任务，或清空难度以停用' : []" hide-details="auto" @update:model-value="update(row.poolId, { tasks: $event ?? [] })" />
            </td>
            <td>
              <span class="capability-mobile-label">处理难度</span>
              <div class="capability-tiers">
                <UiSwitch :model-value="isTierLimited(row.tiers)" label="限制难度" :aria-label="row.name + '限制处理难度'" :disabled="disabled || !row.tasks.length" hide-details @update:model-value="setTierLimit(row.poolId, !!$event)" />
                <span v-if="!isTierLimited(row.tiers)" class="text-xs text-muted-foreground">不限</span>
                <UiSelect v-else class="w-full" :model-value="row.tiers" :items="tiers" multiple chips inline-chips closable-chips placeholder="选择难度…" :aria-label="row.name + '允许的难度'" :disabled="disabled" hide-details="auto" @update:model-value="update(row.poolId, { tiers: $event ?? [] })" />
              </div>
            </td>
            <td v-if="useFeedback">
              <span class="capability-mobile-label">相对成本</span>
              <UiInput :model-value="row.cost" type="number" min="0" :aria-label="row.name + '相对成本'" :disabled="disabled || !row.tasks.length && !row.tiers.length" hide-details="auto" @update:model-value="update(row.poolId, { cost: Number($event) })" />
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
<script setup lang="ts">
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed } from 'vue'
import type { IntentGroupCapability, IntentTask } from '@/features/channels/services/intent'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
const props = defineProps<{ pools: { id: string; name: string }[]; tasks: IntentTask[]; disabled: boolean; useFeedback: boolean; allowCustom?: boolean }>()
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

.capabilities { @apply min-w-0; }
.capability-table-wrap { @apply overflow-hidden rounded-control border border-border; }
.capability-table { @apply w-full table-fixed border-collapse text-left; }
.capability-table thead { @apply bg-muted/35; }
.capability-table thead th { @apply px-4 py-2.5 text-xs font-medium text-muted-foreground; }
.capability-table tbody tr { @apply border-t border-border; }
.capability-table tbody th, .capability-table td { @apply px-4 py-3 align-top; }
.capability-group-column { @apply w-1/5; }
.capability-tier-column { @apply w-[32%]; }
.capability-cost-column { @apply w-28; }
.capability-name { @apply [overflow-wrap:anywhere]; }
.capability-status { @apply mt-1.5 inline-flex items-center gap-1.5 text-[11px] font-normal; }
.capability-tiers { @apply flex min-w-0 flex-wrap items-center justify-between gap-x-2 gap-y-1; }
.capability-tiers :deep(.ui-switch) { @apply min-h-10 text-xs; }
.capability-mobile-label { @apply hidden; }
@media (max-width: 750px) {
  .capability-table, .capability-table tbody { @apply block; }
  .capability-table thead { @apply sr-only; }
  .capability-table tbody tr { @apply grid grid-cols-2 gap-3 p-4; }
  .capability-table tbody tr:first-child { @apply border-t-0; }
  .capability-table tbody th, .capability-table td { @apply block min-w-0 p-0; }
  .capability-table tbody th { @apply col-span-2 flex flex-wrap items-center justify-between gap-2; }
  .capability-status { @apply mt-0; }
  .capability-mobile-label { @apply mb-1.5 block text-xs font-medium text-muted-foreground; }
}
@media (max-width: 500px) {
  .capability-table tbody tr { @apply grid-cols-1; }
  .capability-table tbody th { @apply col-span-1; }
}
</style>
