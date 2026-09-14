<template>
  <div class="blocked-logs-view">
    <header class="page-toolbar">
      <div class="page-title">
        <v-icon size="22" color="error">mdi-shield-alert</v-icon>
        <h1 class="text-h5 font-weight-bold">拦截记录</h1>
        <v-chip size="small" variant="tonal">{{ total }}</v-chip>
      </div>
      <div class="toolbar-actions">
        <v-btn variant="tonal" prepend-icon="mdi-refresh" :loading="loading" @click="loadLogs">刷新</v-btn>
        <v-btn
          color="error"
          variant="tonal"
          prepend-icon="mdi-delete"
          :disabled="total === 0"
          @click="clearDialog = true"
        >
          清空全部
        </v-btn>
      </div>
    </header>

    <section class="filters" aria-label="拦截记录筛选">
      <v-select v-model="apiType" :items="apiTypeItems" label="API 类型" density="compact" hide-details />
      <v-select v-model="blockType" :items="blockTypeItems" label="拦截类型" density="compact" hide-details />
      <v-select
        v-model="category"
        :items="categoryItems"
        label="类别"
        density="compact"
        hide-details
        :disabled="Boolean(blockType)"
      />
      <v-switch
        v-model="activeOnly"
        label="仅活跃会话"
        density="compact"
        hide-details
        color="primary"
        class="filter-switch"
      />
      <v-text-field v-model="fromTime" type="datetime-local" label="开始时间" density="compact" hide-details />
      <v-text-field v-model="toTime" type="datetime-local" label="结束时间" density="compact" hide-details />
      <v-btn color="primary" prepend-icon="mdi-magnify" class="filter-action" @click="applyFilters">应用</v-btn>
      <v-btn variant="text" class="filter-action" :disabled="!hasFilters" @click="resetFilters">重置</v-btn>
    </section>

    <v-alert v-if="error" type="error" variant="tonal" density="compact" closable @click:close="error = ''">
      {{ error }}
    </v-alert>

    <div class="table-frame">
      <v-table density="compact" class="blocked-logs-table">
        <thead>
          <tr>
            <th class="expand-column"><span class="sr-only">展开状态</span></th>
            <th>时间</th>
            <th>会话 / 请求</th>
            <th>入口</th>
            <th>类型 / 规则</th>
            <th>模型 / 渠道</th>
            <th>命中片段</th>
            <th class="action-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading">
            <td colspan="8" class="text-center text-medium-emphasis py-10">
              <v-progress-circular indeterminate size="24" width="2" color="primary" />
            </td>
          </tr>
          <tr v-else-if="groups.length === 0">
            <td colspan="8" class="empty-cell">
              <v-icon size="28" color="secondary">mdi-shield-alert</v-icon>
              <span>暂无拦截记录</span>
            </td>
          </tr>
          <template v-else>
            <template v-for="group in groups" :key="group.key">
              <tr
                class="group-row"
                tabindex="0"
                role="button"
                :aria-expanded="isExpanded(group.key)"
                :aria-label="groupAriaLabel(group)"
                @click="toggleGroup(group.key)"
                @keydown.enter.prevent="toggleGroup(group.key)"
                @keydown.space.prevent="toggleGroup(group.key)"
              >
                <td class="expand-cell">
                  <v-icon size="20" :icon="isExpanded(group.key) ? 'mdi-chevron-down' : 'mdi-chevron-right'" />
                </td>
                <td class="time-cell">{{ formatTime(group.latestTimestamp) }}</td>
                <td class="identity-cell" :title="group.conversationId || group.latestLog?.requestId || ''">
                  <div class="font-weight-medium">{{ groupIdentityLabel(group) }}</div>
                  <div class="text-caption text-medium-emphasis">
                    {{
                      group.conversationId
                        ? `${group.count} 条记录`
                        : group.latestLog?.requestId || '旧记录无请求 ID'
                    }}
                  </div>
                </td>
                <td>
                  <div class="chip-list">
                    <v-chip
                      v-for="value in group.apiTypes || []"
                      :key="value"
                      size="x-small"
                      :color="apiTypeColor(value)"
                      variant="tonal"
                    >
                      {{ apiTypeLabel(value) }}
                    </v-chip>
                  </div>
                </td>
                <td class="type-cell">
                  <div class="chip-list">
                    <v-chip
                      v-for="value in group.blockTypes || []"
                      :key="value"
                      size="x-small"
                      :color="blockTypeColor(value)"
                      variant="tonal"
                    >
                      {{ blockTypeLabel(value) }}
                    </v-chip>
                  </div>
                </td>
                <td class="metadata-cell">
                  <div class="font-weight-medium">{{ group.latestLog?.model || '--' }}</div>
                  <div class="text-caption text-medium-emphasis">{{ group.latestLog?.channelName || '--' }}</div>
                </td>
                <td class="snippet-cell" :title="group.latestLog?.promptSnippet || ''">
                  {{ snippetPreview(group.latestLog?.promptSnippet) || '--' }}
                </td>
                <td class="action-cell text-medium-emphasis">
                  {{ isExpanded(group.key) ? '收起' : '展开' }}
                </td>
              </tr>
              <tr
                v-if="isExpanded(group.key) && (groupDetail(group.key)?.loading || groupDetail(group.key)?.error)"
                class="detail-row detail-state-row"
              >
                <td class="detail-marker"><span aria-hidden="true"></span></td>
                <td colspan="7" class="detail-state-cell">
                  <v-progress-circular v-if="groupDetail(group.key)?.loading" indeterminate size="18" width="2" />
                  <v-alert
                    v-else-if="groupDetail(group.key)?.error"
                    type="error"
                    variant="text"
                    density="compact"
                    class="mb-0"
                  >
                    {{ groupDetail(group.key)?.error }}
                  </v-alert>
                </td>
              </tr>
              <template v-if="isExpanded(group.key) && groupDetail(group.key)">
                <tr
                  v-for="{ entry, repeatKey, repeatCount, repeated } in displayedEntries.get(group.key) || []"
                  :key="`${group.key}:${entry.id}`"
                  class="detail-row"
                  :class="{ 'repeat-row': repeated }"
                >
                  <td class="detail-marker">
                    <v-btn
                      v-if="repeatCount > 1"
                      :icon="expandedRepeats.has(repeatKey) ? 'mdi-chevron-down' : 'mdi-chevron-right'"
                      variant="text"
                      size="small"
                      :aria-expanded="expandedRepeats.has(repeatKey)"
                      :aria-label="`展开或收起已加载的 ${repeatCount} 条相同脱敏片段`"
                      @click.stop="toggleRepeat(repeatKey)"
                    />
                    <span v-else aria-hidden="true"></span>
                  </td>
                  <td class="time-cell">{{ formatTime(entry.timestamp) }}</td>
                  <td class="request-id-cell" :title="entry.requestId || ''">
                    <div>{{ entry.requestId || '--' }}</div>
                    <div class="text-caption text-medium-emphasis">
                      {{ repeatCount > 1 ? `相同片段 · 已加载 ${repeatCount} 条` : '请求 ID' }}
                    </div>
                  </td>
                  <td>
                    <v-chip size="x-small" :color="apiTypeColor(entry.apiType)" variant="tonal">
                      {{ apiTypeLabel(entry.apiType) }}
                    </v-chip>
                  </td>
                  <td class="type-cell">
                    <v-chip size="x-small" :color="blockTypeColor(entry.blockType)" variant="tonal">
                      {{ blockTypeLabel(entry.blockType) }}
                    </v-chip>
                    <v-chip
                      v-if="parseBlockedLogSnippet(entry.promptSnippet).mode === 'mask'"
                      size="x-small"
                      color="info"
                      variant="tonal"
                      class="ml-1"
                    >已脱敏</v-chip>
                    <div class="text-caption text-medium-emphasis mt-1">{{ ruleLabel(entry.ruleName) }}</div>
                  </td>
                  <td class="metadata-cell">
                    <div class="font-weight-medium">{{ entry.model || '--' }}</div>
                    <div class="text-caption text-medium-emphasis">{{ entry.channelName || '--' }}</div>
                  </td>
                  <td class="snippet-cell" :title="entry.command || entry.promptSnippet || ''">
                    <span v-if="isDangerousCmdType(entry.blockType) && entry.command" class="command-text">
                      {{ entry.command }}
                    </span>
                    <template v-else>{{ snippetPreview(entry.promptSnippet) || '--' }}</template>
                  </td>
                  <td class="action-cell">
                    <v-chip
                      v-if="isDangerousCmdType(entry.blockType) && entry.conversationId && conversationDecision(entry)"
                      size="x-small"
                      :color="conversationDecision(entry) === 'allow' ? 'success' : 'error'"
                      variant="tonal"
                      class="decision-chip"
                    >
                      {{ conversationDecision(entry) === 'allow' ? '已加白' : '已加黑' }}
                    </v-chip>
                    <v-tooltip v-if="isDangerousCmdType(entry.blockType) && entry.conversationId && entry.commandHash" text="放行本会话的同命令及执行上下文">
                      <template #activator="{ props }">
                        <v-btn
                          v-bind="props"
                          icon="mdi-check-circle-outline"
                          color="success"
                          variant="text"
                          size="small"
                          class="table-action"
                          :aria-label="`移入白名单 ${entry.id}`"
                          :disabled="updatingDecision"
                          @click.stop="applyGateDecision(entry, 'allow')"
                        />
                      </template>
                    </v-tooltip>
                    <v-tooltip v-if="isDangerousCmdType(entry.blockType) && entry.conversationId && entry.commandHash" text="拦截本会话的同命令及执行上下文">
                      <template #activator="{ props }">
                        <v-btn
                          v-bind="props"
                          icon="mdi-block-helper"
                          color="error"
                          variant="text"
                          size="small"
                          class="table-action"
                          :aria-label="`移入黑名单 ${entry.id}`"
                          :disabled="updatingDecision"
                          @click.stop="applyGateDecision(entry, 'block')"
                        />
                      </template>
                    </v-tooltip>
                    <v-tooltip
                      v-if="entry.conversationId && conversationDecision(entry)"
                      text="解除名单（恢复 AI 审查）"
                    >
                      <template #activator="{ props }">
                        <v-btn
                          v-bind="props"
                          icon="mdi-playlist-remove"
                          variant="text"
                          size="small"
                          class="table-action"
                          :aria-label="`解除名单 ${entry.id}`"
                          :disabled="updatingDecision"
                          @click.stop="removeGateDecision(entry)"
                        />
                      </template>
                    </v-tooltip>
                    <v-tooltip text="查看详情">
                      <template #activator="{ props }">
                        <v-btn
                          v-bind="props"
                          icon="mdi-open-in-new"
                          variant="text"
                          size="small"
                          class="table-action"
                          :aria-label="`查看记录 ${entry.id}`"
                          @click.stop="openDetail(entry.id)"
                        />
                      </template>
                    </v-tooltip>
                    <v-tooltip text="删除记录">
                      <template #activator="{ props }">
                        <v-btn
                          v-bind="props"
                          icon="mdi-delete"
                          color="error"
                          variant="text"
                          size="small"
                          class="table-action"
                          :aria-label="`删除记录 ${entry.id}`"
                          @click.stop="openDelete(entry)"
                        />
                      </template>
                    </v-tooltip>
                  </td>
                </tr>
                <tr v-if="hasMoreEntries(group.key)" class="detail-row detail-more-row">
                  <td class="detail-marker"><span aria-hidden="true"></span></td>
                  <td colspan="7" class="detail-more-cell">
                    <v-btn
                      variant="tonal"
                      size="small"
                      :loading="groupDetail(group.key)?.loading"
                      @click.stop="loadMoreEntries(group.key)"
                    >
                      加载更多（{{ loadedCount(group.key) }} / {{ group.count }}）
                    </v-btn>
                  </td>
                </tr>
              </template>
            </template>
          </template>
        </tbody>
      </v-table>
    </div>

    <footer class="pagination-bar">
      <div class="text-body-2 text-medium-emphasis">{{ totalGroups }} 个会话 · {{ total }} 条记录</div>
      <v-pagination
        v-model="page"
        :length="pageCount"
        :total-visible="5"
        density="comfortable"
        aria-label="拦截记录会话分页"
      />
      <v-select
        v-model="pageSize"
        :items="pageSizeItems"
        label="每页"
        density="compact"
        hide-details
        class="page-size-select"
      />
    </footer>

    <v-dialog v-model="detailDialog" max-width="680">
      <v-card>
        <v-card-title class="dialog-title">
          <span>拦截详情</span>
          <v-chip v-if="selectedLog" size="small" :color="blockTypeColor(selectedLog.blockType)" variant="tonal">
            {{ blockTypeLabel(selectedLog.blockType) }}
          </v-chip>
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-progress-linear v-if="detailLoading" indeterminate color="primary" />
          <dl v-else-if="selectedLog" class="detail-grid">
            <dt>时间</dt>
            <dd>{{ formatTime(selectedLog.timestamp) }}</dd>
            <dt>API 类型</dt>
            <dd>{{ apiTypeLabel(selectedLog.apiType) }}</dd>
            <dt>规则</dt>
            <dd>{{ ruleLabel(selectedLog.ruleName) }}</dd>
            <dt>模型</dt>
            <dd>{{ selectedLog.model || '--' }}</dd>
            <dt>渠道</dt>
            <dd>{{ selectedLog.channelName || '--' }}</dd>
            <dt>会话 ID</dt>
            <dd class="break-text">{{ selectedLog.conversationId || '--' }}</dd>
            <dt>请求 ID</dt>
            <dd class="break-text">{{ selectedLog.requestId || '--' }}</dd>
            <template v-if="selectedLog.aiReview">
              <dt>AI 复核模型</dt>
              <dd>{{ selectedLog.aiReview.model || '--' }}</dd>
              <dt>AI 复核渠道</dt>
              <dd>{{ selectedLog.aiReview.channelName || '--' }}</dd>
              <dt>AI 复核原因</dt>
              <dd class="break-text">{{ selectedLog.aiReview.reason }}</dd>
              <dt>AI 复核来源</dt>
              <dd>{{ selectedLog.aiReview.reused ? '复用同会话结论' : '本次复核' }}</dd>
              <dt>AI 复核日志</dt>
              <dd>
                <v-btn
                  v-if="selectedLog.aiReview.requestId"
                  size="small"
                  variant="tonal"
                  :to="{ path: '/logs', query: { tab: 'system', id: selectedLog.aiReview.requestId } }"
                >查看复核请求与响应</v-btn>
              </dd>
            </template>
            <template v-if="isDangerousCmdType(selectedLog.blockType)">
              <dt>命中的命令</dt>
              <dd>
                <pre class="snippet-detail">{{ selectedLog.command || snippetPreview(selectedLog.promptSnippet) || '--' }}</pre>
              </dd>
            </template>
            <dt>命中片段</dt>
            <dd>
              <pre class="snippet-detail">{{ selectedLog.promptSnippet || '--' }}</pre>
            </dd>
          </dl>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="detailDialog = false">关闭</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="deleteDialog" max-width="440">
      <v-card>
        <v-card-title>删除拦截记录</v-card-title>
        <v-card-text>确定删除这条拦截记录吗？</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="deleteDialog = false">取消</v-btn>
          <v-btn color="error" :loading="deleting" @click="confirmDelete">删除</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="clearDialog" max-width="440">
      <v-card>
        <v-card-title>清空拦截记录</v-card-title>
        <v-card-text>确定清空全部 {{ total }} 条拦截记录吗？此操作无法撤销。</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="clearDialog = false">取消</v-btn>
          <v-btn color="error" :loading="deleting" @click="confirmClear">清空全部</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import {
  blockedLogSnippetPreview as snippetPreview,
  groupRepeatedBlockedLogs,
  parseBlockedLogSnippet
} from '@/utils/blockedLogPresentation'
import {
  api,
  type BlockedLogEntry,
  type BlockedLogGroup,
  type BlockedLogType,
  type ContentSafetyAPIType
} from '@/services/api'

const apiTypeItems: Array<{ title: string; value: ContentSafetyAPIType | '' }> = [
  { title: '全部入口', value: '' },
  { title: 'Messages', value: 'messages' },
  { title: 'Responses', value: 'responses' },
  { title: 'Chat', value: 'chat' },
  { title: 'Gemini', value: 'gemini' }
]

const blockTypeItems: Array<{ title: string; value: BlockedLogType | '' }> = [
  { title: '全部类型', value: '' },
  { title: '敏感词', value: 'sensitive_word' },
  { title: '个人信息', value: 'sensitive_info' },
  { title: '凭据', value: 'credential' },
  { title: '危险命令', value: 'dangerous_cmd' },
  { title: '危险命令（AI 闸门拦截）', value: 'dangerous_cmd_ai' },
  { title: '危险命令（AI 闸门放行）', value: 'dangerous_cmd_allow' },
  { title: '白名单放行', value: 'whitelist' },
  { title: '放行字面量', value: 'allowlist' },
  { title: '自定义规则脱敏', value: 'custom_rule' },
  { title: '还原未命中', value: 'restore_miss' }
]

const pageSizeItems = [20, 50, 100]
const entryPageSize = 50
const apiType = ref<ContentSafetyAPIType | ''>('')
const blockType = ref<BlockedLogType | ''>('')
const category = ref<'dangerous_cmd' | ''>('')
const activeOnly = ref(false)
const fromTime = ref('')
const toTime = ref('')

const categoryItems: Array<{ title: string; value: 'dangerous_cmd' | '' }> = [
  { title: '全部类别', value: '' },
  { title: '危险命令相关', value: 'dangerous_cmd' }
]
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const totalGroups = ref(0)
const groups = ref<BlockedLogGroup[]>([])
const expandedGroups = ref<Set<string>>(new Set())
const expandedRepeats = ref<Set<string>>(new Set())

interface GroupDetailState {
  logs: BlockedLogEntry[]
  loading: boolean
  error: string
  loadedAll: boolean
}

const groupDetails = reactive(new Map<string, GroupDetailState>())

interface DisplayedLogEntry {
  entry: BlockedLogEntry
  repeatKey: string
  repeatCount: number
  repeated: boolean
}

// 仅折叠已加载的相同审计片段；原始分页游标与逐条操作仍使用完整记录。
const displayedEntries = computed(() => {
  const result = new Map<string, DisplayedLogEntry[]>()
  for (const [key, detail] of groupDetails) {
    const rows: DisplayedLogEntry[] = []
    for (const group of groupRepeatedBlockedLogs(detail.logs)) {
      const entry = group.entry
      rows.push({ entry, repeatKey: group.key, repeatCount: group.entries.length, repeated: false })
      if (expandedRepeats.value.has(group.key)) {
        for (const duplicate of group.entries.slice(1)) {
          rows.push({ entry: duplicate, repeatKey: group.key, repeatCount: 0, repeated: true })
        }
      }
    }
    result.set(key, rows)
  }
  return result
})

const toggleRepeat = (key: string) => {
  const next = new Set(expandedRepeats.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedRepeats.value = next
}
const loading = ref(false)
const deleting = ref(false)
const error = ref('')
const detailDialog = ref(false)
const detailLoading = ref(false)
const selectedLog = ref<BlockedLogEntry | null>(null)
const deleteDialog = ref(false)
const deletingLog = ref<BlockedLogEntry | null>(null)
const clearDialog = ref(false)

const pageCount = computed(() => Math.max(1, Math.ceil(totalGroups.value / pageSize.value)))
const hasFilters = computed(() =>
  Boolean(apiType.value || blockType.value || category.value || activeOnly.value || fromTime.value || toTime.value)
)

const asRFC3339 = (value: string) => (value ? new Date(value).toISOString() : '')

const loadLogs = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await api.getBlockedLogs({
      apiType: apiType.value,
      blockType: blockType.value,
      category: category.value,
      activeOnly: activeOnly.value,
      from: asRFC3339(fromTime.value),
      to: asRFC3339(toTime.value),
      page: page.value,
      pageSize: pageSize.value,
      groupBy: 'conversation'
    })
    groups.value = response.groups || []
    expandedRepeats.value = new Set()
    total.value = response.total || 0
    totalGroups.value = response.totalGroups || 0
    const visibleKeys = new Set(groups.value.map(group => group.key))
    expandedGroups.value = new Set([...expandedGroups.value].filter(key => visibleKeys.has(key)))
    for (const key of [...groupDetails.keys()]) {
      if (!visibleKeys.has(key)) groupDetails.delete(key)
    }
    // 筛选条件变化后组内明细可能不再命中，重置为待加载状态。
    if (expandedGroups.value.size > 0) {
      for (const key of expandedGroups.value) {
        groupDetails.set(key, { logs: [], loading: false, error: '', loadedAll: false })
        void loadEntriesPage(key, true)
      }
    }
    if (page.value > pageCount.value) {
      page.value = pageCount.value
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载拦截记录失败'
    groups.value = []
    total.value = 0
    totalGroups.value = 0
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  if (fromTime.value && toTime.value && new Date(fromTime.value) > new Date(toTime.value)) {
    error.value = '开始时间不能晚于结束时间'
    return
  }
  if (page.value === 1) loadLogs()
  else page.value = 1
}

const resetFilters = () => {
  apiType.value = ''
  blockType.value = ''
  category.value = ''
  activeOnly.value = false
  fromTime.value = ''
  toTime.value = ''
  applyFilters()
}

const openDetail = async (id: number) => {
  detailDialog.value = true
  detailLoading.value = true
  selectedLog.value = null
  try {
    selectedLog.value = await api.getBlockedLog(id)
  } catch (err) {
    detailDialog.value = false
    error.value = err instanceof Error ? err.message : '加载拦截详情失败'
  } finally {
    detailLoading.value = false
  }
}

const openDelete = (entry: BlockedLogEntry) => {
  deletingLog.value = entry
  deleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingLog.value) return
  deleting.value = true
  error.value = ''
  try {
    await api.deleteBlockedLog(deletingLog.value.id)
    deleteDialog.value = false
    deletingLog.value = null
    await loadLogs()
  } catch (err) {
    error.value = err instanceof Error ? err.message : '删除拦截记录失败'
  } finally {
    deleting.value = false
  }
}

const confirmClear = async () => {
  deleting.value = true
  error.value = ''
  try {
    await api.clearBlockedLogs()
    clearDialog.value = false
    if (page.value === 1) await loadLogs()
    else page.value = 1
  } catch (err) {
    error.value = err instanceof Error ? err.message : '清空拦截记录失败'
  } finally {
    deleting.value = false
  }
}

const apiTypeLabel = (value: ContentSafetyAPIType) =>
  ({
    messages: 'Messages',
    responses: 'Responses',
    chat: 'Chat',
    gemini: 'Gemini'
  })[value]

const apiTypeColor = (value: ContentSafetyAPIType) =>
  ({
    messages: 'primary',
    responses: 'secondary',
    chat: 'info',
    gemini: 'success'
  })[value]

const blockTypeLabel = (value: BlockedLogType) =>
  ({
    sensitive_word: '敏感词',
    sensitive_info: '个人信息',
    credential: '凭据',
    dangerous_cmd: '危险命令',
    whitelist: '白名单放行',
    dangerous_cmd_ai: '危险命令·AI 拦截',
    dangerous_cmd_allow: '危险命令·AI 放行',
    allowlist: '放行字面量',
    custom_rule: '自定义规则脱敏',
    restore_miss: '还原未命中'
  })[value]

const blockTypeColor = (value: BlockedLogType) =>
  ({
    sensitive_word: 'error',
    sensitive_info: 'warning',
    credential: 'error',
    dangerous_cmd: 'error',
    whitelist: 'success',
    dangerous_cmd_ai: 'error',
    dangerous_cmd_allow: 'success',
    allowlist: 'success',
    custom_rule: 'warning',
    restore_miss: 'warning'
  })[value]

const ruleLabels: Record<string, string> = {
  pornography: '色情',
  gambling: '赌博',
  drugs: '毒品',
  violence_terror: '暴力与恐怖',
  political: '政治敏感',
  illegal_crime: '违法犯罪',
  custom: '自定义敏感词',
  api_key: 'API Key',
  named_secret: '密码与命名密钥',
  private_key: 'PEM 私钥',
  connection_string: '数据库连接串',
  high_entropy: '高熵环境变量值',
  phone: '手机号',
  id_card: '身份证号',
  email: '邮箱',
  ip_address: 'IP 地址',
  destructive: '破坏性命令',
  download_execute: '远程下载执行',
  reverse_shell: '反弹 Shell',
  privilege_escalation: '权限提升',
  environment_tampering: '环境变量篡改'
}

const ruleLabel = (value?: string) => (value ? ruleLabels[value] || value : '--')

const groupIdentityLabel = (group: BlockedLogGroup) => {
  if (group.conversationId) return group.conversationId
  if (group.latestLog?.requestId) return '旧记录（按请求保留）'
  return '旧记录（未关联）'
}

const isExpanded = (key: string) => expandedGroups.value.has(key)

const groupDetail = (key: string) => groupDetails.get(key)

const loadedCount = (key: string) => groupDetails.get(key)?.logs.length || 0

const hasMoreEntries = (key: string) => {
  const detail = groupDetails.get(key)
  return Boolean(detail && !detail.loadedAll)
}

const loadEntriesPage = async (key: string, reset: boolean) => {
  if (!isExpanded(key) || !groupDetails.has(key)) return
  const detail = groupDetails.get(key)!
  if (detail.loading) return
  detail.loading = true
  detail.error = ''
  const nextPage = reset ? 1 : Math.floor(detail.logs.length / entryPageSize) + 1
  try {
    const response = await api.getBlockedLogGroupEntries({
      apiType: apiType.value,
      blockType: blockType.value,
      category: category.value,
      activeOnly: activeOnly.value,
      from: asRFC3339(fromTime.value),
      to: asRFC3339(toTime.value),
      page: nextPage,
      pageSize: entryPageSize,
      groupKey: key
    })
    if (reset) detail.logs = response.logs || []
    else detail.logs.push(...(response.logs || []))
    detail.loadedAll = detail.logs.length >= (response.total || 0)
  } catch (err) {
    detail.error = err instanceof Error ? err.message : '加载会话明细失败'
  } finally {
    detail.loading = false
  }
}

const toggleGroup = (key: string) => {
  const next = new Set(expandedGroups.value)
  let shouldLoad = false
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
    if (!groupDetails.has(key)) {
      groupDetails.set(key, { logs: [], loading: false, error: '', loadedAll: false })
      shouldLoad = true
    }
  }
  expandedGroups.value = next
  if (shouldLoad) void loadEntriesPage(key, true)
}

const loadMoreEntries = (key: string) => {
  void loadEntriesPage(key, false)
}

const groupAriaLabel = (group: BlockedLogGroup) =>
  `${groupIdentityLabel(group)}，${group.count} 条记录，${isExpanded(group.key) ? '已展开' : '已折叠'}`

// 命令名单按会话与完整命令身份匹配，旧记录没有身份时不能据展示片段授权。

const conversationDecisions = ref<Map<string, 'allow' | 'block'>>(new Map())
const updatingDecision = ref(false)

const isDangerousCmdType = (value: BlockedLogType) =>
  value === 'dangerous_cmd' || value === 'dangerous_cmd_ai' || value === 'dangerous_cmd_allow'

const commandDecisionKey = (conversationId: string, commandHash: string) => `${conversationId}\0${commandHash}`

const conversationDecision = (entry: BlockedLogEntry): 'allow' | 'block' | undefined =>
  entry.conversationId && entry.commandHash
    ? conversationDecisions.value.get(commandDecisionKey(entry.conversationId, entry.commandHash))
    : undefined

const loadDecisions = async () => {
  try {
    const response = await api.listGateDecisions()
    const map = new Map<string, 'allow' | 'block'>()
    for (const decision of response.decisions || []) {
      if (decision.commandHash) {
        map.set(commandDecisionKey(decision.conversationId, decision.commandHash), decision.decision)
      }
    }
    conversationDecisions.value = map
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载会话名单失败'
  }
}

const applyGateDecision = async (entry: BlockedLogEntry, decision: 'allow' | 'block') => {
  if (!entry.conversationId || !entry.commandHash) return
  updatingDecision.value = true
  error.value = ''
  try {
    await api.addGateDecision({
      conversationId: entry.conversationId,
      decision,
      rule: entry.ruleName,
      command: entry.command,
      commandHash: entry.commandHash
    })
    await loadDecisions()
  } catch (err) {
    error.value = err instanceof Error ? err.message : '写入会话名单失败'
  } finally {
    updatingDecision.value = false
  }
}

const removeGateDecision = async (entry: BlockedLogEntry) => {
  if (!entry.conversationId || !entry.commandHash) return
  updatingDecision.value = true
  error.value = ''
  try {
    await api.removeGateDecisions(entry.conversationId, entry.commandHash)
    await loadDecisions()
  } catch (err) {
    error.value = err instanceof Error ? err.message : '解除会话名单失败'
  } finally {
    updatingDecision.value = false
  }
}

const formatTime = (value?: string) => {
  if (!value) return '--'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

watch(page, loadLogs)
watch(pageSize, () => {
  if (page.value === 1) loadLogs()
  else page.value = 1
})

onMounted(() => {
  void loadLogs()
  void loadDecisions()
})
</script>

<style scoped>
.blocked-logs-view {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1480px;
  margin: 0 auto;
}

.page-toolbar,
.page-title,
.toolbar-actions,
.pagination-bar,
.dialog-title,
.action-cell {
  display: flex;
  align-items: center;
}

.page-toolbar {
  justify-content: space-between;
  gap: 16px;
  padding: 16px 0 4px;
}

.page-title,
.toolbar-actions,
.dialog-title,
.action-cell {
  gap: 10px;
}

.filters {
  display: grid;
  grid-template-columns:
    minmax(130px, 0.8fr) minmax(140px, 0.9fr) minmax(130px, 0.8fr) auto
    minmax(180px, 1fr) minmax(180px, 1fr) auto auto;
  gap: 10px;
  align-items: center;
  padding: 14px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: rgb(var(--v-theme-surface));
}

.filter-switch {
  white-space: nowrap;
}

.filter-action,
.table-action {
  min-height: 44px;
}

.table-action {
  min-width: 44px;
}

.table-frame {
  overflow-x: auto;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: rgb(var(--v-theme-surface));
}

.blocked-logs-table {
  min-width: 1060px;
}

.blocked-logs-table :deep(th) {
  color: rgb(var(--v-theme-on-surface));
  background: rgba(var(--v-theme-primary), 0.07);
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.blocked-logs-table :deep(td) {
  height: 58px;
  font-size: 13px;
  vertical-align: middle;
}

.group-row {
  cursor: pointer;
  outline: none;
}

.group-row:hover,
.group-row:focus-visible {
  background: rgba(var(--v-theme-primary), 0.055);
}

.group-row:focus-visible {
  box-shadow: inset 3px 0 rgb(var(--v-theme-primary));
}

.detail-row {
  background: rgba(var(--v-theme-surface-variant), 0.18);
}

.repeat-row .request-id-cell {
  padding-left: 24px;
}

.detail-state-row .detail-state-cell {
  height: 44px;
  padding-left: 8px !important;
}

.detail-more-row .detail-more-cell {
  height: 48px;
  padding-left: 8px !important;
}

.expand-column,
.expand-cell,
.detail-marker {
  width: 44px;
  min-width: 44px;
  padding-right: 0 !important;
  text-align: center;
}

.detail-marker span {
  display: inline-block;
  width: 12px;
  height: 20px;
  border-bottom: 1px solid rgba(var(--v-border-color), 0.7);
  border-left: 1px solid rgba(var(--v-border-color), 0.7);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.time-cell {
  width: 168px;
  white-space: nowrap;
}

.type-cell,
.metadata-cell {
  min-width: 150px;
}

.identity-cell,
.request-id-cell {
  max-width: 210px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.request-id-cell {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}

.snippet-cell {
  max-width: 340px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.command-text {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}

.action-column,
.action-cell {
  width: 208px;
}

.action-cell {
  flex-wrap: wrap;
  justify-content: flex-end;
}

.decision-chip {
  margin-right: 4px;
}

.empty-cell {
  height: 140px !important;
  text-align: center;
  color: rgb(var(--v-theme-on-surface-variant));
}

.empty-cell > * + * {
  margin-left: 10px;
}

.pagination-bar {
  display: grid;
  grid-template-columns: minmax(130px, 1fr) auto minmax(100px, 1fr);
  gap: 16px;
  min-height: 52px;
}

.page-size-select {
  width: 104px;
  justify-self: end;
}

.dialog-title {
  justify-content: space-between;
}

.detail-grid {
  display: grid;
  grid-template-columns: 92px minmax(0, 1fr);
  gap: 12px 18px;
  margin: 0;
}

.detail-grid dt {
  color: rgb(var(--v-theme-on-surface-variant));
  font-size: 13px;
}

.detail-grid dd {
  min-width: 0;
  margin: 0;
}

.break-text,
.snippet-detail {
  overflow-wrap: anywhere;
}

.snippet-detail {
  max-height: 240px;
  margin: 0;
  padding: 12px;
  overflow: auto;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 4px;
  background: rgba(var(--v-theme-surface-variant), 0.35);
  font: inherit;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  white-space: pre-wrap;
}

@media (max-width: 1100px) {
  .filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 700px) {
  .page-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .toolbar-actions {
    width: 100%;
    flex-wrap: wrap;
  }

  .filters {
    grid-template-columns: 1fr;
  }

  .pagination-bar {
    grid-template-columns: 1fr;
    justify-items: center;
  }

  .page-size-select {
    justify-self: center;
  }

  .detail-grid {
    grid-template-columns: 1fr;
    gap: 4px;
  }

  .detail-grid dd + dt {
    margin-top: 10px;
  }
}
</style>
