<template>
  <teleport to="body">
    <!-- 悬浮按钮（可拖拽，位置持久化） -->
    <UiTooltip location="left" text="AI 配置助手" :disabled="dragging">
      <template #activator="{ props }">
        <UiButton
          v-bind="props"
          ref="fabRef"
          class="assistant-fab"
          :style="fabStyle"
          :color="assistantStore.panelOpen ? 'primary' : undefined"
          icon
          size="large"
          aria-label="AI 配置助手"
          :aria-expanded="assistantStore.panelOpen"
          aria-controls="assistant-panel"
          @pointerdown="startDrag('fab', $event)"
          @click="onFabClick"
        >
          <UiIcon size="26">mdi-robot</UiIcon>
        </UiButton>
      </template>
    </UiTooltip>
  </teleport>

  <!-- 助手面板传送到 body，嵌套选项浮层显示在面板上方。 -->
  <UiOverlay
    ref="panelOverlay"
    v-model="assistantStore.panelOpen"
    :activator="fabRef?.$el"
    :open-on-click="false"
    :scrim="false"
    scroll-strategy="none"
    class="assistant-overlay"
    content-class="assistant-panel"
    :content-props="{ id: 'assistant-panel', style: panelStyle, role: 'dialog', 'aria-label': 'AI 配置助手' }"
    :transition="{ name: 'assistant-panel' }"
  >
    <div class="assistant-panel__head" @pointerdown="startDrag('panel', $event)">
      <UiIcon size="20" class="mr-2">mdi-robot</UiIcon>
      <span class="text-sm font-medium font-bold">AI 配置助手</span>
      <span class="flex-1" ></span>
      <UiTooltip location="bottom" text="新会话" :disabled="dragging">
        <template #activator="{ props }">
          <UiButton v-bind="props" icon size="small" variant="text" aria-label="新会话" @click="assistantStore.newSession()">
            <UiIcon size="18">mdi-plus</UiIcon>
          </UiButton>
        </template>
      </UiTooltip>
      <UiTooltip location="bottom" text="删除当前会话" :disabled="dragging">
        <template #activator="{ props }">
          <UiButton
            v-bind="props"
            icon
            size="small"
            variant="text"
            aria-label="删除当前会话"
            :disabled="assistantStore.activeSessionId === null"
            @click="deleteActive"
          >
            <UiIcon size="18">mdi-delete</UiIcon>
          </UiButton>
        </template>
      </UiTooltip>
      <UiButton icon size="small" variant="text" aria-label="关闭助手" @click="assistantStore.panelOpen = false">
        <UiIcon size="18">mdi-close</UiIcon>
      </UiButton>
    </div>

    <div v-if="positionError" class="assistant-panel__position-error" role="status">{{ positionError }}</div>

    <!-- 会话切换 -->
    <UiSelect
      v-if="assistantStore.sessions.length > 0"
      v-model:menu="sessionMenuOpen"
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

    <!-- 渠道选择：自动（四协议优先级编排）或显式指定渠道 -->
    <ChannelPicker
      ref="channelPicker"
      v-model="channelDraft"
      :options="assistantStore.channels"
      allow-auto
      :loading="assistantStore.statusLoading || assistantStore.channelSaving"
      :disabled="!assistantStore.statusChecked || assistantStore.channelSaving || assistantStore.sending"
      class="assistant-panel__channels"
      aria-label="选择助手渠道"
      @select="selectChannel"
      @menu="channelMenuOpen = $event"
    />

    <!-- 当前生效的模型路由：分组（匹配词）→ 命中渠道 -->
    <div v-if="assistantStore.available" class="assistant-panel__route" title="在 设置 → AI 助手 中修改">
      {{ routeLabel }}
    </div>

    <!-- 消息区 -->
    <div ref="messagesRef" class="assistant-panel__messages">
      <div v-if="assistantStore.messages.length === 0" class="assistant-panel__hint">
        <UiIcon size="22" class="mb-2">mdi-chat-outline</UiIcon>
        <div>问我任何配置问题，或直接说"帮我建一个 chat 渠道"。</div>
        <div class="text-xs text-muted-foreground mt-1">协议转换、分组、兜底、模型映射都可以问</div>
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
              <UiIcon size="14" class="mr-1">
                {{ action.success ? 'mdi-check-circle' : 'mdi-close-circle' }}
              </UiIcon>
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

    <div v-if="statusMessage" id="assistant-status-message" class="assistant-panel__notice" role="status">
      <span>{{ statusMessage }}</span>
      <UiButton
        size="small"
        variant="text"
        :loading="assistantStore.statusLoading"
        :disabled="assistantStore.channelSaving || assistantStore.sending"
        @click="refreshStatus"
      >
        重新检查
      </UiButton>
    </div>

    <!-- 渠道不可用时仍允许编辑草稿；可用性只限制发送。 -->
    <div class="assistant-panel__input" @pointerdown="closeMenus" @focusin="closeMenus">
      <UiTextarea
        v-model="draft"
        :rows="draft.includes('\n') ? 3 : 1"
        auto-grow
        max-rows="5"
        variant="outlined"
        density="compact"
        hide-details
        placeholder="输入你的问题…（Enter 发送）"
        aria-label="助手消息"
        :aria-describedby="statusMessage ? 'assistant-status-message' : undefined"
        :disabled="assistantStore.sending"
        @keydown.enter.exact="onDraftEnter"
      />
      <UiButton
        class="ml-2"
        color="primary"
        variant="flat"
        icon
        size="small"
        aria-label="发送"
        :loading="assistantStore.sending"
        :disabled="!assistantStore.available || assistantStore.statusLoading || assistantStore.channelSaving || !draft.trim()"
        @click="send"
      >
        <UiIcon size="18">mdi-send</UiIcon>
      </UiButton>
    </div>
  </UiOverlay>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiOverlay from '@/shared/ui/overlay/Overlay.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAssistantStore } from '@/features/assistant/stores/assistant'
import type { AssistantSession } from '@/api'
import ChannelPicker from '@/shared/components/domain/ChannelPicker.vue'

const assistantStore = useAssistantStore()
const draft = ref('')
const sendError = ref('')
const messagesRef = ref<HTMLElement | null>(null)

const statusMessage = computed(() => {
  if (sendError.value) return sendError.value
  if (assistantStore.unavailableReason) return assistantStore.unavailableReason
  if (assistantStore.statusChecked && !assistantStore.available) {
    return '当前没有可用的助手渠道，请选择其他渠道或恢复渠道后重新检查。'
  }
  return ''
})

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

// 协议和分组选择只编辑本地状态，选定渠道后才保存，避免中途切成自动路由。
const channelDraft = ref(assistantStore.channelId)
const channelPicker = ref<{ closeMenus: () => void }>()
watch(() => assistantStore.channelId, value => { channelDraft.value = value })

const refreshStatus = async () => {
  sendError.value = ''
  await assistantStore.checkStatus()
}

const selectChannel = async (channelId: string) => {
  sendError.value = ''
  await assistantStore.selectChannel(channelId)
}

// 路由信息一行：分组「匹配词」→ 渠道名（协议）；自动模式下走兜底分组。
const routeLabel = computed(() => {
  if (assistantStore.channelId) {
    const channel = assistantStore.channel
    const model = assistantStore.model ? `分组「${assistantStore.model}」` : '自动（兜底分组）'
    const target = channel ? `${channel.name}（${kindLabels[channel.kind] ?? channel.kind}）` : '（渠道不可用）'
    return `指定渠道 → ${target} · ${model}`
  }
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
  if (
    !content ||
    assistantStore.sending ||
    assistantStore.statusLoading ||
    assistantStore.channelSaving ||
    !assistantStore.available
  ) {
    return
  }
  sendError.value = ''
  draft.value = ''
  try {
    await assistantStore.send(content)
  } catch (error) {
    // 发送失败保留草稿，并在输入区明确展示原因。
    draft.value = content
    sendError.value = `发送失败：${error instanceof Error ? error.message : String(error)}`
  } finally {
    void assistantStore.checkStatus()
  }
}

const onDraftEnter = (event: KeyboardEvent) => {
  // 中文输入法确认候选时的 Enter 不应被拦截为发送。
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  void send()
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

// ----- 浮窗位置与拖拽 -----
// 只在手势结束时提交响应式坐标；移动帧直接更新 transform，避免重渲染消息列表。
type FloatingTarget = 'fab' | 'panel'
type FloatingPosition = { right: number; bottom: number }

const POSITION_KEYS: Record<FloatingTarget, string> = {
  fab: 'assistant-fab-position',
  panel: 'assistant-panel-position',
}
const EDGE_GAP = 8
const DRAG_THRESHOLD = 4

const fabRef = ref<{ $el?: HTMLElement } | null>(null)
const panelOverlay = ref<{ contentEl?: HTMLElement } | null>(null)
const positions = {
  fab: ref<FloatingPosition | null>(null),
  panel: ref<FloatingPosition | null>(null),
}
const positionError = ref('')
const dragging = ref(false)
const sessionMenuOpen = ref(false)
const channelMenuOpen = ref(false)
const viewportSize = ref<{ width: number; height: number } | null>(null)

const closeMenus = () => {
  sessionMenuOpen.value = false
  channelMenuOpen.value = false
  channelPicker.value?.closeMenus()
}

watch(channelMenuOpen, open => {
  if (!open) return
  sessionMenuOpen.value = false
  // 面板保持打开时渠道也可能熔断；每次展开刷新，避免一直使用旧候选。
  if (!assistantStore.statusLoading && !assistantStore.channelSaving && !assistantStore.sending) {
    void refreshStatus()
  }
})

watch(sessionMenuOpen, open => {
  if (open) { channelMenuOpen.value = false; channelPicker.value?.closeMenus() }
})

const positionStyle = (position: FloatingPosition | null) =>
  position ? { right: `${position.right}px`, bottom: `${position.bottom}px` } : {}

const fabStyle = computed(() => positionStyle(positions.fab.value))
const panelStyle = computed(() => ({
  ...positionStyle(positions.panel.value),
  ...(viewportSize.value
    ? {
        maxWidth: `${Math.max(0, viewportSize.value.width - EDGE_GAP * 2)}px`,
        maxHeight: `${Math.max(0, viewportSize.value.height - EDGE_GAP * 2)}px`,
      }
    : {}),
}))

const loadPosition = (target: FloatingTarget): FloatingPosition | null => {
  try {
    const raw = localStorage.getItem(POSITION_KEYS[target])
    if (raw === null) return null
    const value: unknown = JSON.parse(raw)
    if (
      !value ||
      typeof value !== 'object' ||
      !('right' in value) ||
      !('bottom' in value) ||
      typeof value.right !== 'number' ||
      typeof value.bottom !== 'number' ||
      !Number.isFinite(value.right) ||
      !Number.isFinite(value.bottom)
    ) {
      throw new Error('位置坐标必须是有限数字')
    }
    return { right: value.right, bottom: value.bottom }
  } catch (error) {
    positionError.value = `读取${target === 'fab' ? '助手按钮' : '助手面板'}位置失败，已使用默认位置：${error instanceof Error ? error.message : String(error)}`
    return null
  }
}

const savePosition = (target: FloatingTarget, position: FloatingPosition) => {
  try {
    localStorage.setItem(POSITION_KEYS[target], JSON.stringify(position))
  } catch (error) {
    positionError.value = `保存助手位置失败，当前位置仅本次有效：${error instanceof Error ? error.message : String(error)}`
  }
}

const getFloatingElement = (target: FloatingTarget) =>
  target === 'fab' ? fabRef.value?.$el : panelOverlay.value?.contentEl

const getViewport = () => {
  const viewport = window.visualViewport
  return {
    left: viewport?.offsetLeft ?? 0,
    top: viewport?.offsetTop ?? 0,
    width: viewport?.width ?? document.documentElement.clientWidth,
    height: viewport?.height ?? document.documentElement.clientHeight,
  }
}

const applyPosition = (target: FloatingTarget, element: HTMLElement, position: FloatingPosition) => {
  positions[target].value = position
  // 同步落位后再清除拖拽 transform，避免 Vue 下次更新前闪回原点。
  element.style.right = `${position.right}px`
  element.style.bottom = `${position.bottom}px`
}

const fitInViewport = (target: FloatingTarget) => {
  if (target === 'panel' && !assistantStore.panelOpen) return
  const element = getFloatingElement(target)
  if (!element) return
  const rect = element.getBoundingClientRect()
  const viewport = getViewport()
  const minLeft = viewport.left + EDGE_GAP
  const minTop = viewport.top + EDGE_GAP
  const left = Math.max(minLeft, Math.min(rect.left, viewport.left + viewport.width - rect.width - EDGE_GAP))
  const top = Math.max(minTop, Math.min(rect.top, viewport.top + viewport.height - rect.height - EDGE_GAP))
  if (left === rect.left && top === rect.top) return
  applyPosition(target, element, {
    right: document.documentElement.clientWidth - left - rect.width,
    bottom: document.documentElement.clientHeight - top - rect.height,
  })
}

type DragState = {
  target: FloatingTarget
  element: HTMLElement
  handle: HTMLElement
  pointerId: number
  startX: number
  startY: number
  startPosition: FloatingPosition
  minX: number
  maxX: number
  minY: number
  maxY: number
  dx: number
  dy: number
  moved: boolean
}

let dragState: DragState | null = null
let dragFrame: number | null = null
let viewportFrame: number | null = null
let suppressFabClick = false
let disposed = false

const renderDrag = () => {
  dragFrame = null
  if (!dragState?.moved) return
  dragState.element.style.transform = `translate3d(${dragState.dx}px, ${dragState.dy}px, 0)`
}

const updateDrag = (event: PointerEvent) => {
  if (!dragState || event.pointerId !== dragState.pointerId) return
  const dx = event.clientX - dragState.startX
  const dy = event.clientY - dragState.startY
  if (!dragState.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return
  dragState.moved = true
  dragState.dx = Math.max(dragState.minX, Math.min(dx, dragState.maxX))
  dragState.dy = Math.max(dragState.minY, Math.min(dy, dragState.maxY))
  if (dragFrame === null) dragFrame = requestAnimationFrame(renderDrag)
}

const finishDrag = (cancelled = false, persist = true) => {
  const state = dragState
  if (!state) return
  dragState = null
  if (dragFrame !== null) {
    cancelAnimationFrame(dragFrame)
    dragFrame = null
  }
  state.handle.removeEventListener('pointermove', updateDrag)
  state.handle.removeEventListener('pointerup', onPointerUp)
  state.handle.removeEventListener('pointercancel', onPointerCancel)
  state.handle.removeEventListener('lostpointercapture', onPointerCancel)
  window.removeEventListener('blur', onDragBlur)
  if (state.handle.hasPointerCapture(state.pointerId)) {
    state.handle.releasePointerCapture(state.pointerId)
  }
  if (state.target === 'fab') suppressFabClick = cancelled || state.moved
  if (state.moved) {
    const position = {
      right: state.startPosition.right - state.dx,
      bottom: state.startPosition.bottom - state.dy,
    }
    applyPosition(state.target, state.element, position)
    if (persist) savePosition(state.target, position)
  }
  state.element.style.removeProperty('transform')
  state.element.classList.remove('assistant-floating--dragging')
  dragging.value = false
  if (persist) scheduleViewportFit()
}

const onPointerUp = (event: PointerEvent) => {
  if (!dragState || event.pointerId !== dragState.pointerId) return
  // pointerup 可能先于最后一个动画帧，仍须采用最后的指针位置。
  updateDrag(event)
  finishDrag()
}

const onPointerCancel = (event: PointerEvent) => {
  if (event.pointerId === dragState?.pointerId) finishDrag(true)
}

const onDragBlur = () => finishDrag(true)

const startDrag = (target: FloatingTarget, event: PointerEvent) => {
  if (dragState || !event.isPrimary || event.button !== 0) return
  if (
    target === 'panel' &&
    event.target instanceof Element &&
    event.target.closest('button, a, input, textarea, select, [role="button"]')
  ) {
    return
  }
  const element = getFloatingElement(target)
  const handle = event.currentTarget
  if (!element || !(handle instanceof HTMLElement)) return

  // 从实际布局取起点，兼容移动端默认边距、已保存位置与实际按钮大小。
  const rect = element.getBoundingClientRect()
  const viewport = getViewport()
  const minX = viewport.left + EDGE_GAP - rect.left
  const minY = viewport.top + EDGE_GAP - rect.top
  event.preventDefault()
  handle.setPointerCapture(event.pointerId)
  dragState = {
    target,
    element,
    handle,
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    startPosition: {
      right: document.documentElement.clientWidth - rect.right,
      bottom: document.documentElement.clientHeight - rect.bottom,
    },
    minX,
    maxX: Math.max(minX, viewport.left + viewport.width - EDGE_GAP - rect.right),
    minY,
    maxY: Math.max(minY, viewport.top + viewport.height - EDGE_GAP - rect.bottom),
    dx: 0,
    dy: 0,
    moved: false,
  }
  if (target === 'fab') suppressFabClick = false
  closeMenus()
  dragging.value = true
  element.classList.add('assistant-floating--dragging')
  handle.addEventListener('pointermove', updateDrag, { passive: true })
  handle.addEventListener('pointerup', onPointerUp)
  handle.addEventListener('pointercancel', onPointerCancel)
  handle.addEventListener('lostpointercapture', onPointerCancel)
  window.addEventListener('blur', onDragBlur)
}

const onFabClick = (event: MouseEvent) => {
  // 键盘 Enter/空格产生的 click 仍有效；拖拽后的指针 click 不开关面板。
  if (suppressFabClick && event.detail > 0) {
    suppressFabClick = false
    return
  }
  assistantStore.togglePanel()
}

const scheduleViewportFit = () => {
  if (disposed) return
  if (dragState) finishDrag(true)
  if (viewportFrame !== null) return
  viewportFrame = requestAnimationFrame(async () => {
    viewportFrame = null
    // 等待帧期间若开始了新手势，等释放后再校正，避免中途改动拖拽基准。
    if (disposed || dragState) return
    const viewport = getViewport()
    viewportSize.value = { width: viewport.width, height: viewport.height }
    await nextTick()
    if (disposed || dragState) return
    fitInViewport('fab')
    fitInViewport('panel')
  })
}

watch(
  () => assistantStore.panelOpen,
  open => {
    if (open) {
      scheduleViewportFit()
    } else {
      closeMenus()
      if (dragState?.target === 'panel') finishDrag(true)
    }
  },
  { flush: 'post' }
)

onMounted(() => {
  positions.fab.value = loadPosition('fab')
  positions.panel.value = loadPosition('panel')
  scheduleViewportFit()
  window.addEventListener('resize', scheduleViewportFit, { passive: true })
  window.visualViewport?.addEventListener('resize', scheduleViewportFit, { passive: true })
  window.visualViewport?.addEventListener('scroll', scheduleViewportFit, { passive: true })
})

onBeforeUnmount(() => {
  disposed = true
  finishDrag(true, false)
  if (viewportFrame !== null) cancelAnimationFrame(viewportFrame)
  window.removeEventListener('resize', scheduleViewportFit)
  window.visualViewport?.removeEventListener('resize', scheduleViewportFit)
  window.visualViewport?.removeEventListener('scroll', scheduleViewportFit)
})
</script>

<style scoped>
@reference "@/styles/index.css";

.assistant-fab {
  @apply fixed right-5 bottom-5;
  /* 悬浮按钮位于助手面板、菜单和对话框下方。 */
  @apply [z-index:1999] [background:var(--surface)] [border:1px_solid_var(--border)] shadow-popover cursor-grab [touch-action:none]; /* 拖拽期间禁用浏览器默认触摸行为 */
  @apply [user-select:none] [transition-property:box-shadow,_opacity,_background];
}

.assistant-fab:active {
  @apply cursor-grabbing;
}

.assistant-overlay.assistant-panel {
  @apply fixed right-5 bottom-23 [width:min(440px,_calc(100vw_-_40px))] [height:min(560px,_calc(100vh_-_140px))] [height:min(560px,_calc(100dvh_-_140px))] flex flex-col rounded-card [border:1px_solid_var(--border)];
  /* 半透明玻璃拟态：约 50% 透明度 + 背景模糊，透出下层页面内容 */
  @apply [background:color-mix(in_srgb,_var(--surface)_50%,_transparent)] [-webkit-backdrop-filter:blur(14px)] [backdrop-filter:blur(14px)] shadow-popover;
  /* 极矮视口仍可滚动到输入区，避免固定头部和选择器把输入框裁掉。 */
  @apply overflow-x-hidden overflow-y-auto [overscroll-behavior:contain];
}

.assistant-fab.assistant-floating--dragging,
.assistant-overlay.assistant-panel.assistant-floating--dragging {
  @apply [transition:none] [will-change:transform];
}

/* 拖动时暂停背景模糊，避免每帧重新采样底层页面；释放后恢复玻璃效果。 */
.assistant-overlay.assistant-panel.assistant-floating--dragging {
  @apply [background:var(--surface)] [-webkit-backdrop-filter:none] [backdrop-filter:none];
}

.assistant-panel__head {
  @apply flex [flex:0_0_auto] cursor-grab [touch-action:none] [user-select:none] items-center [padding:10px_12px] [border-bottom:1px_solid_var(--border)];
}

.assistant-panel__head:active {
  @apply cursor-grabbing;
}

.assistant-panel__position-error {
  @apply [flex:0_0_auto] [margin:6px_12px_0] max-h-16 overflow-y-auto text-destructive [font-size:0.75rem];
}

.assistant-panel__sessions {
  @apply [margin:8px_12px_0] [flex:0_0_auto];
}

.assistant-panel__channels {
  @apply [margin:6px_12px_0] [flex:0_0_auto];
}

.assistant-panel__route {
  @apply [margin:6px_12px_0] [padding:4px_10px] rounded-badge [background:color-mix(in_srgb,_var(--surface)_60%,_transparent)] [font-size:0.72rem] [color:color-mix(in_srgb,_var(--foreground)_70%,_transparent)] whitespace-nowrap overflow-hidden text-ellipsis [flex:0_0_auto];
}

.assistant-panel__messages {
  @apply [flex:1_1_auto] min-h-0 overflow-y-auto [overscroll-behavior:contain] p-3 flex flex-col gap-2.5;
}

.assistant-panel__hint {
  @apply m-auto text-center [color:color-mix(in_srgb,_var(--foreground)_60%,_transparent)] [font-size:0.85rem] max-w-70;
}

.assistant-panel__notice {
  @apply flex [flex:0_0_auto] items-start gap-2 [padding:8px_12px] text-warning [font-size:0.75rem];
}

.assistant-panel__notice > span {
  @apply [flex:1_1_auto] min-w-0 max-h-20 overflow-y-auto [overflow-wrap:anywhere];
}

.assistant-panel__notice > .ui-button {
  @apply [flex:0_0_auto];
}

.assistant-panel__input {
  @apply flex [flex:0_0_auto] items-end [padding:10px_12px_12px] [border-top:1px_solid_var(--border)];
}

.assistant-msg {
  @apply [max-width:88%] rounded-control [padding:8px_12px];
}

.assistant-msg--user {
  @apply [align-self:flex-end] bg-primary text-primary-foreground;
}

.assistant-msg--bot {
  @apply [align-self:flex-start];
  /* 面板已是半透明玻璃，气泡用实底保证正文对比度 */
  @apply [background:var(--surface)] [border:1px_solid_var(--border-soft)];
}

.assistant-msg__content {
  @apply m-0 [font-family:inherit] [font-size:0.85rem] [line-height:1.55] whitespace-pre-wrap [word-break:break-word];
}

.assistant-msg__content--pending {
  @apply opacity-65;
}

.assistant-msg__actions {
  @apply mt-2 flex flex-col gap-1;
}

.assistant-action {
  @apply flex items-baseline gap-1 [font-size:0.75rem] [padding:4px_8px] rounded-badge [background:color-mix(in_srgb,_var(--surface)_60%,_transparent)];
}

.assistant-action .ui-icon {
  @apply [align-self:center];
}

.assistant-action--ok {
  @apply text-success;
}

.assistant-action--fail {
  @apply text-destructive;
}

.assistant-action__name {
  @apply font-semibold [flex:0_0_auto];
}

.assistant-action__detail {
  @apply opacity-85 [word-break:break-all];
}

/* 进出只改变透明度，避免动画与拖拽争用 transform。 */
.assistant-overlay :deep(.assistant-panel-enter-active),
.assistant-overlay :deep(.assistant-panel-leave-active) {
  @apply [transition:opacity_150ms_ease];
}

.assistant-overlay :deep(.assistant-panel-enter-from),
.assistant-overlay :deep(.assistant-panel-leave-to) {
  @apply opacity-0;
}

@media (prefers-reduced-motion: reduce) {
  .assistant-overlay :deep(.assistant-panel-enter-active),
  .assistant-overlay :deep(.assistant-panel-leave-active) {
    @apply [transition:none];
  }
}

@media (max-width: 600px) {
  .assistant-fab {
    @apply right-3 bottom-3;
  }

  .assistant-overlay.assistant-panel {
    @apply right-2 bottom-21 [width:calc(100vw_-_16px)] [height:min(520px,_calc(100vh_-_120px))] [height:min(520px,_calc(100dvh_-_120px))];
  }
}
</style>
