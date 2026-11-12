<template>
  <div class="datasets-page">
    <div class="d-flex flex-wrap align-center justify-space-between ga-3 mb-5">
      <div><h1 class="text-h5">数据集</h1><p class="text-body-2 text-medium-emphasis mt-1">管理意图和工具风险样本，提前计算向量，发布后跨请求复用。</p></div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn variant="outlined" :disabled="busy" @click="create">新建数据集</v-btn>
        <v-btn variant="tonal" :disabled="busy" @click="showBuiltin">内置起步样本</v-btn>
      </div>
    </div>
    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-alert v-if="notice" type="success" variant="tonal" class="mb-4">{{ notice }}</v-alert>
    <div class="dataset-toolbar">
      <v-select :model-value="draft?.revision ? draft.id : undefined" :items="store.items" item-title="name" item-value="id" label="选择数据集" :disabled="busy" @update:model-value="selectDataset" />
      <v-file-input label="导入数据集 JSON" accept="application/json,.json" :disabled="busy" @update:model-value="importFile" />
    </div>
    <v-progress-linear v-if="busy" indeterminate color="primary" class="mb-4" />
    <v-alert v-if="!draft && !busy" type="info" variant="tonal">选择已有数据集，或导入内置起步样本。样本先审核，再计算并发布向量索引。</v-alert>
    <template v-if="draft">
      <div class="d-flex flex-wrap align-center ga-3 mb-4">
        <v-chip size="small">{{ draft.kind === 'intent' ? '意图路由' : '工具安全' }}</v-chip>
        <span class="text-body-2">{{ draft.samples.length }} 条样本 · {{ draft.revision ? `修订 ${draft.revision}` : '尚未保存' }}</span>
        <v-chip v-if="dirty" size="small" color="warning">草稿未保存</v-chip>
        <v-spacer />
        <v-btn v-if="draft.revision" variant="text" color="error" :disabled="busy || dirty" @click="removeDataset">删除数据集</v-btn>
        <v-btn variant="text" :disabled="busy" @click="exportFile">导出 JSON</v-btn>
        <v-btn color="primary" :loading="action === 'save'" :disabled="busy || !dirty" @click="save">保存数据集</v-btn>
      </div>
      <v-form :disabled="busy" class="dataset-editor" @submit.prevent>
        <div class="dataset-fields">
          <v-text-field v-model="draft.name" label="数据集名称" />
          <v-text-field v-model="draft.id" label="稳定 ID" :disabled="draft.revision > 0" hint="英文、数字、下划线或短横线" />
          <v-select v-model="draft.kind" :items="kinds" label="数据集用途" :disabled="draft.revision > 0" />
        </div>
        <v-text-field v-model="draft.description" label="说明与来源" />
        <v-tabs v-model="tab" color="primary" class="mb-4"><v-tab value="samples">样本审核</v-tab><v-tab value="index">向量索引</v-tab><v-tab value="evaluation">独立评测</v-tab></v-tabs>
        <div v-if="tab === 'samples'">
          <div class="d-flex flex-wrap align-center ga-3 mb-3">
            <v-text-field v-model="search" label="搜索样本、类别或来源" hide-details density="compact" class="sample-search" />
            <v-btn variant="tonal" @click="editSample()">添加样本</v-btn>
            <v-btn variant="text" :disabled="!selected.length" @click="approve(true)">批准所选（{{ selected.length }}）</v-btn>
            <v-btn variant="text" :disabled="!selected.length" @click="approve(false)">撤回批准</v-btn>
          </div>
          <p class="text-caption text-medium-emphasis mb-3">批准的 index 样本参与匹配；eval 样本只用于评测。同一样本族不能跨两个集合。撤回批准并发布新索引后才影响业务请求。</p>
          <v-data-table v-model="selected" :headers="headers" :items="draft.samples" :search="search" item-value="id" show-select :items-per-page="10" :items-per-page-options="[10, 25, 50]">
            <template #[`item.text`]="{ item }"><div class="sample-text">{{ item.text }}</div></template>
            <template #[`item.negative`]="{ item }">{{ item.negative ? '反例' : '正例' }}</template>
            <template #[`item.approved`]="{ item }"><v-chip size="small" :color="item.approved ? 'success' : undefined">{{ item.approved ? '已批准' : '待审核' }}</v-chip></template>
            <template #[`item.actions`]="{ item }"><v-btn size="small" variant="text" @click="editSample(item)">编辑</v-btn></template>
          </v-data-table>
        </div>
        <div v-else>
          <v-alert v-if="dirty" type="warning" variant="tonal" class="mb-4">请先保存草稿，再构建、检索或评测。</v-alert>
          <EmbeddingProfileFields v-model="profile" />
          <template v-if="tab === 'index'">
            <div class="d-flex flex-wrap ga-3 mb-4">
              <v-btn color="primary" :disabled="dirty || !draft.revision" :loading="action === 'build'" @click="build">增量构建并发布</v-btn>
              <span class="text-caption align-self-center">只计算变化的文本；失败保留旧索引。模型升级需更新版本标识。</span>
            </div>
            <v-table v-if="published.length" density="comfortable" class="mb-5">
              <thead><tr><th>模型</th><th>数据修订</th><th>样本 / 维度</th><th>构建时间</th><th>配置</th></tr></thead>
              <tbody><tr v-for="index in published" :key="index.space + JSON.stringify(index.profile)">
                <td>{{ index.profile.model }} · {{ index.profile.modelVersion || '未指定版本' }}</td>
                <td>{{ index.revision }} {{ index.revision === draft.revision ? '（当前）' : '（有待发布修改）' }}</td>
                <td>{{ index.count }} / {{ index.dimension }}</td><td>{{ new Date(index.builtAt).toLocaleString() }}</td>
                <td><v-btn variant="text" size="small" :disabled="busy" @click="profile = clone(index.profile)">使用此配置</v-btn><v-btn variant="text" size="small" color="error" :disabled="busy" @click="removeIndex(index)">删除索引</v-btn></td>
              </tr></tbody>
            </v-table>
            <v-textarea v-model="query" label="检索模拟文本" rows="3" />
            <v-btn variant="tonal" :disabled="dirty || !draft.revision || !query.trim()" :loading="action === 'search'" @click="preview">检索已发布索引</v-btn>
            <div v-if="result" class="mt-4"><p class="text-body-2 mb-2">检索耗时 {{ result.durationMs }} ms</p><v-table><thead><tr><th>类别</th><th>相似度</th><th>样本</th></tr></thead><tbody><tr v-for="hit in result.hits" :key="hit.datasetId + hit.sample.id"><td>{{ hit.sample.label }} · {{ hit.sample.negative ? '反例' : '正例' }}</td><td>{{ hit.score.toFixed(4) }}</td><td class="sample-text">{{ hit.sample.text }}</td></tr></tbody></v-table></div>
          </template>
          <template v-else>
            <div class="dataset-fields"><v-text-field v-model.number="threshold" type="number" min="0" max="1" step="0.01" label="相似度阈值" /><v-text-field v-model.number="margin" type="number" min="0" max="1" step="0.01" label="意图正反例最小差值" /></div>
            <p class="text-body-2 text-medium-emphasis mb-4">评测当前修订中已批准的 eval 样本，最多 500 条。安全数据集评测的是风险召回，不代表 AI 闸门最终阻断准确率。真实样本应与内置合成样本分开评估。</p>
            <v-btn variant="tonal" :disabled="dirty || !draft.revision" :loading="action === 'evaluate'" @click="evaluate">运行独立评测</v-btn>
            <div v-if="evaluation" class="mt-4"><v-alert type="info" variant="tonal" class="mb-3">{{ evaluation.correct }} / {{ evaluation.total }} 正确；误报 {{ evaluation.falsePositive }}，漏报或分类错误 {{ evaluation.falseNegative }}；总耗时 {{ evaluation.durationMs }} ms</v-alert><v-data-table :headers="evaluationHeaders" :items="evaluation.cases" :items-per-page="10"><template #[`item.correct`]="{ item }">{{ item.correct ? '正确' : '待改进' }}</template><template #[`item.score`]="{ item }">{{ item.score.toFixed(4) }}</template></v-data-table></div>
          </template>
        </div>
      </v-form>
    </template>
    <v-dialog v-model="sampleDialog" max-width="760" scrollable><v-card v-if="sample"><v-card-title>编辑样本</v-card-title><v-card-text>
      <div class="dataset-fields"><v-text-field v-model="sample.id" label="样本 ID" /><v-text-field v-model="sample.label" label="类别标识" /><v-text-field v-model="sample.family" label="样本族" /><v-select v-model="sample.split" :items="splits" label="样本用途" /></div>
      <v-textarea v-model="sample.text" label="样本文本或完整命令" rows="5" />
      <v-text-field v-model="sample.source" label="来源" />
      <div class="d-flex ga-4"><v-checkbox v-model="sample.negative" label="反例" /><v-checkbox v-model="sample.approved" label="已审核，批准参与索引或评测" /></div>
      <template v-if="draft?.kind === 'safety'"><p class="text-caption mb-3">类别使用 destructive、download_execute、reverse_shell、privilege_escalation 或 environment_tampering。</p><v-text-field v-model="sample.tool" label="适用执行工具（可选）" hint="填写去除命名空间后的工具名；留空适用于全部执行工具" persistent-hint /><v-text-field v-model="sample.platform" label="平台说明（windows / posix / any）" /><v-textarea v-model="sample.pattern" label="直接阻断核验正则（可选，Go RE2）" rows="2" hint="留空时语义命中仍需 AI 审核；不要用宽泛正则代替执行范围核验" persistent-hint /></template>
      <v-alert v-if="sampleError" type="error" variant="tonal">{{ sampleError }}</v-alert>
    </v-card-text><v-card-actions><v-btn v-if="editingID" color="error" variant="text" @click="removeSample">移除样本</v-btn><v-spacer /><v-btn @click="sampleDialog = false">取消</v-btn><v-btn color="primary" @click="applySample">应用到草稿</v-btn></v-card-actions></v-card></v-dialog>
    <v-dialog v-model="builtinDialog" max-width="620"><v-card><v-card-title>内置起步样本</v-card-title><v-card-text><p class="mb-4">这些是合成样本，不含真实用户日志，默认待审核。导入后审核所需样本，再构建索引。</p><v-list><v-list-item v-for="item in builtins" :key="item.id" :title="item.name" :subtitle="`${item.samples.length} 条 · ${item.kind}`"><template #append><v-btn variant="tonal" @click="importBuiltin(item)">导入草稿</v-btn></template></v-list-item></v-list></v-card-text><v-card-actions><v-spacer /><v-btn @click="builtinDialog = false">关闭</v-btn></v-card-actions></v-card></v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { datasetsApi, defaultEmbeddingProfile, type Dataset, type DatasetSample, type Evaluation, type SearchResult } from '@/services/datasets'
import { intentErrorText } from '@/services/intent'
import { useDatasetsStore } from '@/stores/datasets'
import { parseDatasetImport } from '@/utils/datasetImport'
import type { IndexStatus } from '@/services/datasets'
import EmbeddingProfileFields from '@/components/channel/EmbeddingProfileFields.vue'
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T
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
const builtinDialog = ref(false)
const builtins = ref<Dataset[]>([])
const kinds = [{ title: '意图路由', value: 'intent' }, { title: '工具安全', value: 'safety' }]
const splits = [{ title: '索引样本', value: 'index' }, { title: '独立评测', value: 'eval' }]
const headers = [{ title: '类别', key: 'label' }, { title: '文本', key: 'text', sortable: false }, { title: '正反例', key: 'negative' }, { title: '集合', key: 'split' }, { title: '审核', key: 'approved' }, { title: '操作', key: 'actions', sortable: false }]
const evaluationHeaders = [{ title: '样本', key: 'id' }, { title: '期望', key: 'expected' }, { title: '实际', key: 'actual' }, { title: '相似度', key: 'score' }, { title: '结果', key: 'correct' }]
function mayDiscard() { return !dirty.value || window.confirm('当前草稿尚未保存，确定放弃这些修改吗？') }
onBeforeRouteLeave(() => !busy.value && mayDiscard())
async function run(name: string, work: () => Promise<void>) {
  if (busy.value) return
  action.value = name; error.value = ''; notice.value = ''
  try { await work() } catch (cause) { error.value = intentErrorText(cause) } finally { action.value = '' }
}
function setDraft(value: Dataset, persisted: boolean) { draft.value = clone(value); saved.value = persisted ? JSON.stringify(value) : ''; selected.value = []; result.value = undefined; evaluation.value = undefined; tab.value = 'samples' }
async function selectDataset(id: string) { if (!id || !mayDiscard()) return; await run('load', async () => { setDraft(await datasetsApi.get(id), true); const current = published.value[published.value.length - 1]; if (current) profile.value = clone(current.profile) }) }
function create() { if (!busy.value && mayDiscard()) setDraft({ id: '', name: '', kind: 'intent', description: '', revision: 0, samples: [] }, false) }
async function save() { if (!draft.value) return; const value = clone(draft.value); await run('save', async () => { setDraft(await datasetsApi.save(value), true); notice.value = '数据集已保存，已发布索引保持不变。'; await store.load() }) }
async function showBuiltin() { await run('builtin', async () => { builtins.value = await datasetsApi.builtin(); builtinDialog.value = true }) }
function importBuiltin(value: Dataset) { if (busy.value || !mayDiscard()) return; setDraft(value, false); builtinDialog.value = false }
async function importFile(value: File | File[] | null) {
  const file = Array.isArray(value) ? value[0] : value
  if (!file || !mayDiscard()) return
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
function removeSample() { if (!draft.value || !window.confirm('将从草稿移除此样本，保存并发布后才影响匹配。继续吗？')) return; draft.value.samples = draft.value.samples.filter(row => row.id !== editingID.value); sampleDialog.value = false }
function approve(value: boolean) { if (busy.value || !draft.value) return; const ids = new Set(selected.value); for (const row of draft.value.samples) if (ids.has(row.id)) row.approved = value }
async function removeDataset() {
  if (!draft.value || busy.value || dirty.value || !window.confirm('将删除此数据集及全部索引。仍引用它的路由或安全策略将按检查失败处理，请先解除引用。确定删除吗？')) return
  const current = draft.value
  await run('delete', async () => { await datasetsApi.remove(current.id, current.revision); draft.value = undefined; saved.value = ''; await store.load(); notice.value = '数据集已删除。' })
}
async function removeIndex(index: IndexStatus) {
  if (!draft.value || busy.value || !window.confirm('删除此模型的已发布索引后，仍使用该配置的请求将按检查失败处理。确定删除吗？')) return
  const id = draft.value.id
  await run('delete-index', async () => { await datasetsApi.removeIndex(id, index); result.value = undefined; evaluation.value = undefined; await store.load(); notice.value = '索引已删除，样本保留。' })
}
async function build() { if (!draft.value) return; const current = draft.value; await run('build', async () => { const built = await datasetsApi.build(current.id, current.revision, profile.value); notice.value = `已发布修订 ${built.revision}：${built.count} 条向量，其中 ${built.reused} 条复用。`; await store.load() }) }
async function preview() { if (!draft.value) return; const id = draft.value.id; await run('search', async () => { result.value = await datasetsApi.search(id, query.value, profile.value) }) }
async function evaluate() { if (!draft.value) return; const id = draft.value.id; await run('evaluate', async () => { evaluation.value = await datasetsApi.evaluate(id, profile.value, threshold.value, margin.value) }) }
watch([profile, query, threshold, margin, draft], () => { result.value = undefined; evaluation.value = undefined }, { deep: true })
onMounted(() => run('load', () => store.load()))
</script>

<style scoped>
.datasets-page { max-width: 1440px; margin: 0 auto; padding: 24px; }
.dataset-toolbar, .dataset-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.dataset-editor { border: 0; padding: 0; margin: 0; min-width: 0; }
.sample-search { flex: 1 1 280px; }
.sample-text { max-width: 640px; white-space: pre-wrap; overflow-wrap: anywhere; padding: 10px 0; }
@media (max-width: 700px) { .datasets-page { padding: 16px; } .dataset-toolbar, .dataset-fields { grid-template-columns: minmax(0, 1fr); gap: 8px; } }
</style>
