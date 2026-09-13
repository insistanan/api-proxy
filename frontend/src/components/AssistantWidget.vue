<template>
  <teleport to="body">
    <div ref="widgetRoot" class="assistant-widget">
    <!-- 悬浮按钮（可拖拽，位置持久化） -->
    <v-tooltip location="left" text="AI 配置助手">
      <template #activator="{ props }">
        <v-btn
          v-bind="props"
          ref="fabRef"
          class="assistant-fab"
          :style="fabStyle"
          :color="assistantStore.panelOpen ? 'primary' : undefined"
          icon
          size="large"
          aria-label="AI 配置助手"
          @pointerdown.prevent="onFabPointerDown"
        >
          <v-icon size="26">mdi-robot</v-icon>
        </v-btn>
      </template>
    </v-tooltip>

    <!-- 对话面板 -->
    <transition name="assistant-panel">
      <div ref="panelRoot" v-if="assistantStore.panelOpen" class="assistant-panel" role="dialog" aria-label="AI 配置助手">
        <div class="assistant-panel__head">
          <v-icon size="20" class="mr-2">mdi-robot</v-icon>
          <span class="text-subtitle-2 font-weight-bold">AI 配置助手</span>
          <v-spacer />
          <v-tooltip location="bottom" text="新会话">
            <template #activator="{ props }">
              <v-btn v-bind="props" icon size="small" variant="text" aria-label="新会话" @click="assistantStore.newSession()">
                <v-icon size="18">mdi-plus</v-icon>
              </v-btn>
            </template>
          </v-tooltip>
          <v-tooltip location="bottom" text="删除当前会话">
            <template #activator="{ props }">
              <v-btn
                v-bind="props"
                icon
                size="small"
                variant="text"
                aria-label="删除当前会话"
                :disabled="assistantStore.activeSessionId === null"
                @click="deleteActive"
              >
                <v-icon size="18">mdi-delete</v-icon>
              </v-btn>
            </template>
          </v-tooltip>
          <v-btn icon size="small" variant="text" aria-label="关闭助手" @click="assistantStore.panelOpen = false">
            <v-icon size="18">mdi-close</v-icon>
          </v-btn>
        </div>

        <!-- 会话切换 -->
        <v-select
          v-if="assistantStore.sessions.length > 0"
          :model-value="assistantStore.activeSessionId"
          :items="sessionItems"
          item-title="title"
          item-value="id"
          density="compact"
          variant="outlined"
          hide-details
          class="assistant-panel__sessions"
          aria-label="切换会话"
          @update:model-value="assistantStore.selectSession($event as number)"
        />

        <!-- 当前生效的模型路由：分组（匹配词）→ 命中渠道 -->
        <div v-if="assistantStore.available" class="assistant-panel__route" title="在 设置 → AI 助手 中修改">
          {{ routeLabel }}
        </div>

        <!-- 消息区 -->
        <div ref="messagesRef" class="assistant-panel__messages">
          <div v-if="!assistantStore.available" class="assistant-panel__hint">
            <v-icon size="22" class="mb-2">mdi-robot-off</v-icon>
            <div>{{ assistantStore.unavailableReason || 'AI 助手不可用' }}</div>
          </div>
          <div v-else-if="assistantStore.messages.length === 0" class="assistant-panel__hint">
            <v-icon size="22" class="mb-2">mdi-chat-outline</v-icon>
            <div>问我任何配置问题，或直接说"帮我建一个 chat 渠道"。</div>
            <div class="text-caption text-medium-emphasis mt-1">协议转换、分组、兜底、模型映射都可以问</div>
          </div>
          <template v-else>
            <div
              v-for="message in assistantStore.messages"
              :key="message.id"
              class="assistant-msg"
              :class="message.role === 'user' ? 'assistant-msg--user' : 'assistant-msg--bot'"
            >
              <pre class="assistant-msg__content">{{ message.content }}</pre>
              <div v-if="message.actions?.length" class="assistant-msg__actions">
                <div
                  v-for="(action, index) in message.actions"
                  :key="index"
                  class="assistant-action"
                  :class="action.success ? 'assistant-action--ok' : 'assistant-action--fail'"
                >
                  <v-icon size="14" class="mr-1">
                    {{ action.success ? 'mdi-check-circle' : 'mdi-close-circle' }}
                  </v-icon>
                  <span class="assistant-action__name">{{ actionLabel(action.action) }}</span>
                  <span class="assistant-action__detail">{{ action.detail }}</span>
                </div>
              </div>
            </div>
            <div v-if="assistantStore.sending" class="assistant-msg assistant-msg--bot">
              <pre class="assistant-msg__content assistant-msg__content--pending">思考中…</pre>
            </div>
          </template>
        </div>

        <!-- 输入区 -->
        <div class="assistant-panel__input">
          <v-textarea
            v-model="draft"
            :rows="draft.includes('\n') ? 3 : 1"
            auto-grow
            max-rows="5"
            variant="outlined"
            density="compact"
            hide-details
            placeholder="输入你的问题…（Enter 发送）"
            :disabled="!assistantStore.available || assistantStore.sending"
            @keydown.enter.exact.prevent="send"
          />
          <v-btn
            class="ml-2"
            color="primary"
            variant="flat"
            icon
            size="small"
            aria-label="发送"
            :loading="assistantStore.sending"
            :disabled="!assistantStore.available || !draft.trim()"
            @click="send"
          >
            <v-icon size="18">mdi-send</v-icon>
          </v-btn>
        </div>
      </div>
    </transition>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAssistantStore } from '@/stores/assistant'
import type { AssistantSession } from '@/services/api'

const assistantStore = useAssistantStore()
const draft = ref('')
const messagesRef = ref<HTMLElement | null>(null)

const sessionItems = computed<Array<{ id: number; title: string }>>(() =>
  assistantStore.sessions.map((session: AssistantSession) => ({
    id: session.id,
    title: session.title || `会话 #${session.id}`,
  }))
)

const kindLabels: Record<string, string> = {
  messages: 'Messages',
  responses: 'Responses',
  chat: 'Chat',
  gemini: 'Gemini',
}

// 路由信息一行：分组「匹配词」→ 渠道名（协议）；自动模式下走兜底分组。
const routeLabel = computed(() => {
  const model = assistantStore.model ? `分组「${assistantStore.model}」` : '自动（兜底分组）'
  const channel = assistantStore.channel
  if (!channel) return model
  return `${model} → ${channel.name}（${kindLabels[channel.kind] ?? channel.kind}）`
})

const actionLabel = (action: string) =>
  ({
    create_channel: '创建渠道',
    create_pool: '创建分组',
    set_model_mapping: '设置模型映射',
    update_channel: '更新渠道',
    explain: '说明',
    parse: '解析',
  })[action] ?? action

const send = async () => {
  const content = draft.value.trim()
  if (!content || assistantStore.sending || !assistantStore.available) return
  draft.value = ''
  try {
    await assistantStore.send(content)
  } catch {
    // 发送失败：恢复输入内容让用户重试（store 已回滚乐观消息）。
    draft.value = content
  }
}

const deleteActive = async () => {
  if (assistantStore.activeSessionId === null) return
  await assistantStore.deleteSession(assistantStore.activeSessionId)
}

// 新消息到达时滚动到底部。
watch(
  () => [assistantStore.messages.length, assistantStore.sending],
  async () => {
    await nextTick()
    if (messagesRef.value) {
      messagesRef.value.scrollTop = messagesRef.value.scrollHeight
    }
  }
)

// ----- 悬浮按钮拖拽（位置持久化到 localStorage）-----
const FAB_POS_KEY = 'assistant-fab-position'

const fabRef = ref<{ $el?: HTMLElement } | null>(null)
const fabPosition = ref<{ right: number; bottom: number } | null>(null)
const fabStyle = computed(() => {
  if (!fabPosition.value) return undefined
  return { right: `${fabPosition.value.right}px`, bottom: `${fabPosition.value.bottom}px` }
})

const loadFabPosition = () => {
  try {
    const raw = localStorage.getItem(FAB_POS_KEY)
    if (!raw) return
    const parsed = JSON.parse(raw)
    if (typeof parsed.right === 'number' && typeof parsed.bottom === 'number') return parsed
  } catch {
    // 位置数据损坏时回落默认位置。
  }
  return null
}

const clampFabPosition = (right: number, bottom: number) => ({
  right: Math.max(4, Math.min(right, window.innerWidth - 56)),
  bottom: Math.max(4, Math.min(bottom, window.innerHeight - 56)),
})

const saveFabPosition = (position: { right: number; bottom: number }) => {
  try {
    localStorage.setItem(FAB_POS_KEY, JSON.stringify(position))
  } catch {
    // 存储不可用（隐私模式等）时静默跳过，仅本次会话生效。
  }
}

// 拖拽状态机：pointerdown 记录起点，move 跟随，up 判断"位移 < 6px = 点击"。
// 点击才切换面板；拖动只更新位置。
let dragState: {
  pointerId: number
  startX: number
  startY: number
  startRight: number
  startBottom: number
  moved: boolean
} | null = null

const currentFabRect = (): { right: number; bottom: number } => {
  if (fabPosition.value) return { ...fabPosition.value }
  return { right: 20, bottom: 20 }
}

const onFabPointerDown = (event: PointerEvent) => {
  const base = currentFabRect()
  dragState = {
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    startRight: base.right,
    startBottom: base.bottom,
    moved: false,
  }
  window.addEventListener('pointermove', onFabPointerMove)
  window.addEventListener('pointerup', onFabPointerUp, { once: true })
}

const onFabPointerMove = (event: PointerEvent) => {
  if (!dragState || event.pointerId !== dragState.pointerId) return
  const dx = dragState.startX - event.clientX
  const dy = dragState.startY - event.clientY
  if (!dragState.moved && Math.hypot(dx, dy) < 6) return
  dragState.moved = true
  fabPosition.value = clampFabPosition(dragState.startRight + dx, dragState.startBottom + dy)
}

const onFabPointerUp = () => {
  window.removeEventListener('pointermove', onFabPointerMove)
  if (!dragState) return
  const wasMoved = dragState.moved
  dragState = null
  if (!wasMoved) {
    assistantStore.togglePanel()
    return
  }
  if (fabPosition.value) saveFabPosition(fabPosition.value)
}

onBeforeUnmount(() => {
  window.removeEventListener('pointermove', onFabPointerMove)
})

// ----- 点击面板外部自动收起 -----
// 面板与按钮都在 teleport 到 body 的容器里；点击其它区域时收起面板。
// 输入中的拖拽选区等不会触发（它们发生在面板内部）。
const panelRoot = ref<HTMLElement | null>(null)
const widgetRoot = ref<HTMLElement | null>(null)

const onDocumentPointerDown = (event: PointerEvent) => {
  if (!assistantStore.panelOpen) return
  const target = event.target as Node
  // Vuetify 的 overlay/菜单等内容 teleport 到 body 但逻辑上属于面板交互，
  // 只放行助手自己的 DOM 子树（按钮 + 面板 + 它们的 v-select 下拉挂载点按组件树排除）。
  if (widgetRoot.value?.contains(target)) return
  if (panelRoot.value?.contains(target)) return
  assistantStore.panelOpen = false
}

onMounted(() => {
  fabPosition.value = loadFabPosition()
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
})
</script>

<style scoped>
.assistant-widget {
  /* 仅作为点击外部判定的 DOM 子树容器，不产生布局 */
  display: contents;
}

.assistant-fab {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 2300;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.22);
  cursor: grab;
  touch-action: none; /* 拖拽期间禁用浏览器默认触摸行为 */
  user-select: none;
}

.assistant-fab:active {
  cursor: grabbing;
}

.assistant-panel {
  position: fixed;
  right: 20px;
  bottom: 92px;
  z-index: 2301;
  width: min(440px, calc(100vw - 40px));
  height: min(560px, calc(100vh - 140px));
  display: flex;
  flex-direction: column;
  border-radius: 12px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  /* 半透明玻璃拟态：约 50% 透明度 + 背景模糊，透出下层页面内容 */
  background: color-mix(in srgb, rgb(var(--v-theme-surface)) 50%, transparent);
  -webkit-backdrop-filter: blur(14px);
  backdrop-filter: blur(14px);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.3);
  overflow: hidden;
}

.assistant-panel__head {
  display: flex;
  align-items: center;
  padding: 10px 12px;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.assistant-panel__sessions {
  margin: 8px 12px 0;
  flex: 0 0 auto;
}

.assistant-panel__route {
  margin: 6px 12px 0;
  padding: 4px 10px;
  border-radius: 6px;
  background: rgba(var(--v-theme-surface), 0.6);
  font-size: 0.72rem;
  color: rgba(var(--v-theme-on-surface), 0.7);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 0 0 auto;
}

.assistant-panel__messages {
  flex: 1 1 auto;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.assistant-panel__hint {
  margin: auto;
  text-align: center;
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-size: 0.85rem;
  max-width: 280px;
}

.assistant-panel__input {
  display: flex;
  align-items: flex-end;
  padding: 10px 12px 12px;
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.assistant-msg {
  max-width: 88%;
  border-radius: 10px;
  padding: 8px 12px;
}

.assistant-msg--user {
  align-self: flex-end;
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
}

.assistant-msg--bot {
  align-self: flex-start;
  /* 面板已是半透明玻璃，气泡用实底保证正文对比度 */
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-border-color), calc(var(--v-border-opacity) * 0.6));
}

.assistant-msg__content {
  margin: 0;
  font-family: inherit;
  font-size: 0.85rem;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.assistant-msg__content--pending {
  opacity: 0.65;
}

.assistant-msg__actions {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.assistant-action {
  display: flex;
  align-items: baseline;
  gap: 4px;
  font-size: 0.75rem;
  padding: 4px 8px;
  border-radius: 6px;
  background: rgba(var(--v-theme-surface), 0.6);
}

.assistant-action .v-icon {
  align-self: center;
}

.assistant-action--ok {
  color: rgb(var(--v-theme-success));
}

.assistant-action--fail {
  color: rgb(var(--v-theme-error));
}

.assistant-action__name {
  font-weight: 600;
  flex: 0 0 auto;
}

.assistant-action__detail {
  opacity: 0.85;
  word-break: break-all;
}

/* 进出动画 */
.assistant-panel-enter-active,
.assistant-panel-leave-active {
  transition: all 200ms ease;
}

.assistant-panel-enter-from,
.assistant-panel-leave-to {
  opacity: 0;
  transform: translateY(16px) scale(0.98);
}

@media (max-width: 600px) {
  .assistant-fab {
    right: 12px;
    bottom: 12px;
  }

  .assistant-panel {
    right: 8px;
    left: 8px;
    bottom: 84px;
    width: auto;
    height: min(520px, calc(100vh - 120px));
  }
}
</style>
