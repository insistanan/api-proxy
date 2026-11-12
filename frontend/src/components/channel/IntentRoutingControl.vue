<template>
  <v-btn :variant="saved?.enabled ? 'tonal' : 'outlined'" :color="saved?.enabled ? 'primary' : undefined" :loading="loading" :aria-pressed="!!saved?.enabled" aria-label="配置当前协议的意图匹配" @click="open">
    <v-icon start size="18">mdi-server-network</v-icon>
    意图匹配 <span class="ml-2 text-caption">{{ saved ? saved.enabled ? '开' : '关' : '未知' }}</span>
  </v-btn>
  <v-dialog v-model="show" max-width="960" scrollable :persistent="saving || indexing">
    <v-card>
      <v-card-title class="pt-5 px-6">{{ protocol }} · 意图匹配</v-card-title>
      <v-card-text class="intent-dialog-body">
        <v-alert v-if="error" type="error" variant="tonal" class="mb-5">{{ error }}</v-alert>
        <v-form v-if="draft" :disabled="saving || indexing" @submit.prevent>
          <v-switch v-model="draft.enabled" color="primary" label="启用当前协议的意图匹配" hide-details class="mb-5" />
          <v-tabs v-model="tab" color="primary" class="mb-6" :disabled="saving || indexing">
            <v-tab value="routing">路由设置</v-tab>
            <v-tab value="groups">分组能力</v-tab>
            <v-tab value="diagnostics" @click="loadEvents">预览与反馈</v-tab>
            <v-tab value="advanced">高级设置</v-tab>
          </v-tabs>
          <div v-if="tab === 'routing'" class="intent-stack">
            <v-select v-model="draft.strategy" :items="strategies" label="匹配方式" />
            <template v-if="draft.strategy === 'auto'">
              <div class="intent-flow" aria-label="先识别任务类型，再判断难度，最后选择合适分组">
                <span>① 识别任务类型</span><span>② 判断处理难度</span><span>③ 选择合适分组</span>
              </div>
              <p class="text-body-2 text-medium-emphasis">根据任务类型和难度，自动选择现有渠道分组。分组内沿用渠道顺序和模型映射。</p>
              <v-select v-model="draft.classifier" :items="classifiers" label="自动判断方式" />
              <p v-if="draft.classifier === 'heuristic'" class="text-caption text-medium-emphasis">内置规则识别明确任务，速度快、不调用额外模型；类型或难度不明确时按未命中策略处理。</p>
              <template v-if="draft.classifier !== 'heuristic'">
                <v-select v-model="draft.classifierChannel" :items="classifierOptions" item-title="name" item-value="id" label="辅助判断渠道" clearable @update:model-value="draft.classifierChannel ||= ''" />
                <v-text-field v-model="draft.classifierModel" label="辅助判断模型" hint="用于任务类型和难度判断；远程模式最多执行两次辅助分类，共享总超时" persistent-hint />
              </template>
              <div class="d-flex flex-wrap align-center ga-3">
                <v-btn variant="tonal" @click="tab = 'groups'">设置分组能力</v-btn>
                <span class="text-body-2 text-medium-emphasis">当前 {{ pools.length }} 个渠道分组，{{ groups.filter(group => group.tasks.length && group.tiers.length).length }} 个已设置能力</span>
              </div>
              <p v-if="draft.taskMatcher === 'semantic'" class="text-body-2">任务类型使用数据集向量匹配，难度使用上方判断方式。向量配置在高级设置中管理。</p>
            </template>
            <template v-else>
              <p class="text-body-2 text-medium-emphasis">{{ strategyHelp }}</p>
              <template v-if="draft.strategy === 'complexity' || draft.strategy === 'adaptive'">
                <v-select v-model="draft.classifier" :items="classifiers" label="难度判断方式" />
                <template v-if="draft.classifier !== 'heuristic'">
                  <v-select v-model="draft.classifierChannel" :items="classifierOptions" item-title="name" item-value="id" label="辅助判断渠道" clearable @update:model-value="draft.classifierChannel ||= ''" />
                  <v-text-field v-model="draft.classifierModel" label="辅助判断模型" />
                </template>
              </template>
              <v-alert v-if="draft.strategy === 'adaptive'" type="info" variant="tonal">
                同一难度有多个候选分组时，参考任务反馈和成本，仅在新会话选组。
                <v-switch v-model="draft.observeOnly" label="仅观察建议，暂不改变选组" hide-details class="mt-3" />
              </v-alert>
              <DatasetPicker v-if="draft.strategy === 'semantic'" v-model="draft.datasetIds" kind="intent" />
              <div class="d-flex flex-wrap align-center justify-space-between ga-3">
                <h3 class="text-subtitle-1">{{ draft.strategy === 'regex' ? '正则规则与分组' : '任务或难度与分组' }}</h3>
                <v-btn variant="tonal" size="small" :disabled="saving" @click="addRoute">添加映射</v-btn>
              </div>
              <v-alert v-if="!draft.routes.length" type="info" variant="tonal">添加目标分组映射后即可启用。自动综合匹配请改用上方推荐模式。</v-alert>
              <v-expansion-panels variant="accordion">
                <v-expansion-panel v-for="(route, index) in draft.routes" :key="index">
                  <v-expansion-panel-title>{{ poolName(route.poolId) }} · {{ route.description || route.id || '待配置' }}</v-expansion-panel-title>
                  <v-expansion-panel-text>
                    <div class="intent-stack pt-2">
                      <v-select v-model="route.poolId" :items="pools" item-title="name" item-value="id" label="目标分组" />
                      <v-text-field v-model="route.description" label="任务说明" />
                      <v-text-field v-if="draft.strategy === 'semantic'" v-model="route.label" label="数据集类别" hint="与样本 label 一致，留空使用映射 ID" persistent-hint />
                      <v-textarea v-if="draft.strategy === 'regex'" :model-value="route.patterns.join('\n')" label="正则表达式，每行一条" rows="3" @update:model-value="route.patterns = lines($event)" />
                      <v-textarea v-if="draft.strategy === 'semantic' && !draft.datasetIds?.length" :model-value="route.examples.join('\n')" label="任务例句，每行一句" rows="3" @update:model-value="route.examples = lines($event)" />
                      <v-select v-model="route.tier" :items="tiers" label="难度等级" />
                      <v-text-field v-if="draft.strategy === 'adaptive'" v-model.number="route.cost" type="number" min="0" label="相对成本" />
                      <v-text-field v-model="route.id" label="映射 ID" hint="已自动生成，用于稳定引用" persistent-hint />
                      <v-btn variant="text" color="error" @click="draft.routes.splice(index, 1)">移除映射</v-btn>
                    </div>
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>
            </template>
            <p class="text-caption text-medium-emphasis">显式渠道和人工会话固定优先。工具续轮、加密推理及服务端历史保留原来源；分类结果不会写入主请求提示词。</p>
          </div>
          <IntentGroupCapabilities v-else-if="tab === 'groups'" v-model="groups" :pools="pools" :tasks="options.tasks ?? []" :disabled="saving || indexing" :use-feedback="!!draft.useFeedback" />
          <div v-else-if="tab === 'diagnostics'" class="intent-stack">
            <section class="intent-stack">
              <div><h3 class="text-subtitle-1">试一条任务</h3><p class="text-body-2 text-medium-emphasis mt-2">只测试当前草稿的分类结果，不向目标组生成回答、不切换真实会话。远程判断或向量匹配会调用辅助模型。</p></div>
              <v-textarea v-model="previewText" label="模拟用户任务" rows="3" placeholder="例如：排查并修复 Go 服务中的并发死锁" />
              <div><v-btn variant="tonal" :loading="previewing" :disabled="!previewText.trim()" @click="preview">测试匹配</v-btn></div>
              <v-alert v-if="decision" type="info" variant="tonal">
                <div v-if="decision.task">{{ taskName(decision.task) }} · {{ decision.tier >= 0 ? tierName(decision.tier) : '难度未确定' }}</div>
                <div>{{ poolName(decision.poolId) }} · {{ decision.reason }} · {{ decision.durationMs }} ms</div>
                <div class="text-caption mt-2">真实会话还会执行驻留、轮次和历史连续性检查，最终分组可能不同。</div>
              </v-alert>
            </section>
            <v-divider class="my-2" />
            <section class="intent-stack">
              <div class="d-flex flex-wrap align-center justify-space-between ga-3"><h3 class="text-subtitle-1">最近路由与任务反馈</h3><v-btn variant="text" :loading="eventsLoading" @click="loadEvents">刷新记录</v-btn></div>
              <p class="text-body-2 text-medium-emphasis">记录真实请求的实际分组、模型、原因和判断耗时。“已交付”表示请求完成，不代表回答质量；满意度由你评价，不会自动训练模型或更改数据集。已分类任务按类型和难度分别统计，驻留期未分类的新任务只保留评价。</p>
              <p v-if="!events.length" class="text-body-2 text-medium-emphasis">暂无记录；先保存并启用意图匹配，再发送新任务。</p>
              <div v-for="event in events" :key="event.requestId" class="intent-event">
                <div>{{ poolName(event.target.poolId) }} · {{ event.target.model }} · {{ event.success ? '已交付' : '未完成' }}</div>
                <div v-if="event.decision.task" class="text-body-2">{{ taskName(event.decision.task) }} · {{ tierName(event.decision.tier) }}</div>
                <div class="text-caption text-medium-emphasis">{{ event.decision.reason }} · {{ event.decision.durationMs }} ms</div>
                <div class="text-caption text-medium-emphasis">{{ event.requestId }}</div>
                <div v-if="event.success && !event.control && event.feedback === undefined" class="d-flex ga-2 mt-2">
                  <v-btn size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, true)">效果满意</v-btn>
                  <v-btn size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, false)">效果不满意</v-btn>
                </div>
                <span v-else-if="event.feedback !== undefined" class="text-caption">已记录：{{ event.feedback ? '满意' : '不满意' }}</span>
              </div>
            </section>
          </div>
          <div v-else class="intent-stack">
            <template v-if="draft.strategy === 'auto'">
              <h3 class="text-subtitle-1">任务识别与选组优化</h3>
              <v-select v-model="draft.taskMatcher" :items="[{ title: '按基础设置中的判断方式识别', value: 'builtin' }, { title: '使用数据集向量识别任务类型', value: 'semantic' }]" label="任务类型识别来源" />
              <v-switch v-model="draft.useFeedback" label="同能力分组参考任务反馈和相对成本" hide-details />
              <p class="text-caption text-medium-emphasis">只在满足任务和难度的候选组内优化。关闭时按渠道分组顺序选择；已固定会话不会因为一次反馈立即换组。</p>
            </template>
            <template v-if="usesEmbedding">
              <h3 class="text-subtitle-1">向量任务识别</h3>
              <DatasetPicker v-if="draft.strategy === 'auto'" v-model="draft.datasetIds" kind="intent" />
              <EmbeddingProfileFields v-model="embeddingProfile" hide-timeout />
              <v-text-field v-model.number="draft.threshold" type="number" min="0" max="1" step="0.01" label="语义命中阈值" />
              <div><v-btn variant="tonal" :loading="indexing" :disabled="saving || indexing" @click="buildIndex">增量构建并发布索引</v-btn></div>
              <p class="text-caption text-medium-emphasis">索引提前构建并持久保存；模型、版本和前缀需与数据集一致。新模型启用前需重新构建。</p>
            </template>
            <h3 class="text-subtitle-1">切换保护与未命中处理</h3>
            <div class="intent-fields">
              <v-text-field v-model.number="draft.minResidenceSeconds" type="number" min="30" label="最短驻留时间（秒）" />
              <v-text-field v-model.number="draft.minTurns" type="number" min="1" label="切换前最少用户轮次" />
              <v-text-field v-if="usesEmbedding" v-model.number="draft.switchMargin" type="number" min="0" max="1" step="0.01" label="最小相似度差值" hint="防止相近样本反复选组；综合匹配还比较前两种任务类型" persistent-hint />
              <v-text-field v-model.number="draft.timeoutMs" type="number" min="100" max="30000" label="辅助判断总超时（毫秒）" />
            </div>
            <v-select v-model="draft.unmatched" :items="[{ title: '保留原分组规则', value: 'original' }, { title: '返回未命中错误', value: 'error' }, { title: '使用指定分组', value: 'fallback' }]" label="任务或难度不明确、无合适分组时" />
            <v-select v-if="draft.unmatched === 'fallback'" v-model="draft.fallbackPoolId" :items="pools" item-title="name" item-value="id" label="未命中目标分组" />
            <v-select v-model="draft.failurePolicy" :items="[{ title: '返回明确错误', value: 'error' }, { title: '记录失败并保留原路由', value: 'keep' }]" label="辅助判断失败或超时" />
            <v-expansion-panels variant="accordion">
              <v-expansion-panel title="本地向量助手 · 仅向量匹配需要"><v-expansion-panel-text><LocalEmbeddingSettings /></v-expansion-panel-text></v-expansion-panel>
            </v-expansion-panels>
          </div>
        </v-form>
      </v-card-text>
      <v-card-actions class="px-6 py-4">
        <v-spacer /><v-btn :disabled="saving || indexing" @click="show = false">取消</v-btn>
        <v-btn color="primary" variant="flat" :loading="saving" :disabled="!draft || indexing" @click="save">保存当前协议</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChannelStore } from '@/stores/channel'
import { useIntentStore } from '@/stores/intent'
import { useToast } from '@/composables/useToast'
import { intentApi, intentErrorText, type IntentConfig, type IntentDecision, type IntentEvent, type IntentGroupCapability, type IntentOptions, type IntentProtocol } from '@/services/intent'
import type { EmbeddingProfile } from '@/services/datasets'
import LocalEmbeddingSettings from './LocalEmbeddingSettings.vue'
import DatasetPicker from './DatasetPicker.vue'
import EmbeddingProfileFields from './EmbeddingProfileFields.vue'
import IntentGroupCapabilities from './IntentGroupCapabilities.vue'

const props = defineProps<{ protocol: IntentProtocol }>()
const store = useIntentStore()
const channelStore = useChannelStore()
const { showToast } = useToast()
const show = ref(false)
const tab = ref('routing')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const draft = ref<IntentConfig>()
const options = ref<IntentOptions>({ classifiers: [], embeddings: [], tasks: [] })
const previewText = ref('')
const previewing = ref(false)
const indexing = ref(false)
const decision = ref<IntentDecision>()
const events = ref<IntentEvent[]>([])
const eventsLoading = ref(false)
const feedbackBusy = ref(false)
const saved = computed(() => store.configs[props.protocol])
const pools = computed(() => channelStore.channelPoolsMap[props.protocol].filter(pool => pool.id !== 'builtin-free'))
const poolName = (id: string) => pools.value.find(pool => pool.id === id)?.name ?? (id || '沿用未命中策略')
const taskName = (id: string) => options.value.tasks?.find(task => task.id === id)?.name ?? id
const tierName = (tier: number) => tiers.find(item => item.value === tier)?.title ?? '未确定'
const groups = computed({ get: () => orderGroups(draft.value?.groups ?? []), set: value => { if (draft.value) draft.value.groups = orderGroups(value) } })
const usesEmbedding = computed(() => draft.value?.strategy === 'semantic' || draft.value?.strategy === 'auto' && draft.value.taskMatcher === 'semantic')
const embeddingProfile = computed<EmbeddingProfile>({
  get: () => ({ source: draft.value!.embeddingSource, channel: draft.value!.embeddingChannel, model: draft.value!.embeddingModel, modelVersion: draft.value!.embeddingVersion ?? '', queryPrefix: draft.value!.queryPrefix, examplePrefix: draft.value!.examplePrefix, timeoutMs: draft.value!.timeoutMs }),
  set: value => { if (draft.value) Object.assign(draft.value, { embeddingSource: value.source, embeddingChannel: value.channel, embeddingModel: value.model, embeddingVersion: value.modelVersion, queryPrefix: value.queryPrefix, examplePrefix: value.examplePrefix, timeoutMs: value.timeoutMs }) }
})
const classifierOptions = computed(() => options.value.classifiers.filter(option =>
  draft.value?.classifier === 'decisions' || draft.value?.classifier === 'systemone' ? option.kind === 'decisions' : option.kind !== 'decisions'))
const strategies = [{ title: '自动综合匹配 · 类型 + 难度（推荐）', value: 'auto' }, { title: '数据集语义匹配', value: 'semantic' }, { title: '仅按难度分级', value: 'complexity' }, { title: '按难度与反馈自适应选择', value: 'adaptive' }, { title: '手动正则匹配', value: 'regex' }]
const classifiers = [{ title: '内置规则 · 不调用额外模型', value: 'heuristic' }, { title: '普通 LLM 自动判断', value: 'llm' }, { title: 'Decisions 决策模型', value: 'decisions' }, { title: 'System One 决策模型', value: 'systemone' }, { title: '混合 · 明确信号用规则，其余交 LLM', value: 'hybrid' }]
const tiers = [{ title: '简单', value: 0 }, { title: '常规', value: 1 }, { title: '复杂', value: 2 }]
const strategyHelp = computed(() => ({ regex: '只有这种方式需要手写正则，适合固定关键词或精确规则。', semantic: '自动匹配已发布的数据集，再按类别选择分组。向量模型在高级设置中配置。', complexity: '自动判断难度，你只需要把难度映射到分组，无需编写匹配句子。', adaptive: '自动判断难度，再从同难度候选组中参考质量反馈和相对成本选择。', auto: '' })[draft.value?.strategy ?? 'auto'])
const lines = (text: string | null) => (text ?? '').split('\n').map(line => line.trim()).filter(Boolean)
const report = (cause: unknown) => { error.value = intentErrorText(cause) }

function orderGroups(value: IntentGroupCapability[]) {
  const order = new Map(pools.value.map((pool, index) => [pool.id, index]))
  return [...value].sort((a, b) => (order.get(a.poolId) ?? pools.value.length) - (order.get(b.poolId) ?? pools.value.length))
}
function validateGroups() {
  const incomplete = groups.value.find(group => !group.tasks.length || !group.tiers.length)
  if (!incomplete) return true
  error.value = `请为“${poolName(incomplete.poolId)}”同时选择任务和难度，或将两项清空。`
  tab.value = 'groups'
  return false
}

async function load() {
  const protocol = props.protocol
  loading.value = true
  try { await store.load(protocol) }
  catch (cause) { if (protocol === props.protocol) report(cause) }
  finally { if (protocol === props.protocol) loading.value = false }
}
async function open() {
  draft.value = undefined
  show.value = true
  tab.value = 'routing'
  error.value = ''
  decision.value = undefined
  const protocol = props.protocol
  try {
    const [config, choices] = await Promise.all([store.load(protocol), intentApi.options()])
    if (protocol !== props.protocol || !show.value) return
    const copy = JSON.parse(JSON.stringify(config)) as IntentConfig
    // 只为尚未设置的草稿推荐自动模式，不改动已保存的手动规则。
    if (!copy.enabled && !copy.routes.length && !copy.groups?.length) copy.strategy = 'auto'
    copy.groups ??= []
    copy.taskMatcher ??= 'builtin'
    draft.value = copy
    options.value = choices
  } catch (cause) { if (protocol === props.protocol && show.value) report(cause) }
}
function addRoute() {
  draft.value?.routes.push({ id: 'task_' + Date.now(), poolId: '', description: '', patterns: [], examples: [], tier: 1, cost: 0 })
}
async function save() {
  if (!draft.value || saving.value || indexing.value || !validateGroups()) return
  saving.value = true
  error.value = ''
  try { await store.save(props.protocol, { ...draft.value, groups: groups.value }); showToast(props.protocol + ' 意图匹配配置已保存', 'success'); show.value = false }
  catch (cause) { report(cause) }
  finally { saving.value = false }
}
async function preview() {
  if (!draft.value || previewing.value || !validateGroups()) return
  const snapshot = JSON.stringify(draft.value)
  const text = previewText.value
  previewing.value = true
  error.value = ''
  decision.value = undefined
  try {
    const result = await intentApi.preview(props.protocol, { ...draft.value, groups: groups.value }, text)
    if (show.value && snapshot === JSON.stringify(draft.value) && text === previewText.value) decision.value = result
  } catch (cause) { if (snapshot === JSON.stringify(draft.value) && text === previewText.value) report(cause) }
  finally { previewing.value = false }
}
async function buildIndex() {
  if (!draft.value || indexing.value || saving.value) return
  indexing.value = true
  error.value = ''
  try { await intentApi.buildIndex(props.protocol, draft.value); showToast('数据集索引已构建并发布', 'success') }
  catch (cause) { report(cause) }
  finally { indexing.value = false }
}
async function loadEvents() {
  const protocol = props.protocol
  eventsLoading.value = true
  try { const result = await intentApi.events(protocol); if (protocol === props.protocol) events.value = result }
  catch (cause) { if (protocol === props.protocol) report(cause) }
  finally { eventsLoading.value = false }
}
async function feedback(event: IntentEvent, good: boolean) {
  if (feedbackBusy.value) return
  feedbackBusy.value = true
  try { await intentApi.feedback(event.protocol, event.requestId, good); await loadEvents() }
  catch (cause) { report(cause) }
  finally { feedbackBusy.value = false }
}
watch(() => props.protocol, () => { show.value = false; draft.value = undefined; events.value = []; void load() }, { immediate: true })
watch([draft, previewText], () => { decision.value = undefined }, { deep: true })
</script>

<style scoped>
.intent-dialog-body { padding: 20px 24px 28px; }
.intent-stack { display: grid; gap: 22px; }
.intent-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 22px 20px; }
.intent-flow { display: flex; flex-wrap: wrap; gap: 12px 28px; padding: 16px 0; border-block: 1px solid rgba(var(--v-theme-outline), 0.2); }
.intent-event { display: grid; gap: 6px; padding: 16px 0; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.2); overflow-wrap: anywhere; }
@media (max-width: 650px) { .intent-fields { grid-template-columns: minmax(0, 1fr); } .intent-dialog-body { padding: 16px; } }
</style>
