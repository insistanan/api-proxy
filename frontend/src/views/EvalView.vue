<template>
  <div class="eval-page">
    <div class="page-heading mb-4">
      <div class="text-h5 font-weight-bold">评测</div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="reloadAll">刷新</v-btn>
        <v-btn variant="text" prepend-icon="mdi-history" @click="historyDrawer = true">历史</v-btn>
        <v-btn variant="text" prepend-icon="mdi-format-list-bulleted" @click="probeDrawer = true">题目管理</v-btn>
      </div>
    </div>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-4" closable @click:close="error = ''">{{ error }}</v-alert>

    <v-card class="mb-4 pa-4" elevation="0" border>
      <div class="d-flex flex-wrap align-center ga-3">
        <div class="suite-select">
          <v-select
            v-model="selectedSuiteId"
            :items="suiteItems"
            item-title="title"
            item-value="value"
            label="模式"
            density="compact"
            variant="outlined"
            hide-details
            style="min-width: 240px"
          >
            <template #selection="{ item }">
              <span class="text-truncate font-weight-medium">{{ item.raw.title }}</span>
            </template>
            <template #item="{ props: itemProps, item }">
              <v-list-item v-bind="itemProps" :title="item.raw.title">
                <template #append>
                  <v-tooltip :text="item.raw.description || '暂无说明'" location="start">
                    <template #activator="{ props: tip }">
                      <v-icon v-bind="tip" icon="mdi-help-circle-outline" size="small" class="text-medium-emphasis" />
                    </template>
                  </v-tooltip>
                </template>
              </v-list-item>
            </template>
          </v-select>
          <v-tooltip v-if="selectedSuiteDescription" :text="selectedSuiteDescription" location="bottom">
            <template #activator="{ props: tip }">
              <v-icon v-bind="tip" icon="mdi-help-circle-outline" size="small" class="ml-1 text-medium-emphasis" />
            </template>
          </v-tooltip>
        </div>

        <span class="text-body-2 text-medium-emphasis">已选 {{ selectedChannelIds.length }} 渠道</span>
        <v-spacer />

        <v-menu v-model="watchMenu" :close-on-content-click="false" location="bottom end">
          <template #activator="{ props: activator }">
            <v-btn v-bind="activator" variant="text" size="small" prepend-icon="mdi-clock-outline">
              {{ watchButtonLabel }}
            </v-btn>
          </template>
          <v-card min-width="320">
            <v-card-text>
              <v-switch v-model="watchForm.enabled" label="定时复查" color="primary" density="compact" hide-details class="mb-2" />
              <v-select
                v-model="watchForm.suiteId"
                :items="cheapSuiteItems"
                item-title="title"
                item-value="value"
                label="模式"
                density="compact"
                variant="outlined"
                hide-details
                class="mb-3"
              />
              <v-select
                v-model="watchForm.interval"
                :items="INTERVAL_ITEMS"
                item-title="title"
                item-value="value"
                label="间隔"
                density="compact"
                variant="outlined"
                hide-details
              />
            </v-card-text>
            <v-card-actions>
              <v-spacer />
              <v-btn variant="text" size="small" @click="watchMenu = false">取消</v-btn>
              <v-btn
                color="primary"
                size="small"
                :loading="savingWatch"
                :disabled="watchForm.enabled && selectedChannelIds.length === 0"
                @click="saveWatch"
              >
                保存
              </v-btn>
            </v-card-actions>
          </v-card>
        </v-menu>

        <v-btn v-if="busy" color="error" variant="tonal" :loading="cancelling" @click="cancelRun">取消</v-btn>
        <v-btn v-else color="primary" :loading="starting" :disabled="!canStart" @click="startRun">开始评测</v-btn>
      </div>

      <div class="mt-3 d-flex flex-wrap align-center ga-2 probe-select-bar">
        <span class="text-body-2 text-medium-emphasis">题目 ({{ activeProbeIds.length }}/{{ matrixProbes.length }})：</span>
        
        <v-chip
          v-for="probe in probeCandidates"
          :key="probe.id"
          size="small"
          :color="probe.candidate ? (probe.builtin ? 'primary' : 'purple') : 'grey'"
          :variant="probe.candidate ? 'flat' : 'outlined'"
          class="probe-chip"
          @click="toggleProbe(probe.id)"
        >
          <v-icon v-if="!probe.builtin" start size="14">mdi-star</v-icon>
          {{ probe.name }}
          <span v-if="!probe.builtin" class="text-caption ml-1 opacity-80">(自建)</span>
        </v-chip>

        <v-tooltip :text="probeModeHint" location="top">
          <template #activator="{ props: tip }">
            <v-icon v-bind="tip" icon="mdi-help-circle-outline" size="small" class="text-medium-emphasis" />
          </template>
        </v-tooltip>

        <v-spacer />

        <v-btn
          size="small"
          color="primary"
          variant="tonal"
          prepend-icon="mdi-checkbox-multiple-marked-outline"
          @click="openProbeSelector"
        >
          挑选题目
        </v-btn>

        <v-btn size="small" variant="text" prepend-icon="mdi-plus" @click="openProbeDrawerCreate">
          新增题目
        </v-btn>
        <v-btn size="small" variant="text" prepend-icon="mdi-format-list-bulleted" @click="probeDrawer = true">
          题目管理
        </v-btn>
      </div>

      <div class="mt-4">
        <EvalChannelPicker
          v-model="selectedChannelIds"
          v-model:protocols="enabledProtocols"
          v-model:channel-models="channelModels"
          v-model:channel-thinking="channelThinking"
          :channels-by-kind="channelsByKind"
          :pools-by-kind="poolsByKind"
        />
      </div>
    </v-card>

    <v-card
      v-if="currentRun"
      :id="`run-card-${currentRun.id}`"
      class="pa-4 mb-4 elevation-0 border run-card"
      :class="{ 'run-card--flash': flashRunId === currentRun.id }"
    >
      <div class="d-flex flex-wrap align-center ga-2 mb-2">
        <div class="text-h6 font-weight-bold">{{ currentRun.suiteName || '评测批次' }}</div>
        <v-chip size="small" variant="tonal" :color="evalRunStatusColor(currentRun.status)">
          {{ evalRunStatusLabel(currentRun.status) }}
        </v-chip>
        <v-chip v-if="currentRun.trigger === 'watch'" size="small" variant="outlined" color="grey">定时</v-chip>
        <v-chip v-if="currentRun.thinking && currentRun.thinking !== 'inherit'" size="small" variant="outlined" color="grey">
          思考 {{ evalThinkingLabel(currentRun.thinking) }}
        </v-chip>
        <v-spacer />
        <v-btn variant="text" size="small" prepend-icon="mdi-history" @click="historyDrawer = true">历史</v-btn>
      </div>

      <div class="text-caption text-medium-emphasis mb-3 run-overview-meta">
        <span><v-icon size="12" icon="mdi-account-group-outline" /> {{ currentRun.channelIds.length }} 渠道</span>
        <span><v-icon size="12" icon="mdi-format-list-bulleted" /> {{ matrixProbes.length }} 题</span>
        <span><v-icon size="12" icon="mdi-timer-sand" /> {{ evalDurationLabel(currentRun.startedAt, currentRun.finishedAt) }}</span>
        <span><v-icon size="12" icon="mdi-clock-outline" /> {{ evalFormatTime(currentRun.startedAt) }}</span>
      </div>

      <div v-if="tallySegments.length" class="tally-bar mb-3">
        <div
          v-for="segment in tallySegments"
          :key="segment.key"
          class="tally-segment"
          :class="`tally-segment--${segment.color}`"
          :style="{ flex: segment.ratio }"
          :title="`${segment.label} ${segment.count} (${Math.round(segment.ratio * 100)}%)`"
        >
          <span v-if="segment.ratio > 0.08" class="tally-segment-label">{{ segment.count }}</span>
        </div>
      </div>

      <div v-if="tallySegments.length" class="tally-legend mb-3">
        <span v-for="segment in tallySegments" :key="segment.key" class="tally-legend-item">
          <span class="tally-dot" :class="`tally-dot--${segment.color}`" ></span>
          {{ segment.label }} {{ segment.count }}
        </span>
      </div>

      <v-alert v-if="currentRun?.error" type="warning" variant="tonal" density="compact" class="mb-3">
        {{ currentRun.error }}
      </v-alert>

      <EvalMatrix :run="currentRun" :probes="matrixProbes" :channels-by-kind="channelsByKind" @cell="openCell" />
    </v-card>

    <!-- 最近批次缩略条：开始新任务后，上一个批次不会消失，从这里快速切回。 -->
    <v-card v-if="recentRuns.length" class="mb-4 pa-3 elevation-0 border recent-runs" elevation="0" border>
      <div class="d-flex align-center ga-2 mb-2">
        <div class="text-body-2 font-weight-medium">最近批次</div>
        <v-spacer />
        <v-btn size="x-small" variant="text" prepend-icon="mdi-history" @click="historyDrawer = true">全部历史</v-btn>
      </div>
      <div class="recent-runs-list">
        <div
          v-for="item in recentRuns"
          :key="item.id"
          class="recent-run-card"
          role="button"
          tabindex="0"
          @click="selectRun(item.id)"
          @keydown.enter.prevent="selectRun(item.id)"
        >
          <div class="d-flex align-center ga-2">
            <v-chip size="x-small" variant="tonal" :color="evalRunStatusColor(item.status)">
              {{ evalRunStatusLabel(item.status) }}
            </v-chip>
            <span class="text-truncate font-weight-medium recent-run-title">{{ item.suiteName || '评测批次' }}</span>
          </div>
          <div v-if="historyTallySegments(item).length" class="history-tally-bar">
            <div
              v-for="segment in historyTallySegments(item)"
              :key="segment.key"
              class="history-tally-seg"
              :class="`history-tally-seg--${segment.color}`"
              :style="{ flex: segment.ratio }"
              :title="`${segment.label} ${segment.count}`"
            ></div>
          </div>
          <div class="history-card-time">{{ recentRunStatusLabel(item) }}</div>
        </div>
      </div>
    </v-card>

    <!-- 题目挑选器对话框 -->
    <v-dialog v-model="probeSelectorOpen" max-width="680" scrollable>
      <v-card>
        <v-card-title class="d-flex align-center ga-2 pa-4">
          <v-icon icon="mdi-checkbox-multiple-marked-outline" color="primary" />
          <span class="text-h6 font-weight-bold">挑选评测题目</span>
          <v-spacer />
          <v-btn icon="mdi-close" size="small" variant="text" @click="probeSelectorOpen = false" />
        </v-card-title>
        
        <v-divider />

        <v-card-text class="pa-4">
          <div class="d-flex flex-wrap align-center ga-2 mb-3">
            <v-chip
              v-for="tab in SELECTOR_TABS"
              :key="tab.value"
              size="small"
              :color="selectorTab === tab.value ? 'primary' : undefined"
              :variant="selectorTab === tab.value ? 'flat' : 'outlined'"
              @click="selectorTab = tab.value"
            >
              {{ tab.title }}
            </v-chip>
            <v-spacer />
            <v-text-field
              v-model="selectorSearch"
              density="compact"
              variant="outlined"
              prepend-inner-icon="mdi-magnify"
              label="搜索题目"
              hide-details
              clearable
              style="max-width: 180px"
            />
          </div>

          <div class="d-flex align-center ga-2 mb-3">
            <span class="text-caption text-medium-emphasis">
              已选中 {{ selectorSelectedIds.length }} / {{ probes.length }} 道题
            </span>
            <v-spacer />
            <v-btn size="x-small" variant="text" @click="selectAllFilteredProbes">全选当前筛选</v-btn>
            <v-btn size="x-small" variant="text" @click="selectCustomProbesOnly">只选自建题</v-btn>
            <v-btn size="x-small" variant="text" @click="selectorSelectedIds = []">清空</v-btn>
          </div>

          <div v-if="!filteredSelectorProbes.length" class="text-body-2 text-medium-emphasis py-8 text-center">
            没有匹配的题目
          </div>

          <div v-else class="selector-probes-list">
            <v-card
              v-for="probe in filteredSelectorProbes"
              :key="probe.id"
              variant="outlined"
              class="mb-2 selector-probe-card"
              :class="{ 'selector-probe-card--selected': isProbeSelectedInSelector(probe.id) }"
              role="button"
              tabindex="0"
              @click="toggleProbeInSelector(probe.id)"
            >
              <div class="pa-3 d-flex align-center ga-3">
                <v-icon size="20" :color="isProbeSelectedInSelector(probe.id) ? 'primary' : undefined">
                  {{ isProbeSelectedInSelector(probe.id) ? 'mdi-checkbox-marked' : 'mdi-checkbox-blank-outline' }}
                </v-icon>
                <div class="flex-grow-1">
                  <div class="d-flex align-center ga-2 mb-1">
                    <span class="text-body-2 font-weight-bold">{{ probe.name }}</span>
                    <v-chip v-if="probe.builtin" size="x-small" variant="tonal" color="info">内置</v-chip>
                    <v-chip v-else size="x-small" variant="tonal" color="purple">自建</v-chip>
                    <v-chip size="x-small" variant="outlined" color="grey">
                      {{ probe.category === 'authenticity' ? '真伪' : '智商' }}
                    </v-chip>
                  </div>
                  <div v-if="probe.description" class="text-caption text-medium-emphasis mb-1">
                    {{ probe.description }}
                  </div>
                  <div class="text-caption text-disabled">
                    抽取 {{ probe.extract.kind }} · 判定 {{ probe.judge.kind }} · 采样 {{ probe.sampleCount }} 次
                  </div>
                </div>
              </div>
            </v-card>
          </div>
        </v-card-text>

        <v-divider />

        <v-card-actions class="pa-4">
          <span class="text-caption text-medium-emphasis">
            确认后将自动切换为自定义选题评测
          </span>
          <v-spacer />
          <v-btn variant="text" @click="probeSelectorOpen = false">取消</v-btn>
          <v-btn color="primary" :disabled="selectorSelectedIds.length === 0" @click="applyProbeSelection">
            确认选择 ({{ selectorSelectedIds.length }} 题)
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-navigation-drawer v-model="historyDrawer" location="end" temporary width="520">
      <div class="pa-4">
        <div class="d-flex align-center ga-2 mb-4">
          <v-icon icon="mdi-history" size="small" class="text-medium-emphasis" />
          <div class="text-h6">评测历史</div>
          <v-spacer />
          <v-btn icon="mdi-close" size="small" variant="text" @click="historyDrawer = false" />
        </div>

        <div v-if="deepLinkChannelId" class="history-scope-banner mb-3">
          <v-icon icon="mdi-filter-variant" size="18" color="primary" />
          <div class="history-scope-copy">
            <div class="text-body-2 font-weight-medium">渠道历史评测</div>
            <div class="text-caption text-medium-emphasis">
              {{ channelNameOf(deepLinkChannelId) }} · {{ visibleHistoryRuns.length }} 条记录
            </div>
          </div>
          <v-chip size="x-small" color="primary" variant="tonal">已定位</v-chip>
        </div>

        <div v-if="!visibleHistoryRuns.length" class="text-body-2 text-medium-emphasis py-8 text-center">
          {{ deepLinkChannelId ? '该渠道还没有评测记录' : '还没有评测记录' }}
        </div>

        <div v-else class="history-list">
          <div
            v-for="item in visibleHistoryRuns"
            :id="`history-run-card-${item.id}`"
            :key="item.id"
            class="history-card"
            :class="{
              'history-card--active': currentRun?.id === item.id,
              'history-card--channel-focus': !!deepLinkChannelId
            }"
            role="button"
            tabindex="0"
            :aria-label="`${item.suiteName || '评测批次'}，${evalRunStatusLabel(item.status)}，${evalFormatTime(item.startedAt || item.createdAt)}`"
            @click="selectRun(item.id)"
            @keydown.enter.prevent="selectRun(item.id)"
            @keydown.space.prevent="selectRun(item.id)"
          >
            <div class="history-card-header">
              <div class="text-truncate font-weight-medium">{{ item.suiteName || '评测批次' }}</div>
              <v-chip size="x-small" variant="tonal" :color="evalRunStatusColor(item.status)">
                {{ evalRunStatusLabel(item.status) }}
              </v-chip>
            </div>

            <div class="history-card-meta">
              <span class="meta-item">
                <v-icon size="12" icon="mdi-account-group-outline" />
                {{ item.channelIds.length }} 渠道
              </span>
              <span v-if="item.trigger === 'watch'" class="meta-item">
                <v-icon size="12" icon="mdi-clock-outline" />定时
              </span>
              <span class="meta-item">
                <v-icon size="12" icon="mdi-timer-sand" />
                {{ evalDurationLabel(item.startedAt, item.finishedAt) }}
              </span>
            </div>

            <div v-if="historyTallySegments(item).length" class="history-tally-bar">
              <div
                v-for="segment in historyTallySegments(item)"
                :key="segment.key"
                class="history-tally-seg"
                :class="`history-tally-seg--${segment.color}`"
                :style="{ flex: segment.ratio }"
                :title="`${segment.label} ${segment.count}`"
              ></div>
            </div>

            <div class="history-card-time">{{ evalFormatTime(item.startedAt || item.createdAt) }}</div>
          </div>
        </div>
      </div>
    </v-navigation-drawer>

    <EvalResultDrawer
      v-model="cellDrawer"
      :selection="cellSelection"
      :channel-name-of="channelNameOf"
      :probes="matrixProbes"
      :results="currentRun?.results"
      @navigate="onCellNavigate"
    />
    <EvalProbeManager ref="probeManagerRef" v-model="probeDrawer" :probes="probes" @changed="reloadProbes" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import EvalChannelPicker from '@/components/EvalChannelPicker.vue'
import EvalMatrix from '@/components/EvalMatrix.vue'
import EvalProbeManager from '@/components/EvalProbeManager.vue'
import EvalResultDrawer from '@/components/EvalResultDrawer.vue'
import {
  api,
  channelApiByType,
  type ApiTab,
  type Channel,
  type ChannelPool,
  type EvalProbe,
  type EvalRun,
  type EvalSuite,
  type EvalWatchConfig
} from '@/services/api'
import { usePreferencesStore } from '@/stores/preferences'
import {
  EVAL_PROTOCOL_KINDS,
  EVAL_THINKING_ITEMS,
  evalDurationLabel,
  evalFormatTime,
  evalIntervalLabel,
  evalRunStatusColor,
  evalRunStatusLabel,
  evalThinkingLabel,
  tallyToSegments,
  tallyVerdicts,
  type EvalCellSelection,
  type EvalVerdictTally
} from '@/utils/eval'

const CUSTOM_SUITE_ID = 'custom'

const INTERVAL_ITEMS = [
  { title: '30 分钟', value: '30m' },
  { title: '2 小时', value: '2h' },
  { title: '1 天', value: '1d' }
]

const SELECTOR_TABS = [
  { title: '全部', value: 'all' },
  { title: '真伪', value: 'authenticity' },
  { title: '智商', value: 'iq' },
  { title: '自建题', value: 'custom' },
  { title: '内置题', value: 'builtin' }
]

const emptyByKind = <T,>(): Record<ApiTab, T[]> => ({
  messages: [],
  responses: [],
  gemini: [],
  chat: [],
  images: []
})

const route = useRoute()
const preferences = usePreferencesStore()
const probeManagerRef = ref<InstanceType<typeof EvalProbeManager> | null>(null)

/** 从渠道条跳转时，使用稳定 channel.id 筛出该渠道的全部历史批次。 */
const deepLinkChannelId = computed(() => {
  const value = route.query.channel
  return typeof value === 'string' ? value.trim() : ''
})

const EVAL_CHANNEL_HISTORY_LIMIT = 200

const loading = ref(false)
const starting = ref(false)
const cancelling = ref(false)
const savingWatch = ref(false)
const error = ref('')

const probes = ref<EvalProbe[]>([])
const suites = ref<EvalSuite[]>([])
const channelsByKind = ref<Record<ApiTab, Channel[]>>(emptyByKind<Channel>())
const poolsByKind = ref<Record<ApiTab, ChannelPool[]>>(emptyByKind<ChannelPool>())

const selectedSuiteId = ref('')
const selectedChannelIds = ref<string[]>([])
const enabledProtocols = ref<ApiTab[]>(['messages'])
const thinking = ref('inherit')
const modelOverride = ref('')
const channelModels = ref<Record<string, string>>({})
const channelThinking = ref<Record<string, string>>({})

/** 自定义模式/自选题库下用户选择的 probe IDs */
const customSelectedProbeIds = ref<string[]>([])
/** 取消勾选的题（基于当前模式）。跑历史批次时忽略。 */
const excludedProbeIds = ref<string[]>([])

const busy = ref(false)
const currentRun = ref<EvalRun | null>(null)
const runHistory = ref<EvalRun[]>([])
const historyDrawer = ref(false)
const probeDrawer = ref(false)
const cellDrawer = ref(false)
const cellSelection = ref<EvalCellSelection | null>(null)
const flashRunId = ref('')

// 题目挑选器弹窗状态
const probeSelectorOpen = ref(false)
const selectorTab = ref('all')
const selectorSearch = ref('')
const selectorSelectedIds = ref<string[]>([])

const visibleHistoryRuns = computed(() => {
  const channelId = deepLinkChannelId.value
  if (!channelId) return runHistory.value
  return runHistory.value.filter(run => run.channelIds.includes(channelId))
})

const watchMenu = ref(false)
const watchConfig = ref<EvalWatchConfig>({
  enabled: false,
  suiteId: '',
  interval: '2h',
  channelIds: [],
  lastRunAt: 0,
  nextRunAt: 0
})
const watchForm = ref({ enabled: false, suiteId: '', interval: '2h' })

let runStream: AbortController | null = null

const suiteItems = computed(() => [
  ...suites.value.map(suite => ({
    title: suite.cheap ? `${suite.name} · 便宜` : suite.name,
    description: suite.description || '',
    value: suite.id
  })),
  {
    title: '自定义选题 (全题库)',
    description: '自由挑选内置题与自建题目组合进行评测',
    value: CUSTOM_SUITE_ID
  }
])

const selectedSuiteDescription = computed(() => {
  if (selectedSuiteId.value === CUSTOM_SUITE_ID) {
    return '自由挑选内置题与自建题目组合进行评测'
  }
  const suite = suites.value.find(item => item.id === selectedSuiteId.value)
  return suite?.description || ''
})

const cheapSuiteItems = computed(() =>
  suites.value.filter(suite => suite.cheap).map(suite => ({ title: suite.name, value: suite.id }))
)

const watchButtonLabel = computed(() => {
  if (!watchConfig.value.enabled) return '定时'
  return `定时 · ${evalIntervalLabel(watchConfig.value.interval)}`
})

/**
 * 当前模式下应包含的题目列表：
 * 1. 若当前展示历史批次，以批次实际题目为准；
 * 2. 若选了自定义模式，以用户选择的 customSelectedProbeIds（默认全部）为准；
 * 3. 若选了内置套件模式，以该套件的 probeIds 为准。
 */
const matrixProbes = computed<EvalProbe[]>(() => {
  if (currentRun.value) {
    if (currentRun.value.suiteId === CUSTOM_SUITE_ID) {
      if (customSelectedProbeIds.value.length) {
        return customSelectedProbeIds.value
          .map(id => probes.value.find(p => p.id === id))
          .filter((p): p is EvalProbe => !!p)
      }
      return probes.value
    }
    const suite = suites.value.find(item => item.id === currentRun.value?.suiteId)
    if (suite) {
      return suite.probeIds
        .map(id => probes.value.find(probe => probe.id === id))
        .filter((probe): probe is EvalProbe => !!probe)
    }
  }

  if (selectedSuiteId.value === CUSTOM_SUITE_ID) {
    if (customSelectedProbeIds.value.length) {
      return customSelectedProbeIds.value
        .map(id => probes.value.find(p => p.id === id))
        .filter((p): p is EvalProbe => !!p)
    }
    return probes.value
  }

  const suite = suites.value.find(item => item.id === selectedSuiteId.value)
  if (!suite) return probes.value
  return suite.probeIds
    .map(id => probes.value.find(probe => probe.id === id))
    .filter((probe): probe is EvalProbe => !!probe)
})

/** 配置条显示的题带 candidate 标记：true=已勾选参与，false=已取消勾选。 */
const probeCandidates = computed(() =>
  matrixProbes.value.map(probe => ({
    ...probe,
    candidate: !excludedProbeIds.value.includes(probe.id)
  }))
)

/** 本次要跑的题：已勾选参与的题目 IDs */
const activeProbeIds = computed(() =>
  matrixProbes.value.map(probe => probe.id).filter(id => !excludedProbeIds.value.includes(id))
)

const probeModeHint = computed(() => {
  if (excludedProbeIds.value.length === 0) return '点击题目 Chip 可快速启用/排除；支持点击「挑选题目」自由添加自建题。'
  return `已排除 ${excludedProbeIds.value.length} 题，本次将评测 ${activeProbeIds.value.length} 题。`
})

const toggleProbe = (probeId: string) => {
  if (excludedProbeIds.value.includes(probeId)) {
    excludedProbeIds.value = excludedProbeIds.value.filter(id => id !== probeId)
    return
  }
  excludedProbeIds.value = [...excludedProbeIds.value, probeId]
}

// ===== 题目挑选器逻辑 =====

const openProbeSelector = () => {
  selectorTab.value = 'all'
  selectorSearch.value = ''
  selectorSelectedIds.value = activeProbeIds.value.length
    ? [...activeProbeIds.value]
    : probes.value.map(p => p.id)
  probeSelectorOpen.value = true
}

const filteredSelectorProbes = computed(() => {
  let list = probes.value || []
  if (selectorTab.value === 'authenticity') {
    list = list.filter(p => p.category === 'authenticity')
  } else if (selectorTab.value === 'iq') {
    list = list.filter(p => p.category === 'iq')
  } else if (selectorTab.value === 'custom') {
    list = list.filter(p => !p.builtin)
  } else if (selectorTab.value === 'builtin') {
    list = list.filter(p => p.builtin)
  }

  if (selectorSearch.value.trim()) {
    const kw = selectorSearch.value.trim().toLowerCase()
    list = list.filter(p =>
      p.name.toLowerCase().includes(kw) ||
      p.slug.toLowerCase().includes(kw) ||
      (p.description || '').toLowerCase().includes(kw)
    )
  }
  return list
})

const isProbeSelectedInSelector = (probeId: string) => selectorSelectedIds.value.includes(probeId)

const toggleProbeInSelector = (probeId: string) => {
  if (isProbeSelectedInSelector(probeId)) {
    selectorSelectedIds.value = selectorSelectedIds.value.filter(id => id !== probeId)
  } else {
    selectorSelectedIds.value = [...selectorSelectedIds.value, probeId]
  }
}

const selectAllFilteredProbes = () => {
  const currentFilteredIds = filteredSelectorProbes.value.map(p => p.id)
  const merged = new Set([...selectorSelectedIds.value, ...currentFilteredIds])
  selectorSelectedIds.value = Array.from(merged)
}

const selectCustomProbesOnly = () => {
  const customIds = probes.value.filter(p => !p.builtin).map(p => p.id)
  selectorSelectedIds.value = customIds
}

const applyProbeSelection = () => {
  customSelectedProbeIds.value = [...selectorSelectedIds.value]
  selectedSuiteId.value = CUSTOM_SUITE_ID
  excludedProbeIds.value = []
  probeSelectorOpen.value = false
}

// =========================

const canStart = computed(
  () => !!selectedSuiteId.value && selectedChannelIds.value.length > 0 && activeProbeIds.value.length > 0 && !busy.value
)

const currentTally = computed<EvalVerdictTally | null>(() => {
  if (!currentRun.value) return null
  if (currentRun.value.tally) {
    return {
      pass: currentRun.value.tally.pass,
      suspect: currentRun.value.tally.suspect,
      fail: currentRun.value.tally.fail,
      error: currentRun.value.tally.error,
      insufficient: currentRun.value.tally.insufficient,
      inapplicable: currentRun.value.tally.inapplicable,
      pending: 0,
      total: currentRun.value.tally.total
    }
  }
  const cellCount = currentRun.value.channelIds.length * matrixProbes.value.length
  return tallyVerdicts(currentRun.value.results, cellCount)
})

const tallySegments = computed(() => tallyToSegments(currentTally.value))

const historyTallySegments = (run: EvalRun) => {
  if (!run.tally) return []
  return tallyToSegments({
    pass: run.tally.pass,
    suspect: run.tally.suspect,
    fail: run.tally.fail,
    error: run.tally.error,
    insufficient: run.tally.insufficient,
    inapplicable: run.tally.inapplicable,
    pending: 0,
    total: run.tally.total
  })
}

const recentRuns = computed(() => {
  if (!currentRun.value) return []
  return runHistory.value
    .filter(item => item.id !== currentRun.value?.id)
    .slice(0, 4)
})

const recentRunStatusLabel = (run: EvalRun) => {
  const finished = run.finishedAt || 0
  const now = Math.floor(Date.now() / 1000)
  if (finished && now - finished < 5) return '刚刚'
  return evalFormatTime(finished || run.startedAt || run.createdAt)
}

const channelNameOf = (channelId: string) => {
  for (const kind of EVAL_PROTOCOL_KINDS) {
    const found = (channelsByKind.value[kind] || []).find(channel => channel.id === channelId)
    if (found) return found.name
  }
  return channelId
}

const focusDeepLinkedHistory = async () => {
  const channelId = deepLinkChannelId.value
  if (!channelId) return

  historyDrawer.value = true
  await nextTick()
  const firstMatchedRun = visibleHistoryRuns.value[0]
  if (!firstMatchedRun) return
  document
    .getElementById(`history-run-card-${firstMatchedRun.id}`)
    ?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}

const openCell = (selection: EvalCellSelection) => {
  cellSelection.value = selection
  cellDrawer.value = true
}

const onCellNavigate = (selection: EvalCellSelection) => {
  cellSelection.value = selection
}

const stopRunStream = () => {
  runStream?.abort()
  runStream = null
}

const subscribeRun = (runId: string) => {
  stopRunStream()
  const controller = new AbortController()
  runStream = controller
  api
    .streamEvalRun(
      runId,
      payload => {
        if (payload.error) {
          error.value = payload.error
          return
        }
        if (!payload.run) return
        currentRun.value = payload.run
        busy.value = payload.run.status === 'running' || payload.run.status === 'queued'
      },
      { signal: controller.signal }
    )
    .catch(err => {
      if (controller.signal.aborted) return
      error.value = err instanceof Error ? err.message : String(err)
    })
    .finally(() => {
      if (runStream === controller) {
        runStream = null
        busy.value = false
      }
    })
}

const loadChannels = async () => {
  await Promise.all(
    EVAL_PROTOCOL_KINDS.map(async kind => {
      const [channelResponse, poolResponse] = await Promise.all([
        channelApiByType(kind).getChannels(),
        api.getChannelPools(kind)
      ])
      channelsByKind.value[kind] = channelResponse.channels || []
      poolsByKind.value[kind] = poolResponse.pools || []
    })
  )
}

const applyDeepLinkOrMemory = () => {
  const deepLink = typeof route.query.channel === 'string' ? route.query.channel : ''
  if (deepLink) {
    selectedChannelIds.value = [deepLink]
    const kind = EVAL_PROTOCOL_KINDS.find(item =>
      (channelsByKind.value[item] || []).some(channel => channel.id === deepLink)
    )
    if (kind) {
      enabledProtocols.value = [kind]
    }
    return
  }
  if (preferences.evalLastChannelIds.length) {
    selectedChannelIds.value = [...preferences.evalLastChannelIds]
  }
  if (preferences.evalLastSuiteId) {
    selectedSuiteId.value = preferences.evalLastSuiteId
  }
}

const reloadProbes = async () => {
  const [probeResponse, suiteResponse] = await Promise.all([api.listEvalProbes(), api.listEvalSuites()])
  probes.value = probeResponse.probes || []
  suites.value = suiteResponse.suites || []
}

const reloadAll = async () => {
  loading.value = true
  error.value = ''
  try {
    const deepLinkChannel = deepLinkChannelId.value
    const [watchResponse, runResponse] = await Promise.all([
      api.getEvalWatch(),
      api.listEvalRuns(
        deepLinkChannel ? { channelId: deepLinkChannel, limit: EVAL_CHANNEL_HISTORY_LIMIT } : undefined
      ),
      reloadProbes(),
      loadChannels()
    ])
    watchConfig.value = watchResponse.watch
    watchForm.value = {
      enabled: watchConfig.value.enabled,
      suiteId: watchConfig.value.suiteId,
      interval: watchConfig.value.interval
    }
    busy.value = runResponse.busy

    if (!selectedSuiteId.value && suites.value.length) {
      selectedSuiteId.value = suites.value.find(suite => suite.cheap)?.id || suites.value[0].id
    }
    applyDeepLinkOrMemory()

    if (!deepLinkChannel && watchConfig.value.channelIds?.length) {
      selectedChannelIds.value = [...watchConfig.value.channelIds]
    }
    modelOverride.value = watchConfig.value.model || ''
    channelModels.value = { ...(watchConfig.value.channelModels || {}) }
    channelThinking.value = { ...(watchConfig.value.channelThinking || {}) }
    thinking.value = watchConfig.value.thinking || 'inherit'

    runHistory.value = runResponse.runs || []
    const deepLinkRun = deepLinkChannelId.value
      ? runHistory.value.find(run => run.channelIds.includes(deepLinkChannelId.value))
      : undefined
    const runId = deepLinkRun?.id || runResponse.currentRunId || runHistory.value[0]?.id
    if (runId) {
      const detail = await api.getEvalRun(runId)
      currentRun.value = detail.run
      if (runResponse.busy && runResponse.currentRunId === runId) {
        subscribeRun(runResponse.currentRunId)
      }
    }
    await focusDeepLinkedHistory()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

const selectRun = async (runId: string) => {
  if (!runId) return
  error.value = ''
  try {
    const detail = await api.getEvalRun(runId)
    currentRun.value = detail.run
    historyDrawer.value = false
    flashRunId.value = runId
    await nextTick()
    document
      .getElementById(`run-card-${runId}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    window.setTimeout(() => {
      if (flashRunId.value === runId) flashRunId.value = ''
    }, 1400)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

const startRun = async () => {
  starting.value = true
  error.value = ''
  try {
    const response = await api.startEvalRun({
      suiteId: selectedSuiteId.value,
      channelIds: selectedChannelIds.value,
      model: modelOverride.value,
      channelModels: channelModels.value,
      channelThinking: channelThinking.value,
      thinking: thinking.value,
      probeIds: activeProbeIds.value
    })
    currentRun.value = response.run
    busy.value = true
    subscribeRun(response.run.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    starting.value = false
  }
}

const cancelRun = async () => {
  if (!currentRun.value) return
  cancelling.value = true
  try {
    await api.cancelEvalRun(currentRun.value.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    cancelling.value = false
  }
}

const saveWatch = async () => {
  savingWatch.value = true
  error.value = ''
  try {
    const response = await api.putEvalWatch({
      enabled: watchForm.value.enabled,
      suiteId: watchForm.value.suiteId,
      interval: watchForm.value.interval,
      channelIds: selectedChannelIds.value,
      model: modelOverride.value,
      channelModels: channelModels.value,
      channelThinking: channelThinking.value,
      thinking: thinking.value
    })
    watchConfig.value = response.watch
    watchMenu.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    savingWatch.value = false
  }
}

const openProbeDrawerCreate = () => {
  probeDrawer.value = true
  nextTick(() => {
    probeManagerRef.value?.startCreate()
  })
}

watch(selectedSuiteId, () => {
  excludedProbeIds.value = []
})

watch([selectedChannelIds, selectedSuiteId], () => {
  preferences.setEvalLastSelection(selectedChannelIds.value, selectedSuiteId.value)
})

onMounted(() => {
  void reloadAll()
})

watch(deepLinkChannelId, (next, previous) => {
  if (next === previous) return
  void reloadAll()
})

onUnmounted(() => {
  stopRunStream()
})
</script>

<style scoped>
.eval-page {
  max-width: 1440px;
  margin: 0 auto;
}

.page-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

@media (max-width: 600px) {
  .page-heading {
    align-items: flex-start;
    flex-direction: column;
  }
}

.probe-chip {
  cursor: pointer;
  user-select: none;
}

.selector-probes-list {
  max-height: 400px;
  overflow-y: auto;
}

.selector-probe-card {
  border-color: rgba(var(--v-theme-on-surface), 0.1);
  transition: all 0.15s ease-in-out;
  cursor: pointer;
}

.selector-probe-card:hover {
  background: rgba(var(--v-theme-on-surface), 0.03);
  border-color: rgba(var(--v-theme-primary), 0.3);
}

.selector-probe-card--selected {
  background: rgba(var(--v-theme-primary), 0.08);
  border-color: rgba(var(--v-theme-primary), 0.5);
}

/* 批次概览元信息行 */
.run-overview-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}

.run-overview-meta span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

/* verdict 分布条 */
.tally-bar {
  display: flex;
  height: 22px;
  border-radius: 6px;
  overflow: hidden;
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.tally-segment {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 4px;
  transition: flex 0.3s;
}

.tally-segment-label {
  color: white;
  font-size: 0.72rem;
  font-weight: bold;
}

.tally-segment--success { background: #4caf50; }
.tally-segment--warning { background: #fb8c00; }
.tally-segment--error { background: #e53935; }
.tally-segment--grey { background: #9e9e9e; }

/* 图例 */
.tally-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 0.8rem;
}

.tally-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.tally-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.tally-dot--success { background: #4caf50; }
.tally-dot--warning { background: #fb8c00; }
.tally-dot--error { background: #e53935; }
.tally-dot--grey { background: #9e9e9e; }

/* 最近批次缩略条 */
.recent-runs {
  background: rgba(var(--v-theme-surface), 0.6);
}

.recent-runs-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 8px;
}

.recent-run-card {
  padding: 8px 10px;
  border-radius: 6px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
  background: rgba(var(--v-theme-surface), 0.4);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.recent-run-card:hover {
  background: rgba(var(--v-theme-on-surface), 0.04);
}

.recent-run-title {
  font-size: 0.8rem;
}

/* 批次卡片高亮动效 */
.run-card {
  transition: box-shadow 0.3s, border-color 0.3s;
}

.run-card--flash {
  border-color: rgba(var(--v-theme-primary), 0.8) !important;
  box-shadow: 0 0 0 2px rgba(var(--v-theme-primary), 0.3);
}

/* 历史抽屉 */
.history-scope-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 6px;
  background: rgba(var(--v-theme-primary), 0.08);
  border: 1px solid rgba(var(--v-theme-primary), 0.2);
}

.history-scope-copy {
  flex: 1;
  min-width: 0;
}

.history-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.history-card {
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.1);
  background: rgba(var(--v-theme-surface), 0.6);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 6px;
  transition: all 0.15s ease-in-out;
}

.history-card:hover {
  background: rgba(var(--v-theme-on-surface), 0.04);
}

.history-card--active {
  border-color: rgba(var(--v-theme-primary), 0.6);
  background: rgba(var(--v-theme-primary), 0.06);
}

.history-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.history-card-meta {
  display: flex;
  gap: 12px;
  font-size: 0.75rem;
  color: rgba(var(--v-theme-on-surface), 0.6);
}

.history-card-meta .meta-item {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.history-tally-bar {
  display: flex;
  height: 6px;
  border-radius: 3px;
  overflow: hidden;
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.history-tally-seg {
  min-width: 2px;
}

.history-tally-seg--success { background: #4caf50; }
.history-tally-seg--warning { background: #fb8c00; }
.history-tally-seg--error { background: #e53935; }
.history-tally-seg--grey { background: #9e9e9e; }

.history-card-time {
  font-size: 0.7rem;
  color: rgba(var(--v-theme-on-surface), 0.4);
  text-align: right;
}
</style>
