<template>
  <section class="match-trace" aria-label="意图匹配过程">
    <div v-if="!trace" class="text-body-2 text-medium-emphasis">这条旧记录未保存输入和匹配过程，不能按当前配置还原。</div>
    <template v-else>
      <div class="trace-toolbar">
        <span class="text-caption text-medium-emphasis">{{ trace.classified ? '匹配结果回放 · 非实时推理' : '本轮未重新分类 · 不代表任何分组匹配胜出' }}</span>
        <v-btn v-if="trace.classified" size="small" variant="text" @click="replay++">重播连线</v-btn>
      </div>
      <div ref="canvas" class="trace-canvas">
        <svg :key="replay" class="trace-lines" :width="size.width" :height="size.height" aria-hidden="true">
          <defs><marker :id="arrowId" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" /></marker></defs>
          <path v-for="line in connectors" :key="line.id" :d="line.path" :class="['trace-line', { 'trace-line--active': line.active }]" :marker-end="`url(#${arrowId})`" />
        </svg>
        <article ref="inputNode" class="trace-node trace-input">
          <h4 class="text-subtitle-2">本轮用户输入</h4>
          <p class="trace-input-text">{{ trace.input || '本轮没有新的用户文本' }}</p>
          <span class="text-caption text-medium-emphasis">已隐藏可识别凭据与脱敏占位符{{ trace.truncated ? ' · 输入已截取至 4000 字符' : '' }}</span>
        </article>
        <article ref="classifierNode" class="trace-node trace-classifier">
          <h4 class="text-subtitle-2">{{ trace.classified ? '识别与筛选' : '路由保护' }}</h4>
          <template v-if="trace.classified">
            <p>{{ taskName(decision.task) }} · {{ tierName(decision.tier) }}</p>
            <p class="text-caption">任务来源：{{ sourceName(trace.taskSource) }}</p>
            <p v-if="trace.taskEvidence" class="text-caption">{{ trace.taskEvidence }}</p>
            <p class="text-caption">难度来源：{{ sourceName(trace.tierSource) }}</p>
            <p v-if="trace.tierEvidence" class="text-caption">{{ trace.tierEvidence }}</p>
            <p class="text-caption">判断耗时 {{ decision.durationMs }} ms</p>
          </template>
          <p v-else class="text-body-2">{{ decision.reason }}</p>
          <p v-if="trace.error" class="text-body-2 text-error">{{ trace.error }}</p>
        </article>
        <div class="trace-groups" aria-label="当时的分组候选快照">
          <article v-for="group in trace.groups" :key="group.poolId" :data-pool-id="group.poolId" :class="['trace-node trace-group', { 'trace-group--selected': group.status === 'selected' }]">
            <div class="trace-group-title"><h4 class="text-subtitle-2">{{ group.name }}</h4><span class="trace-status text-caption">{{ statusName(group.status) }}</span></div>
            <p class="text-body-2">{{ group.reason }}</p>
            <p v-if="group.tasks?.length" class="text-caption text-medium-emphasis">任务：{{ group.tasks.map(taskName).join('、') }} · 难度：{{ group.tiers?.map(tierName).join('、') }}</p>
            <p v-if="group.selectionScore !== undefined" class="text-caption">反馈选组分 {{ group.selectionScore.toFixed(4) }} · 满意 {{ group.good ?? 0 }} / 不满意 {{ group.bad ?? 0 }} · 相对成本 {{ group.cost }}（不是置信度）</p>
          </article>
          <p v-if="!trace.groups.length" class="text-body-2 text-medium-emphasis">没有已记录的候选分组。</p>
        </div>
      </div>
      <p v-if="trace.selection" class="text-caption mt-3">选组规则：{{ trace.selection }}</p>
      <div class="trace-outcome text-body-2">
        <p>分类建议：{{ trace.classified ? snapshotName(trace.suggestedPoolId) : '未执行分类' }}</p>
        <p>{{ preview ? '草稿路由建议' : '保护后路由' }}：{{ snapshotName(decision.poolId) }} · {{ decision.reason }}</p>
        <p v-if="preview" class="text-caption text-medium-emphasis">仅预览分类，不生成回答。真实会话还要检查人工固定、驻留与历史连续性。</p>
        <p v-else>实际执行：{{ target?.channelId ? `${snapshotName(target.poolId)} · ${target.model} · ${target.channelId}` : '未记录已确认的执行目标' }}</p>
      </div>
      <details v-if="trace.semantic?.length" class="trace-semantic">
        <summary>语义命中依据 · 阈值 {{ trace.threshold }} / 最小差值 {{ trace.switchMargin }}</summary>
        <ul><li v-for="hit in trace.semantic" :key="`${hit.datasetId}:${hit.revision}:${hit.sampleId}`">{{ hit.datasetId }} @ {{ hit.revision }} · {{ hit.sampleId }} · {{ hit.label }} · {{ hit.negative ? '反例' : '正例' }} · 相似度 {{ hit.score.toFixed(4) }}</li></ul>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import type { IntentDecision, IntentEvent, IntentTask } from '@/services/intent'

const props = defineProps<{ decision: IntentDecision; tasks: IntentTask[]; target?: IntentEvent['target']; preview?: boolean }>()
const trace = computed(() => props.decision.trace)
const canvas = ref<HTMLElement>()
const inputNode = ref<HTMLElement>()
const classifierNode = ref<HTMLElement>()
const size = ref({ width: 0, height: 0 })
const connectors = ref<{ id: string; path: string; active: boolean }[]>([])
const replay = ref(0)
const arrowId = `intent-arrow-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
let observer: ResizeObserver | undefined
const taskName = (id?: string) => props.tasks.find(task => task.id === id)?.name ?? (id || '任务未确定')
const tierName = (tier: number) => ['简单', '常规', '复杂'][tier] ?? '难度未确定'
const snapshotName = (id?: string) => trace.value?.groups.find(group => group.poolId === id)?.name ?? (id || '未选组，沿用未命中策略')
const sourceLabels: Record<string, string> = { heuristic: '内置规则', regex: '正则规则', semantic: '数据集语义', llm: '辅助 LLM', decisions: 'Decisions', systemone: 'Decisions（旧配置）', hybrid: '混合模式的辅助模型' }
const statusLabels: Record<string, string> = { selected: '分类选中', candidate: '满足条件', excluded: '条件不符', unconfigured: '未配置能力', disabled: '已停用', skipped: '未参与选组' }
const sourceName = (source?: string) => sourceLabels[source ?? ''] ?? '未执行或未记录'
const statusName = (status: string) => statusLabels[status] ?? status

function measure() {
  const root = canvas.value
  if (!root || !inputNode.value || !classifierNode.value) return
  const rect = root.getBoundingClientRect()
  size.value = { width: rect.width, height: rect.height }
  const narrow = rect.width < 620
  const pathBetween = (from: HTMLElement, to: HTMLElement) => {
    const a = from.getBoundingClientRect()
    const b = to.getBoundingClientRect()
    if (narrow) {
      const x1 = a.left + a.width / 2 - rect.left
      const y1 = a.bottom - rect.top
      const x2 = b.left + b.width / 2 - rect.left
      const y2 = b.top - rect.top
      const middle = (y1 + y2) / 2
      return `M ${x1} ${y1} C ${x1} ${middle}, ${x2} ${middle}, ${x2} ${y2}`
    }
    const x1 = a.right - rect.left
    const y1 = a.top + a.height / 2 - rect.top
    const x2 = b.left - rect.left
    const y2 = b.top + b.height / 2 - rect.top
    const middle = (x1 + x2) / 2
    return `M ${x1} ${y1} C ${middle} ${y1}, ${middle} ${y2}, ${x2} ${y2}`
  }
  const lines = [{ id: 'input', path: pathBetween(inputNode.value, classifierNode.value), active: !!trace.value?.classified }]
  for (const node of root.querySelectorAll<HTMLElement>('[data-pool-id]')) {
    const group = trace.value?.groups.find(item => item.poolId === node.dataset.poolId)
    // 窄屏沿左侧留白连接候选，连线不穿过中间卡片的文字。
    let path = pathBetween(classifierNode.value, node)
    if (narrow) {
      const from = classifierNode.value.getBoundingClientRect()
      const to = node.getBoundingClientRect()
      const x = from.left - rect.left
      const y1 = from.top + from.height / 2 - rect.top
      const y2 = to.top + to.height / 2 - rect.top
      path = `M ${x} ${y1} H 1 V ${y2} H ${to.left - rect.left}`
    }
    lines.push({ id: node.dataset.poolId!, path, active: group?.status === 'selected' })
  }
  connectors.value = lines
}
watch(() => props.decision, async () => { await nextTick(); measure(); replay.value++ }, { deep: true })
onMounted(() => {
  observer = new ResizeObserver(measure)
  if (canvas.value) observer.observe(canvas.value)
  measure()
})
onUnmounted(() => observer?.disconnect())
</script>

<style scoped>
.match-trace { min-width: 0; }
.trace-toolbar, .trace-group-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.trace-canvas { position: relative; display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1.25fr); gap: 36px; align-items: center; margin-top: 12px; }
.trace-lines { position: absolute; inset: 0; pointer-events: none; overflow: visible; color: rgb(var(--v-theme-primary)); }
.trace-line { fill: none; stroke: rgba(var(--v-theme-on-surface), 0.22); stroke-width: 1.5; }
.trace-line--active { stroke: rgb(var(--v-theme-primary)); stroke-width: 2.5; stroke-dasharray: 8 5; animation: trace-flow 1s linear 3; }
.trace-node { z-index: 1; min-width: 0; padding: 14px; border: 1px solid rgba(var(--v-theme-outline), 0.3); border-radius: 10px; background: rgb(var(--v-theme-surface)); overflow-wrap: anywhere; }
.trace-node p { margin-top: 8px; }
.trace-input-text { white-space: pre-wrap; max-height: 250px; overflow: auto; font-size: 0.875rem; line-height: 1.6; }
.trace-groups { display: grid; gap: 12px; min-width: 0; }
.trace-group--selected { border: 2px solid rgb(var(--v-theme-primary)); }
.trace-status { padding: 2px 6px; border-radius: 4px; background: rgba(var(--v-theme-on-surface), 0.06); }
.trace-group--selected .trace-status { color: rgb(var(--v-theme-primary)); }
.trace-outcome { display: grid; gap: 6px; padding: 14px; margin-top: 16px; border-left: 3px solid rgb(var(--v-theme-primary)); background: rgba(var(--v-theme-primary), 0.05); overflow-wrap: anywhere; }
.trace-semantic { margin-top: 16px; font-size: 0.8125rem; overflow-wrap: anywhere; }
.trace-semantic summary { cursor: pointer; padding: 10px 0; }
.trace-semantic ul { padding-left: 20px; line-height: 1.8; }
@keyframes trace-flow { to { stroke-dashoffset: -26; } }
@media (max-width: 800px) { .trace-canvas { gap: 24px; } }
@container intent-trace (max-width: 619px) { .trace-canvas { grid-template-columns: minmax(0, 1fr); padding-inline: 14px; } }
.match-trace { container-type: inline-size; container-name: intent-trace; }
@media (prefers-reduced-motion: reduce) { .trace-line--active { animation: none; stroke-dasharray: none; } }
</style>
