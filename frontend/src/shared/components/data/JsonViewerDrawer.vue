<template>
  <UiOverlay :model-value="isOpen" :scrim="false" :z-index="200025" class="inset-y-0 right-0 h-dvh w-[min(660px,94vw)]" persistent scroll-strategy="none" :close-on-back="false">
    <aside class="json-viewer-drawer" aria-label="JSON 查看器">
      <header class="drawer-header">
        <div class="drawer-heading"><h2>{{ source?.title || 'JSON 查看器' }}</h2><p v-if="source?.meta">{{ source.meta }}</p><p>{{ (prepared.bytes / 1024).toFixed(1) }} KB · 按 Esc 或点击关闭</p></div>
        <UiButton icon="mdi-close" variant="text" size="small" aria-label="关闭 JSON 查看器" @click="close" />
      </header>
      <div class="drawer-toolbar">
        <UiToggleGroup v-model="mode" mandatory divided density="compact" variant="outlined" aria-label="JSON 展示方式"><UiButton value="tree" size="small">结构树</UiButton><UiButton value="raw" size="small">格式化原文</UiButton></UiToggleGroup>
        <UiButton size="small" variant="text" prepend-icon="mdi-content-copy" :disabled="!!prepared.error" @click="copyDocument">复制 JSON / 原文</UiButton>
        <UiButton v-if="focusNode" size="small" variant="text" @click="resetRoot">返回根节点</UiButton>
      </div>
      <div v-if="prepared.error" class="drawer-notice" role="alert">{{ prepared.error }}</div>
      <div v-else-if="needsConsent" class="drawer-notice">
        <p>JSON 超过 64 KB，已暂停自动解析以避免卡顿。原文分段展示，复制仍包含完整内容。</p>
        <UiButton v-if="prepared.bytes <= JSON_MAX_PARSE_BYTES" variant="tonal" size="small" class="mt-2" @click="consented = true">按需解析 JSON</UiButton>
        <p v-else>超过 2 MB 的内容仅提供原文与复制；请使用本地工具处理。</p>
      </div>
      <div v-if="parseResult.notice" class="drawer-notice" role="status">{{ parseResult.notice }}</div>
      <template v-if="parseResult.valid && mode === 'tree'">
        <div class="drawer-search"><UiInput v-model="searchInput" label="搜索键名或值" prepend-inner-icon="mdi-magnify" density="compact" variant="outlined" hide-details clearable /></div>
        <div class="drawer-toolbar">
          <UiButton size="small" variant="text" prepend-icon="mdi-unfold-more-horizontal" @click="setExpansion(true)">全部展开</UiButton>
          <UiButton size="small" variant="text" prepend-icon="mdi-chevron-up" @click="setExpansion(false)">全部折叠</UiButton>
          <span class="drawer-hint">{{ focusNode?.path || '$' }} · {{ jsonType(treeValue) }}<template v-if="jsonLength(treeValue) !== undefined"> · {{ jsonLength(treeValue) }} 项/字符</template></span>
        </div>
        <div v-if="tree.limited || tree.searchLimited" class="drawer-notice" role="status">为避免卡顿，最多显示 {{ JSON_MAX_ROWS }} 个节点或连续 {{ JSON_MAX_DEPTH }} 层，搜索最多扫描 20000 项。可缩小关键词，或单独查看子节点继续浏览。</div>
        <div class="drawer-content" role="tree" aria-label="JSON 结构树">
          <JsonTreeNode v-for="row in tree.rows" :key="row.path" :row="row" @toggle="toggleNode" @copy="copyNode" @focus="selectNode" />
          <p v-if="!tree.rows.length" class="drawer-notice">没有匹配的键名或值。</p>
        </div>
      </template>
      <div v-else-if="!prepared.error" class="drawer-content">
        <pre class="drawer-raw">{{ formatted.text.slice(0, rawLimit) }}</pre>
        <p v-if="formatted.notice" class="drawer-notice">{{ formatted.notice }}</p>
        <UiButton v-if="formatted.text.length > rawLimit" variant="tonal" size="small" class="m-3" @click="rawLimit += RAW_PAGE_CHARS">继续显示原文（剩余 {{ (formatted.text.length - rawLimit).toLocaleString() }} 字符）</UiButton>
      </div>
    </aside>
  </UiOverlay>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiOverlay from '@/shared/ui/overlay/Overlay.vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import JsonTreeNode from '@/shared/components/data/JsonTreeNode.vue'
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'
import { useClipboard } from '@/shared/composables/useClipboard'
import { useToast } from '@/shared/composables/useToast'
import { JSON_AUTO_PARSE_BYTES, JSON_MAX_PARSE_BYTES, JSON_MAX_ROWS, JSON_MAX_DEPTH, jsonLength, jsonText, jsonTreeRows, jsonType, type JsonTreeRow } from '@/shared/utils/jsonViewer'

const { isOpen, source, close } = useJsonViewerDrawer()
const { copyText } = useClipboard()
const { showToast } = useToast()
const RAW_PAGE_CHARS = 32 * 1024
const prepared = shallowRef({ raw: '', bytes: 0, error: '' })
const consented = ref(false)
const mode = ref<'tree' | 'raw'>('tree')
const opened = shallowRef<ReadonlySet<string>>(new Set(['$']))
const allExpanded = ref(false)
const searchInput = ref('')
const search = ref('')
const rawLimit = ref(RAW_PAGE_CHARS)
const focusNode = shallowRef<{ value: unknown; path: string } | null>(null)
const needsConsent = computed(() => prepared.value.bytes > JSON_MAX_PARSE_BYTES || (prepared.value.bytes > JSON_AUTO_PARSE_BYTES && !consented.value))
const parseResult = computed(() => {
  if (!isOpen.value || needsConsent.value || prepared.value.error) return { valid: false, value: undefined, notice: '' }
  try { return { valid: true, value: JSON.parse(prepared.value.raw) as unknown, notice: '' } }
  catch (error) { return { valid: false, value: undefined, notice: `非合法 JSON，显示原文：${error instanceof Error ? error.message : String(error)}` } }
})
const treeValue = computed(() => focusNode.value ? focusNode.value.value : parseResult.value.value)
const tree = computed(() => isOpen.value && mode.value === 'tree' && parseResult.value.valid ? jsonTreeRows(treeValue.value, opened.value, allExpanded.value, search.value) : { rows: [], limited: false, searchLimited: false })
const formatted = computed(() => {
  if (!parseResult.value.valid || mode.value !== 'raw') return { text: prepared.value.raw, notice: '' }
  try { return { text: JSON.stringify(treeValue.value, null, 2), notice: '' } }
  catch (error) { return { text: prepared.value.raw, notice: `无法格式化，显示原文：${error instanceof Error ? error.message : String(error)}` } }
})

function setExpansion(expand: boolean) { allExpanded.value = expand; opened.value = new Set() }
function toggleNode(path: string) {
  const next = new Set(opened.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  opened.value = next
}
function resetRoot() { focusNode.value = null; setExpansion(false); opened.value = new Set(['$']); rawLimit.value = RAW_PAGE_CHARS }
function selectNode(row: JsonTreeRow) {
  focusNode.value = { value: row.value, path: `${focusNode.value?.path || '$'}${row.path.slice(1)}` }
  searchInput.value = ''; search.value = ''; setExpansion(false); opened.value = new Set(['$'])
}
async function copyNode(value: unknown) {
  try { await copyText(JSON.stringify(value, null, 2), '已复制节点值') }
  catch (error) { showToast(`节点无法序列化：${error instanceof Error ? error.message : String(error)}`, 'error') }
}
async function copyDocument() {
  if (!parseResult.value.valid || (mode.value === 'raw' && formatted.value.notice)) { await copyText(prepared.value.raw, '已复制完整原文'); return }
  try { await copyText(JSON.stringify(treeValue.value, null, 2), '已复制格式化 JSON') }
  catch (error) { showToast(`JSON 无法格式化：${error instanceof Error ? error.message : String(error)}，可切换原文后复制。`, 'error') }
}
function onKeydown(event: KeyboardEvent) {
  if (isOpen.value && event.key === 'Escape') {
    event.preventDefault()
    event.stopImmediatePropagation()
    close()
  }
}
watch(source, next => {
  consented.value = false; mode.value = 'tree'; searchInput.value = ''; search.value = ''; resetRoot()
  if (!next) { prepared.value = { raw: '', bytes: 0, error: '' }; return }
  try {
    const raw = jsonText(next.data)
    prepared.value = { raw, bytes: new TextEncoder().encode(raw).byteLength, error: '' }
  } catch (error) {
    prepared.value = { raw: '', bytes: 0, error: `无法序列化 JSON：${error instanceof Error ? error.message : String(error)}` }
  }
}, { immediate: true })
watch(searchInput, (value, _previous, onCleanup) => {
  const timer = setTimeout(() => {
    search.value = value || ''
    allExpanded.value = Boolean(search.value.trim())
    opened.value = search.value.trim() ? new Set() : new Set(['$'])
  }, 180)
  onCleanup(() => clearTimeout(timer))
})
onMounted(() => window.addEventListener('keydown', onKeydown, true))
onBeforeUnmount(() => { window.removeEventListener('keydown', onKeydown, true); close() })
</script>

<style scoped>
@reference "@/styles/index.css";

.json-viewer-drawer { @apply h-full flex flex-col min-w-0 text-foreground [background:var(--surface)] [border-left:1px_solid_color-mix(in_srgb,_var(--foreground)_20%,_transparent)] shadow-popover; }
.drawer-header { @apply flex items-start gap-1.5 [padding:18px_12px_12px_20px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)]; }
.drawer-heading { @apply [flex:1] min-w-0; }
h2 { @apply [font-size:16px] [overflow-wrap:anywhere]; }
.drawer-heading p { @apply [font-size:12px] [margin-top:5px] text-muted-foreground [overflow-wrap:anywhere]; }
.drawer-toolbar { @apply flex flex-wrap items-center gap-1.5 [padding:8px_14px]; }
.drawer-search { @apply [padding:4px_18px]; }
.drawer-hint { @apply [font-size:11px] [overflow-wrap:anywhere]; }
.drawer-content { @apply [flex:1] min-h-0 overflow-auto [padding:8px_14px_24px]; }
.drawer-notice { @apply [padding:12px_18px] [font-size:12px] text-muted-foreground [overflow-wrap:anywhere]; }
.drawer-raw { @apply whitespace-pre-wrap [overflow-wrap:anywhere] [font:12px/1.7_var(--font-code,_monospace)] [tab-size:2]; }
</style>
