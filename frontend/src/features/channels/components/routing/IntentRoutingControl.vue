<template>
  <v-btn :variant="saved?.enabled ? 'tonal' : 'outlined'" :color="saved?.enabled ? 'primary' : undefined" :loading="loading" :aria-pressed="!!saved?.enabled" aria-label="配置当前协议的意图匹配" @click="open">
    <v-icon start size="18">mdi-server-network</v-icon>
    意图匹配 <span class="ml-2 text-caption">{{ saved ? saved.enabled ? '开' : '关' : '未知' }}</span>
  </v-btn>
  <AppDialog v-model="show" :title="protocol + ' · 意图匹配'" size="xl" :busy="saving || indexing || localBusy">
    <v-alert v-if="error" type="error" variant="tonal" class="mb-5">{{ error }}</v-alert>
    <v-form v-if="draft" :disabled="saving || indexing" class="intent-stack" @submit.prevent>
      <v-switch v-model="draft.enabled" color="primary" label="启用意图分流" hint="关闭后沿用网关原有的渠道选择" persistent-hint />
      <section class="intent-stack" aria-labelledby="intent-step-routing">
        <div class="d-flex flex-wrap align-center justify-space-between ga-3">
          <div>
            <h3 id="intent-step-routing" class="text-subtitle-1">怎么识别任务</h3>
            <p class="text-body-2 text-medium-emphasis mb-0">先选一种方式，其他参数会自动使用安全默认值。</p>
          </div>
          <v-chip v-if="simpleMode === 'builtin'" color="primary" variant="tonal">推荐</v-chip>
        </div>
        <v-radio-group v-model="simpleMode" hide-details>
          <div class="intent-strategies">
            <div v-for="mode in simpleModes" :key="mode.value" class="intent-strategy">
              <v-radio :label="mode.title" :value="mode.value" />
              <p class="text-caption text-medium-emphasis ml-8 mb-0">{{ mode.description }}</p>
            </div>
          </div>
        </v-radio-group>
        <template v-if="simpleMode === 'semantic'">
          <div class="intent-inline-status">
            <span class="text-body-2" :class="vectorReason ? 'text-warning' : 'text-primary'">{{ vectorReason || '向量模型已就绪' }}</span>
            <v-btn variant="tonal" size="small" @click="vectorGuide = true">{{ vectorReason ? '准备向量模型' : '更换向量模型' }}</v-btn>
          </div>
          <DatasetPicker v-model="draft.datasetIds" kind="intent" :multiple="false" require-published :refresh-key="datasetRefresh" @published-profile="usePublishedEmbedding" />
          <p class="text-caption text-medium-emphasis mb-0">没有数据集时，可直接采用内置合成样本；仅索引样本会被确认并发布。</p>
          <v-btn variant="tonal" :loading="indexing" :disabled="indexing || localBusy" @click="startSemanticSetup">{{ vectorReason ? '准备向量模型' : draft.datasetIds?.length ? '构建并发布索引' : '采用内置样本并构建索引' }}</v-btn>
        </template>
        <template v-if="simpleMode === 'assistant'">
          <v-select :model-value="draft.classifier" :items="assistantClassifiers" label="判断类型" @update:model-value="selectClassifier" />
          <v-alert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</v-alert>
          <template v-else>
            <v-autocomplete v-model="draft.classifierChannel" :items="classifierOptions" item-title="title" item-value="id" label="判断渠道" clearable @update:model-value="selectClassifierChannel" />
            <v-combobox v-model="draft.classifierModel" :items="classifierModels" :return-object="false" label="判断模型" clearable @update:model-value="draft.classifierModel ||= ''" />
          </template>
        </template>
        <div v-if="simpleMode === 'custom'" class="d-flex flex-wrap align-center ga-3">
          <span class="text-body-2 text-medium-emphasis">当前使用自定义模式：{{ strategyTitle(draft.strategy) }}</span>
          <v-btn variant="tonal" size="small" @click="simpleMode = 'builtin'">切换到自动识别</v-btn>
        </div>
        <p class="text-caption text-medium-emphasis">按任务和难度选组，会话固定与续轮保护仍优先。</p>
      </section>
      <v-divider />
      <section v-if="draft.strategy === 'auto'" ref="groupsSection" class="intent-stack" aria-labelledby="intent-step-groups">
        <div class="d-flex flex-wrap align-center justify-space-between ga-3">
          <h3 id="intent-step-groups" class="text-subtitle-1">第 2 步 · 分组能力</h3>
          <span class="text-body-2 text-medium-emphasis">{{ configuredGroups }} / {{ pools.length }} 个分组已配置能力</span>
        </div>
        <IntentGroupCapabilities v-model="groups" :pools="pools" :tasks="options.tasks ?? []" :disabled="saving || indexing" :use-feedback="!!draft.useFeedback" />
      </section>
      <v-divider />
      <section class="intent-stack" aria-labelledby="intent-step-preview">
        <h3 id="intent-step-preview" class="text-subtitle-1">第 3 步 · 试一条</h3>
        <v-textarea v-model="previewText" label="模拟用户任务" rows="3" placeholder="例如：排查并修复 Go 服务中的并发死锁" hint="只预览当前草稿；远程判断和向量匹配会调用辅助模型。" />
        <div><v-btn variant="tonal" :loading="previewing" :disabled="!previewText.trim() || !!vectorReason || !!classifierReason" @click="preview">测试匹配</v-btn></div>
        <IntentMatchTrace v-if="decision" :decision="decision" :tasks="options.tasks ?? []" preview />
      </section>
      <v-expansion-panels variant="accordion">
        <v-expansion-panel title="高级设置">
          <v-expansion-panel-text>
            <div class="intent-stack">
              <v-select v-if="simpleMode === 'custom'" v-model="draft.strategy" :items="strategies" label="自定义匹配模式" item-title="title" item-value="value" />
              <template v-if="draft.strategy !== 'auto'">
                <v-alert v-if="!draft.routes.length" type="info" variant="tonal">添加规则映射后即可启用。</v-alert>
                <div class="d-flex flex-wrap align-center justify-space-between ga-3">
                  <h3 class="text-subtitle-1">{{ draft.strategy === 'regex' ? '正则规则' : '规则映射' }}</h3>
                  <v-btn variant="tonal" size="small" :disabled="saving" @click="addRoute">添加映射</v-btn>
                </div>
                <v-expansion-panels variant="accordion" class="mb-5">
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
              <v-select v-if="draft.strategy === 'auto'" v-model="draft.taskMatcher" :items="[{ title: '内置规则', value: 'builtin' }, { title: '向量数据集', value: 'semantic' }]" label="任务识别来源" />
              <v-select v-if="needsClassifier && simpleMode !== 'assistant'" :model-value="draft.classifier" :items="classifiers" label="不明确任务的判断方式" @update:model-value="selectClassifier" />
              <template v-if="needsClassifier && draft.classifier !== 'heuristic' && simpleMode !== 'assistant'">
                <v-alert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</v-alert>
                <template v-else>
                  <v-autocomplete v-model="draft.classifierChannel" :items="classifierOptions" item-title="title" item-value="id" label="辅助判断渠道" clearable @update:model-value="selectClassifierChannel" />
                  <v-combobox v-model="draft.classifierModel" :items="classifierModels" :return-object="false" label="辅助判断模型" clearable @update:model-value="draft.classifierModel ||= ''" />
                </template>
              </template>
              <v-expansion-panels variant="accordion">
                <v-expansion-panel title="会话稳定性"><v-expansion-panel-text>
                  <div class="intent-fields">
                    <v-text-field v-model.number="draft.minResidenceSeconds" type="number" min="30" label="最短驻留时间（秒）" />
                    <v-text-field v-model.number="draft.minTurns" type="number" min="1" label="切换前最少用户轮次" />
                  </div>
                  <v-switch v-if="draft.strategy === 'auto'" v-model="draft.useFeedback" label="同能力分组参考反馈与相对成本" hide-details />
                  <v-switch v-if="draft.strategy === 'adaptive'" v-model="draft.observeOnly" label="自适应选组仅观察建议" hide-details />
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel title="未命中与失败"><v-expansion-panel-text>
                  <div class="intent-stack">
                    <v-select v-model="draft.unmatched" :items="[{ title: '不干预，使用网关默认选组', value: 'original' }, { title: '返回未命中错误', value: 'error' }, { title: '使用指定分组', value: 'fallback' }]" label="识别不出来时" />
                    <v-select v-if="draft.unmatched === 'fallback'" v-model="draft.fallbackPoolId" :items="pools" item-title="name" item-value="id" label="未命中目标分组" />
                    <v-select v-model="draft.failurePolicy" :items="[{ title: '沿用原路由并记录失败', value: 'keep' }, { title: '返回明确错误', value: 'error' }]" label="辅助判断失败或超时" />
                    <v-text-field v-model.number="draft.timeoutMs" type="number" min="100" max="30000" label="辅助判断总超时（毫秒）" />
                  </div>
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel v-if="usesEmbedding" title="高级向量参数"><v-expansion-panel-text>
                  <div class="intent-stack">
                    <EmbeddingProfileFields v-model="embeddingProfile" hide-timeout compact />
                    <div class="intent-fields">
                      <v-text-field v-model.number="draft.threshold" type="number" min="0" max="1" step="0.01" label="语义命中阈值" />
                      <v-text-field v-model.number="draft.switchMargin" type="number" min="0" max="1" step="0.01" label="最小相似度差值" />
                    </div>
                    <div><v-btn variant="tonal" :loading="indexing" :disabled="saving || indexing || !!vectorReason" @click="buildIndex">增量构建并发布索引</v-btn></div>
                    <p class="text-caption text-medium-emphasis">模型或前缀变化后需重新构建索引。</p>
                  </div>
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel title="手动安装"><v-expansion-panel-text><LocalEmbeddingSettings manual-only @status="updateLocalStatus" @busy="localBusy = $event" /></v-expansion-panel-text></v-expansion-panel>
              </v-expansion-panels>
            </div>
          </v-expansion-panel-text>
        </v-expansion-panel>
        <v-expansion-panel title="最近路由与任务反馈" @group:selected="loadEvents"><v-expansion-panel-text>
        <section class="intent-stack">
          <div class="d-flex flex-wrap align-center justify-space-between ga-3"><h3 class="text-subtitle-1">最近路由与任务反馈</h3><v-btn variant="text" :loading="eventsLoading" @click="loadEvents">刷新记录</v-btn></div>
          <p class="text-body-2 text-medium-emphasis">记录实际选组和判断耗时；满意度由你评价，不会自动训练模型。</p>
          <EmptyState v-if="!events.length" variant="inline" icon="mdi-history" title="暂无路由记录" description="先保存并启用意图匹配，再发送新任务，这里会记录实际命中的分组与判断耗时。" />
          <div v-for="event in events" :key="event.requestId" class="intent-event">
            <div>{{ event.target.poolId ? event.decision.trace?.groups.find(group => group.poolId === event.target.poolId)?.name ?? poolName(event.target.poolId) : '未确认执行目标' }} · {{ event.target.model || '未记录模型' }} · {{ event.success ? '已交付' : event.status === 'pending' ? '已确认目标，未记录完成' : '未完成' }}</div>
            <div v-if="event.decision.task" class="text-body-2">{{ taskName(event.decision.task) }} · {{ tierName(event.decision.tier) }}</div>
            <div class="text-caption text-medium-emphasis">{{ event.decision.trace?.classified ? '已执行分类' : event.decision.trace ? '本轮未分类' : '旧记录未保存分类过程' }} · {{ event.decision.reason }}<template v-if="event.decision.trace?.classified"> · {{ event.decision.durationMs }} ms</template></div>
            <div class="text-caption text-medium-emphasis">{{ event.requestId }} · {{ formatDateTime(event.at) }}</div>
            <details class="intent-event-detail" @toggle="toggleEvent(event.requestId, $event)"><summary>查看输入与匹配过程</summary><IntentMatchTrace v-if="expandedEvents.has(event.requestId)" :decision="event.decision" :tasks="options.tasks ?? []" :target="event.target" /></details>
            <div v-if="event.success && !event.control && event.feedback === undefined" class="d-flex ga-2 mt-2">
              <v-btn size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, true)">效果满意</v-btn>
              <v-btn size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, false)">效果不满意</v-btn>
            </div>
            <span v-else-if="event.feedback !== undefined" class="text-caption">已记录：{{ event.feedback ? '满意' : '不满意' }}</span>
          </div>
        </section>
        </v-expansion-panel-text></v-expansion-panel>
      </v-expansion-panels>
    </v-form>
    <template #actions>
      <span v-if="vectorReason || classifierReason" class="text-caption text-error mr-auto" role="status">{{ vectorReason || classifierReason }}</span>
      <v-btn :disabled="saving || indexing || localBusy" @click="show = false">取消</v-btn>
      <v-btn color="primary" variant="flat" :loading="saving" :disabled="!draft || indexing || localBusy || !!vectorReason || !!classifierReason" @click="save">保存当前协议</v-btn>
    </template>
  </AppDialog>
  <AppDialog v-model="vectorGuide" title="先准备向量模型" size="md" :busy="localBusy">
    <div class="intent-stack">
      <p class="text-body-2">语义匹配需要本地模型就绪，或配置已有 Embeddings 渠道。</p>
      <LocalEmbeddingSettings @status="updateLocalStatus" @busy="localBusy = $event" />
      <v-btn v-if="localStatus?.ready" variant="tonal" @click="continueWithLocalEmbedding">使用本地模型并继续</v-btn>
      <v-divider />
      <v-btn v-if="options.embeddings.length" variant="tonal" @click="useRemoteEmbedding">使用已有 Embeddings 渠道</v-btn>
      <p v-else class="text-body-2 text-medium-emphasis">暂无可用的 Embeddings 渠道，请先添加并启用渠道、配置密钥。</p>
      <template v-if="draft && draft.embeddingSource === 'remote' && options.embeddings.length">
        <v-select v-model="draft.embeddingChannel" :items="options.embeddings" item-title="name" item-value="id" label="Embeddings 渠道" @update:model-value="selectEmbeddingChannel" />
        <v-text-field v-model="draft.embeddingModel" label="向量模型名称" />
        <v-btn variant="tonal" :disabled="!draft.embeddingChannel || !draft.embeddingModel.trim()" @click="continueWithRemoteEmbedding">选择渠道并继续</v-btn>
      </template>
    </div>
    <template #actions><v-btn :disabled="localBusy" @click="vectorGuide = false">{{ vectorReason ? '稍后配置' : '完成' }}</v-btn></template>
  </AppDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useChannelStore } from '@/features/channels/stores/channel'
import { useIntentStore } from '@/features/channels/stores/intent'
import { useToast } from '@/shared/composables/useToast'
import { intentApi, intentErrorText, intentPreviewFailure, type IntentConfig, type IntentDecision, type IntentEvent, type IntentGroupCapability, type IntentOptions, type IntentProtocol, type LocalStatus } from '@/features/channels/services/intent'
import type { Dataset, EmbeddingProfile } from '@/types'
import LocalEmbeddingSettings from '@/features/channels/components/routing/LocalEmbeddingSettings.vue'
import { datasetsApi } from '@/features/extensions/datasets'
import { DatasetPicker, EmbeddingProfileFields } from '@/features/extensions/datasets/ui'
import IntentGroupCapabilities from '@/features/channels/components/routing/IntentGroupCapabilities.vue'
import IntentMatchTrace from '@/features/channels/components/routing/IntentMatchTrace.vue'
import AppDialog from '@/shared/components/common/AppDialog.vue'
import EmptyState from '@/shared/components/common/EmptyState.vue'
import { formatDateTime } from '@/shared/utils/datetime'
import { ApiError } from '@/shared/services/http'

const props = defineProps<{ protocol: IntentProtocol }>()
const store = useIntentStore()
const channelStore = useChannelStore()
const { showToast } = useToast()
const show = ref(false)
const groupsSection = ref<HTMLElement>()
const vectorGuide = ref(false)
const datasetRefresh = ref(0)
const localStatus = ref<LocalStatus>()
const vectorChecking = ref(false)
const vectorLoadError = ref('')
const embeddingOptionsError = ref('')
const localBusy = ref(false)
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
const expandedEvents = ref(new Set<string>())
function toggleEvent(id: string, event: Event) {
  if ((event.currentTarget as HTMLElement).hasAttribute('open')) expandedEvents.value.add(id)
  else expandedEvents.value.delete(id)
}
const eventsLoading = ref(false)
const feedbackBusy = ref(false)
const saved = computed(() => store.configs[props.protocol])
const pools = computed(() => channelStore.channelPoolsMap[props.protocol])
const poolName = (id: string) => pools.value.find(pool => pool.id === id)?.name ?? (id || '沿用未命中策略')
const taskName = (id: string) => options.value.tasks?.find(task => task.id === id)?.name ?? id
const tierName = (tier: number) => tiers.find(item => item.value === tier)?.title ?? '未确定'
const groups = computed({ get: () => orderGroups(draft.value?.groups ?? []), set: value => { if (draft.value) draft.value.groups = orderGroups(value) } })
const usesEmbedding = computed(() => draft.value?.strategy === 'semantic' || draft.value?.strategy === 'auto' && draft.value.taskMatcher === 'semantic')
const embeddingProfile = computed<EmbeddingProfile>({
  get: () => ({ source: draft.value!.embeddingSource, channel: draft.value!.embeddingChannel, model: draft.value!.embeddingModel, modelVersion: draft.value!.embeddingVersion ?? '', queryPrefix: draft.value!.queryPrefix, examplePrefix: draft.value!.examplePrefix, timeoutMs: draft.value!.timeoutMs }),
  set: value => { if (draft.value) Object.assign(draft.value, { embeddingSource: value.source, embeddingChannel: value.channel, embeddingModel: value.model, embeddingVersion: value.modelVersion, queryPrefix: value.queryPrefix, examplePrefix: value.examplePrefix, timeoutMs: value.timeoutMs }) }
})
const needsClassifier = computed(() => ['auto', 'complexity', 'adaptive'].includes(draft.value?.strategy ?? ''))
const simpleMode = computed<'builtin' | 'semantic' | 'assistant' | 'custom'>({
  get() {
    if (!draft.value || draft.value.strategy !== 'auto') return 'custom'
    if (draft.value.taskMatcher === 'semantic') return 'semantic'
    return draft.value.classifier === 'heuristic' ? 'builtin' : 'assistant'
  },
  set(mode) {
    if (!draft.value || mode === 'custom') return
    draft.value.strategy = 'auto'
    if (mode === 'semantic') {
      draft.value.taskMatcher = 'semantic'
      draft.value.classifier = 'heuristic'
      return
    }
    draft.value.taskMatcher = 'builtin'
    if (mode === 'builtin') {
      draft.value.classifier = 'heuristic'
      return
    }
    if (draft.value.classifier === 'heuristic') selectClassifier('llm')
  }
})
const simpleModes = [
  { title: '自动识别', value: 'builtin', description: '使用内置规则，不额外调用模型。' },
  { title: '向量语义', value: 'semantic', description: '按已发布样本寻找语义相近的任务。' },
  { title: '辅助模型', value: 'assistant', description: '使用你选择的渠道判断任务类型。' }
]
const usesDecisions = computed(() => draft.value?.classifier === 'decisions' || draft.value?.classifier === 'systemone')
const classifierOptions = computed(() => options.value.classifiers
  .filter(option => usesDecisions.value ? option.kind === 'decisions' : option.kind !== 'decisions')
  .map(option => {
    // 旧配置保留四入口中的任一稳定 ID；页面仍只显示一个共享选项。
    const savedId = draft.value?.classifierChannel ?? ''
    const id = option.builtin && option.sharedKey === 'kilo' && /^builtin-kilo-(messages|responses|chat|gemini)$/.test(savedId) ? savedId : option.id
    return { ...option, id, title: option.builtin && option.sharedKey === 'kilo' ? 'Kilo 免费池（四个入口共享）' : `${option.name} · ${option.kind}` }
  }))
const classifierModels = computed(() => {
  const selected = classifierOptions.value.find(option => option.id === draft.value?.classifierChannel)
  return selected?.models ?? [...new Set(classifierOptions.value.flatMap(option => option.models))]
})
const configuredGroups = computed(() => groups.value.filter(group => group.tasks.length && group.tiers.length).length)
const vectorReason = computed(() => {
  if (!usesEmbedding.value || !draft.value) return ''
  if (vectorChecking.value && !localStatus.value) return '正在检查向量模型状态'
  if (draft.value.embeddingSource === 'local') {
    if (vectorLoadError.value) return vectorLoadError.value
    if (!localStatus.value?.ready) return '本地向量模型尚未就绪，请安装、启动并预热'
    if (draft.value.embeddingModel !== localStatus.value.manifest.modelName) return '请选择已安装的本地向量模型'
  } else {
    if (embeddingOptionsError.value) return embeddingOptionsError.value
    if (!options.value.embeddings.some(channel => channel.id === draft.value?.embeddingChannel) || !draft.value.embeddingModel.trim()) return '请选择可用的 Embeddings 渠道和向量模型'
  }
  return ''
})
const classifierReason = computed(() => {
  if (!draft.value?.enabled || !needsClassifier.value || draft.value.classifier === 'heuristic') return ''
  if (!classifierOptions.value.length) return usesDecisions.value ? '没有可用的 Decisions 渠道' : '没有可用的辅助判断渠道'
  return draft.value.classifierModel.trim() ? '' : '请选择辅助判断模型'
})
function selectClassifierChannel(id: string | null) {
  if (!draft.value) return
  draft.value.classifierChannel = id ?? ''
  const option = classifierOptions.value.find(channel => channel.id === id)
  if (option) draft.value.classifierModel = option.defaultModel
}
function selectClassifier(classifier: IntentConfig['classifier']) {
  if (!draft.value || draft.value.classifier === classifier) return
  const previousUsesDecisions = usesDecisions.value
  draft.value.classifier = classifier
  if (previousUsesDecisions !== usesDecisions.value) {
    draft.value.classifierChannel = ''
    draft.value.classifierModel = ''
  }
  const [onlyChannel] = classifierOptions.value
  if (classifier !== 'heuristic' && !draft.value.classifierChannel && !draft.value.classifierModel && classifierOptions.value.length === 1 && onlyChannel) {
    selectClassifierChannel(onlyChannel.id)
  }
}
function selectEmbeddingChannel(id: string) {
  if (!draft.value) return
  const option = options.value.embeddings.find(channel => channel.id === id)
  draft.value.embeddingModel = option?.defaultModel ?? ''
}
function useRemoteEmbedding() {
  if (!draft.value) return
  draft.value.embeddingSource = 'remote'
  if (options.value.embeddings.length === 1) {
    draft.value.embeddingChannel = options.value.embeddings[0]!.id
    selectEmbeddingChannel(draft.value.embeddingChannel)
  }
}
function updateLocalStatus(status: LocalStatus) {
  localStatus.value = status
  vectorLoadError.value = ''
}
function useLocalEmbedding() {
  if (!draft.value || !localStatus.value?.ready) return
  draft.value.embeddingSource = 'local'
  draft.value.embeddingChannel = ''
  draft.value.embeddingModel = localStatus.value.manifest.modelName
  draft.value.embeddingVersion = ''
  draft.value.queryPrefix = ''
  draft.value.examplePrefix = ''
}
function usePublishedEmbedding(profile?: EmbeddingProfile) {
  if (!draft.value || !profile) return
  embeddingProfile.value = { ...profile }
}
async function startSemanticSetup() {
  if (vectorReason.value) {
    vectorGuide.value = true
    await checkVector(true)
    return
  }
  await prepareSemanticIndex()
}
async function continueWithLocalEmbedding() {
  useLocalEmbedding()
  vectorGuide.value = false
  await prepareSemanticIndex()
}
async function continueWithRemoteEmbedding() {
  if (!draft.value) return
  draft.value.embeddingSource = 'remote'
  vectorGuide.value = false
  await prepareSemanticIndex()
}
async function prepareSemanticIndex() {
  if (!draft.value || indexing.value || vectorReason.value) return
  indexing.value = true
  error.value = ''
  try {
    if (!draft.value.datasetIds?.length) {
      const existing = (await datasetsApi.list()).find(item => item.id === 'starter-intents-v2' && item.kind === 'intent')
      if (existing) {
        const published = existing.indexes[existing.indexes.length - 1]
        if (published) {
          draft.value.datasetIds = [existing.id]
          usePublishedEmbedding(published.profile)
          datasetRefresh.value++
          showToast('已选用现有内置样本索引，请保存路由配置', 'success')
          return
        }
        if (!existing.approved) throw new Error('检测到尚未审核的内置数据集。请到数据集页面审核样本后再构建索引；现有内容未被覆盖。')
        draft.value.datasetIds = [existing.id]
      } else {
        const builtins = await datasetsApi.builtin()
        const source = builtins.find(item => item.id === 'starter-intents-v2')
        if (!source) throw new Error('内置意图样本不可用，请在数据集页面导入起步样本。')
        const accepted: Dataset = {
          ...source,
          samples: source.samples.map(sample => ({ ...sample, approved: sample.split === 'index' }))
        }
        const savedDataset = await datasetsApi.save(accepted)
        draft.value.datasetIds = [savedDataset.id]
      }
    }
    await intentApi.buildIndex(props.protocol, draft.value)
    datasetRefresh.value++
    showToast('意图样本索引已发布，请保存路由配置', 'success')
  } catch (cause) { report(cause) }
  finally { indexing.value = false }
}
async function checkVector(refreshChannels = false) {
  if (vectorChecking.value) return
  vectorChecking.value = true
  const results = await Promise.allSettled([intentApi.local(), ...(refreshChannels ? [intentApi.options()] : [])])
  const local = results[0]!
  if (local.status === 'fulfilled') { localStatus.value = local.value as LocalStatus; vectorLoadError.value = '' }
  else vectorLoadError.value = intentErrorText(local.reason)
  if (refreshChannels) {
    const channels = results[1]!
    if (channels.status === 'fulfilled') { options.value = channels.value as IntentOptions; embeddingOptionsError.value = '' }
    else embeddingOptionsError.value = intentErrorText(channels.reason)
  }
  vectorChecking.value = false
}

const strategies = [{ title: '自动综合匹配（推荐）', value: 'auto' }, { title: '语义相似度', value: 'semantic' }, { title: '仅按难度分级', value: 'complexity' }, { title: '按难度与反馈自适应选择', value: 'adaptive' }, { title: '手动正则匹配', value: 'regex' }]
const assistantClassifiers = [{ title: '普通 LLM', value: 'llm' }, { title: 'Decisions 决策模型', value: 'decisions' }, { title: '混合 · 明确信号用规则，其余交 LLM', value: 'hybrid' }]
const classifiers = [{ title: '内置规则 · 不调用额外模型', value: 'heuristic' }, ...assistantClassifiers]
const tiers = [{ title: '简单', value: 0 }, { title: '常规', value: 1 }, { title: '复杂', value: 2 }]
const strategyTitle = (strategy: IntentConfig['strategy']) => strategies.find(item => item.value === strategy)?.title ?? strategy
const lines = (text: string | null) => (text ?? '').split('\n').map(line => line.trim()).filter(Boolean)
const report = (cause: unknown) => {
  error.value = intentErrorText(cause)
  if (show.value && cause instanceof ApiError && cause.code === 'VECTOR_MODEL_REQUIRED') {
    vectorGuide.value = true
    void checkVector(true)
  }
}

function orderGroups(value: IntentGroupCapability[]) {
  const order = new Map(pools.value.map((pool, index) => [pool.id, index]))
  return [...value].sort((a, b) => (order.get(a.poolId) ?? pools.value.length) - (order.get(b.poolId) ?? pools.value.length))
}
function validateGroups() {
  if (draft.value?.strategy !== 'auto') return true
  const incomplete = groups.value.find(group => !group.tasks.length || !group.tiers.length)
  if (!incomplete) return true
  error.value = `请为“${poolName(incomplete.poolId)}”同时选择任务和难度，或将两项清空。`
  groupsSection.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
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
  vectorGuide.value = false
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
    if (copy.classifier === 'systemone') copy.classifier = 'decisions'
    draft.value = copy
    options.value = choices
    await checkVector()
  } catch (cause) { if (protocol === props.protocol && show.value) report(cause) }
}
function addRoute() {
  draft.value?.routes.push({ id: 'task_' + Date.now(), poolId: '', description: '', patterns: [], examples: [], tier: 1, cost: 0 })
}
async function save() {
  if (!draft.value || saving.value || indexing.value || vectorReason.value || classifierReason.value || !validateGroups()) return
  saving.value = true
  error.value = ''
  try { await store.save(props.protocol, { ...draft.value, groups: groups.value }); showToast(props.protocol + ' 意图匹配配置已保存', 'success'); show.value = false }
  catch (cause) { report(cause) }
  finally { saving.value = false }
}
async function preview() {
  if (!draft.value || previewing.value || vectorReason.value || classifierReason.value || !validateGroups()) return
  const snapshot = JSON.stringify(draft.value)
  const text = previewText.value
  previewing.value = true
  error.value = ''
  decision.value = undefined
  try {
    const result = await intentApi.preview(props.protocol, { ...draft.value, groups: groups.value }, text)
    if (show.value && snapshot === JSON.stringify(draft.value) && text === previewText.value) decision.value = result
  } catch (cause) {
    if (snapshot === JSON.stringify(draft.value) && text === previewText.value) {
      decision.value = intentPreviewFailure(cause)
      report(cause)
    }
  }
  finally { previewing.value = false }
}
async function buildIndex() {
  if (!draft.value || indexing.value || saving.value || vectorReason.value) return
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
let statusTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { statusTimer = setInterval(() => { if (show.value && usesEmbedding.value) void checkVector() }, 3000) })
onUnmounted(() => { clearInterval(statusTimer) })
watch(usesEmbedding, async needed => {
  if (!needed || !show.value) return
  await checkVector(true)
})
watch(show, visible => { if (!visible) vectorGuide.value = false })
watch(vectorGuide, visible => { if (visible) void checkVector(true) })
watch(() => props.protocol, () => { show.value = false; draft.value = undefined; events.value = []; expandedEvents.value.clear(); void load() }, { immediate: true })
watch([draft, previewText], () => { decision.value = undefined }, { deep: true })
</script>

<style scoped>
.intent-stack { display: grid; gap: 22px; }
.intent-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 22px 20px; }
.intent-strategies { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.intent-strategy { padding: 8px 12px; border: 1px solid rgba(var(--v-theme-outline), 0.25); border-radius: 8px; }
.intent-strategy-disabled { opacity: 0.55; }
.intent-event-detail { min-width: 0; }
.intent-event-detail > summary { cursor: pointer; padding: 12px 0; min-height: 44px; }
.intent-event { display: grid; gap: 6px; padding: 16px 0; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.2); overflow-wrap: anywhere; }
@media (max-width: 650px) { .intent-fields, .intent-strategies { grid-template-columns: minmax(0, 1fr); } }
</style>
