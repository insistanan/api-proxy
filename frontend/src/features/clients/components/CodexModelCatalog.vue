<template>
  <UiCard elevation="0" class="settings-card agent-config-panel">
    <div class="agent-config-panel-header">
      <div>
        <div class="agent-config-panel-title">模型目录</div>
      </div>
      <UiButton color="primary" variant="tonal" prepend-icon="mdi-plus" @click="openCreateForm">新增模型</UiButton>
    </div>
    <hr class="border-0 border-t border-border" />
    <div class="p-4 p-5">
      <UiAlert v-if="settings?.modelsError" type="warning" variant="tonal" density="compact" class="mb-4">
        models.json 解析失败：{{ settings.modelsError }}
      </UiAlert>
      <UiAlert v-else-if="settings?.builtinError" type="warning" variant="tonal" density="compact" class="mb-4">
        内置模型清单获取失败：{{ settings.builtinError }}
      </UiAlert>

      <section class="agent-config-callout mb-4">
        <div class="agent-config-callout__title">
          <UiIcon size="18">mdi-lightning-bolt</UiIcon>
          默认模型
        </div>
        <div class="grid grid-cols-12 gap-4">
          <div class="min-w-0 col-span-12 sm:col-span-5"  >
            <UiSelect
              v-model="defaultModel"
              :items="modelOptions"
              item-title="title"
              item-value="value"
              label="model"
              variant="outlined"
              density="comfortable"
              :disabled="!selectableModels.length"
            />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-4"  >
            <UiSelect
              v-model="defaultEffort"
              :items="effortOptions"
              label="推理强度"
              variant="outlined"
              density="comfortable"
              :disabled="!defaultModel"
            />
          </div>
          <div   class="min-w-0 col-span-12 sm:col-span-3 flex items-center">
            <UiButton
              color="primary"
              prepend-icon="mdi-content-save"
              :loading="savingDefault"
              :disabled="!defaultModel"
              block
              @click="saveDefault"
            >
              写入 config.toml
            </UiButton>
          </div>
        </div>
      </section>

      <UiTable v-if="models.length" density="comfortable">
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
              <div class="flex items-center gap-2">
                <span class="font-medium">{{ model.slug }}</span>
                <UiBadge v-if="model.builtin" size="x-small" variant="tonal">内置</UiBadge>
                <UiBadge v-if="settings?.model === model.slug" size="x-small" color="primary" variant="tonal">默认</UiBadge>
              </div>
              <div v-if="model.displayName || model.description" class="text-xs text-muted-foreground">
                {{ model.displayName }}<template v-if="model.description"> · {{ model.description }}</template>
              </div>
            </td>
            <td>
              <div class="flex gap-1">
                <UiBadge size="x-small" variant="tonal" prepend-icon="mdi-text">文本</UiBadge>
                <UiBadge v-if="model.supportsImage" size="x-small" variant="tonal" color="primary" prepend-icon="mdi-image">图片</UiBadge>
              </div>
            </td>
            <td class="whitespace-nowrap">{{ formatContextWindow(model.contextWindow) }}</td>
            <td>
              <div>{{ model.defaultReasoningLevel }}</div>
              <div class="text-xs text-muted-foreground">{{ model.reasoningLevels.join(' / ') }}</div>
            </td>
            <td class="text-right whitespace-nowrap">
              <UiButton icon="mdi-pencil" size="small" variant="text" title="编辑" @click="openEditForm(model)" />
              <UiButton
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
      </UiTable>
      <UiAlert v-else type="info" variant="tonal" density="compact">尚未登记自定义模型。</UiAlert>

      <AppDialog v-model="form.visible" :title="form.editing ? `编辑模型：${form.slug}` : '新增模型'" size="lg">
        <div class="grid grid-cols-12 gap-4">
          <div class="min-w-0 col-span-12 sm:col-span-6"  >
            <UiInput
              v-if="!form.editing"
              v-model.trim="form.slug"
              label="Slug"
              variant="outlined"
              density="comfortable"
              placeholder="例如 my-deepseek"
            />
            <UiInput v-else v-model="form.slug" label="Slug" variant="outlined" density="comfortable" disabled />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-6"  >
            <UiInput v-model.trim="form.displayName" label="显示名称" variant="outlined" density="comfortable" placeholder="留空则使用 slug" />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-6"  >
            <UiInput v-model.trim="form.description" label="描述" variant="outlined" density="comfortable" />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-4"  >
            <UiSwitch v-model="form.supportsImage" color="primary" label="图片输入" />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-4"  >
            <UiInput
              v-model.number="form.contextWindow"
              type="number"
              label="上下文窗口"
              variant="outlined"
              density="comfortable"
              min="1"
            />
          </div>
          <div class="min-w-0 col-span-12 sm:col-span-4"  >
            <UiSelect
              v-model="form.defaultReasoningLevel"
              :items="formReasoningLevels"
              label="默认推理档位"
              variant="outlined"
              density="comfortable"
              :disabled="!formReasoningLevels.length"
            />
          </div>
        </div>
        <template #actions>
          <UiButton variant="text" @click="closeForm">取消</UiButton>
          <UiButton color="primary" prepend-icon="mdi-content-save" :loading="savingModel" :disabled="!formValid" @click="saveModel">
            {{ form.editing ? '保存修改' : '新增模型' }}
          </UiButton>
        </template>
      </AppDialog>
    </div>
  </UiCard>
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
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
// 模型目录（models.json）管理面板。
// 数据来源：
// - catalogModels：models.json 的全部条目（内置复制品 builtin=true + 自定义条目）
// - builtinModels：动态获取的 Codex 内置清单（codex debug models，visibility=list 才在 Codex /models 中可见）
// 默认模型下拉 = 内置清单（list）+ 自定义条目；内置清单获取失败时退回 catalog 全量。
// 新增表单默认值（图片=开、上下文=200000、默认档位=high），
// 其余元数据由后端固定骨架生成（推理档位 low/high/max、priority=99、search_tool=false），
// 指令模板（model_messages）由后端从内置清单复制，满足 Codex 0.144+ 的硬性校验。
import { computed, reactive, ref, watch } from 'vue'
import { api, type CodexCatalogModel, type CodexSettings, type SaveCodexModelCatalog } from '@/api'
import { useToast } from '@/shared/composables/useToast'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import { useConfirm } from '@/shared/composables/useConfirm'

const { showToast } = useToast()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()

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
  revision: '',
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
  form.revision = props.settings?.catalogRevision ?? ''
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
  form.revision = props.settings?.catalogRevision ?? ''
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
      revision: form.revision,
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
    const payload: SaveCodexModelCatalog = {
      action: 'setDefault',
      slug: defaultModel.value,
      revision: props.settings?.catalogRevision ?? ''
    }
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

const deleteModel = (model: CodexCatalogModel) => {
  const revision = props.settings?.catalogRevision ?? ''
  askConfirm({
    message: `删除模型“${model.slug}”？若目录删空将自动回退 config.toml 的模型目录配置。`,
    confirmText: '删除',
    action: () => removeModel(model, revision)
  })
}

const removeModel = async (model: CodexCatalogModel, revision: string) => {
  deletingSlug.value = model.slug
  try {
    const response = await api.saveCodexModelCatalog({ action: 'delete', slug: model.slug, revision })
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
