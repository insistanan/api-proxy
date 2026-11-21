<template>
  <UiButton :variant="saved?.enabled ? 'tonal' : 'outlined'" :color="saved?.enabled ? 'primary' : undefined" :loading="loading" :aria-pressed="!!saved?.enabled" aria-label="配置当前协议的意图匹配" @click="open">
    <UiIcon start size="18">mdi-server-network</UiIcon>
    意图匹配 <span class="ml-2 text-xs">{{ saved ? saved.enabled ? '开' : '关' : '未知' }}</span>
  </UiButton>
  <AppDialog v-model="show" :title="protocol + ' · 意图匹配'" size="lg" :busy="saving || indexing || localBusy">
    <UiAlert v-if="error" type="error" variant="tonal" class="mb-5">{{ error }}</UiAlert>
    <UiForm v-if="draft" :disabled="saving || indexing" @submit.prevent>
      <div class="intent-layout">
        <div class="intent-enable">
          <span class="text-sm font-medium">意图分流</span>
          <UiSwitch v-model="draft.enabled" color="primary" aria-label="启用意图分流" :label="draft.enabled ? '已启用' : '未启用'" />
        </div>
        <section class="intent-stack" aria-labelledby="intent-step-routing">
          <div class="intent-section-heading">
            <h3 id="intent-step-routing">识别方式</h3>
          </div>
          <div class="intent-strategies" role="radiogroup" aria-labelledby="intent-step-routing">
            <label v-for="mode in simpleModes" :key="mode.value" class="intent-strategy" :class="{ 'intent-strategy-selected': simpleMode === mode.value }">
              <input type="radio" :name="modeGroupId" :value="mode.value" :checked="simpleMode === mode.value" :disabled="saving || indexing" class="size-4 shrink-0 accent-primary" @change="chooseMode(mode.value)" />
              <span>{{ mode.title }}</span>
            </label>
          </div>
          <div v-if="simpleMode === 'hybrid' || usesEmbedding || usesAssistant || simpleMode === 'custom'" class="intent-model-settings">
            <template v-if="simpleMode === 'hybrid'">
              <UiSelect :model-value="hybridCombination" :items="hybridCombinations" label="混合组合" @update:model-value="selectHybridCombination" />
            </template>
            <template v-if="usesEmbedding">
              <UiSelect :model-value="draft.embeddingSource" :items="[{ title: '一键下载本地模型', value: 'local' }, { title: '使用已配置的 Embeddings 渠道', value: 'remote' }]" label="向量模型来源" @update:model-value="selectEmbeddingSource" />
              <template v-if="draft.embeddingSource === 'local'">
                <LocalEmbeddingSettings @status="updateLocalStatus" @busy="localBusy = $event" />
                <UiButton v-if="localStatus?.ready && draft.embeddingModel !== localStatus.manifest.modelName" variant="tonal" @click="useLocalEmbedding">使用已安装的模型 {{ localStatus.manifest.modelName }}</UiButton>
                <p v-if="draft.embeddingModel" class="text-sm">当前向量模型：{{ draft.embeddingModel }}</p>
              </template>
              <ChannelPicker v-else v-model="draft.embeddingChannel" v-model:selected-model="draft.embeddingModel" :options="embeddingOptions" show-model model-label="向量模型" :disabled="saving || indexing" />
              <p v-if="vectorReason" class="text-sm text-warning">{{ vectorReason }}</p>
              <DatasetPicker v-model="draft.datasetIds" kind="intent" :multiple="false" :profile="embeddingProfile" :refresh-key="datasetRefresh" />
              <UiAlert v-if="indexError" type="warning" variant="tonal">{{ indexError }}</UiAlert>
              <div><UiButton variant="tonal" :loading="indexing" :disabled="indexing || localBusy || !!vectorReason" @click="prepareSemanticIndex">{{ draft.datasetIds?.length ? '重新构建索引' : '使用内置样本并构建索引' }}</UiButton></div>
            </template>
            <template v-if="usesAssistant">
              <UiSelect :model-value="draft.classifier" :items="assistantClassifiers" label="判断类型" @update:model-value="selectClassifier" />
              <UiAlert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</UiAlert>
              <template v-else>
                <ChannelPicker v-model="draft.classifierChannel" v-model:selected-model="draft.classifierModel" :options="classifierOptions" label="判断渠道" show-model model-label="判断模型" :disabled="saving || indexing" />
              </template>
            </template>
            <div v-if="simpleMode === 'custom'" class="flex flex-wrap items-center gap-3">
              <span class="text-sm text-muted-foreground">当前使用自定义模式：{{ strategyTitle(draft.strategy) }}</span>
              <UiButton variant="tonal" size="small" @click="simpleMode = 'builtin'">改用内置规则与分组能力</UiButton>
            </div>
          </div>
        </section>
        <section v-if="draft.strategy === 'auto'" ref="groupsSection" class="intent-stack" aria-labelledby="intent-step-groups">
          <div class="intent-section-heading">
            <h3 id="intent-step-groups">分组能力</h3>
            <span class="text-xs text-muted-foreground">已配置 {{ configuredGroups }} / {{ pools.length }}</span>
          </div>
          <IntentGroupCapabilities v-model="groups" :pools="pools" :tasks="options.tasks ?? []" :disabled="saving || indexing" :use-feedback="!!draft.useFeedback" />
        </section>
        <UiAccordion variant="accordion" class="intent-accordion">
          <UiAccordionItem title="测试匹配">
            <section class="intent-stack px-4 pt-3 pb-4" aria-label="测试匹配">
              <UiSelect v-model="previewModel" searchable creatable :items="requestModels" :return-object="false" label="请求模型" placeholder="选择或输入模型" clearable @update:model-value="previewModel ||= ''" />
              <UiTextarea v-model="previewText" label="任务内容" rows="3" placeholder="例如：排查并修复 Go 服务中的并发死锁" />
              <div class="flex flex-wrap items-center gap-3">
                <UiButton variant="tonal" :loading="previewing" :disabled="!previewText.trim() || !!vectorReason || !!classifierReason || !!indexError || indexChecking" @click="preview">开始测试</UiButton>
                <span v-if="usesEmbedding || usesAssistant" class="text-xs text-muted-foreground">测试将调用配置的识别模型</span>
              </div>
              <UiAlert v-if="decision?.routing" type="info" variant="tonal">
                请求模型：{{ decision.routing.requestedModel }}；原路由分组：{{ decision.routing.originalPools.join('、') || '无' }}；本次预览分组：{{ decision.routing.selectedPools.join('、') || '无' }}。
                <div v-for="(candidate, index) in decision.routing.candidates" :key="index">{{ candidate.channel }} → {{ candidate.model }}</div>
              </UiAlert>
              <p v-if="decision?.trace?.stages?.length" class="text-sm">{{ decision.trace.stages.map(stage => `${stageTitles[stage.stage] ?? stage.stage}：${stage.matched ? '已识别' : '未可靠识别'}`).join(' → ') }}</p>
              <IntentMatchTrace v-if="decision" :decision="decision" :tasks="options.tasks ?? []" preview />
            </section>
          </UiAccordionItem>
          <UiAccordionItem title="高级设置">
            <div class="px-4 pt-3 pb-4">
              <div class="intent-stack">
                <UiSelect v-if="simpleMode === 'custom'" v-model="draft.strategy" :items="strategies" label="自定义匹配模式" item-title="title" item-value="value" />
                <template v-if="draft.strategy !== 'auto'">
                  <UiAlert v-if="!draft.routes.length" type="info" variant="tonal">添加规则映射后即可启用。</UiAlert>
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <h3 class="text-base font-medium">{{ draft.strategy === 'regex' ? '正则规则' : '规则映射' }}</h3>
                    <UiButton variant="tonal" size="small" :disabled="saving" @click="addRoute">添加映射</UiButton>
                  </div>
                  <UiAccordion variant="accordion" class="mb-5">
                    <UiAccordionItem v-for="(route, index) in draft.routes" :key="index">
                      <summary class="cursor-pointer px-4 py-3 font-medium">{{ poolName(route.poolId) }} · {{ route.description || route.id || '待配置' }}</summary>
                      <div class="px-4 pb-4">
                        <div class="intent-stack pt-2">
                          <UiSelect v-model="route.poolId" :items="pools" item-title="name" item-value="id" label="目标分组" />
                          <UiInput v-model="route.description" label="任务说明" />
                          <UiInput v-if="draft.strategy === 'semantic'" v-model="route.label" label="数据集类别" hint="与样本 label 一致，留空使用映射 ID" persistent-hint />
                          <UiTextarea v-if="draft.strategy === 'regex'" :model-value="route.patterns.join('\n')" label="正则表达式，每行一条" rows="3" @update:model-value="route.patterns = lines($event)" />
                          <UiTextarea v-if="draft.strategy === 'semantic' && !draft.datasetIds?.length" :model-value="route.examples.join('\n')" label="任务例句，每行一句" rows="3" @update:model-value="route.examples = lines($event)" />
                          <UiSelect v-model="route.tier" :items="tiers" label="难度等级" />
                          <UiInput v-if="draft.strategy === 'adaptive'" v-model.number="route.cost" type="number" min="0" label="相对成本" />
                          <UiInput v-model="route.id" label="映射 ID" hint="已自动生成，用于稳定引用" persistent-hint />
                          <UiButton variant="text" color="error" @click="draft.routes.splice(index, 1)">移除映射</UiButton>
                        </div>
                      </div>
                    </UiAccordionItem>
                  </UiAccordion>
                </template>
                <UiSelect v-if="simpleMode === 'custom' && draft.strategy === 'auto'" v-model="draft.taskMatcher" :items="[{ title: '内置规则', value: 'builtin' }, { title: '向量数据集', value: 'semantic' }]" label="旧配置任务识别来源" />
                <UiSelect v-if="needsClassifier && simpleMode === 'custom'" :model-value="draft.classifier" :items="classifiers" label="旧配置判断方式" @update:model-value="selectClassifier" />
                <template v-if="needsClassifier && draft.classifier !== 'heuristic' && simpleMode === 'custom'">
                  <UiAlert v-if="!classifierOptions.length" type="warning" variant="tonal">{{ usesDecisions ? '没有可用的 Decisions 渠道，请先添加并启用渠道。' : '没有可用的辅助判断渠道，请先启用渠道并配置密钥。' }}</UiAlert>
                  <template v-else>
                    <ChannelPicker v-model="draft.classifierChannel" v-model:selected-model="draft.classifierModel" :options="classifierOptions" label="判断渠道" show-model model-label="判断模型" />
                  </template>
                </template>
                <UiAccordion variant="accordion">
                  <UiAccordionItem title="会话稳定性"><div class="px-4 pb-4">
                    <div class="intent-fields">
                      <UiInput v-model.number="draft.minResidenceSeconds" type="number" min="30" label="最短驻留时间（秒）" hint="新任务至少在原组停留多久，避免反复切换；默认 300 秒。" persistent-hint />
                      <UiInput v-model.number="draft.minTurns" type="number" min="1" label="切换前最少用户轮次" hint="至少完成多少轮后才重新判断分组；默认 4 轮。工具续轮始终保护原来源。" persistent-hint />
                    </div>
                    <UiSwitch v-if="draft.strategy === 'auto'" v-model="draft.useFeedback" label="同能力分组参考反馈与相对成本" hide-details />
                    <UiSwitch v-if="draft.strategy === 'adaptive'" v-model="draft.observeOnly" label="自适应选组仅观察建议" hide-details />
                  </div></UiAccordionItem>
                  <UiAccordionItem title="未命中与失败"><div class="px-4 pb-4">
                    <div class="intent-stack">
                      <UiSelect v-model="draft.unmatched" :items="[{ title: '沿用客户端模型的原路由', value: 'original' }, { title: '返回未命中错误', value: 'error' }, { title: '使用指定分组', value: 'fallback' }]" label="识别不出来时" hint="所有已选识别方式都未可靠命中时执行。" persistent-hint />
                      <UiSelect v-if="draft.unmatched === 'fallback'" v-model="draft.fallbackPoolId" :items="pools" item-title="name" item-value="id" label="未命中目标分组" />
                      <UiSelect v-model="draft.failurePolicy" :items="[{ title: '沿用原路由并记录失败', value: 'keep' }, { title: '返回明确错误', value: 'error' }]" label="辅助判断失败或超时" />
                      <UiInput v-model.number="draft.timeoutMs" type="number" min="100" max="30000" label="识别总超时（毫秒）" hint="混合模式共用这个时间预算，超时视为调用失败。" persistent-hint />
                    </div>
                  </div></UiAccordionItem>
                  <UiAccordionItem v-if="usesEmbedding" title="高级向量参数"><div class="px-4 pb-4">
                    <div class="intent-stack">
                      <EmbeddingProfileFields v-model="embeddingProfile" hide-timeout hide-source />
                      <div class="intent-fields">
                        <UiInput v-model.number="draft.threshold" type="number" min="0" max="1" step="0.01" label="语义命中阈值" hint="相似度低于此值不采纳；默认 0.75。" persistent-hint />
                        <UiInput v-model.number="draft.switchMargin" type="number" min="0" max="1" step="0.01" label="最小相似度差值" hint="最佳与次佳类别太接近时不采纳；默认 0.15。" persistent-hint />
                      </div>
                      <div v-if="simpleMode === 'custom'"><UiButton variant="tonal" :loading="indexing" :disabled="saving || indexing || !!vectorReason" @click="buildIndex">增量构建并发布索引</UiButton></div>
                      <p class="text-xs text-muted-foreground">模型或前缀变化后需重新构建索引。</p>
                    </div>
                  </div></UiAccordionItem>
                </UiAccordion>
              </div>
            </div>
          </UiAccordionItem>
          <UiAccordionItem title="最近路由与任务反馈" @group:selected="$event.value && loadEvents()"><div class="px-4 pt-3 pb-4">
          <section class="intent-stack">
            <div class="flex justify-end"><UiButton size="small" variant="text" :loading="eventsLoading" @click="loadEvents">刷新记录</UiButton></div>
            <EmptyState v-if="!events.length" variant="inline" icon="mdi-history" title="暂无路由记录" description="先保存并启用意图匹配，再发送新任务，这里会记录实际命中的分组与判断耗时。" />
            <div v-for="event in events" :key="event.requestId" class="intent-event">
              <div>{{ event.target.poolId ? event.decision.trace?.groups.find(group => group.poolId === event.target.poolId)?.name ?? poolName(event.target.poolId) : '未确认执行目标' }} · {{ event.target.model || '未记录模型' }} · {{ event.success ? '已交付' : event.status === 'pending' ? '已确认目标，未记录完成' : '未完成' }}</div>
              <div v-if="event.decision.task" class="text-sm">{{ taskName(event.decision.task) }} · {{ tierName(event.decision.tier) }}</div>
              <div class="text-xs text-muted-foreground">{{ event.decision.trace?.classified ? '已执行分类' : event.decision.trace ? '本轮未分类' : '旧记录未保存分类过程' }} · {{ event.decision.reason }}<template v-if="event.decision.trace?.classified"> · {{ event.decision.durationMs }} ms</template></div>
              <div class="text-xs text-muted-foreground">{{ event.requestId }} · {{ formatDateTime(event.at) }}</div>
              <details class="intent-event-detail" @toggle="toggleEvent(event.requestId, $event)"><summary>查看输入与匹配过程</summary><IntentMatchTrace v-if="expandedEvents.has(event.requestId)" :decision="event.decision" :tasks="options.tasks ?? []" :target="event.target" /></details>
              <div v-if="event.success && !event.control && event.feedback === undefined" class="flex gap-2 mt-2">
                <UiButton size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, true)">效果满意</UiButton>
                <UiButton size="small" variant="text" :disabled="feedbackBusy" @click="feedback(event, false)">效果不满意</UiButton>
              </div>
              <span v-else-if="event.feedback !== undefined" class="text-xs">已记录：{{ event.feedback ? '满意' : '不满意' }}</span>
            </div>
          </section>
          </div></UiAccordionItem>
          <UiAccordionItem title="使用说明">
            <div class="intent-help px-4 pt-3 pb-4">
              <dl class="intent-help-modes">
                <div v-for="mode in simpleModes" :key="mode.value"><dt>{{ mode.title }}</dt><dd>{{ mode.description }}</dd></div>
              </dl>
              <p>混合模式按所选顺序识别，可靠命中后停止；未识别时继续，调用失败按高级设置处理。</p>
              <p>为分组选择任务后即可参与匹配，默认接受所有难度。多个分组均满足时按分组顺序选择；启用反馈后还会参考反馈与相对成本。</p>
              <p>意图分流命中时优先选择目标分组，客户端请求模型仍用于渠道内模型映射。人工固定、会话驻留和工具续轮保护优先。关闭意图分流后沿用原路由。</p>
              <p>向量匹配使用同一套模型配置计算请求与数据集样本。模型、渠道、版本或前缀变更后，需重新构建并发布索引。内置样本可在数据集页面查看和编辑。</p>
              <p>测试仅预览当前草稿中的候选分组与模型映射，不调用任务模型，也不模拟会话固定、健康检查或故障转移。实际路由可在最近记录中查看，满意度反馈不会自动训练模型。</p>
            </div>
          </UiAccordionItem>
        </UiAccordion>
      </div>
    </UiForm>
    <template #actions>
      <span v-if="vectorReason || classifierReason || indexError" class="text-xs text-destructive mr-auto" role="status">{{ vectorReason || classifierReason || indexError }}</span>
      <UiButton :disabled="saving || indexing || localBusy" @click="show = false">取消</UiButton>
      <UiButton color="primary" variant="flat" :loading="saving" :disabled="!draft || indexing || localBusy || (draft.enabled && (!!vectorReason || !!classifierReason || !!indexError || indexChecking))" @click="save">保存当前协议</UiButton>
    </template>
  </AppDialog>
  <AppDialog v-if="simpleMode === 'custom'" v-model="vectorGuide" title="先准备向量模型" size="md" :busy="localBusy">
    <div class="intent-stack">
      <p class="text-sm">语义匹配需要本地模型就绪，或配置已有 Embeddings 渠道。</p>
      <LocalEmbeddingSettings @status="updateLocalStatus" @busy="localBusy = $event" />
      <UiButton v-if="localStatus?.ready" variant="tonal" @click="continueWithLocalEmbedding">使用本地模型并继续</UiButton>
      <hr class="border-0 border-t border-border" />
      <UiButton v-if="options.embeddings.length" variant="tonal" @click="useRemoteEmbedding">使用已有 Embeddings 渠道</UiButton>
      <p v-else class="text-sm text-muted-foreground">暂无可用的 Embeddings 渠道，请先添加并启用渠道、配置密钥。</p>
      <template v-if="draft && draft.embeddingSource === 'remote' && options.embeddings.length">
        <UiSelect v-model="draft.embeddingChannel" :items="options.embeddings" item-title="name" item-value="id" label="Embeddings 渠道" @update:model-value="selectEmbeddingChannel" />
        <UiInput v-model="draft.embeddingModel" label="向量模型名称" />
        <UiButton variant="tonal" :disabled="!draft.embeddingChannel || !draft.embeddingModel.trim()" @click="continueWithRemoteEmbedding">选择渠道并继续</UiButton>
      </template>
    </div>
    <template #actions><UiButton :disabled="localBusy" @click="vectorGuide = false">{{ vectorReason ? '稍后配置' : '完成' }}</UiButton></template>
  </AppDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAccordion from '@/shared/ui/disclosure/Accordion.vue'
import UiAccordionItem from '@/shared/ui/disclosure/AccordionItem.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { useChannelStore } from '@/features/channels/stores/channel'
import { useIntentStore } from '@/features/channels/stores/intent'
import { useToast } from '@/shared/composables/useToast'
import { intentApi, intentErrorText, intentPreviewFailure, type IntentConfig, type IntentDecision, type IntentEvent, type IntentGroupCapability, type IntentOptions, type IntentProtocol, type LocalStatus } from '@/features/channels/services/intent'
import type { EmbeddingProfile } from '@/types'
import LocalEmbeddingSettings from '@/features/channels/components/routing/LocalEmbeddingSettings.vue'
import { datasetsApi } from '@/features/extensions/datasets'
import { DatasetPicker, EmbeddingProfileFields } from '@/features/extensions/datasets/ui'
import IntentGroupCapabilities from '@/features/channels/components/routing/IntentGroupCapabilities.vue'
import IntentMatchTrace from '@/features/channels/components/routing/IntentMatchTrace.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import ChannelPicker from '@/shared/components/domain/ChannelPicker.vue'
import { formatDateTime } from '@/shared/utils/datetime'
import { ApiError } from '@/shared/services/http'

const props = defineProps<{ protocol: IntentProtocol }>()
const store = useIntentStore()
const channelStore = useChannelStore()
const { showToast } = useToast()
const show = ref(false)
const modeGroupId = useId()
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
      if (!existing) throw new Error('未找到内置意图数据集，请到数据集页面新建数据集，并在此选择。')
      if (!existing.approved) throw new Error('内置意图样本尚未审核，请到数据集页面审核并保存后再构建索引。')
      draft.value.datasetIds = [existing.id]
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
@reference "@/styles/index.css";

.intent-layout { @apply grid min-w-0 gap-6; }
.intent-stack { @apply grid min-w-0 gap-4; }
.intent-enable { @apply flex flex-wrap items-center justify-between gap-3 rounded-control bg-muted/30 px-4 py-2; }
.intent-section-heading { @apply flex flex-wrap items-center justify-between gap-3; }
.intent-section-heading h3 { @apply text-sm font-semibold; }
.intent-fields { @apply grid grid-cols-2 gap-4; }
.intent-strategies { @apply grid grid-cols-4 gap-2; }
.intent-strategy { @apply flex min-w-0 cursor-pointer items-center gap-2 rounded-control border border-border px-3 py-3 text-sm transition-colors hover:bg-muted/30; }
.intent-strategy-selected { @apply border-primary/50 bg-primary/5; }
.intent-strategy:focus-within { @apply outline-2 outline-offset-2 outline-ring; }
.intent-strategy:has(input:disabled) { @apply cursor-not-allowed opacity-50; }
.intent-model-settings { @apply grid min-w-0 gap-4 border-l-2 border-border pl-4; }
.intent-accordion :deep(.ui-accordion-item) { @apply rounded-control bg-transparent shadow-none; }
.intent-help { @apply grid gap-3 text-xs leading-6 text-muted-foreground; }
.intent-help-modes { @apply grid gap-2; }
.intent-help-modes > div { @apply grid [grid-template-columns:5rem_minmax(0,_1fr)] gap-3; }
.intent-help dt { @apply font-medium text-foreground; }
.intent-event-detail { @apply min-w-0; }
.intent-event-detail > summary { @apply cursor-pointer [padding:12px_0] min-h-11; }
.intent-event { @apply grid gap-1.5 [padding:16px_0] [border-bottom:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)] [overflow-wrap:anywhere]; }
@media (max-width: 900px) { .intent-strategies { @apply grid-cols-2; } }
@media (max-width: 550px) {
  .intent-fields { @apply grid-cols-1; }
  .intent-strategy { @apply gap-1.5 px-2.5; }
  .intent-layout { @apply gap-5; }
}
</style>
