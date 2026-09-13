<template>
  <div class="conversation-view">
    <v-card class="conversation-toolbar mb-4 pa-4" rounded="lg">
      <div class="conversation-filters">
        <v-text-field
          v-model="searchText"
          label="搜索对话"
          variant="outlined"
          density="compact"
          prepend-inner-icon="mdi-magnify"
          hide-details
          class="search-field"
          @keyup.enter="loadConversations()"
        />

        <v-select
          v-model="kindFilter"
          :items="kindItems"
          label="类型"
          variant="outlined"
          density="compact"
          hide-details
          class="filter-field"
          clearable
        />

        <v-select
          v-model="statusFilter"
          :items="statusItems"
          label="状态"
          variant="outlined"
          density="compact"
          hide-details
          class="filter-field"
        />

        <v-select
          v-model="orderMode"
          :items="orderItems"
          label="排序"
          variant="outlined"
          density="compact"
          hide-details
          class="order-field"
          prepend-inner-icon="mdi-sort"
        />

        <v-btn color="primary" prepend-icon="mdi-refresh" variant="tonal" :loading="loading" @click="loadConversations()">
          刷新
        </v-btn>

        <v-btn color="error" prepend-icon="mdi-delete" variant="tonal" :disabled="conversations.length === 0" @click="openDeleteAllDialog">
          清空全部
        </v-btn>

        <div class="conversation-count text-body-2 text-medium-emphasis">
          显示 {{ filteredConversations.length }} / {{ conversations.length }} 条
        </div>
      </div>
    </v-card>

    <v-card rounded="lg">
      <v-data-table
        :headers="headers"
        :items="filteredConversations"
        :loading="loading"
        item-key="id"
        density="compact"
        class="conversation-table"
      >
        <template #[`item.identity`]="{ item }">
          <div class="conversation-identity">
            <button type="button" class="conversation-title" title="查看对话详情" @click="openDetailDialog(item)">
              {{ item.name || '未命名对话' }}
            </button>
            <div class="conversation-id-row">
              <span class="conversation-id font-mono" :title="item.id">{{ formatConversationID(item.id) }}</span>
              <v-btn
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
          <span v-else class="text-medium-emphasis">--</span>
        </template>

        <template #[`item.protocol`]="{ item }">
          <div class="protocol-model">
            <v-chip size="x-small" variant="tonal" class="text-uppercase">{{ item.apiKind }}</v-chip>
            <span class="model-name">{{ formatModelChain(item) }}</span>
          </div>
        </template>

        <template #[`item.activity`]="{ item }">
          <div v-if="item.isSending" class="activity-active">
            <v-chip
              size="small"
              color="success"
              variant="tonal"
              prepend-icon="mdi-loading"
              class="sending-chip"
            >
              发送中 {{ formatDurationSince(item.lastRequestAt || item.lastSeenAt) }}
            </v-chip>
            <span v-if="(item.activeRequests || 0) > 1" class="text-caption text-medium-emphasis">
              {{ item.activeRequests }} 个并发请求
            </span>
          </div>
          <span v-else class="text-caption text-medium-emphasis">
            {{ formatIdleText(item) }}
          </span>
        </template>

        <template #[`item.requests`]="{ item }">
          <div class="request-stats">
            <span><strong>{{ item.requestCount }}</strong> 请求</span>
            <span :class="item.errorCount > 0 ? 'text-error' : 'text-medium-emphasis'">
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
            <div v-else class="text-medium-emphasis">尚未解析</div>
            <div v-if="item.routeOverride" class="routing-line text-primary">
              <span class="routing-label">固定</span>
              <span>#{{ item.routeOverride.channelIndex }} {{ item.routeOverride.channelName || item.routeOverride.kind }}</span>
            </div>
            <div v-else class="text-caption text-medium-emphasis">默认调度</div>
          </div>
        </template>

        <template #[`item.timestamps`]="{ item }">
          <div class="timestamp-summary">
            <span>{{ formatDateTime(item.firstSeenAt) }}</span>
            <span class="text-caption text-medium-emphasis">活动 {{ formatDateTime(item.lastSeenAt) }}</span>
          </div>
        </template>

        <template #[`item.actions`]="{ item }">
          <div class="conversation-actions">
            <v-btn icon="mdi-pencil" size="small" variant="text" title="命名" aria-label="命名对话" @click="openNameDialog(item)" />
            <v-btn icon="mdi-swap-horizontal" size="small" variant="text" title="固定渠道" aria-label="固定渠道" @click="openRouteDialog(item)" />
            <v-btn
              v-if="item.routeOverride"
              icon="mdi-close"
              size="small"
              variant="text"
              color="warning"
              title="清除固定渠道"
              aria-label="清除固定渠道"
              @click="clearRoute(item)"
            />
            <v-btn icon="mdi-delete" size="small" variant="text" color="error" title="删除" aria-label="删除对话" @click="openDeleteDialog(item)" />
          </div>
        </template>
      </v-data-table>
    </v-card>

    <v-dialog v-model="routeDialog" max-width="640">
      <v-card rounded="lg">
        <v-card-title>设置固定渠道</v-card-title>
        <v-card-text>
          <div class="mb-3 text-body-2 text-medium-emphasis">
            {{ editingConversation?.id }}
          </div>
          <div class="mb-3">
            <v-chip size="small" variant="tonal">{{ editingConversation?.apiKind }}</v-chip>
          </div>
          <v-select
            v-model="selectedChannelIndex"
            :items="routeChannelItems"
            label="渠道"
            variant="outlined"
            density="compact"
            class="mt-3"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="routeDialog = false">取消</v-btn>
          <v-btn color="primary" :loading="saving" @click="saveRoute">保存</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="nameDialog" max-width="520">
      <v-card rounded="lg">
        <v-card-title>给对话命名</v-card-title>
        <v-card-text>
          <div class="text-body-2 text-medium-emphasis mb-3">{{ editingNameConversation?.id }}</div>
          <v-text-field
            v-model="conversationName"
            label="对话名称"
            maxlength="120"
            counter="120"
            variant="outlined"
            density="compact"
            autofocus
            @keyup.enter="saveName"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="nameDialog = false">取消</v-btn>
          <v-btn color="primary" :loading="saving" @click="saveName">保存</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="deleteDialog" max-width="480">
      <v-card rounded="lg">
        <v-card-title>删除对话</v-card-title>
        <v-card-text>
          确定删除“{{ deletingConversation?.name || deletingConversation?.firstPrompt || deletingConversation?.id }}”吗？
          此操作会删除该对话的提示词、图片指纹、图片理解结果、固定渠道设置和 Responses 持久化会话链，且无法恢复。
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="deleteDialog = false">取消</v-btn>
          <v-btn color="error" :loading="saving" @click="deleteConversation">删除</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="deleteAllDialog" max-width="520">
      <v-card rounded="lg">
        <v-card-title>清空全部对话</v-card-title>
        <v-card-text>
          此操作会删除全部对话、图片理解结果、固定渠道设置和 Responses 持久化会话链，且无法恢复。
          <v-text-field
            v-model="deleteAllConfirmation"
            label="输入 清空全部 以确认"
            variant="outlined"
            density="compact"
            class="mt-4"
            autofocus
            @keyup.enter="deleteAllConversations"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="deleteAllDialog = false">取消</v-btn>
          <v-btn color="error" :disabled="deleteAllConfirmation !== '清空全部'" :loading="saving" @click="deleteAllConversations">清空全部</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- 详情 Dialog -->
    <v-dialog v-model="detailDialog" max-width="960" scrollable>
      <v-card rounded="lg" class="detail-dialog-card pa-2">
        <v-card-title class="d-flex align-center justify-between border-b pb-2">
          <span class="text-h6 font-weight-bold d-flex align-center ga-2">
            <v-icon icon="mdi-message-processing" color="primary" />
            对话详情
          </span>
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" size="small" @click="detailDialog = false" />
        </v-card-title>
        
        <v-card-text class="pt-4 pb-2">
          <!-- ID & Meta -->
          <div class="d-flex flex-wrap align-center justify-space-between mb-4 ga-2">
            <div class="d-flex align-center ga-2">
              <div>
                <div class="text-caption text-medium-emphasis">内部对话 ID</div>
                <span class="text-subtitle-1 font-weight-bold font-mono conversation-detail-id">{{ selectedConversation?.id }}</span>
              </div>
              <v-btn icon="mdi-content-copy" size="x-small" variant="text" color="primary" title="复制ID" @click="copyText(selectedConversation?.id || '')" />
            </div>
            <div class="d-flex align-center ga-2">
              <v-chip size="small" color="primary" variant="tonal" class="text-uppercase">{{ selectedConversation?.apiKind }}</v-chip>
              <v-chip v-if="selectedConversation?.isSending" size="small" color="success" variant="tonal" prepend-icon="mdi-loading" class="sending-chip">
                发送中<span v-if="(selectedConversation?.activeRequests || 0) > 1"> · {{ selectedConversation?.activeRequests }} 个请求</span>
              </v-chip>
              <v-chip v-else size="small" color="grey" variant="tonal">已完成/空闲</v-chip>
            </div>
          </div>

          <v-alert v-if="selectedConversation?.name" type="info" variant="tonal" density="compact" class="mb-4">
            对话名称：{{ selectedConversation.name }}
          </v-alert>

          <!-- Grid Info -->
          <v-row class="mb-2" dense>
            <v-col cols="12" sm="6">
              <div class="info-card">
                <span class="info-label">请求模型</span>
                <span class="info-value font-mono">{{ selectedConversation?.lastModel || '--' }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="6">
              <div class="info-card">
                <span class="info-label">实际路由模型</span>
                <span class="info-value font-mono">{{ selectedConversation?.lastResolvedModel || '--' }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="6">
              <div class="info-card">
                <span class="info-label">首次看到时间</span>
                <span class="info-value text-caption">{{ selectedConversation?.firstSeenAt ? new Date(selectedConversation.firstSeenAt).toLocaleString() : '--' }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="6">
              <div class="info-card">
                <span class="info-label">最近活动时间</span>
                <span class="info-value text-caption">{{ selectedConversation?.lastSeenAt ? new Date(selectedConversation.lastSeenAt).toLocaleString() : '--' }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="4">
              <div class="info-card text-center">
                <span class="info-label">总请求次数</span>
                <span class="info-value text-h6 text-primary">{{ selectedConversation?.requestCount || 0 }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="4">
              <div class="info-card text-center">
                <span class="info-label">总错误次数</span>
                <span class="info-value text-h6 text-error">{{ selectedConversation?.errorCount || 0 }}</span>
              </div>
            </v-col>
            <v-col cols="12" sm="4">
              <div class="info-card text-center">
                <span class="info-label">固定渠道</span>
                <span class="info-value text-body-2">
                  <span v-if="selectedConversation?.routeOverride">
                    #{{ selectedConversation.routeOverride.channelIndex }}
                  </span>
                  <span v-else class="text-medium-emphasis">默认调度</span>
                </span>
              </div>
            </v-col>
            <v-col v-if="selectedConversation?.clientFamily || selectedConversation?.identitySource" cols="12">
              <div class="info-card info-card--horizontal">
                <span class="info-label">客户端身份</span>
                <span class="info-value font-mono">
                  {{ selectedConversation?.clientFamily || '未知客户端' }}
                  <span v-if="selectedConversation?.identitySource" class="text-medium-emphasis"> · {{ selectedConversation.identitySource }}</span>
                </span>
              </div>
            </v-col>
          </v-row>

          <div class="mt-4">
            <div class="text-subtitle-2 font-weight-bold mb-2 d-flex align-center ga-2">
              <span class="prompt-section-marker" aria-hidden="true"></span>
              <span>图片指纹（{{ selectedConversation?.imageFingerprints?.length || 0 }}）</span>
            </div>
            <div v-if="!selectedConversation?.imageFingerprints?.length" class="text-caption text-medium-emphasis">
              该对话尚未收到图片。
            </div>
            <div v-else class="image-fingerprint-list">
              <div v-for="fingerprint in selectedConversation.imageFingerprints" :key="fingerprint" class="image-fingerprint-item">
                <span class="font-mono">{{ fingerprint }}</span>
                <v-btn size="x-small" icon="mdi-content-copy" variant="text" color="primary" title="复制图片指纹" @click="copyText(fingerprint)" />
              </div>
            </div>
          </div>

          <!-- 错误信息 (如果有) -->
          <v-alert
            v-if="selectedConversation?.lastError"
            type="error"
            variant="tonal"
            icon="mdi-alert-circle"
            title="最近一次请求报错"
            class="mb-4 text-caption border-error border"
            density="compact"
          >
            <div class="d-flex align-start justify-space-between mt-1">
              <pre class="error-pre">{{ selectedConversation.lastError }}</pre>
              <v-btn size="x-small" icon="mdi-content-copy" variant="text" color="error" title="复制报错" @click="copyText(selectedConversation.lastError || '')" />
            </div>
          </v-alert>

          <CommandSafetyHistory
            v-if="detailDialog && selectedConversation"
            :conversation-id="selectedConversation.id"
          />

          <!-- 提示词详情 -->
          <div class="mt-4">
            <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center ga-2">
              <span class="prompt-section-marker" aria-hidden="true"></span>
              <span>对话提示词历史</span>
            </div>
            <div class="text-caption text-medium-emphasis mb-2">
              保存该会话前 3 条不同的真实用户输入；展示与复制均使用完整原文
            </div>

            <!-- 无提示词 -->
            <div v-if="!selectedConversation || conversationPrompts(selectedConversation).length === 0" class="prompt-empty text-center py-4 rounded text-medium-emphasis text-body-2">
              暂无提示词记录
            </div>

            <!-- 提示词卡片列表 -->
            <div v-else class="d-flex flex-column ga-3">
              <div
                v-for="(prompt, idx) in conversationPrompts(selectedConversation)"
                :key="idx"
                class="prompt-detail-card"
              >
                <div class="prompt-card-header">
                  <v-chip size="x-small" color="primary" variant="flat" class="font-weight-bold">
                    输入 #{{ idx + 1 }}
                  </v-chip>
                  <v-spacer />
                  <span class="prompt-length text-caption text-medium-emphasis">{{ prompt.length.toLocaleString() }} 字符</span>
                  <v-btn
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
        </v-card-text>

        <v-card-actions class="border-t mt-2 pt-2">
          <v-btn
            v-if="selectedConversation"
            color="error"
            variant="tonal"
            prepend-icon="mdi-delete"
            @click="openDeleteDialog(selectedConversation)"
          >
            删除对话
          </v-btn>
          <v-spacer />
          <v-btn color="primary" variant="tonal" prepend-icon="mdi-swap-horizontal" @click="openRouteOverrideFromDetail()">
            去固定该会话渠道
          </v-btn>
          <v-btn variant="outlined" @click="detailDialog = false">关闭</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackbarColor" timeout="2000">
      {{ snackbarText }}
    </v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import CommandSafetyHistory from '@/components/CommandSafetyHistory.vue'
import { api, type ConversationEntry, type ConversationKind, type ConversationRouteOptionChannel } from '@/services/api'

const headers = [
  { title: '对话', key: 'identity', sortable: false, width: 190 },
  { title: '提示词摘要', key: 'prompt', sortable: false, width: 300 },
  { title: '协议 / 模型', key: 'protocol', sortable: false, width: 220 },
  { title: '活动状态', key: 'activity', sortable: false, width: 130 },
  { title: '请求统计', key: 'requests', sortable: false, width: 100 },
  { title: '渠道路由', key: 'routing', sortable: false, width: 210 },
  { title: '创建 / 活动时间', key: 'timestamps', sortable: false, width: 170 },
  { title: '操作', key: 'actions', sortable: false, width: 160 }
]

const kindItems: ConversationKind[] = ['messages', 'responses', 'gemini', 'chat', 'images']
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
  value: number
  channel: ConversationRouteOptionChannel
}

const loading = ref(false)
const saving = ref(false)
const conversations = ref<ConversationEntry[]>([])
const searchText = ref('')
const kindFilter = ref<ConversationKind | null>(null)
const statusFilter = ref<ConversationStatusFilter>('all')
const orderMode = ref<ConversationOrderMode>('created')
const now = ref(Date.now())
const routeDialog = ref(false)
const editingConversation = ref<ConversationEntry | null>(null)
const selectedChannelIndex = ref<number | null>(null)
const nameDialog = ref(false)
const editingNameConversation = ref<ConversationEntry | null>(null)
const conversationName = ref('')
const deleteDialog = ref(false)
const deletingConversation = ref<ConversationEntry | null>(null)
const deleteAllDialog = ref(false)
const deleteAllConfirmation = ref('')
const routeOptions = ref<Record<ConversationKind, RouteSelectItem[]>>({
  messages: [],
  responses: [],
  gemini: [],
  chat: [],
  images: []
})
let clockTimer: ReturnType<typeof setInterval> | null = null
let refreshTimer: ReturnType<typeof setInterval> | null = null

const detailDialog = ref(false)
const selectedConversation = ref<ConversationEntry | null>(null)
const snackbar = ref(false)
const snackbarText = ref('')
const snackbarColor = ref('success')

const openDetailDialog = (item: ConversationEntry) => {
  selectedConversation.value = item
  detailDialog.value = true
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
  if (!editingNameConversation.value || !conversationName.value.trim()) return
  saving.value = true
  try {
    const updated = await api.setConversationName(editingNameConversation.value.id, conversationName.value.trim())
    updateConversation(updated)
    nameDialog.value = false
  } finally {
    saving.value = false
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
    conversations.value = conversations.value.filter(item => item.id !== id)
    if (selectedConversation.value?.id === id) {
      detailDialog.value = false
      selectedConversation.value = null
		}
		deleteDialog.value = false
		snackbarText.value = '对话已删除'
		snackbarColor.value = 'success'
		snackbar.value = true
	} catch (error) {
		snackbarText.value = error instanceof Error ? error.message : '删除对话失败'
		snackbarColor.value = 'error'
		snackbar.value = true
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
    conversations.value = []
    selectedConversation.value = null
    detailDialog.value = false
    deleteAllDialog.value = false
    snackbarText.value = '已清空全部对话'
		snackbarColor.value = 'success'
		snackbar.value = true
	} catch (error) {
		snackbarText.value = error instanceof Error ? error.message : '清空全部对话失败'
		snackbarColor.value = 'error'
		snackbar.value = true
	} finally {
    saving.value = false
  }
}

const openRouteOverrideFromDetail = () => {
  if (!selectedConversation.value) return
  detailDialog.value = false
  openRouteDialog(selectedConversation.value)
}

const copyText = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    snackbarText.value = '已复制到剪贴板'
    snackbarColor.value = 'success'
    snackbar.value = true
  } catch {
    snackbarText.value = '复制失败，请手动选择复制'
    snackbarColor.value = 'error'
    snackbar.value = true
  }
}

const filteredConversations = computed(() => {
  const q = searchText.value.trim().toLowerCase()
  return conversations.value
    .filter(item => {
      if (kindFilter.value && item.apiKind !== kindFilter.value) return false
      if (statusFilter.value === 'active' && !item.isSending) return false
      if (statusFilter.value === 'idle' && item.isSending) return false
      if (!q) return true
      const promptMatch = item.prompts && item.prompts.some(p => p.toLowerCase().includes(q))
      return [item.id, item.name, item.firstPrompt, item.lastModel, item.lastResolvedModel, item.lastError, item.routeOverride?.channelName, item.lastResolved?.channelName]
        .filter(Boolean)
        .some(value => String(value).toLowerCase().includes(q)) || promptMatch
    })
    .slice()
    .sort(compareConversations)
})

const compareConversations = (a: ConversationEntry, b: ConversationEntry): number => {
  if (orderMode.value === 'status' && Boolean(a.isSending) !== Boolean(b.isSending)) {
    return a.isSending ? -1 : 1
  }
  const createdDifference = timestampOf(b.firstSeenAt) - timestampOf(a.firstSeenAt)
  if (createdDifference !== 0) return createdDifference
  return a.id.localeCompare(b.id)
}

const timestampOf = (value?: string): number => {
  if (!value) return 0
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) ? timestamp : 0
}

const conversationPrompts = (item?: ConversationEntry | null): string[] => {
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

const routeChannelItems = computed(() => {
  const kind = editingConversation.value?.apiKind
  return kind ? routeOptions.value[kind] ?? [] : []
})

const loadRouteOptions = async () => {
  const res = await api.getConversationRouteOptions()
  const next: Record<ConversationKind, RouteSelectItem[]> = {
    messages: [],
    responses: [],
    gemini: [],
    chat: [],
    images: []
  }
  for (const group of res.kinds) {
    next[group.kind] = group.channels.map(ch => ({
      title: formatRouteOptionTitle(ch),
      value: ch.channelIndex,
      channel: ch
    }))
  }
  routeOptions.value = next
}

const loadConversations = async (silent = false) => {
  if (!silent) loading.value = true
  try {
    const res = await api.getConversations()
    conversations.value = res.conversations
    if (selectedConversation.value) {
      selectedConversation.value = res.conversations.find(item => item.id === selectedConversation.value?.id) ?? null
      if (!selectedConversation.value) detailDialog.value = false
    }
  } finally {
    if (!silent) loading.value = false
  }
}

const openRouteDialog = (item: ConversationEntry) => {
  editingConversation.value = item
  selectedChannelIndex.value = item.routeOverride?.channelIndex ?? null
  routeDialog.value = true
}

const saveRoute = async () => {
  if (!editingConversation.value || selectedChannelIndex.value === null) return
  saving.value = true
  try {
    const updated = await api.setConversationRoute(
      editingConversation.value.id,
      editingConversation.value.apiKind,
      selectedChannelIndex.value
    )
    updateConversation(updated)
    routeDialog.value = false
  } finally {
    saving.value = false
  }
}

const clearRoute = async (item: ConversationEntry) => {
  const updated = await api.clearConversationRoute(item.id)
  updateConversation(updated)
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
  if (kind === 'responses' || kind === 'chat' || kind === 'images') return 'GPT'
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
    if (channel.kind === 'responses' || channel.kind === 'chat') return 'GPT'
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

const formatDateTime = (value?: string): string => {
  const timestamp = timestampOf(value)
  if (!timestamp) return '--'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  }).format(timestamp)
}

onMounted(async () => {
  await Promise.all([loadRouteOptions(), loadConversations()])
  clockTimer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
  refreshTimer = setInterval(() => {
    void loadConversations(true)
  }, 3000)
})

onUnmounted(() => {
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

<style scoped>
.conversation-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.search-field {
  flex: 1 1 280px;
  max-width: 380px;
}

.filter-field {
  flex: 0 1 170px;
}

.order-field {
  flex: 0 1 200px;
}

.conversation-count {
  margin-left: auto;
  white-space: nowrap;
}

.conversation-table :deep(.v-table__wrapper) {
  overflow-x: auto;
}

.conversation-table :deep(table) {
  min-width: 1480px;
  table-layout: fixed;
}

.conversation-table :deep(th) {
  white-space: nowrap;
}

.conversation-table :deep(td) {
  padding-top: 10px !important;
  padding-bottom: 10px !important;
  vertical-align: middle;
}

.conversation-identity,
.protocol-model,
.request-stats,
.routing-summary,
.timestamp-summary,
.activity-active {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.conversation-title {
  display: block;
  width: fit-content;
  max-width: 100%;
  overflow: hidden;
  color: rgb(var(--v-theme-on-surface));
  font: inherit;
  font-weight: 600;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}

.conversation-title:hover,
.conversation-title:focus-visible {
  color: rgb(var(--v-theme-primary));
  text-decoration: underline;
}

.conversation-id-row {
  display: flex;
  align-items: center;
  gap: 2px;
  min-height: 24px;
}

.conversation-id {
  overflow: hidden;
  color: rgba(var(--v-theme-on-surface), 0.62);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation-prompts {
  max-width: 100%;
}

.image-fingerprint-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.image-fingerprint-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.14);
  border-radius: 6px;
  background: rgba(var(--v-theme-on-surface), 0.035);
  font-size: 11px;
  overflow: hidden;
}

.prompt-empty {
  border: 1px dashed rgba(var(--v-theme-on-surface), 0.2);
  background: rgba(var(--v-theme-on-surface), 0.025);
}

.image-fingerprint-item > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}

.conversation-prompt-text {
  display: -webkit-box;
  overflow: hidden;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: normal;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.prompt-count {
  margin-top: 4px;
  color: rgba(var(--v-theme-on-surface), 0.58);
  font-size: 11px;
}

.clickable-prompt {
  cursor: pointer;
}
.clickable-prompt:hover {
  text-decoration: underline;
  color: rgb(var(--v-theme-primary));
}

.info-card {
  background: rgba(var(--v-theme-on-surface), 0.025);
  border: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  border-radius: 8px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  height: 100%;
}

.info-card--horizontal {
  flex-direction: row;
  align-items: baseline;
  justify-content: space-between;
  gap: 16px;
}

.info-label {
  font-size: 11px;
  color: rgba(var(--v-theme-on-surface), 0.62);
  font-weight: 500;
  text-transform: uppercase;
  margin-bottom: 2px;
}

.info-value {
  font-size: 13px;
  color: rgb(var(--v-theme-on-surface));
  font-weight: 600;
  word-break: break-all;
}

.error-pre {
  white-space: pre-wrap;
  word-break: break-all;
  font-family: 'SF Mono', Monaco, 'Cascadia Code', monospace;
  font-size: 11px;
  max-height: 120px;
  overflow-y: auto;
  flex: 1;
}

.prompt-detail-card {
  border: 1px solid rgba(var(--v-theme-on-surface), 0.14);
  border-radius: 8px;
  overflow: hidden;
  background: rgb(var(--v-theme-surface));
}

.prompt-section-marker {
  display: inline-block;
  width: 4px;
  height: 16px;
  border-radius: 2px;
  background: rgb(var(--v-theme-primary));
  flex: 0 0 auto;
}

.prompt-card-header {
  background: rgba(var(--v-theme-on-surface), 0.035);
  border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  padding: 6px 12px;
  display: flex;
  align-items: center;
}

.prompt-length {
  margin-right: 4px;
  white-space: nowrap;
}

.prompt-card-body {
  padding: 12px;
  font-family: inherit;
  font-size: 13px;
  line-height: 1.6;
  color: rgb(var(--v-theme-on-surface));
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.model-name {
  display: block;
  overflow: hidden;
  font-family: 'SF Mono', Monaco, 'Cascadia Code', monospace;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.request-stats strong {
  font-variant-numeric: tabular-nums;
}

.routing-line {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}

.routing-line > span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.routing-label {
  flex: 0 0 auto;
  color: rgba(var(--v-theme-on-surface), 0.56);
  font-size: 11px;
}

.timestamp-summary {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.conversation-actions {
  display: flex;
  align-items: center;
  min-height: 40px;
}

.conversation-detail-id {
  overflow-wrap: anywhere;
}

.detail-dialog-card {
  max-height: calc(100vh - 48px);
}

.sending-chip :deep(.v-icon) {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 960px) {
  .search-field {
    max-width: none;
  }

  .conversation-count {
    flex-basis: 100%;
    margin-left: 0;
  }
}

@media (max-width: 600px) {
  .conversation-toolbar {
    padding: 12px !important;
  }

  .search-field,
  .filter-field,
  .order-field {
    flex: 1 1 100%;
  }

  .detail-dialog-card {
    max-height: calc(100vh - 24px);
  }

  .info-card--horizontal {
    align-items: flex-start;
    flex-direction: column;
    gap: 2px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .sending-chip :deep(.v-icon) {
    animation: none;
  }
}
</style>
