<template>
  <div class="conversation-view">
    <PageHeader title="对话" description="查看会话状态、真实输入与请求链路，追踪一次对话使用的渠道和模型。" />
    <UiCard class="conversation-toolbar mb-4 p-4" rounded="lg">
      <div class="conversation-filters">
        <UiInput
          v-model="searchText"
          label="搜索对话"
          variant="outlined"
          density="compact"
          prepend-inner-icon="mdi-magnify"
          hide-details
          class="search-field"
          @keyup.enter="loadConversations()"
        />

        <UiSelect
          v-model="kindFilter"
          :items="kindItems"
          label="类型"
          variant="outlined"
          density="compact"
          hide-details
          class="filter-field"
          clearable
        />

        <UiSelect
          v-model="statusFilter"
          :items="statusItems"
          label="状态"
          variant="outlined"
          density="compact"
          hide-details
          class="filter-field"
        />

        <UiSelect
          v-model="orderMode"
          :items="orderItems"
          label="排序"
          variant="outlined"
          density="compact"
          hide-details
          class="order-field"
          prepend-inner-icon="mdi-sort"
        />

        <UiButton color="primary" prepend-icon="mdi-refresh" variant="tonal" :loading="loading" @click="loadConversations()">
          刷新
        </UiButton>

        <UiButton color="error" prepend-icon="mdi-delete" variant="tonal" :disabled="conversations.length === 0" @click="openDeleteAllDialog">
          清空全部
        </UiButton>

        <div class="conversation-count text-sm text-muted-foreground">
          本页 {{ conversations.length }} 条 · 共 {{ totalConversations }} 条
        </div>
      </div>
    </UiCard>

    <UiAlert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-4">{{ loadError }}<span v-if="conversations.length">；下方保留上次成功加载的结果。</span></UiAlert>

    <section aria-label="对话列表">
      <UiDataTable
        v-model:page="page"
        v-model:items-per-page="pageSize"
        server
        :items-length="totalConversations"
        :items-per-page-options="[10, 25, 50, 100]"
        :headers="headers"
        :items="conversations"
        :loading="loading"
        item-key="id"
        density="compact"
        class="conversation-table"
      >
        <template #[`item.identity`]="{ item }">
          <div class="conversation-identity">
            <button type="button" class="conversation-title" title="查看对话详情" @click="openDetailDialog(item)">
              {{ displayConversationName(item) }}
            </button>
            <div class="conversation-id-row">
              <span class="conversation-id font-mono" :title="item.id">{{ formatConversationID(item.id) }}</span>
              <UiButton
                icon="mdi-content-copy"
                size="x-small"
                variant="text"
                color="primary"
                :aria-label="`复制对话 ID ${item.id}`"
                title="复制完整 ID"
                @click="copyText(item.id)"
              />
            </div>
          </div>
        </template>

        <template #[`item.prompt`]="{ item }">
          <div v-if="conversationPrompts(item).length > 0" class="conversation-prompts clickable-prompt" title="点击查看对话详情" @click="openDetailDialog(item)">
            <div class="conversation-prompt-text">{{ conversationPrompts(item)[0] }}</div>
            <div v-if="conversationPrompts(item).length > 1" class="prompt-count">
              共 {{ conversationPrompts(item).length }} 条真实输入
            </div>
          </div>
          <span v-else class="text-muted-foreground">--</span>
        </template>

        <template #[`item.protocol`]="{ item }">
          <div class="protocol-model">
            <UiBadge size="x-small" variant="tonal" class="uppercase">{{ item.apiKind }}</UiBadge>
            <span class="model-name">{{ formatModelChain(item) }}</span>
          </div>
        </template>

        <template #[`item.activity`]="{ item }">
          <div v-if="item.isSending" class="activity-active">
            <UiBadge
              size="small"
              color="success"
              variant="tonal"
              prepend-icon="mdi-loading"
              class="sending-chip"
            >
              <template v-if="(item.activeRequests || 0) > 1">{{ item.activeRequests }} 个请求进行中</template>
              <template v-else>请求进行中</template>
            </UiBadge>
            <span v-if="formatDurationSince(item.lastRequestAt)" class="text-xs text-muted-foreground">
              最近发起于 {{ formatDurationSince(item.lastRequestAt) }}前
            </span>
          </div>
          <span v-else class="text-xs text-muted-foreground">
            {{ formatIdleText(item) }}
          </span>
        </template>

        <template #[`item.requests`]="{ item }">
          <div class="request-stats">
            <span><strong>{{ item.requestCount }}</strong> 请求</span>
            <span :class="item.errorCount > 0 ? 'text-destructive' : 'text-muted-foreground'">
              <strong>{{ item.errorCount }}</strong> 错误
            </span>
          </div>
        </template>

        <template #[`item.routing`]="{ item }">
          <div class="routing-summary">
            <div v-if="item.lastResolved" class="routing-line">
              <span class="routing-label">最近</span>
              <span>#{{ item.lastResolved.channelIndex }} {{ item.lastResolved.channelName || item.lastResolved.kind }}</span>
            </div>
            <div v-else class="text-muted-foreground">尚未解析</div>
            <div v-if="item.routeOverride" class="routing-line text-primary">
              <span class="routing-label">固定</span>
              <span>#{{ item.routeOverride.channelIndex }} {{ item.routeOverride.channelName || item.routeOverride.kind }}</span>
            </div>
            <div v-else class="text-xs text-muted-foreground">默认调度</div>
          </div>
        </template>

        <template #[`item.timestamps`]="{ item }">
          <div class="timestamp-summary">
            <span>{{ formatDateTime(item.firstSeenAt) }}</span>
            <span class="text-xs text-muted-foreground">活动 {{ formatDateTime(item.lastSeenAt) }}</span>
          </div>
        </template>

        <template #[`item.actions`]="{ item }">
          <div class="conversation-actions">
            <UiButton icon="mdi-history" size="small" variant="text" title="查看上下文与轨迹" aria-label="查看上下文与轨迹" @click="openTraceDialog(item)" />
            <UiButton icon="mdi-pencil" size="small" variant="text" title="命名" aria-label="命名对话" @click="openNameDialog(item)" />
            <UiButton icon="mdi-swap-horizontal" size="small" variant="text" title="固定渠道" aria-label="固定渠道" @click="openRouteDialog(item)" />
            <UiButton
              v-if="item.routeOverride"
              icon="mdi-close"
              size="small"
              variant="text"
              color="warning"
              title="清除固定渠道"
              aria-label="清除固定渠道"
              @click="clearRoute(item)"
            />
            <UiButton icon="mdi-delete" size="small" variant="text" color="error" title="删除" aria-label="删除对话" @click="openDeleteDialog(item)" />
          </div>
        </template>
      </UiDataTable>
    </section>

    <AppDialog v-model="routeDialog" title="设置固定渠道" size="md">
      <div class="mb-3 text-sm text-muted-foreground">
        {{ editingConversation?.id }}
      </div>
      <div class="mb-3">
        <UiBadge size="small" variant="tonal">{{ editingConversation?.apiKind }}</UiBadge>
      </div>
      <UiSelect
        v-model="selectedChannelId"
        :items="routeChannelItems"
        label="渠道"
        :error-messages="routeOptionsError"
        variant="outlined"
        density="compact"
        class="mt-3"
      />
      <template #actions>
        <UiButton variant="text" @click="routeDialog = false">取消</UiButton>
        <UiButton color="primary" :loading="saving" @click="saveRoute">保存</UiButton>
      </template>
    </AppDialog>

    <AppDialog v-model="nameDialog" title="给对话命名" size="md">
      <div class="text-sm text-muted-foreground mb-3">{{ editingNameConversation?.id }}</div>
      <UiInput
        v-model="conversationName"
        label="对话名称"
        maxlength="120"
        counter="120"
        variant="outlined"
        density="compact"
        autofocus
        @keyup.enter="saveName"
      />
      <template #actions>
        <UiButton variant="text" @click="nameDialog = false">取消</UiButton>
        <UiButton color="primary" :loading="saving" @click="saveName">保存</UiButton>
      </template>
    </AppDialog>

    <ConfirmDialog
      v-model="deleteDialog"
      title="删除对话"
      :message="`确定删除“${deletingConversation?.name || deletingConversation?.firstPrompt || deletingConversation?.id}”吗？此操作会删除该对话的提示词、图片指纹、图片理解结果、固定渠道设置和 Responses 持久化会话链，且无法恢复。`"
      confirm-text="删除"
      :loading="saving"
      @confirm="deleteConversation"
    />

    <AppDialog v-model="deleteAllDialog" title="清空全部对话" size="md">
      此操作会删除全部对话、图片理解结果、固定渠道设置和 Responses 持久化会话链，且无法恢复。
      <UiInput
        v-model="deleteAllConfirmation"
        label="输入 清空全部 以确认"
        variant="outlined"
        density="compact"
        class="mt-4"
        autofocus
        @keyup.enter="deleteAllConversations"
      />
      <template #actions>
        <UiButton variant="text" @click="deleteAllDialog = false">取消</UiButton>
        <UiButton color="error" :disabled="deleteAllConfirmation !== '清空全部'" :loading="saving" @click="deleteAllConversations">清空全部</UiButton>
      </template>
    </AppDialog>

    <!-- 详情 Dialog -->
    <AppDialog v-model="detailDialog" :title="selectedConversation ? `对话详情 · ${displayConversationName(selectedConversation)}` : '对话详情'" size="xl">
          <!-- ID & Meta -->
          <div class="flex flex-wrap items-center justify-between mb-4 gap-2">
            <div class="flex items-center gap-2">
              <div>
                <div class="text-xs text-muted-foreground">内部对话 ID</div>
                <span class="text-base font-medium font-bold font-mono conversation-detail-id">{{ selectedConversation?.id }}</span>
              </div>
              <UiButton icon="mdi-content-copy" size="x-small" variant="text" color="primary" title="复制ID" @click="copyText(selectedConversation?.id || '')" />
            </div>
            <div class="flex items-center gap-2">
              <UiBadge size="small" color="primary" variant="tonal" class="uppercase">{{ selectedConversation?.apiKind }}</UiBadge>
              <UiBadge v-if="selectedConversation?.isSending" size="small" color="success" variant="tonal" prepend-icon="mdi-loading" class="sending-chip">
                <template v-if="(selectedConversation?.activeRequests || 0) > 1">{{ selectedConversation?.activeRequests }} 个请求进行中</template>
                <template v-else>请求进行中</template>
              </UiBadge>
              <UiBadge v-else size="small" color="grey" variant="tonal">已完成/空闲</UiBadge>
            </div>
          </div>

          <form class="flex items-start flex-wrap gap-2 mb-4" @submit.prevent="saveDetailName">
            <UiInput v-model="detailName" label="对话名称" placeholder="为对话命名" maxlength="120" counter="120" variant="outlined" density="compact" :disabled="savingName" />
            <UiButton type="submit" color="primary" :loading="savingName" :disabled="!detailName.trim() || detailName.trim() === (selectedConversation?.name || '')">保存名称</UiButton>
            <UiButton variant="tonal" prepend-icon="mdi-history" @click="traceDialog = true">查看上下文与轨迹</UiButton>
          </form>

          <!-- Grid Info -->
          <div class="grid grid-cols-12 gap-2 mb-2" >
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <div class="info-card">
                <span class="info-label">请求模型</span>
                <span class="info-value font-mono">{{ selectedConversation?.lastModel || '--' }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <div class="info-card">
                <span class="info-label">实际路由模型</span>
                <span class="info-value font-mono">{{ selectedConversation?.lastResolvedModel || '--' }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <div class="info-card">
                <span class="info-label">首次看到时间</span>
                <span class="info-value text-xs">{{ selectedConversation?.firstSeenAt ? formatDateTime(selectedConversation.firstSeenAt) : '--' }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-6"  >
              <div class="info-card">
                <span class="info-label">最近活动时间</span>
                <span class="info-value text-xs">{{ selectedConversation?.lastSeenAt ? formatDateTime(selectedConversation.lastSeenAt) : '--' }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-4"  >
              <div class="info-card text-center">
                <span class="info-label">总请求次数</span>
                <span class="info-value text-lg text-primary">{{ selectedConversation?.requestCount || 0 }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-4"  >
              <div class="info-card text-center">
                <span class="info-label">总错误次数</span>
                <span class="info-value text-lg text-destructive">{{ selectedConversation?.errorCount || 0 }}</span>
              </div>
            </div>
            <div class="min-w-0 col-span-12 sm:col-span-4"  >
              <div class="info-card text-center">
                <span class="info-label">固定渠道</span>
                <span class="info-value text-sm">
                  <span v-if="selectedConversation?.routeOverride">
                    #{{ selectedConversation.routeOverride.channelIndex }}
                  </span>
                  <span v-else class="text-muted-foreground">默认调度</span>
                </span>
              </div>
            </div>
            <div v-if="selectedConversation?.clientFamily || selectedConversation?.identitySource" class="min-w-0 col-span-12" >
              <div class="info-card info-card--horizontal">
                <span class="info-label">客户端身份</span>
                <span class="info-value font-mono">
                  {{ selectedConversation?.clientFamily || '未知客户端' }}
                  <span v-if="selectedConversation?.identitySource" class="text-muted-foreground"> · {{ selectedConversation.identitySource }}</span>
                </span>
              </div>
            </div>
          </div>

          <div class="mt-4">
            <div class="text-sm font-medium font-bold mb-2 flex items-center gap-2">
              <span class="prompt-section-marker" aria-hidden="true"></span>
              <span>图片指纹（{{ selectedConversation?.imageFingerprints?.length || 0 }}）</span>
            </div>
            <div v-if="!selectedConversation?.imageFingerprints?.length" class="text-xs text-muted-foreground">
              该对话尚未收到图片。
            </div>
            <div v-else class="image-fingerprint-list">
              <div v-for="fingerprint in selectedConversation.imageFingerprints" :key="fingerprint" class="image-fingerprint-item">
                <span class="font-mono">{{ fingerprint }}</span>
                <UiButton size="x-small" icon="mdi-content-copy" variant="text" color="primary" title="复制图片指纹" @click="copyText(fingerprint)" />
              </div>
            </div>
          </div>

          <!-- 错误信息 (如果有) -->
          <UiAlert
            v-if="selectedConversation?.lastError"
            type="error"
            variant="tonal"
            icon="mdi-alert-circle"
            title="最近一次请求报错"
            class="mb-4 text-xs border-error border"
            density="compact"
          >
            <div class="flex items-start justify-between mt-1">
              <pre class="error-pre">{{ selectedConversation.lastError }}</pre>
              <UiButton size="x-small" icon="mdi-content-copy" variant="text" color="error" title="复制报错" @click="copyText(selectedConversation.lastError || '')" />
            </div>
          </UiAlert>

          <CommandSafetyHistory
            v-if="detailDialog && selectedConversation"
            :conversation-id="selectedConversation.id"
          />

          <!-- 提示词详情 -->
          <div class="mt-4">
            <div class="text-sm font-medium font-bold mb-3 flex items-center gap-2">
              <span class="prompt-section-marker" aria-hidden="true"></span>
              <span>对话提示词历史</span>
            </div>
            <!-- 保存该会话前 3 条不同的真实用户输入；展示与复制均使用完整原文 -->

            <!-- 无提示词 -->
            <div v-if="!selectedConversation || conversationPrompts(selectedConversation).length === 0" class="prompt-empty text-center py-4 rounded text-muted-foreground text-sm">
              暂无提示词记录
            </div>

            <!-- 提示词卡片列表 -->
            <div v-else class="flex flex-col gap-3">
              <div
                v-for="(prompt, idx) in conversationPrompts(selectedConversation)"
                :key="idx"
                class="prompt-detail-card"
              >
                <div class="prompt-card-header">
                  <UiBadge size="x-small" color="primary" variant="flat" class="font-bold">
                    输入 #{{ idx + 1 }}
                  </UiBadge>
                  <span class="flex-1" ></span>
                  <span class="prompt-length text-xs text-muted-foreground">{{ prompt.length.toLocaleString() }} 字符</span>
                  <UiButton
                    size="x-small"
                    variant="text"
                    icon="mdi-content-copy"
                    color="primary"
                    title="复制提示词"
                    @click="copyText(prompt)"
                  />
                </div>
                <div class="prompt-card-body">
                  {{ prompt }}
                </div>
              </div>
            </div>
          </div>
      <template #actions>
        <UiButton
          v-if="selectedConversation"
          color="error"
          variant="tonal"
          prepend-icon="mdi-delete"
          @click="openDeleteDialog(selectedConversation)"
        >
          删除对话
        </UiButton>
        <span class="flex-1" ></span>
        <UiButton color="primary" variant="tonal" prepend-icon="mdi-swap-horizontal" @click="openRouteOverrideFromDetail()">
          去固定该会话渠道
        </UiButton>
        <UiButton variant="outlined" @click="detailDialog = false">关闭</UiButton>
      </template>
    </AppDialog>
    <ConversationTraceDialog v-if="traceDialog && selectedConversation" v-model="traceDialog" :conversation="selectedConversation" />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiDataTable from '@/shared/ui/data/DataTable.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import CommandSafetyHistory from '@/features/observability/components/conversation/CommandSafetyHistory.vue'
import ConversationTraceDialog from '@/features/observability/components/conversation/ConversationTraceDialog.vue'
import { api, type ConversationEntry, type ConversationKind, type ConversationRouteOptionChannel } from '@/api'
import { API_TABS } from '@/features/channels'
import { useToast } from '@/shared/composables/useToast'
import { useClipboard } from '@/shared/composables/useClipboard'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'

const { showToast } = useToast()
const { copyText: copyToClipboard } = useClipboard()
const copyText = (text: string) => copyToClipboard(text, '已复制到剪贴板')

const headers = [
  { title: '对话', key: 'identity', sortable: false, width: 180 },
  // 提示词摘要不设宽度，占用其余列分配后的剩余空间。
  { title: '提示词摘要', key: 'prompt', sortable: false },
  { title: '协议 / 模型', key: 'protocol', sortable: false, width: 190 },
  { title: '活动状态', key: 'activity', sortable: false, width: 120 },
  { title: '请求统计', key: 'requests', sortable: false, width: 96 },
  { title: '渠道路由', key: 'routing', sortable: false, width: 200 },
  { title: '创建 / 活动时间', key: 'timestamps', sortable: false, width: 150 },
  { title: '操作', key: 'actions', sortable: false, width: 190 }
]

const kindItems: ConversationKind[] = [...API_TABS]
type ConversationStatusFilter = 'all' | 'active' | 'idle'
type ConversationOrderMode = 'created' | 'status'

const statusItems: Array<{ title: string; value: ConversationStatusFilter }> = [
  { title: '全部状态', value: 'all' },
  { title: '正在活跃', value: 'active' },
  { title: '已完成 / 空闲', value: 'idle' }
]
const orderItems: Array<{ title: string; value: ConversationOrderMode }> = [
  { title: '按创建时间', value: 'created' },
  { title: '按状态', value: 'status' }
]
type RouteSelectItem = {
  title: string
  value: string
  channel: ConversationRouteOptionChannel
}

const loading = ref(false)
const saving = ref(false)
const conversations = shallowRef<ConversationEntry[]>([])
const page = ref(1)
const pageSize = ref(25)
const totalConversations = ref(0)
const loadError = ref('')
const routeOptionsError = ref('')
const searchText = ref('')
const kindFilter = ref<ConversationKind | null>(null)
const statusFilter = ref<ConversationStatusFilter>('all')
const orderMode = ref<ConversationOrderMode>('created')
const now = ref(Date.now())
const routeDialog = ref(false)
const editingConversation = ref<ConversationEntry | null>(null)
const selectedChannelId = ref<string | null>(null)
const nameDialog = ref(false)
const editingNameConversation = ref<ConversationEntry | null>(null)
const conversationName = ref('')
const deleteDialog = ref(false)
const deletingConversation = ref<ConversationEntry | null>(null)
const deleteAllDialog = ref(false)
const deleteAllConfirmation = ref('')
const routeOptions = ref<Record<ConversationKind, RouteSelectItem[]>>(
  API_TABS.reduce(
    (acc, kind) => { acc[kind] = []; return acc },
    {} as Record<ConversationKind, RouteSelectItem[]>,
  ),
)
let clockTimer: ReturnType<typeof setInterval> | null = null
let refreshTimer: ReturnType<typeof setInterval> | null = null
let searchTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false
let conversationRequestSequence = 0
let conversationRequestsInFlight = 0

const detailDialog = ref(false)
const selectedConversation = ref<ConversationEntry | null>(null)
const detailName = ref('')
const savingName = ref(false)
const traceDialog = ref(false)
const showOperationError = (error: unknown, message: string) => {
  showToast(error instanceof Error ? `${message}：${error.message}` : message, 'error')
}

const openDetailDialog = (item: ConversationEntry) => {
  selectedConversation.value = item
  detailName.value = item.name || ''
  detailDialog.value = true
}
const openTraceDialog = (item: ConversationEntry) => {
  selectedConversation.value = item
  detailName.value = item.name || ''
  traceDialog.value = true
}
const displayConversationName = (item: ConversationEntry): string => item.name || item.firstPrompt || conversationPrompts(item)[0] || '未命名对话'

// 所有写入统一使此前启动的轮询失效，已提交结果才可更新列表。
const invalidateConversationReads = () => {
  conversationRequestSequence++
  loading.value = false
}

const updateConversation = (updated: ConversationEntry) => {
  conversations.value = conversations.value.map(item => item.id === updated.id ? updated : item)
  if (selectedConversation.value?.id === updated.id) {
    selectedConversation.value = updated
  }
}

const openNameDialog = (item: ConversationEntry) => {
  editingNameConversation.value = item
  conversationName.value = item.name || ''
  nameDialog.value = true
}

const saveName = async () => {
  if (saving.value || !editingNameConversation.value || !conversationName.value.trim()) return
  saving.value = true
  try {
    const updated = await api.setConversationName(editingNameConversation.value.id, conversationName.value.trim())
    invalidateConversationReads()
    updateConversation(updated)
    if (selectedConversation.value?.id === updated.id) detailName.value = updated.name || ''
    nameDialog.value = false
  } catch (error) {
    showOperationError(error, '保存对话名称失败')
  } finally {
    saving.value = false
  }
}

const saveDetailName = async () => {
  const target = selectedConversation.value
  const name = detailName.value.trim()
  if (!target || !name || savingName.value || name === (target.name || '')) return
  savingName.value = true
  try {
    const updated = await api.setConversationName(target.id, name)
    invalidateConversationReads()
    updateConversation(updated)
    if (selectedConversation.value?.id === target.id) detailName.value = updated.name || ''
  } catch (error) {
    showOperationError(error, '保存对话名称失败')
  } finally {
    savingName.value = false
  }
}

const openDeleteDialog = (item: ConversationEntry) => {
  deletingConversation.value = item
  deleteDialog.value = true
}

const deleteConversation = async () => {
  if (!deletingConversation.value) return
  saving.value = true
  try {
    const id = deletingConversation.value.id
    await api.deleteConversation(id)
    invalidateConversationReads()
    await loadConversations()
    if (selectedConversation.value?.id === id) {
      detailDialog.value = false
      selectedConversation.value = null
    }
    deleteDialog.value = false
    showToast('对话已删除', 'success')
  } catch (error) {
    showToast(error instanceof Error ? error.message : '删除对话失败', 'error')
  } finally {
    saving.value = false
  }
}

const openDeleteAllDialog = () => {
  deleteAllConfirmation.value = ''
  deleteAllDialog.value = true
}

const deleteAllConversations = async () => {
  if (deleteAllConfirmation.value !== '清空全部') return
  saving.value = true
  try {
    await api.deleteAllConversations()
    invalidateConversationReads()
    conversations.value = []
    totalConversations.value = 0
    page.value = 1
    selectedConversation.value = null
    detailDialog.value = false
    deleteAllDialog.value = false
    showToast('已清空全部对话', 'success')
  } catch (error) {
    showToast(error instanceof Error ? error.message : '清空全部对话失败', 'error')
  } finally {
    saving.value = false
  }
}

const openRouteOverrideFromDetail = () => {
  if (!selectedConversation.value) return
  detailDialog.value = false
  openRouteDialog(selectedConversation.value)
}

const timestampOf = (value?: string): number => {
  if (!value) return 0
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) ? timestamp : 0
}

const normalizeConversationPrompts = (item?: ConversationEntry | null): string[] => {
  if (!item) return []
  const prompts = Array.isArray(item.prompts) ? item.prompts : []
  const values = prompts.length > 0 ? prompts : [item.firstPrompt || '']
  const seen = new Set<string>()
  return values
    .map(value => String(value || '').trim())
    .filter(value => {
      if (!value || seen.has(value)) return false
      seen.add(value)
      return true
    })
    .slice(0, 3)
}

// 提示词去重只在快照更新时计算，秒级活动时钟无需反复扫描每条正文。
const promptsByConversation = computed(() => new Map(conversations.value.map(item => [item.id, normalizeConversationPrompts(item)])))
const conversationPrompts = (item?: ConversationEntry | null): string[] => item ? promptsByConversation.value.get(item.id) ?? normalizeConversationPrompts(item) : []

const routeChannelItems = computed(() => {
  const kind = editingConversation.value?.apiKind
  return kind ? routeOptions.value[kind] ?? [] : []
})

const loadRouteOptions = async () => {
  routeOptionsError.value = ''
  try {
    const res = await api.getConversationRouteOptions()
    const next: Record<ConversationKind, RouteSelectItem[]> = {
      messages: [],
      responses: [],
      gemini: [],
      chat: [],
      images: [],
      embeddings: [],
      decisions: []
    }
    for (const group of res.kinds) {
      next[group.kind] = group.channels.map(ch => ({
        title: formatRouteOptionTitle(ch),
        value: ch.channelId,
        channel: ch
      }))
    }
    routeOptions.value = next
  } catch (error) {
    routeOptionsError.value = error instanceof Error ? error.message : '加载渠道选项失败'
  }
}

const loadConversations = async (silent = false) => {
  if (disposed || (silent && (document.hidden || conversationRequestsInFlight > 0))) return
  const sequence = ++conversationRequestSequence
  conversationRequestsInFlight++
  if (!silent) loading.value = true
  try {
    const res = await api.getConversations({
      page: page.value, pageSize: pageSize.value, q: searchText.value.trim(),
      kind: kindFilter.value ?? undefined, status: statusFilter.value, order: orderMode.value
    })
    if (disposed || sequence !== conversationRequestSequence) return
    loadError.value = ''
    conversations.value = res.conversations
    totalConversations.value = res.total ?? res.conversations.length
    if (page.value > Math.max(1, Math.ceil(totalConversations.value / pageSize.value))) {
      page.value = Math.max(1, Math.ceil(totalConversations.value / pageSize.value))
      return
    }
    if (selectedConversation.value) {
      const current = res.conversations.find(item => item.id === selectedConversation.value?.id)
      if (current) selectedConversation.value = current
    }
  } catch (error) {
    if (!disposed && sequence === conversationRequestSequence) loadError.value = error instanceof Error ? error.message : '加载对话失败'
  } finally {
    conversationRequestsInFlight--
    if (sequence === conversationRequestSequence) loading.value = false
  }
}

const openRouteDialog = (item: ConversationEntry) => {
  editingConversation.value = item
  selectedChannelId.value = item.routeOverride?.channelId ?? null
  routeDialog.value = true
}

const saveRoute = async () => {
  if (!editingConversation.value || selectedChannelId.value === null) return
  saving.value = true
  try {
    const target = routeChannelItems.value.find(item => item.value === selectedChannelId.value)?.channel
    if (!target?.channelId) throw new Error('渠道选项缺少稳定 ID，请刷新后重新选择')
    const updated = await api.setConversationRoute(
      editingConversation.value.id,
      editingConversation.value.apiKind,
      target.channelIndex,
      target.channelId
    )
    invalidateConversationReads()
    updateConversation(updated)
    routeDialog.value = false
  } catch (error) {
    showOperationError(error, '保存固定渠道失败')
  } finally {
    saving.value = false
  }
}

const clearRoute = async (item: ConversationEntry) => {
  try {
    const updated = await api.clearConversationRoute(item.id)
    invalidateConversationReads()
    updateConversation(updated)
  } catch (error) {
    showOperationError(error, '清除固定渠道失败')
  }
}

const formatModelChain = (item: ConversationEntry): string => {
  const requested = (item.lastModel || fallbackModelLabel(item.apiKind)).trim()
  const resolved = (item.lastResolvedModel || '').trim()
  if (resolved && requested && resolved !== requested) {
    return `${requested} -> ${resolved}`
  }
  return resolved || requested || '--'
}

const fallbackModelLabel = (kind: ConversationKind): string => {
  if (kind === 'messages') return 'claude'
  if (kind === 'responses' || kind === 'chat' || kind === 'images' || kind === 'embeddings' || kind === 'decisions') return 'GPT'
  if (kind === 'gemini') return 'gemini'
  return '--'
}

const formatRouteOptionTitle = (channel: ConversationRouteOptionChannel): string => {
  const modelPreview = formatRouteChannelModelPreview(channel)
  return modelPreview
    ? `#${channel.channelIndex} ${channel.channelName} · ${modelPreview}`
    : `#${channel.channelIndex} ${channel.channelName}`
}

const formatRouteChannelModelPreview = (channel: ConversationRouteOptionChannel): string => {
  const defaultModel = String(channel.defaultModel || '').trim()
  if (defaultModel) return `兜底 ${defaultModel}`

  const entries = normalizeModelMappingEntries(channel.modelMapping)
  if (entries.length === 0) {
    if (channel.kind === 'messages') return 'claude'
    if (channel.kind === 'responses' || channel.kind === 'chat' || channel.kind === 'embeddings' || channel.kind === 'decisions') return 'GPT'
    if (channel.kind === 'gemini') return 'gemini'
    return ''
  }

  const preferred = pickPreferredMapping(entries, channel)
  if (!preferred) return ''
  const [source, target] = preferred
  return source === target ? target : `${source} -> ${target}`
}

const normalizeModelMappingEntries = (
  mapping?: Record<string, string[]>
): Array<readonly [string, string]> => {
  return Object.entries(mapping || {})
    .flatMap(([source, targets]) => {
      const cleanSource = source.trim()
      const targetList = Array.isArray(targets) ? targets : [targets]
      return targetList
        .map(target => [cleanSource, String(target || '').trim()] as const)
        .filter(([cleanSource, target]) => cleanSource && target)
    })
}

const pickPreferredMapping = (
  entries: Array<readonly [string, string]>,
  channel: ConversationRouteOptionChannel
): readonly [string, string] | undefined => {
  const lowerService = String(channel.serviceType || '').toLowerCase()
  const preferredTerms = channel.kind === 'messages' || lowerService.includes('claude')
    ? ['opus', 'sonnet', 'claude']
    : channel.kind === 'responses' || lowerService.includes('response') || lowerService.includes('openai') || lowerService.includes('chat')
      ? ['gpt', 'codex']
      : ['gemini']

  for (const term of preferredTerms) {
    const match = entries.find(([source]) => source.toLowerCase().includes(term))
    if (match) return match
  }

  return [...entries].sort((a, b) => a[0].localeCompare(b[0]))[0]
}

const formatDurationSince = (value?: string): string => {
  if (!value) return ''
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp)) return ''
  const seconds = Math.max(0, Math.floor((now.value - timestamp) / 1000))
  if (seconds < 60) return `${seconds}秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}小时`
  return `${Math.floor(hours / 24)}天`
}

const formatIdleText = (item: ConversationEntry): string => {
  const value = item.lastRequestAt || item.lastSeenAt
  const duration = formatDurationSince(value)
  return duration ? `${duration}未发送` : '--'
}

const formatConversationID = (id: string): string => {
  if (id.length <= 15) return id
  return `${id.slice(0, 9)}…${id.slice(-6)}`
}

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false
})
const formatDateTime = (value?: string): string => {
  const timestamp = timestampOf(value)
  return timestamp ? dateTimeFormatter.format(timestamp) : '--'
}

watch([page, pageSize], () => { void loadConversations() })
watch([kindFilter, statusFilter, orderMode], () => {
  if (page.value !== 1) page.value = 1
  else void loadConversations()
})
watch(searchText, () => {
  if (searchTimer) clearTimeout(searchTimer)
  // 立即使旧请求失效，防抖期间不能把旧筛选结果当成新结果。
  conversationRequestSequence++
  searchTimer = setTimeout(() => {
    if (page.value !== 1) page.value = 1
    else void loadConversations()
  }, 300)
})

onMounted(async () => {
  await Promise.all([loadRouteOptions(), loadConversations()])
  if (disposed) return
  clockTimer = setInterval(() => {
    if (!document.hidden) now.value = Date.now()
  }, 1000)
  refreshTimer = setInterval(() => {
    void loadConversations(true)
  }, 3000)
})

onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
  disposed = true
  conversationRequestSequence++
  if (clockTimer) {
    clearInterval(clockTimer)
    clockTimer = null
  }
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<style scoped src="@/features/observability/styles/conversations.css"></style>
