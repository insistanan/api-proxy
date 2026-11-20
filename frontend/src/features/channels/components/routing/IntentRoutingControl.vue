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
            <p class="text-body-2 text-medium-emphasis mb-0">选择识别方式，再设置适合各类任务的分组。</p>
          </div>
          <v-chip v-if="simpleMode === 'builtin'" color="primary" variant="tonal">推荐</v-chip>
        </div>
        <v-radio-group v-model="simpleMode" hide-details>
          <div class="intent-strategies">
            <div v-for="mode in simpleModes" :key="mode.value" class="intent-strategy" :class="{ 'intent-strategy-selected': simpleMode === mode.value }" role="radio" :aria-checked="simpleMode === mode.value" :tabindex="saving || indexing ? -1 : 0" @click="chooseMode(mode.value)" @keydown.enter.prevent="chooseMode(mode.value)" @keydown.space.prevent="chooseMode(mode.value)">
              <v-radio :label="mode.title" :value="mode.value" />
              <p class="text-caption text-medium-emphasis ml-8 mb-0">{{ mode.description }}</p>
            </div>
          </div>
        </v-radio-group>
        <template v-if="simpleMode === 'hybrid'">
          <v-select :model-value="hybridCombination" :items="hybridCombinations" label="混合组合" @update:model-value="selectHybridCombination" />
          <p class="text-body-2 text-medium-emphasis">固定顺序：{{ recognitionStages.map(stage => stageTitles[stage]).join(' → ') }}。可靠识别后停止；未识别才继续，调用失败按下方异常处理执行。</p>
        </template>
        <template v-if="usesEmbedding">
          <v-select :model-value="draft.embeddingSource" :items="[{ title: '一键下载本地模型', value: 'local' }, { title: '使用已配置的 Embeddings 渠道', value: 'remote' }]" label="向量模型来源" @update:model-value="selectEmbeddingSource" />
          <template v-if="draft.embeddingSource === 'local'">
            <LocalEmbeddingSettings @status="updateLocalStatus" @busy="localBusy = $event" />
            <v-btn v-if="localStatus?.ready && draft.embeddingModel !== localStatus.manifest.modelName" variant="tonal" @click="useLocalEmbedding">使用已安装的模型 {{ localStatus.manifest.modelName }}</v-btn>
            <p v-if="draft.embeddingModel" class="text-body-2">当前向量模型：{{ draft.embeddingModel }}</p>
          </template>
          <ChannelPicker v-else v-model="draft.embeddingChannel" v-model:selected-model="draft.embeddingModel" :options="embeddingOptions" show-model model-label="向量模型" :disabled="saving || indexing" />
          <div class="intent-inline-status">
            <span class="text-body-2" :class="vectorReason ? 'text-warning' : 'text-primary'">{{ vectorReason || '向量模型已就绪' }}</span>
          </div>
          <DatasetPicker v-model="draft.datasetIds" kind="intent" :multiple="false" :profile="embeddingProfile" :refresh-key="datasetRefresh" />
          <v-alert v-if="indexError" type="warning" variant="tonal">{{ indexError }}</v-alert>
          <p class="text-body-2 text-medium-emphasis">数据集和请求使用同一套向量配置。更换本地模型、渠道、渠道映射后的模型、版本或前缀后，须重新计算并发布索引；旧模型索引仍保留。</p>
          <p class="text-caption text-medium-emphasis mb-0">没有数据集时，可直接采用内置合成样本；仅索引样本会被确认并发布。</p>
          <v-btn variant="tonal" :loading="indexing" :disabled="indexing || localBusy || !!vectorReason" @click="prepareSemanticIndex">{{ draft.datasetIds?.length ? '按当前模型重新构建并发布索引' : '采用内置样本并构建索引' }}</v-btn>
        </template>
        <template v-if="usesAssistant">
          <v-select :model-value="draft.classifier" :items="assistantClassifiers" label="判断类型" @update:model-value="selectClassifier" />
          <v-alert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</v-alert>
          <template v-else>
            <ChannelPicker v-model="draft.classifierChannel" v-model:selected-model="draft.classifierModel" :options="classifierOptions" label="判断渠道" show-model model-label="判断模型" :disabled="saving || indexing" />
          </template>
        </template>
        <div v-if="simpleMode === 'custom'" class="d-flex flex-wrap align-center ga-3">
          <span class="text-body-2 text-medium-emphasis">当前使用自定义模式：{{ strategyTitle(draft.strategy) }}</span>
          <v-btn variant="tonal" size="small" @click="simpleMode = 'builtin'">改用内置规则与分组能力</v-btn>
        </div>
        <p class="text-body-2 text-medium-emphasis">意图分流命中时优先选择目标分组，代替原来的模型分组选组；客户端请求模型仍用于渠道内模型映射。人工固定、会话驻留和工具续轮保护优先。</p>
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
        <v-combobox v-model="previewModel" :items="requestModels" :return-object="false" label="模拟客户端请求模型" hint="用于展示原分组与意图分组下的模型映射，不是辅助判断模型。" persistent-hint clearable @update:model-value="previewModel ||= ''" />
        <v-textarea v-model="previewText" label="模拟用户任务" rows="3" placeholder="例如：排查并修复 Go 服务中的并发死锁" hint="只预览当前草稿；远程判断和向量匹配会调用辅助模型。" />
        <div><v-btn variant="tonal" :loading="previewing" :disabled="!previewText.trim() || !!vectorReason || !!classifierReason || !!indexError || indexChecking" @click="preview">测试匹配</v-btn></div>
        <v-alert v-if="decision?.routing" type="info" variant="tonal">
          请求模型：{{ decision.routing.requestedModel }}；原路由分组：{{ decision.routing.originalPools.join('、') || '无' }}；本次预览分组：{{ decision.routing.selectedPools.join('、') || '无' }}。
          <div v-for="(candidate, index) in decision.routing.candidates" :key="index">{{ candidate.channel }} → {{ candidate.model }}</div>
          <div class="text-caption">这里只展示配置中的候选，不调用任务模型，也不模拟实际会话固定、健康检查及故障转移。</div>
        </v-alert>
        <p v-if="decision?.trace?.stages?.length" class="text-body-2">{{ decision.trace.stages.map(stage => `${stageTitles[stage.stage] ?? stage.stage}：${stage.matched ? '已识别' : '未可靠识别'}`).join(' → ') }}</p>
        <IntentMatchTrace v-if="decision" :decision="decision" :tasks="options.tasks ?? []" preview />
      </section>
      <v-expansion-panels variant="accordion">
        <v-expansion-panel title="路由保护与异常处理（可选）">
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
              <v-select v-if="simpleMode === 'custom' && draft.strategy === 'auto'" v-model="draft.taskMatcher" :items="[{ title: '内置规则', value: 'builtin' }, { title: '向量数据集', value: 'semantic' }]" label="旧配置任务识别来源" />
              <v-select v-if="needsClassifier && simpleMode === 'custom'" :model-value="draft.classifier" :items="classifiers" label="旧配置判断方式" @update:model-value="selectClassifier" />
              <template v-if="needsClassifier && draft.classifier !== 'heuristic' && simpleMode === 'custom'">
                <v-alert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</v-alert>
                <template v-else>
                  <ChannelPicker v-model="draft.classifierChannel" v-model:selected-model="draft.classifierModel" :options="classifierOptions" label="判断渠道" show-model model-label="判断模型" />
                </template>
              </template>
              <v-expansion-panels variant="accordion">
                <v-expansion-panel title="会话稳定性"><v-expansion-panel-text>
                  <div class="intent-fields">
                    <v-text-field v-model.number="draft.minResidenceSeconds" type="number" min="30" label="最短驻留时间（秒）" hint="新任务至少在原组停留多久，避免反复切换；默认 300 秒。" persistent-hint />
                    <v-text-field v-model.number="draft.minTurns" type="number" min="1" label="切换前最少用户轮次" hint="至少完成多少轮后才重新判断分组；默认 4 轮。工具续轮始终保护原来源。" persistent-hint />
                  </div>
                  <v-switch v-if="draft.strategy === 'auto'" v-model="draft.useFeedback" label="同能力分组参考反馈与相对成本" hide-details />
                  <v-switch v-if="draft.strategy === 'adaptive'" v-model="draft.observeOnly" label="自适应选组仅观察建议" hide-details />
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel title="未命中与失败"><v-expansion-panel-text>
                  <div class="intent-stack">
                    <v-select v-model="draft.unmatched" :items="[{ title: '沿用客户端模型的原路由', value: 'original' }, { title: '返回未命中错误', value: 'error' }, { title: '使用指定分组', value: 'fallback' }]" label="识别不出来时" hint="所有已选识别方式都未可靠命中时执行。" persistent-hint />
                    <v-select v-if="draft.unmatched === 'fallback'" v-model="draft.fallbackPoolId" :items="pools" item-title="name" item-value="id" label="未命中目标分组" />
                    <v-select v-model="draft.failurePolicy" :items="[{ title: '沿用原路由并记录失败', value: 'keep' }, { title: '返回明确错误', value: 'error' }]" label="辅助判断失败或超时" />
                    <v-text-field v-model.number="draft.timeoutMs" type="number" min="100" max="30000" label="识别总超时（毫秒）" hint="混合模式共用这个时间预算，超时视为调用失败。" persistent-hint />
                  </div>
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel v-if="usesEmbedding" title="高级向量参数"><v-expansion-panel-text>
                  <div class="intent-stack">
                    <EmbeddingProfileFields v-model="embeddingProfile" hide-timeout hide-source />
                    <div class="intent-fields">
                      <v-text-field v-model.number="draft.threshold" type="number" min="0" max="1" step="0.01" label="语义命中阈值" hint="相似度低于此值不采纳；默认 0.75。" persistent-hint />
                      <v-text-field v-model.number="draft.switchMargin" type="number" min="0" max="1" step="0.01" label="最小相似度差值" hint="最佳与次佳类别太接近时不采纳；默认 0.15。" persistent-hint />
                    </div>
                    <div v-if="simpleMode === 'custom'"><v-btn variant="tonal" :loading="indexing" :disabled="saving || indexing || !!vectorReason" @click="buildIndex">增量构建并发布索引</v-btn></div>
                    <p class="text-caption text-medium-emphasis">模型或前缀变化后需重新构建索引。</p>
                  </div>
                </v-expansion-panel-text></v-expansion-panel>
                <v-expansion-panel v-if="simpleMode === 'custom'" title="旧配置手动安装"><v-expansion-panel-text><LocalEmbeddingSettings manual-only @status="updateLocalStatus" @busy="localBusy = $event" /></v-expansion-panel-text></v-expansion-panel>
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
      <span v-if="vectorReason || classifierReason || indexError" class="text-caption text-error mr-auto" role="status">{{ vectorReason || classifierReason || indexError }}</span>
      <v-btn :disabled="saving || indexing || localBusy" @click="show = false">取消</v-btn>
      <v-btn color="primary" variant="flat" :loading="saving" :disabled="!draft || indexing || localBusy || (draft.enabled && (!!vectorReason || !!classifierReason || !!indexError || indexChecking))" @click="save">保存当前协议</v-btn>
    </template>
  </AppDialog>
  <AppDialog v-if="simpleMode === 'custom'" v-model="vectorGuide" title="先准备向量模型" size="md" :busy="localBusy">
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
import ChannelPicker from '@/shared/components/common/ChannelPicker.vue'
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
const previewModel = ref('')
const indexError = ref('')
const indexChecking = ref(false)
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
const recognitionStages = computed(() => draft.value?.recognitionMode === 'hybrid' ? draft.value.hybridMatchers ?? [] : draft.value?.recognitionMode ? [draft.value.recognitionMode] : [])
const usesEmbedding = computed(() => draft.value?.recognitionMode ? recognitionStages.value.includes('semantic') : draft.value?.strategy === 'semantic' || draft.value?.strategy === 'auto' && draft.value.taskMatcher === 'semantic')
const usesAssistant = computed(() => recognitionStages.value.includes('assistant'))
const embeddingOptions = computed(() => options.value.embeddings.map(option => ({ ...option, kind: 'embeddings' })))
const requestModels = computed(() => [...new Set(pools.value.map(pool => pool.modelMatcher).filter(matcher => matcher && !/[?*[]/.test(matcher)))])
const embeddingProfile = computed<EmbeddingProfile>({
  get: () => ({ source: draft.value!.embeddingSource, channel: draft.value!.embeddingChannel, model: draft.value!.embeddingModel, modelVersion: draft.value!.embeddingVersion ?? '', queryPrefix: draft.value!.queryPrefix, examplePrefix: draft.value!.examplePrefix, timeoutMs: draft.value!.timeoutMs }),
  set: value => { if (draft.value) Object.assign(draft.value, { embeddingSource: value.source, embeddingChannel: value.channel, embeddingModel: value.model, embeddingVersion: value.modelVersion, queryPrefix: value.queryPrefix, examplePrefix: value.examplePrefix, timeoutMs: value.timeoutMs }) }
})
const needsClassifier = computed(() => ['auto', 'complexity', 'adaptive'].includes(draft.value?.strategy ?? ''))
type RecognitionMode = NonNullable<IntentConfig['recognitionMode']>
const simpleMode = computed<RecognitionMode | 'custom'>({
  get() {
    if (!draft.value || draft.value.strategy !== 'auto') return 'custom'
    if (draft.value.recognitionMode) return draft.value.recognitionMode
    return 'custom'
  },
  set(mode) {
    if (!draft.value || mode === 'custom') return
    draft.value.strategy = 'auto'
    draft.value.recognitionMode = mode
    draft.value.hybridMatchers = mode === 'hybrid' ? ['builtin', 'semantic'] : []
    syncRecognition()
  }
})
const simpleModes = [
  { title: '内置规则', value: 'builtin', description: '默认方式：使用内置任务规则，不调用模型。' },
  { title: '向量匹配', value: 'semantic', description: '用同一向量模型比较请求与数据集样本。' },
  { title: '辅助模型', value: 'assistant', description: '使用已配置的 LLM 或 Decisions 渠道判断任务。' },
  { title: '混合模式', value: 'hybrid', description: '按固定顺序尝试选定方式，可靠识别后停止。' }
] satisfies { title: string; value: RecognitionMode; description: string }[]
const stageTitles: Record<string, string> = { builtin: '内置规则', semantic: '向量匹配', assistant: '辅助模型' }
const hybridCombinations = [
  { title: '内置规则 + 向量匹配', value: 'builtin,semantic' },
  { title: '内置规则 + 辅助模型', value: 'builtin,assistant' },
  { title: '内置规则 + 向量匹配 + 辅助模型', value: 'builtin,semantic,assistant' },
  { title: '向量匹配 + 辅助模型', value: 'semantic,assistant' }
]
const hybridCombination = computed(() => draft.value?.hybridMatchers?.join(',') ?? '')
function chooseMode(mode: RecognitionMode) { if (!saving.value && !indexing.value && mode !== simpleMode.value) simpleMode.value = mode }
function syncRecognition() {
  if (!draft.value) return
  draft.value.taskMatcher = recognitionStages.value.includes('semantic') ? 'semantic' : 'builtin'
  if (usesAssistant.value) {
    if (!['llm', 'decisions'].includes(draft.value.classifier)) selectClassifier('llm')
  } else draft.value.classifier = 'heuristic'
}
function selectHybridCombination(value: string) {
  if (!draft.value || !hybridCombinations.some(item => item.value === value)) return
  draft.value.hybridMatchers = value.split(',') as NonNullable<IntentConfig['hybridMatchers']>
  syncRecognition()
}
function selectEmbeddingSource(source: 'local' | 'remote') {
  if (!draft.value) return
  draft.value.embeddingSource = source
  draft.value.embeddingChannel = ''
  draft.value.embeddingModel = ''
  draft.value.embeddingVersion = ''
  if (source === 'local' && localStatus.value?.ready) useLocalEmbedding()
}
const usesDecisions = computed(() => draft.value?.classifier === 'decisions' || draft.value?.classifier === 'systemone')
const classifierOptions = computed(() => options.value.classifiers
  .filter(option => usesDecisions.value ? option.kind === 'decisions' : option.kind !== 'decisions')
  .map(option => {
    // 旧配置保留四入口中的任一稳定 ID；页面仍只显示一个共享选项。
    const savedId = draft.value?.classifierChannel ?? ''
    const id = option.builtin && option.sharedKey === 'kilo' && /^builtin-kilo-(messages|responses|chat|gemini)$/.test(savedId) ? savedId : option.id
    const kind = id.startsWith('builtin-kilo-') ? id.slice('builtin-kilo-'.length) : option.kind
    return { ...option, id, kind, name: option.builtin && option.sharedKey === 'kilo' ? 'Kilo 免费池（四个入口共享）' : option.name }
  }))
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
  if (!draft.value) return ''
  if (draft.value.recognitionMode ? !usesAssistant.value : !needsClassifier.value || draft.value.classifier === 'heuristic') return ''
  if (!classifierOptions.value.length) return usesDecisions.value ? '没有可用的 Decisions 渠道' : '没有可用的辅助判断渠道'
  if (!classifierOptions.value.some(option => option.id === draft.value?.classifierChannel)) return '请选择辅助判断渠道'
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
    await checkIndex()
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
const assistantClassifiers = [{ title: 'LLM 大模型', value: 'llm' }, { title: 'Decisions 决策模型', value: 'decisions' }]
const classifiers = [{ title: '内置规则 · 不调用额外模型', value: 'heuristic' }, ...assistantClassifiers, { title: '旧版规则 + LLM', value: 'hybrid' }]
const tiers = [{ title: '简单', value: 0 }, { title: '常规', value: 1 }, { title: '复杂', value: 2 }]
const strategyTitle = (strategy: IntentConfig['strategy']) => strategies.find(item => item.value === strategy)?.title ?? strategy
const lines = (text: string | null) => (text ?? '').split('\n').map(line => line.trim()).filter(Boolean)
const report = (cause: unknown) => {
  error.value = intentErrorText(cause)
  if (show.value && simpleMode.value === 'custom' && cause instanceof ApiError && cause.code === 'VECTOR_MODEL_REQUIRED') {
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
    if (!copy.enabled && copy.strategy === 'regex' && !copy.recognitionMode && !copy.routes.length && !copy.groups?.length && copy.classifier === 'heuristic') {
      copy.strategy = 'auto'
      copy.recognitionMode = 'builtin'
      copy.hybridMatchers = []
      copy.classifier = 'heuristic'
    }
    copy.groups ??= []
    copy.taskMatcher ??= 'builtin'
    if (copy.classifier === 'systemone') copy.classifier = 'decisions'
    if (copy.strategy === 'auto' && !copy.recognitionMode) {
      if (copy.classifier === 'heuristic') copy.recognitionMode = copy.taskMatcher === 'semantic' ? 'semantic' : 'builtin'
      else if (copy.taskMatcher === 'builtin') {
        copy.recognitionMode = copy.classifier === 'hybrid' ? 'hybrid' : 'assistant'
        if (copy.classifier === 'hybrid') { copy.hybridMatchers = ['builtin', 'assistant']; copy.classifier = 'llm' }
      }
    }
    draft.value = copy
    options.value = choices
    await checkVector()
  } catch (cause) { if (protocol === props.protocol && show.value) report(cause) }
}
function addRoute() {
  draft.value?.routes.push({ id: 'task_' + Date.now(), poolId: '', description: '', patterns: [], examples: [], tier: 1, cost: 0 })
}
async function save() {
  if (!draft.value || saving.value || indexing.value || (draft.value.enabled && (vectorReason.value || classifierReason.value || indexError.value || indexChecking.value)) || !validateGroups()) return
  saving.value = true
  error.value = ''
  try { await store.save(props.protocol, { ...draft.value, groups: groups.value }); showToast(props.protocol + ' 意图匹配配置已保存', 'success'); show.value = false }
  catch (cause) { report(cause) }
  finally { saving.value = false }
}
async function preview() {
  if (!draft.value || previewing.value || vectorReason.value || classifierReason.value || indexError.value || indexChecking.value || !validateGroups()) return
  const snapshot = JSON.stringify(draft.value)
  const text = previewText.value
  const model = previewModel.value
  previewing.value = true
  error.value = ''
  decision.value = undefined
  try {
    const result = await intentApi.preview(props.protocol, { ...draft.value, groups: groups.value }, text, model)
    if (show.value && snapshot === JSON.stringify(draft.value) && text === previewText.value && model === previewModel.value) decision.value = result
  } catch (cause) {
    if (snapshot === JSON.stringify(draft.value) && text === previewText.value && model === previewModel.value) {
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
  try { await intentApi.buildIndex(props.protocol, draft.value); datasetRefresh.value++; await checkIndex(); showToast('数据集索引已构建并发布', 'success') }
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
let indexRequest = 0
async function checkIndex() {
  const request = ++indexRequest
  if (!draft.value || !usesEmbedding.value || !show.value) { indexError.value = ''; indexChecking.value = false; return }
  if (!draft.value.datasetIds?.length) { indexError.value = '请选用数据集，或采用内置样本并构建索引'; indexChecking.value = false; return }
  if (vectorReason.value) { indexError.value = ''; indexChecking.value = false; return }
  indexChecking.value = true
  try { await intentApi.indexStatus(props.protocol, draft.value); if (request === indexRequest) indexError.value = '' }
  catch (cause) { if (request === indexRequest) indexError.value = intentErrorText(cause) }
  finally { if (request === indexRequest) indexChecking.value = false }
}
onMounted(() => { statusTimer = setInterval(() => { if (show.value && usesEmbedding.value) void checkVector() }, 3000) })
onUnmounted(() => { clearInterval(statusTimer) })
watch(usesEmbedding, async needed => {
  if (!needed || !show.value) return
  await checkVector(true)
})
watch(show, visible => { if (!visible) vectorGuide.value = false })
watch(vectorGuide, visible => { if (visible) void checkVector(true) })
watch(() => props.protocol, () => { show.value = false; draft.value = undefined; events.value = []; expandedEvents.value.clear(); void load() }, { immediate: true })
watch([draft, previewText, previewModel], () => { decision.value = undefined }, { deep: true })
watch([() => draft.value ? embeddingProfile.value : undefined, () => draft.value?.datasetIds, usesEmbedding, vectorReason], () => { void checkIndex() }, { deep: true })
</script>

<style scoped>
.intent-stack { display: grid; gap: 22px; }
.intent-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 22px 20px; }
.intent-strategies { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.intent-strategy { padding: 8px 12px; border: 1px solid rgba(var(--v-theme-outline), 0.25); border-radius: 8px; }
.intent-strategy { cursor: pointer; }
.intent-strategy-selected, .intent-strategy:focus-visible { border-color: rgb(var(--v-theme-primary)); background: rgba(var(--v-theme-primary), 0.08); outline: none; }
.intent-strategy-disabled { opacity: 0.55; }
.intent-event-detail { min-width: 0; }
.intent-event-detail > summary { cursor: pointer; padding: 12px 0; min-height: 44px; }
.intent-event { display: grid; gap: 6px; padding: 16px 0; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.2); overflow-wrap: anywhere; }
@media (max-width: 650px) { .intent-fields, .intent-strategies { grid-template-columns: minmax(0, 1fr); } }
</style>
