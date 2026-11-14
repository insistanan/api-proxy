import { readonly, ref, shallowRef } from 'vue'

export interface JsonViewerSource {
  data: unknown
  title?: string
  meta?: string
}

const isOpen = ref(false)
const pinned = ref(false)
const source = shallowRef<JsonViewerSource | null>(null)
let openTimer: ReturnType<typeof setTimeout> | undefined
let closeTimer: ReturnType<typeof setTimeout> | undefined
let hoveredTrigger: symbol | undefined
let activeTrigger: symbol | undefined
let drawerHovered = false

function cancelOpen() { clearTimeout(openTimer); openTimer = undefined }
function cancelClose() { clearTimeout(closeTimer); closeTimer = undefined }
function close() {
  cancelOpen()
  cancelClose()
  isOpen.value = false
  pinned.value = false
  source.value = null
  activeTrigger = undefined
  hoveredTrigger = undefined
  drawerHovered = false
}
function scheduleClose() {
  cancelClose()
  if (!pinned.value && !drawerHovered && !hoveredTrigger) {
    closeTimer = setTimeout(() => close(), 260)
  }
}
function enterTrigger(owner: symbol, getSource: () => JsonViewerSource) {
  hoveredTrigger = owner
  cancelClose()
  cancelOpen()
  if (pinned.value) return
  openTimer = setTimeout(() => {
    source.value = getSource()
    activeTrigger = owner
    isOpen.value = true
  }, 180)
}
function leaveTrigger(owner: symbol) {
  if (hoveredTrigger !== owner) return
  hoveredTrigger = undefined
  cancelOpen()
  scheduleClose()
}
function pinSource(owner: symbol, next: JsonViewerSource) {
  cancelOpen()
  cancelClose()
  source.value = next
  activeTrigger = owner
  pinned.value = true
  isOpen.value = true
}
function releaseTrigger(owner: symbol) {
  leaveTrigger(owner)
  if (activeTrigger === owner && !pinned.value) scheduleClose()
}
function enterDrawer() { drawerHovered = true; cancelClose(); cancelOpen() }
function leaveDrawer() { drawerHovered = false; scheduleClose() }
function togglePin() {
  pinned.value = !pinned.value
  if (!pinned.value) scheduleClose()
}

// 只保存当前预览，不依赖业务 API，不将可能含敏感信息的 JSON 写入持久化状态。
export function useJsonViewerDrawer() {
  return { isOpen: readonly(isOpen), pinned: readonly(pinned), source: readonly(source), enterTrigger, leaveTrigger, releaseTrigger, pinSource, enterDrawer, leaveDrawer, togglePin, close }
}
