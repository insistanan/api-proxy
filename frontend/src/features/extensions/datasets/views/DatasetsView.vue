<template>
  <div class="datasets-page">
    <PageHeader title="数据集" description="内置意图与工具风险样本已保存在本地，选择后即可查看和审核。">
      <UiButton variant="outlined" :disabled="busy" @click="create">新建数据集</UiButton>
    </PageHeader>
    <UiAlert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</UiAlert>
    <UiAlert v-if="notice" type="success" variant="tonal" class="mb-4">{{ notice }}</UiAlert>
    <UiCard class="mb-4 p-4">
      <div class="dataset-toolbar mb-3">
        <div>
          <h2 class="text-base font-medium">本地数据集 <span class="text-muted-foreground">{{ store.items.length }}</span></h2>
          <p class="text-xs text-muted-foreground">选择下方数据集直接查看样本，审核并保存后再构建向量索引。</p>
        </div>
        <UiFileInput label="导入自定义 JSON" accept="application/json,.json" density="compact" hide-details :disabled="busy" @update:model-value="importFile" />
      </div>
      <div v-if="store.items.length" class="dataset-list" aria-label="本地数据集">
        <button v-for="item in store.items" :key="item.id" type="button" class="dataset-choice" :class="{ 'dataset-choice--selected': draft?.revision && draft.id === item.id }" :aria-pressed="!!draft?.revision && draft.id === item.id" :disabled="busy" @click="selectDataset(item.id)">
          <span class="flex items-center justify-between gap-2">
            <span class="text-sm font-medium">{{ item.name }}</span>
            <UiBadge size="x-small" variant="tonal">{{ item.kind === 'intent' ? '意图路由' : '工具安全' }}</UiBadge>
          </span>
          <span class="text-sm">{{ item.count }} 条样本 <span class="text-muted-foreground">· {{ item.labels.length }} 个类别</span></span>
          <span class="text-xs text-muted-foreground">已审核 {{ item.approved }} / {{ item.count }} · {{ item.indexes.length ? `${item.indexes.length} 个已发布索引` : '尚未构建索引' }}</span>
        </button>
      </div>
    </UiCard>
    <UiProgress v-if="busy" indeterminate color="primary" class="mb-4" />
    <EmptyState
      v-if="!draft && !busy"
      icon="mdi-database-plus-outline"
      :title="store.items.length ? '请选择数据集' : '暂无本地数据集'"
      description="选择上方数据集查看样本，也可以新建数据集或导入自己的 JSON。"
    >
      <UiButton variant="outlined" @click="create">新建数据集</UiButton>
    </EmptyState>
    <template v-if="draft">
      <div class="flex flex-wrap items-center gap-3 mb-4">
        <UiBadge size="small">{{ draft.kind === 'intent' ? '意图路由' : '工具安全' }}</UiBadge>
        <span class="text-sm">{{ draft.samples.length }} 条样本 · {{ draft.revision ? `修订 ${draft.revision}` : '尚未保存' }}</span>
        <UiBadge v-if="dirty" size="small" color="warning">草稿未保存</UiBadge>
        <span class="flex-1" ></span>
        <UiButton v-if="draft.revision" variant="text" color="error" :disabled="busy || dirty" @click="removeDataset">删除数据集</UiButton>
        <UiButton variant="text" :disabled="busy" @click="exportFile">导出 JSON</UiButton>
        <JsonPreviewTrigger :data="draft" title="数据集 JSON" inline label="查看 JSON" />
        <UiButton color="primary" :loading="action === 'save'" :disabled="busy || !dirty" @click="save">保存数据集</UiButton>
      </div>
      <UiForm :disabled="busy" class="dataset-editor" @submit.prevent>
        <div class="dataset-fields">
          <UiInput v-model="draft.name" label="数据集名称" />
          <UiInput v-model="draft.id" label="稳定 ID" :disabled="draft.revision > 0" hint="英文、数字、下划线或短横线" />
          <UiSelect v-model="draft.kind" :items="kinds" label="数据集用途" :disabled="draft.revision > 0" />
        </div>
        <UiInput v-model="draft.description" label="说明与来源" />
        <UiTabs v-model="tab" color="primary" class="mb-4"><UiButton role="tab" variant="text" value="samples">样本审核</UiButton><UiButton role="tab" variant="text" value="index">向量索引</UiButton><UiButton role="tab" variant="text" value="evaluation">独立评测</UiButton></UiTabs>
        <div v-if="tab === 'samples'">
          <div class="flex flex-wrap items-center gap-3 mb-3">
            <UiInput v-model="search" label="搜索样本、类别或来源" hide-details density="compact" class="sample-search" />
            <UiButton variant="tonal" @click="editSample()">添加样本</UiButton>
            <UiButton variant="text" :disabled="!selected.length" @click="approve(true)">批准所选（{{ selected.length }}）</UiButton>
            <UiButton variant="text" :disabled="!selected.length" @click="approve(false)">撤回批准</UiButton>
          </div>
          <p class="text-xs text-muted-foreground mb-3">批准的 index 样本参与匹配；eval 样本只用于评测。同一样本族不能跨两个集合。撤回批准并发布新索引后才影响业务请求。</p>
          <UiDataTable v-model="selected" :headers="headers" :items="draft.samples" :search="search" item-value="id" show-select :items-per-page="10" :items-per-page-options="[10, 25, 50]">
            <template #[`item.text`]="{ item }"><div class="sample-text">{{ item.text }}</div></template>
            <template #[`item.negative`]="{ item }">{{ item.negative ? '反例' : '正例' }}</template>
            <template #[`item.approved`]="{ item }"><UiBadge size="small" :color="item.approved ? 'success' : undefined">{{ item.approved ? '已批准' : '待审核' }}</UiBadge></template>
            <template #[`item.actions`]="{ item }"><UiButton size="small" variant="text" @click="editSample(item)">编辑</UiButton></template>
          </UiDataTable>
        </div>
        <div v-else>
          <UiAlert v-if="dirty" type="warning" variant="tonal" class="mb-4">请先保存草稿，再构建、检索或评测。</UiAlert>
          <EmbeddingProfileFields v-model="profile" />
          <template v-if="tab === 'index'">
            <div class="flex flex-wrap gap-3 mb-4">
              <UiButton color="primary" :disabled="dirty || !draft.revision" :loading="action === 'build'" @click="build">增量构建并发布</UiButton>
              <span class="text-xs self-center">只计算变化的文本；失败保留旧索引。模型升级需更新版本标识。</span>
            </div>
            <UiTable v-if="published.length" density="comfortable" class="mb-5">
              <thead><tr><th>模型</th><th>数据修订</th><th>样本 / 维度</th><th>构建时间</th><th>配置</th></tr></thead>
              <tbody><tr v-for="index in published" :key="index.space + JSON.stringify(index.profile)">
                <td><JsonPreviewTrigger :data="index.profile" title="向量索引配置">{{ index.profile.model }} · {{ index.profile.modelVersion || '未指定版本' }}</JsonPreviewTrigger></td>
                <td>{{ index.revision }} {{ index.revision === draft.revision ? '（当前）' : '（有待发布修改）' }}</td>
                <td>{{ index.count }} / {{ index.dimension }}</td><td>{{ formatDateTime(index.builtAt) }}</td>
                <td><UiButton variant="text" size="small" :disabled="busy" @click="profile = clone(index.profile)">使用此配置</UiButton><UiButton variant="text" size="small" color="error" :disabled="busy" @click="removeIndex(index)">删除索引</UiButton></td>
              </tr></tbody>
            </UiTable>
            <UiTextarea v-model="query" label="检索模拟文本" rows="3" />
            <UiButton variant="tonal" :disabled="dirty || !draft.revision || !query.trim()" :loading="action === 'search'" @click="preview">检索已发布索引</UiButton>
            <div v-if="result" class="mt-4"><p class="text-sm mb-2">检索耗时 {{ result.durationMs }} ms</p><UiTable><thead><tr><th>类别</th><th>相似度</th><th>样本</th></tr></thead><tbody><tr v-for="hit in result.hits" :key="hit.datasetId + hit.sample.id"><td>{{ hit.sample.label }} · {{ hit.sample.negative ? '反例' : '正例' }}</td><td>{{ hit.score.toFixed(4) }}</td><td class="sample-text">{{ hit.sample.text }}</td></tr></tbody></UiTable></div>
          </template>
          <template v-else>
            <div class="dataset-fields"><UiInput v-model.number="threshold" type="number" min="0" max="1" step="0.01" label="相似度阈值" /><UiInput v-model.number="margin" type="number" min="0" max="1" step="0.01" label="意图正反例最小差值" /></div>
            <p class="text-sm text-muted-foreground mb-4">评测当前修订中已批准的 eval 样本，最多 500 条。安全数据集评测的是风险召回，不代表 AI 闸门最终阻断准确率。真实样本应与内置合成样本分开评估。</p>
            <UiButton variant="tonal" :disabled="dirty || !draft.revision" :loading="action === 'evaluate'" @click="evaluate">运行独立评测</UiButton>
            <div v-if="evaluation" class="mt-4"><UiAlert type="info" variant="tonal" class="mb-3">{{ evaluation.correct }} / {{ evaluation.total }} 正确；误报 {{ evaluation.falsePositive }}，漏报或分类错误 {{ evaluation.falseNegative }}；总耗时 {{ evaluation.durationMs }} ms</UiAlert><UiDataTable :headers="evaluationHeaders" :items="evaluation.cases" :items-per-page="10"><template #[`item.correct`]="{ item }">{{ item.correct ? '正确' : '待改进' }}</template><template #[`item.score`]="{ item }">{{ item.score.toFixed(4) }}</template></UiDataTable></div>
          </template>
        </div>
      </UiForm>
    </template>
    <AppDialog v-model="sampleDialog" title="编辑样本" size="lg">
      <template v-if="sample">
      <div class="dataset-fields"><UiInput v-model="sample.id" label="样本 ID" /><UiInput v-model="sample.label" label="类别标识" /><UiInput v-model="sample.family" label="样本族" /><UiSelect v-model="sample.split" :items="splits" label="样本用途" /></div>
      <UiTextarea v-model="sample.text" label="样本文本或完整命令" rows="5" />
      <UiInput v-model="sample.source" label="来源" />
      <div class="flex gap-4"><UiCheckbox v-model="sample.negative" label="反例" /><UiCheckbox v-model="sample.approved" label="已审核，批准参与索引或评测" /></div>
      <template v-if="draft?.kind === 'safety'"><p class="text-xs mb-3">类别使用 destructive、download_execute、reverse_shell、privilege_escalation 或 environment_tampering。</p><UiInput v-model="sample.tool" label="适用执行工具（可选）" hint="填写去除命名空间后的工具名；留空适用于全部执行工具" persistent-hint /><UiInput v-model="sample.platform" label="平台说明（windows / posix / any）" /><UiTextarea v-model="sample.pattern" label="直接阻断核验正则（可选，Go RE2）" rows="2" hint="留空时语义命中仍需 AI 审核；不要用宽泛正则代替执行范围核验" persistent-hint /></template>
      <UiAlert v-if="sampleError" type="error" variant="tonal">{{ sampleError }}</UiAlert>
      </template>
      <template #actions>
        <UiButton v-if="editingID" color="error" variant="text" @click="removeSample">移除样本</UiButton>
        <span class="flex-1" ></span>
        <UiButton variant="text" @click="sampleDialog = false">取消</UiButton>
        <UiButton color="primary" @click="applySample">应用到草稿</UiButton>
      </template>
    </AppDialog>
    <ConfirmDialog
      :model-value="pendingConfirm !== null"
      :message="pendingConfirm?.message ?? ''"
      :title="pendingConfirm?.title"
      :confirm-text="pendingConfirm?.confirmText"
      :cancel-text="pendingConfirm?.cancelText"
      :confirm-color="pendingConfirm?.confirmColor"
      @update:model-value="cancelConfirm"
      @confirm="acceptConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiCheckbox from '@/shared/ui/check/Checkbox.vue'
import UiDataTable from '@/shared/ui/data/DataTable.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiFileInput from '@/shared/ui/field/FileInput.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiTabs from '@/shared/ui/navigation/Tabs.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { datasetsApi } from '@/features/extensions/datasets/services/datasets'
import { defaultEmbeddingProfile } from '@/features/extensions/datasets/config'
import type { Dataset, DatasetSample, Evaluation, IndexStatus, SearchResult } from '@/types'
import { intentErrorText } from '@/features/channels'
import { useDatasetsStore } from '@/features/extensions/datasets/stores/datasets'
import { parseDatasetImport } from '@/features/extensions/datasets/utils/datasetImport'
import EmbeddingProfileFields from '@/features/extensions/datasets/components/EmbeddingProfileFields.vue'
import { formatDateTime } from '@/shared/utils/datetime'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { useConfirm } from '@/shared/composables/useConfirm'
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()
const store = useDatasetsStore()
const draft = ref<Dataset>()
const saved = ref('')
const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== saved.value)
const action = ref('')
const busy = computed(() => !!action.value)
const error = ref('')
const notice = ref('')
const tab = ref('samples')
const search = ref('')
const selected = ref<string[]>([])
const profile = ref(defaultEmbeddingProfile())
const published = computed(() => store.items.find(item => item.id === draft.value?.id)?.indexes ?? [])
const query = ref('')
const result = ref<SearchResult>()
const evaluation = ref<Evaluation>()
const threshold = ref(0.8)
const margin = ref(0.1)
const sampleDialog = ref(false)
const sample = ref<DatasetSample>()
const sampleError = ref('')
const editingID = ref('')
const kinds = [{ title: '意图路由', value: 'intent' }, { title: '工具安全', value: 'safety' }]
const splits = [{ title: '索引样本', value: 'index' }, { title: '独立评测', value: 'eval' }]
const headers = [{ title: '类别', key: 'label' }, { title: '文本', key: 'text', sortable: false }, { title: '正反例', key: 'negative' }, { title: '集合', key: 'split' }, { title: '审核', key: 'approved' }, { title: '操作', key: 'actions', sortable: false }]
const evaluationHeaders = [{ title: '样本', key: 'id' }, { title: '期望', key: 'expected' }, { title: '实际', key: 'actual' }, { title: '相似度', key: 'score' }, { title: '结果', key: 'correct' }]
function mayDiscard(): Promise<boolean> {
  if (!dirty.value) return Promise.resolve(true)
  return new Promise(resolve => {
    askConfirm({
      message: '当前草稿尚未保存，确定放弃这些修改吗？',
      confirmText: '放弃修改',
      action: () => resolve(true),
      onCancel: () => resolve(false)
    })
  })
}
onBeforeRouteLeave(() => !busy.value && mayDiscard())
async function run(name: string, work: () => Promise<void>) {
  if (busy.value) return
  action.value = name; error.value = ''; notice.value = ''
  try { await work() } catch (cause) { error.value = intentErrorText(cause) } finally { action.value = '' }
}
function setDraft(value: Dataset, persisted: boolean) { draft.value = clone(value); saved.value = persisted ? JSON.stringify(value) : ''; selected.value = []; search.value = ''; result.value = undefined; evaluation.value = undefined; tab.value = 'samples' }
async function loadDataset(id: string) { setDraft(await datasetsApi.get(id), true); const current = published.value[published.value.length - 1]; profile.value = current ? clone(current.profile) : defaultEmbeddingProfile() }
async function selectDataset(id: string) { if (busy.value || !id || (draft.value?.revision && draft.value.id === id) || !(await mayDiscard())) return; await run('load', () => loadDataset(id)) }
async function create() { if (!busy.value && await mayDiscard()) setDraft({ id: '', name: '', kind: 'intent', description: '', revision: 0, samples: [] }, false) }
async function save() { if (!draft.value) return; const value = clone(draft.value); await run('save', async () => { setDraft(await datasetsApi.save(value), true); notice.value = '数据集已保存，已发布索引保持不变。'; await store.load() }) }
async function importFile(value: File | File[] | null) {
  const file = Array.isArray(value) ? value[0] : value
  if (!file || !(await mayDiscard())) return
  await run('import', async () => {
    if (file.size > 16 * 1024 * 1024) throw new Error('导入文件不能超过 16 MiB')
    const imported = parseDatasetImport(await file.text())
    setDraft(imported, false)
    notice.value = '已导入待审核草稿，批准标记已重置。如 ID 已存在，请改用新 ID；单个数据集保存上限 4 MiB。'
  })
}
function exportFile() { if (!draft.value) return; const url = URL.createObjectURL(new Blob([JSON.stringify(draft.value, null, 2)], { type: 'application/json;charset=utf-8' })); const link = document.createElement('a'); link.href = url; link.download = `${draft.value.id || 'dataset'}.json`; link.click(); URL.revokeObjectURL(url) }
function editSample(value?: DatasetSample) { if (busy.value) return; editingID.value = value?.id ?? ''; sample.value = value ? clone(value) : { id: `sample_${Date.now()}`, label: '', text: '', negative: false, split: 'index', family: '', approved: false, source: 'manual' }; sampleError.value = ''; sampleDialog.value = true }
function applySample() {
  if (busy.value || !sample.value || !draft.value) return
  if (!sample.value.id || !sample.value.label || !sample.value.text.trim() || !sample.value.family) { sampleError.value = '请填写 ID、类别、文本和样本族'; return }
  if (draft.value.samples.some(row => row.id === sample.value!.id && row.id !== editingID.value)) { sampleError.value = '样本 ID 已存在'; return }
  const index = draft.value.samples.findIndex(row => row.id === editingID.value)
  if (index < 0) draft.value.samples.push(clone(sample.value)); else draft.value.samples[index] = clone(sample.value)
  sampleDialog.value = false
}
function removeSample() {
  const current = draft.value
  if (!current) return
  askConfirm({
    message: '将从草稿移除此样本，保存并发布后才影响匹配。继续吗？',
    confirmText: '移除',
    action: () => {
      current.samples = current.samples.filter(row => row.id !== editingID.value)
      sampleDialog.value = false
    }
  })
}
function approve(value: boolean) { if (busy.value || !draft.value) return; const ids = new Set(selected.value); for (const row of draft.value.samples) if (ids.has(row.id)) row.approved = value }
async function removeDataset() {
  if (!draft.value || busy.value || dirty.value) return
  const current = draft.value
  askConfirm({
    message: '将删除此数据集及全部索引。仍引用它的路由或安全策略将按检查失败处理，请先解除引用。确定删除吗？',
    confirmText: '删除',
    action: () => run('delete', async () => { await datasetsApi.remove(current.id, current.revision); draft.value = undefined; saved.value = ''; await store.load(); notice.value = '数据集已删除。' })
  })
}
async function removeIndex(index: IndexStatus) {
  if (!draft.value || busy.value) return
  const current = draft.value
  askConfirm({
    message: '删除此模型的已发布索引后，仍使用该配置的请求将按检查失败处理。确定删除吗？',
    confirmText: '删除',
    action: () => run('delete-index', async () => { await datasetsApi.removeIndex(current.id, index); result.value = undefined; evaluation.value = undefined; await store.load(); notice.value = '索引已删除，样本保留。' })
  })
}
async function build() { if (!draft.value) return; const current = draft.value; await run('build', async () => { const built = await datasetsApi.build(current.id, current.revision, profile.value); notice.value = `已发布修订 ${built.revision}：${built.count} 条向量，其中 ${built.reused} 条复用。`; await store.load() }) }
async function preview() { if (!draft.value) return; const id = draft.value.id; await run('search', async () => { result.value = await datasetsApi.search(id, query.value, profile.value) }) }
async function evaluate() { if (!draft.value) return; const id = draft.value.id; await run('evaluate', async () => { evaluation.value = await datasetsApi.evaluate(id, profile.value, threshold.value, margin.value) }) }
watch([profile, query, threshold, margin, draft], () => { result.value = undefined; evaluation.value = undefined }, { deep: true })
onMounted(() => run('load', async () => { await store.load(); const first = store.items[0]; if (first) await loadDataset(first.id) }))
</script>

<style scoped>
@reference "@/styles/index.css";

.datasets-page { @apply max-w-360 [margin:0_auto] p-6; }
.dataset-toolbar, .dataset-fields { @apply grid [grid-template-columns:repeat(2,_minmax(0,_1fr))] gap-4 items-center; }
.dataset-list { @apply grid [grid-template-columns:repeat(auto-fit,_minmax(min(100%,_280px),_1fr))] gap-3; }
.dataset-choice { @apply grid gap-1.5 [padding:14px_16px] min-w-0 text-left [color:inherit] [background:transparent] [border:1px_solid_var(--border)] rounded-control cursor-pointer [overflow-wrap:anywhere]; }
.dataset-choice:hover:not(:disabled), .dataset-choice--selected { @apply [border-color:color-mix(in_srgb,_var(--primary)_50%,_transparent)] [background:color-mix(in_srgb,_var(--primary)_5%,_transparent)]; }
.dataset-choice:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:2px]; }
.dataset-choice:disabled { @apply cursor-default opacity-65; }
.dataset-editor { @apply border-0 p-0 m-0 min-w-0; }
.sample-search { @apply [flex:1_1_280px]; }
.sample-text { @apply max-w-160 whitespace-pre-wrap [overflow-wrap:anywhere] [padding:10px_0]; }
@media (max-width: 700px) { .datasets-page { @apply p-4; } .dataset-toolbar, .dataset-fields { @apply [grid-template-columns:minmax(0,_1fr)] gap-2; } }
</style>
