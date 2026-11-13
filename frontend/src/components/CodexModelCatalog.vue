<template>
  <v-card elevation="0" class="settings-card agent-config-panel">
    <div class="agent-config-panel-header">
      <div>
        <div class="agent-config-panel-title">模型目录</div>
      </div>
      <v-btn color="primary" variant="tonal" prepend-icon="mdi-plus" @click="openCreateForm">新增模型</v-btn>
    </div>
    <v-divider />
    <v-card-text class="pa-5">
      <v-alert v-if="settings?.modelsError" type="warning" variant="tonal" density="compact" class="mb-4">
        models.json 解析失败：{{ settings.modelsError }}
      </v-alert>
      <v-alert v-else-if="settings?.builtinError" type="warning" variant="tonal" density="compact" class="mb-4">
        内置模型清单获取失败：{{ settings.builtinError }}
      </v-alert>

      <section class="agent-config-callout mb-4">
        <div class="agent-config-callout__title">
          <v-icon size="18">mdi-lightning-bolt</v-icon>
          默认模型
        </div>
        <v-row>
          <v-col cols="12" sm="5">
            <v-select
              v-model="defaultModel"
              :items="modelOptions"
              item-title="title"
              item-value="value"
              label="model"
              variant="outlined"
              density="comfortable"
              :disabled="!selectableModels.length"
            />
          </v-col>
          <v-col cols="12" sm="4">
            <v-select
              v-model="defaultEffort"
              :items="effortOptions"
              label="推理强度"
              variant="outlined"
              density="comfortable"
              :disabled="!defaultModel"
            />
          </v-col>
          <v-col cols="12" sm="3" class="d-flex align-center">
            <v-btn
              color="primary"
              prepend-icon="mdi-content-save"
              :loading="savingDefault"
              :disabled="!defaultModel"
              block
              @click="saveDefault"
            >
              写入 config.toml
            </v-btn>
          </v-col>
        </v-row>
      </section>

      <v-table v-if="models.length" density="comfortable">
        <thead>
          <tr>
            <th>模型</th>
            <th>输入</th>
            <th>上下文窗口</th>
            <th>推理档位</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="model in models" :key="model.slug">
            <td>
              <div class="d-flex align-center ga-2">
                <span class="font-weight-medium">{{ model.slug }}</span>
                <v-chip v-if="model.builtin" size="x-small" variant="tonal">内置</v-chip>
                <v-chip v-if="settings?.model === model.slug" size="x-small" color="primary" variant="tonal">默认</v-chip>
              </div>
              <div v-if="model.displayName || model.description" class="text-caption text-medium-emphasis">
                {{ model.displayName }}<template v-if="model.description"> · {{ model.description }}</template>
              </div>
            </td>
            <td>
              <div class="d-flex ga-1">
                <v-chip size="x-small" variant="tonal" prepend-icon="mdi-text">文本</v-chip>
                <v-chip v-if="model.supportsImage" size="x-small" variant="tonal" color="primary" prepend-icon="mdi-image">图片</v-chip>
              </div>
            </td>
            <td class="text-no-wrap">{{ formatContextWindow(model.contextWindow) }}</td>
            <td>
              <div>{{ model.defaultReasoningLevel }}</div>
              <div class="text-caption text-medium-emphasis">{{ model.reasoningLevels.join(' / ') }}</div>
            </td>
            <td class="text-right text-no-wrap">
              <v-btn icon="mdi-pencil" size="small" variant="text" title="编辑" @click="openEditForm(model)" />
              <v-btn
                v-if="!model.builtin"
                icon="mdi-delete"
                size="small"
                variant="text"
                color="error"
                title="删除"
                :loading="deletingSlug === model.slug"
                @click="deleteModel(model)"
              />
            </td>
          </tr>
        </tbody>
      </v-table>
      <v-alert v-else type="info" variant="tonal" density="compact">尚未登记自定义模型。</v-alert>

      <v-dialog v-model="form.visible" max-width="800">
        <v-card rounded="lg">
          <v-card-title class="text-h6 pa-4">{{ form.editing ? `编辑模型：${form.slug}` : '新增模型' }}</v-card-title>
          <v-divider />
          <v-card-text class="pa-4">
            <v-row>
              <v-col cols="12" sm="6">
                <v-text-field
                  v-if="!form.editing"
                  v-model.trim="form.slug"
                  label="Slug"
                  variant="outlined"
                  density="comfortable"
                  placeholder="例如 my-deepseek"
                />
                <v-text-field v-else v-model="form.slug" label="Slug" variant="outlined" density="comfortable" disabled />
              </v-col>
              <v-col cols="12" sm="6">
                <v-text-field v-model.trim="form.displayName" label="显示名称" variant="outlined" density="comfortable" placeholder="留空则使用 slug" />
              </v-col>
              <v-col cols="12" sm="6">
                <v-text-field v-model.trim="form.description" label="描述" variant="outlined" density="comfortable" />
              </v-col>
              <v-col cols="12" sm="4">
                <v-switch v-model="form.supportsImage" color="primary" label="图片输入" />
              </v-col>
              <v-col cols="12" sm="4">
                <v-text-field
                  v-model.number="form.contextWindow"
                  type="number"
                  label="上下文窗口"
                  variant="outlined"
                  density="comfortable"
                  min="1"
                />
              </v-col>
              <v-col cols="12" sm="4">
                <v-select
                  v-model="form.defaultReasoningLevel"
                  :items="formReasoningLevels"
                  label="默认推理档位"
                  variant="outlined"
                  density="comfortable"
                  :disabled="!formReasoningLevels.length"
                />
              </v-col>
            </v-row>
          </v-card-text>
          <v-card-actions class="pa-4">
            <v-spacer />
            <v-btn variant="text" @click="closeForm">取消</v-btn>
            <v-btn color="primary" prepend-icon="mdi-content-save" :loading="savingModel" :disabled="!formValid" @click="saveModel">
              {{ form.editing ? '保存修改' : '新增模型' }}
            </v-btn>
          </v-card-actions>
        </v-card>
      </v-dialog>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
// 模型目录（models.json）管理面板。
// 数据来源：
// - catalogModels：models.json 的全部条目（内置复制品 builtin=true + 自定义条目）
// - builtinModels：动态获取的 Codex 内置清单（codex debug models，visibility=list 才在 Codex /models 中可见）
// 默认模型下拉 = 内置清单（list）+ 自定义条目；内置清单获取失败时退回 catalog 全量。
// 新增表单默认值（图片=开、上下文=200000、默认档位=high），
// 其余元数据由后端固定骨架生成（推理档位 low/high/max、priority=99、search_tool=false），
// 指令模板（model_messages）由后端从内置清单复制，满足 Codex 0.144+ 的硬性校验。
import { computed, reactive, ref, watch } from 'vue'
import { api, type CodexCatalogModel, type CodexSettings, type SaveCodexModelCatalog } from '@/services/api'
import { useToast } from '@/composables/useToast'

const { showToast } = useToast()

const props = defineProps<{ settings: CodexSettings | null }>()
const emit = defineEmits<{ saved: [] }>()

const models = computed(() => props.settings?.catalogModels ?? [])

const defaultModel = ref('')
const defaultEffort = ref('')
const savingDefault = ref(false)
const deletingSlug = ref('')
const savingModel = ref(false)

// 新增模型时后端骨架固定支持的推理档位（与后端 codexDefaultReasoningLevels 一致）。
const CREATE_REASONING_LEVELS = ['low', 'high', 'max']
// 新增表单默认值。
const CREATE_DEFAULTS = { supportsImage: true, contextWindow: 200000, defaultReasoningLevel: 'high' }

const form = reactive({
  visible: false,
  editing: false,
  slug: '',
  displayName: '',
  description: '',
  supportsImage: CREATE_DEFAULTS.supportsImage,
  contextWindow: CREATE_DEFAULTS.contextWindow,
  defaultReasoningLevel: CREATE_DEFAULTS.defaultReasoningLevel
})

const slugPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

// 默认模型下拉数据源：内置（visibility=list）在前，自定义条目去重追加；
// 内置清单不可用时退回 catalog 全量（含内置复制品），保证下拉可用。
const selectableModels = computed<CodexCatalogModel[]>(() => {
  const builtinList = (props.settings?.builtinModels ?? []).filter(model => model.visibility === 'list')
  const seen = new Set(builtinList.map(model => model.slug))
  const extra = builtinList.length
    ? models.value.filter(model => !model.builtin && !seen.has(model.slug))
    : models.value.filter(model => !seen.has(model.slug))
  return [...builtinList, ...extra].sort((a, b) => a.slug.localeCompare(b.slug))
})

const modelOptions = computed(() =>
  selectableModels.value.map(model => ({
    title: model.displayName ? `${model.slug}（${model.displayName}）` : model.slug,
    value: model.slug
  }))
)

const selectedDefaultModel = computed(() => selectableModels.value.find(model => model.slug === defaultModel.value))

const effortOptions = computed(() => [
  { title: '跟随模型默认', value: '' },
  ...(selectedDefaultModel.value?.reasoningLevels ?? []).map(level => ({ title: level, value: level }))
])

const formReasoningLevels = computed(() => {
  if (form.editing) return models.value.find(model => model.slug === form.slug)?.reasoningLevels ?? []
  return CREATE_REASONING_LEVELS
})

const formValid = computed(() => {
  if (!slugPattern.test(form.slug)) return false
  if (!form.contextWindow || form.contextWindow <= 0) return false
  return form.defaultReasoningLevel !== ''
})

watch(defaultModel, slug => {
  const model = selectableModels.value.find(item => item.slug === slug)
  if (model && !model.reasoningLevels.includes(defaultEffort.value)) {
    defaultEffort.value = ''
  }
})

watch(
  () => props.settings,
  settings => {
    if (!settings) return
    defaultModel.value = selectableModels.value.some(model => model.slug === settings.model) ? settings.model : ''
    const current = selectableModels.value.find(model => model.slug === settings.model)
    defaultEffort.value = current?.reasoningLevels.includes(settings.modelReasoningEffort)
      ? settings.modelReasoningEffort
      : ''
  },
  { immediate: true }
)

const openCreateForm = () => {
  form.visible = true
  form.editing = false
  form.slug = ''
  form.displayName = ''
  form.description = ''
  form.supportsImage = CREATE_DEFAULTS.supportsImage
  form.contextWindow = CREATE_DEFAULTS.contextWindow
  form.defaultReasoningLevel = CREATE_DEFAULTS.defaultReasoningLevel
}

const openEditForm = (model: CodexCatalogModel) => {
  form.visible = true
  form.editing = true
  form.slug = model.slug
  form.displayName = model.displayName
  form.description = model.description
  form.supportsImage = model.supportsImage
  form.contextWindow = model.contextWindow
  form.defaultReasoningLevel = model.defaultReasoningLevel
}

const closeForm = () => {
  form.visible = false
}

const saveModel = async () => {
  savingModel.value = true
  try {
    const payload: SaveCodexModelCatalog = {
      action: 'upsert',
      slug: form.slug,
      displayName: form.displayName,
      description: form.description,
      supportsImage: form.supportsImage,
      contextWindow: form.contextWindow,
      defaultReasoningLevel: form.defaultReasoningLevel
    }
    const response = await api.saveCodexModelCatalog(payload)
    showToast(`已保存到 ${response.modelsPath}`, 'success')
    closeForm()
    emit('saved')
  } catch (saveError) {
    showToast(saveError instanceof Error ? saveError.message : '保存模型失败', 'error')
  } finally {
    savingModel.value = false
  }
}

const saveDefault = async () => {
  if (!defaultModel.value) return
  savingDefault.value = true
  try {
    const payload: SaveCodexModelCatalog = { action: 'setDefault', slug: defaultModel.value }
    if (defaultEffort.value) {
      payload.reasoningEffort = defaultEffort.value
    } else if (props.settings?.modelReasoningEffort) {
      payload.clearReasoningEffort = true
    }
    const response = await api.saveCodexModelCatalog(payload)
    showToast(`已写入 ${response.configPath}`, 'success')
    emit('saved')
  } catch (saveError) {
    showToast(saveError instanceof Error ? saveError.message : '写入默认模型失败', 'error')
  } finally {
    savingDefault.value = false
  }
}

const deleteModel = async (model: CodexCatalogModel) => {
  if (!window.confirm(`删除模型“${model.slug}”？若目录删空将自动回退 config.toml 的模型目录配置。`)) return
  deletingSlug.value = model.slug
  try {
    const response = await api.saveCodexModelCatalog({ action: 'delete', slug: model.slug })
    showToast(`已从 ${response.modelsPath} 删除`, 'success')
    if (form.visible && form.editing && form.slug === model.slug) closeForm()
    emit('saved')
  } catch (deleteError) {
    showToast(deleteError instanceof Error ? deleteError.message : '删除模型失败', 'error')
  } finally {
    deletingSlug.value = ''
  }
}

const formatContextWindow = (value: number) => {
  if (value >= 1048576 && value % 1048576 === 0) return `${value / 1048576}M`
  return value.toLocaleString()
}
</script>
