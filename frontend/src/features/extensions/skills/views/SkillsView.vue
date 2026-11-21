<template>
  <div class="skills-page">
    <div class="skills-header-sticky">
      <PageHeader title="Skills" description="发现、安装并管理各客户端使用的技能。">
          <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadSkills">刷新</UiButton>
          <UiButton variant="text" prepend-icon="mdi-sync" :loading="syncing" @click="syncManifest">同步到 Agent</UiButton>
          <UiButton variant="text" prepend-icon="mdi-archive-outline" :loading="consolidating" @click="consolidateInstalledSkills">导入现有</UiButton>
          <UiButton variant="text" prepend-icon="mdi-swap-horizontal" @click="showTranslatedNames = !showTranslatedNames">{{ showTranslatedNames ? '显示原名称' : '显示译名' }}</UiButton>
          <UiButton variant="tonal" prepend-icon="mdi-magnify" @click="openDiscover">发现 Skill</UiButton>
          <UiButton color="primary" prepend-icon="mdi-plus" @click="openImport">本地安装</UiButton>
      </PageHeader>
    </div>

    <UiAlert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{ error }}</UiAlert>

    <DisclosureSection v-model="sourcesExpanded" title="来源与安装位置" description="查看各客户端的技能目录、读写状态和备份位置。" icon="mdi-folder-open" class="mb-5">
      <div class="locations-strip">
        <div class="locations-heading">
          <div>
            <div class="font-medium">已检查的本机目录</div>
            <div class="text-xs text-muted-foreground mt-1">{{ locations.length }} 个位置 · {{ locations.filter(location => location.exists).length }} 个目录可用</div>
          </div>
          <UiButton v-if="agentFilter" variant="text" size="small" prepend-icon="mdi-filter-remove-outline" @click="agentFilter = ''">清除来源筛选</UiButton>
        </div>
        <div class="location-grid">
          <div v-for="location in locations" :key="location.key" :class="['location-card', { 'location-card--active': agentFilter === location.key }]">
            <div class="location-card-heading">
              <button type="button" class="location-name" :aria-pressed="agentFilter === location.key" :title="`筛选 ${location.agent} 来源`" @click="filterByAgent(location.key)">
                <UiIcon icon="mdi-folder-open" size="small" />
                <span>{{ location.agent }}</span>
              </button>
              <UiBadge size="x-small" label :color="location.exists ? (location.readOnly ? 'info' : 'success') : 'secondary'">
                {{ location.exists ? (location.readOnly ? '只读' : '可安装') : '目录不存在' }}
              </UiBadge>
            </div>
            <div class="location-path-row">
              <code class="location-path" :title="location.path">{{ location.path || '未解析到目录路径' }}</code>
              <UiTooltip text="复制目录路径" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-copy" variant="text" size="x-small" :disabled="!location.path" :aria-label="`复制 ${location.agent} 目录路径`" @click="copySkillPath(location.path)" /></template></UiTooltip>
            </div>
          </div>
        </div>
      </div>
    </DisclosureSection>

    <div class="grid grid-cols-12 gap-4 mb-4 rounded-card border border-border bg-card p-4">
      <div class="min-w-0 col-span-12 md:col-span-7"  >
        <UiInput v-model.trim="query" density="comfortable" variant="outlined" prepend-inner-icon="mdi-magnify" label="搜索名称、描述或来源" hide-details />
      </div>
      <div class="min-w-0 col-span-12 md:col-span-5"  >
        <UiSelect v-model="agentFilter" :items="agentOptions" item-title="title" item-value="value" label="来源" density="comfortable" variant="outlined" hide-details />
      </div>
    </div>

    <UiProgress v-if="loading" indeterminate color="primary" class="mb-4" />

    <UiCard elevation="0" class="skills-list-card">
      <UiTable fixed-header density="comfortable" class="skills-table">
        <colgroup>
          <col class="skill-column" />
          <col class="source-column" />
          <col class="status-column" />
          <col class="agents-column" />
          <col class="path-column" />
          <col class="actions-column" />
        </colgroup>
        <thead>
          <tr><th scope="col">Skill</th><th scope="col">来源</th><th scope="col">状态</th><th scope="col">启用 Agent</th><th scope="col">路径</th><th scope="col" class="text-right">操作</th></tr>
        </thead>
        <tbody>
          <tr v-if="!loading && filteredSkills.length === 0"><td colspan="6" class="text-center text-muted-foreground py-10">未发现符合条件的 Skill</td></tr>
          <tr v-for="skill in visibleSkills" :key="`${skill.locationKey}:${skill.name}`">
            <td>
              <UiTooltip :text="skill.note || ''" :disabled="!skill.note" location="top" theme="dark"><template #activator="{ props }"><button v-bind="props" type="button" class="skill-name-button font-medium" @click="openSkill(skill)">{{ displaySkillName(skill) }}<UiTooltip v-if="skill.translatedName" text="已翻译" location="top" theme="dark"><template #activator="{ props: tipProps }"><UiIcon v-bind="tipProps" icon="mdi-translate" size="x-small" color="primary" class="ml-1" /></template></UiTooltip></button></template></UiTooltip>
              <div class="text-xs text-muted-foreground skill-description">{{ skill.description || '未读取到说明' }}</div>
            </td>
            <td>
              <UiBadge size="small" label :color="skill.readOnly ? 'info' : undefined" :class="['source-chip', { 'source-chip--active': agentFilter === skill.locationKey }]" title="点击筛选此来源" @click="filterByAgent(skill.locationKey)">{{ skill.agent }}</UiBadge>
              <div v-if="skill.readOnly" class="text-xs text-muted-foreground mt-1">只读来源</div>
            </td>
            <td class="status-cell"><UiBadge size="small" :color="skill.valid ? 'success' : 'warning'" label>{{ skill.valid ? '有效' : skill.issue || '需检查' }}</UiBadge></td>
            <td class="agent-toggles-cell">
              <div v-if="skill.locationKey === 'project'" class="flex flex-wrap gap-1">
                <UiBadge
                  v-for="agent in managedAgentItems"
                  :key="agent.key"
                  size="x-small"
                  label
                  :color="isAgentEnabled(skill.name, agent.key) ? 'primary' : undefined"
                  :variant="isAgentEnabled(skill.name, agent.key) ? 'flat' : 'outlined'"
                  :prepend-icon="isAgentEnabled(skill.name, agent.key) ? 'mdi-check' : undefined"
                  :disabled="togglingKey === `${skill.name}:${agent.key}`"
                  class="agent-toggle-chip"
                  :title="`${isAgentEnabled(skill.name, agent.key) ? '禁用' : '启用'} ${agent.label}`"
                  @click="toggleSkillAgent(skill.name, agent.key)"
                >
                  {{ agent.label }}
                </UiBadge>
              </div>
              <span v-else class="text-xs text-muted-foreground">—</span>
            </td>
            <td><div class="skill-path-cell"><code class="skill-path" :title="skill.path">{{ skill.path }}</code><UiTooltip text="复制路径" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-copy" variant="text" size="small" :aria-label="`复制 ${skill.name} 的路径`" @click="copySkillPath(skill.path)" /></template></UiTooltip></div></td>
            <td class="text-right whitespace-nowrap">
              <UiTooltip text="查看与翻译" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-text-box-search-outline" variant="text" size="small" @click="openSkill(skill)" /></template></UiTooltip>
              <UiTooltip text="一键翻译名称和内容" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-translate" color="primary" variant="text" size="small" :loading="translationStates[skillStateKey(skill)]?.translating" :disabled="loading || translationStates[skillStateKey(skill)]?.translating" :aria-label="`一键翻译 ${skill.name} 的名称和内容`" @click="translateSkillFromList(skill)" /></template></UiTooltip>
              <UiTooltip :text="skill.readOnly ? '只读来源不可复制' : '复制到其他 Agent'" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-copy" :disabled="skill.readOnly" variant="text" size="small" @click="openCopy(skill)" /></template></UiTooltip>
              <UiTooltip :text="skill.readOnly ? '只读来源不可删除' : '删除'" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-delete" :disabled="skill.readOnly" color="error" variant="text" size="small" @click="confirmDelete(skill)" /></template></UiTooltip>
            </td>
          </tr>
        </tbody>
      </UiTable>
      <div class="skills-pagination">
        <span class="text-sm text-muted-foreground" role="status">共 {{ filteredSkills.length }} 项 · 每页 {{ skillsPageSize }} 项</span>
        <UiPagination v-if="skillsPageCount > 1" v-model="skillsPage" :length="skillsPageCount" :total-visible="3" density="compact" aria-label="技能列表分页" />
      </div>
    </UiCard>

    <AppDialog v-model="importDialog" title="安装 Skill" description="上传 ZIP 压缩包或 SKILL.md，选择要写入的 Agent 目录。" size="md" :busy="importing">
      <UiFileInput v-model="importFile" accept=".zip,.md,text/markdown,application/zip" label="ZIP 压缩包或 SKILL.md" variant="outlined" prepend-icon="mdi-paperclip" show-size />
      <UiSelect v-model="importTargets" :items="writableLocations" item-title="agent" item-value="key" label="安装目标" variant="outlined" multiple chips />
      <UiAlert type="warning" density="compact" variant="tonal">项目 Skills 目录会始终同步保留一份。其他同名目标会被覆盖；ZIP 只接受单一 Skill 目录，且必须包含有效的 <code>SKILL.md</code>。</UiAlert>
      <template #actions>
        <UiButton variant="text" :disabled="importing" @click="importDialog = false">取消</UiButton>
        <UiButton color="primary" :disabled="!importFile || importTargets.length === 0" :loading="importing" @click="importSelected">安装</UiButton>
      </template>
    </AppDialog>

    <AppDialog v-model="discoverDialog" title="发现 Skill" description="从 skills.sh 搜索并安装到本机，安装前会从 GitHub 校验 SKILL.md 和文件清单。" size="xl" :busy="remoteSearching">
      <UiInput v-model.trim="discoverQuery" label="搜索 Skill，例如 ui、testing、react" variant="outlined" hide-details="auto" clearable @keyup.enter="searchRemote">
        <template #append-inner><UiButton icon="mdi-magnify" variant="text" size="small" :loading="remoteSearching" @click="searchRemote" /></template>
      </UiInput>
      <UiAlert v-if="discoverError" type="error" variant="tonal" class="mt-4">{{ discoverError }}</UiAlert>
      <UiProgress v-if="remoteSearching" indeterminate color="primary" class="mt-4" />
      <EmptyState v-if="!remoteSearching && remoteResults.length === 0 && remoteSearched" variant="inline" icon="mdi-magnify-close" title="没有找到匹配的 Skill" description="换个关键词再搜一次，例如 ui、testing、react。" />
      <UiTable v-if="remoteResults.length" density="comfortable" class="mt-4 remote-results-table">
        <thead><tr><th>Skill</th><th>来源</th><th>安装量</th><th class="text-right">操作</th></tr></thead>
        <tbody>
          <tr v-for="result in remoteResults" :key="result.id">
            <td><div class="font-medium">{{ result.name || result.skillId }}</div><div class="text-xs text-muted-foreground">{{ result.skillId }}</div></td>
            <td><a v-if="result.repositoryUrl" :href="result.repositoryUrl" target="_blank" rel="noreferrer" class="source-link">{{ result.source }}</a><span v-else>{{ result.source }}</span></td>
            <td class="install-count-cell"><span class="install-count">{{ formatInstalls(result.installs) }}</span></td>
            <td class="text-right"><UiButton size="small" variant="tonal" color="primary" :loading="remoteInspectingId === result.id" @click="inspectRemote(result.id)">查看并安装</UiButton></td>
          </tr>
        </tbody>
      </UiTable>
    </AppDialog>

    <AppDialog v-model="remotePreviewDialog" :title="`安装 ${remotePreview?.name || ''}`" :description="remotePreview?.source" size="lg" :busy="remoteInstalling">
      <UiAlert v-if="remoteError" type="error" variant="tonal" class="mb-4">{{ remoteError }}</UiAlert>
      <div v-if="remotePreview" class="text-sm mb-4">{{ remotePreview.description }}</div>
      <div v-if="remotePreview" class="flex flex-wrap gap-2 mb-4">
        <UiBadge size="small" label>{{ remotePreview.files.length }} 个文件</UiBadge>
        <UiBadge v-if="remotePreview.license" size="small" label>许可证：{{ remotePreview.license }}</UiBadge>
        <UiBadge v-if="remotePreview.commit" size="small" label :title="remotePreview.commit">提交 {{ remotePreview.commit.slice(0, 7) }}</UiBadge>
        <UiBadge v-if="remotePreview.containsScripts" size="small" color="warning" label>包含 scripts/，请先审阅</UiBadge>
        <UiButton v-if="remotePreview.repositoryUrl" :href="remotePreview.repositoryUrl" target="_blank" rel="noreferrer" size="small" variant="text" prepend-icon="mdi-open-in-new">GitHub 仓库</UiButton>
      </div>
      <UiSelect v-model="remoteTargets" :items="writableLocations" item-title="agent" item-value="key" label="安装目标" variant="outlined" multiple chips :disabled="remoteInstalling" />
      <UiAlert type="warning" density="compact" variant="tonal" class="mb-4">项目 Skills 目录会始终同步保留一份；其他目标中的同名 Skill 会被覆盖。远程内容只会写入文件，不会在服务端执行脚本。</UiAlert>
      <div v-if="remotePreview" class="file-list"><div class="text-xs text-muted-foreground mb-2">将安装的文件</div><div v-for="file in remotePreview.files" :key="file.path" class="flex justify-between text-sm"><code>{{ file.path }}</code><span class="text-muted-foreground">{{ formatBytes(file.size) }}</span></div></div>
      <template #actions>
        <UiButton variant="text" :disabled="remoteInstalling" @click="remotePreviewDialog = false">取消</UiButton>
        <UiButton color="primary" :disabled="!remotePreview || remoteTargets.length === 0" :loading="remoteInstalling" @click="installRemote">安装</UiButton>
      </template>
    </AppDialog>

    <AppDialog v-model="detailDialog" :title="selectedDisplayName" :description="selectedSkill?.path" size="xl">
      <UiAlert v-if="detailError" type="error" variant="tonal" class="mb-4">{{ detailError }}</UiAlert>
      <UiAlert v-else type="info" variant="tonal" density="compact" class="mb-4">{{ translationRecommendation }}</UiAlert>
      <div class="grid grid-cols-12 gap-4">
        <div class="min-w-0 col-span-12 lg:col-span-6"  ><UiTextarea :model-value="originalContent" label="原 Skill（只读）" variant="outlined" rows="19" readonly no-resize class="skill-editor" /></div>
        <div class="min-w-0 col-span-12 lg:col-span-6"  ><UiTextarea v-model="translatedContent" label="中文翻译" variant="outlined" rows="19" no-resize class="skill-editor" /></div>
      </div>
      <template #actions>
        <div  class="grid grid-cols-12 gap-4 detail-controls">
          <div class="min-w-0 col-span-12 md:col-span-6 lg:col-span-3"   ><UiInput v-model="skillNote" label="全局备注" variant="outlined" clearable hide-details="auto" @keyup.enter="saveSkillNote"><template #append-inner><UiTooltip text="保存备注" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-save" variant="text" size="small" :loading="savingNote" :disabled="savingNote" @click="saveSkillNote" /></template></UiTooltip></template></UiInput></div>
          <div class="min-w-0 col-span-12 md:col-span-6 lg:col-span-3"   ><UiSelect v-model="translationChannel" :items="translationChannelOptions" item-title="title" item-value="value" label="翻译协议 / 渠道" variant="outlined" /></div>
          <div class="min-w-0 col-span-12 md:col-span-6 lg:col-span-4"   ><UiSelect v-model="translationModel" searchable creatable :items="translationModelOptions" item-title="title" item-value="value" label="翻译模型" variant="outlined" clearable :loading="translationModelsLoading" :error-messages="translationModelsError || undefined" /></div>
          <div class="min-w-0 col-span-12 md:col-span-6 lg:col-span-2"   ><div class="detail-actions"><UiTooltip :text="selectedSkill?.readOnly ? '只读来源不可复制' : '复制到其他 Agent'" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-copy" :disabled="!selectedSkill || selectedSkill.readOnly" variant="tonal" @click="openSelectedCopy" /></template></UiTooltip><UiTooltip text="翻译并自动保存" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-translate" color="primary" :loading="translating" @click="translateSkill" /></template></UiTooltip><UiTooltip text="保存当前译文" location="top" theme="dark"><template #activator="{ props }"><UiButton v-bind="props" icon="mdi-content-save" :disabled="!translatedContent || savingBackup" :loading="savingBackup" @click="() => saveBackup()" /></template></UiTooltip></div></div>
        </div>
      </template>
    </AppDialog>

    <AppDialog v-model="copyDialog" title="复制 Skill 到其他 Agent" size="md" :busy="copying">
      <div class="text-sm mb-4">来源：<strong>{{ selectedSkill?.name }}</strong>（{{ selectedSkill?.agent }}）</div>
      <UiSelect v-model="copyTargets" :items="copyTargetLocations" item-title="agent" item-value="key" label="复制目标" variant="outlined" multiple chips :disabled="copying" />
      <UiAlert type="warning" density="compact" variant="tonal">将复制 SKILL.md 以及 references、scripts 等附属文件。同名 Skill 会覆盖目标用户目录；内置和插件来源不可作为复制源或目标。</UiAlert>
      <template #actions>
        <UiButton variant="text" :disabled="copying" @click="copyDialog = false">取消</UiButton>
        <UiButton color="primary" :disabled="copyTargets.length === 0" :loading="copying" @click="copySelected">复制</UiButton>
      </template>
    </AppDialog>

    <ConfirmDialog
      v-model="deleteDialog"
      title="删除 Skill"
      :message="`确认删除「${selectedSkill?.name || ''}」吗？将移除 ${selectedSkill?.path || ''} 下的整个 Skill 目录。`"
      confirm-text="删除"
      :loading="deleting"
      @confirm="deleteSelected"
    />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiPagination from '@/shared/ui/data/Pagination.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiFileInput from '@/shared/ui/field/FileInput.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import DisclosureSection from '@/shared/components/layout/DisclosureSection.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { api, fetchUpstreamModels, channelApiByType, type ApiTab, type AvailabilityCounts, type Channel, type ManagedSkill, type ModelAvailabilityRow, type RemoteSkillPreview, type SkillLocation, type SkillSearchResult } from '@/api'
import { CONVERSATION_PROTOCOLS, getModelAvailability } from '@/features/channels'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'
import { useClipboard } from '@/shared/composables/useClipboard'
import { readSSEStream } from '@/shared/utils/sse'

const { showToast } = useToast()
const { copyText } = useClipboard()

type TranslationApiTab = Extract<ApiTab, 'messages' | 'responses' | 'gemini' | 'chat'>
type TranslationChannel = { protocol: TranslationApiTab; channel: Channel; key: string }
type TranslationAvailability = { protocol: TranslationApiTab; row: ModelAvailabilityRow }
const TRANSLATION_PROTOCOLS: readonly TranslationApiTab[] = CONVERSATION_PROTOCOLS
const TRANSLATION_PROTOCOL_LABELS: Record<TranslationApiTab, string> = {
  messages: 'Messages', responses: 'Responses', gemini: 'Gemini', chat: 'Chat'
}

const PROXY_BASE = import.meta.env.PROD
  ? ''
  : import.meta.env.VITE_BACKEND_URL || ''

const sourcesExpanded = ref(true)
const loading = ref(false)
const syncing = ref(false)
const consolidating = ref(false)
const togglingKey = ref('')
const importing = ref(false)
const copying = ref(false)
const remoteSearching = ref(false)
const remoteInstalling = ref(false)
const remoteInspectingId = ref('')
const deleting = ref(false)
const error = ref('')
const query = ref('')
const agentFilter = ref('')
const locations = ref<SkillLocation[]>([])
const skills = ref<ManagedSkill[]>([])
const manifest = ref<Record<string, string[]>>({})
const managedAgents = ref<string[]>([])
const translationChannels = ref<TranslationChannel[]>([])
const modelAvailabilityRows = ref<TranslationAvailability[]>([])
const modelAvailabilityLoading = ref(false)
const modelAvailabilityError = ref('')
const translationChannelError = ref('')
let modelAvailabilityController: AbortController | null = null
const importDialog = ref(false)
const importFile = ref<File | null>(null)
const importTargets = ref<string[]>([])
const copyDialog = ref(false)
const copyTargets = ref<string[]>([])
const discoverDialog = ref(false)
const discoverQuery = ref('')
const remoteResults = ref<SkillSearchResult[]>([])
const remoteSearched = ref(false)
const discoverError = ref('')
const remotePreviewDialog = ref(false)
const remotePreview = ref<RemoteSkillPreview | null>(null)
const remoteTargets = ref<string[]>([])
const remoteError = ref('')
const detailDialog = ref(false)
const deleteDialog = ref(false)
const selectedSkill = ref<ManagedSkill | null>(null)
const savingNote = ref(false)

type TranslationState = {
  original: string
  translated: string
  error: string
  translating: boolean
  translationRequestId: number
  saving: boolean
  channel: string | null
  model: string
  modelOptions: Array<{ title: string; value: string }>
  modelsLoading: boolean
  modelsError: string
}

const translationStates = reactive<Record<string, TranslationState>>({})
const inactiveTranslationState: TranslationState = {
  original: '', translated: '', error: '', translating: false, translationRequestId: 0, saving: false,
  channel: null, model: '', modelOptions: [], modelsLoading: false, modelsError: ''
}
const translationModelRequestIds = new Map<string, number>()
const showTranslatedNames = ref(false)

const availabilitySampleSize = (row: AvailabilityCounts) => row.success + row.failure + row.unsupported + row.credential + row.rateLimited
const availabilitySample = (row: ModelAvailabilityRow) => {
  const recentTotal = availabilitySampleSize(row.recent)
  if (recentTotal > 0) return { counts: row.recent, total: recentTotal, period: 0, label: '近 1 小时' }
  const total = availabilitySampleSize(row)
  return total > 0 ? { counts: row, total, period: 1, label: '24 小时' } : null
}
const rankedAvailabilityRows = computed(() => modelAvailabilityRows.value
  .flatMap(({ protocol, row }) => {
    const target = translationChannels.value.find(item => item.protocol === protocol && item.channel.id === row.channelId)
    const sample = availabilitySample(row)
    return target && sample ? [{ target, row, sample }] : []
  })
  .sort((left, right) => left.sample.period - right.sample.period
    || right.sample.counts.success / right.sample.total - left.sample.counts.success / left.sample.total
    || right.sample.total - left.sample.total
    || right.row.lastSeen - left.row.lastSeen))
const recommendedTranslation = computed(() => rankedAvailabilityRows.value[0] ?? null)
const translationRecommendation = computed(() => {
  if (modelAvailabilityLoading.value) return '正在读取模型观测数据，并为翻译推荐可用率最高的模型。'
  const best = recommendedTranslation.value
  if (!best) {
    const errors = [translationChannelError.value, modelAvailabilityError.value].filter(Boolean).join('；')
    return errors
      ? `无法读取完整的模型统计：${errors}。将使用渠道默认模型或首个可用模型。`
      : '暂无有效请求样本，将使用渠道默认模型或首个可用模型。'
  }
  const rate = (100 * best.sample.counts.success / best.sample.total).toFixed(1)
  const partialError = [translationChannelError.value, modelAvailabilityError.value].filter(Boolean).join('；')
  return `按模型观测推荐：${TRANSLATION_PROTOCOL_LABELS[best.target.protocol]} · ${best.target.channel.name} · ${best.row.model}（${best.sample.label}成功率 ${rate}%，${best.sample.total} 次有效请求）${partialError ? `。部分协议统计不可用：${partialError}` : '。'}`
})
const translationChannelOptions = computed(() => translationChannels.value.map(item => ({
  title: `${TRANSLATION_PROTOCOL_LABELS[item.protocol]} · ${item.channel.name}`,
  value: item.key
})))
const translationChannelByKey = (key: string) => translationChannels.value.find(item => item.key === key)
const availabilityModelTitle = (model: string, channelKey: string) => {
  const item = rankedAvailabilityRows.value.find(candidate => candidate.target.key === channelKey && candidate.row.model === model)
  if (!item) return model
  const rate = (100 * item.sample.counts.success / item.sample.total).toFixed(1)
  return `${model} · ${item.sample.label} ${rate}%（${item.sample.total} 次）`
}

const skillStateKey = (skill: ManagedSkill) => `${skill.locationKey}:${skill.name}`
const ensureTranslationState = (skill: ManagedSkill): TranslationState => {
  const key = skillStateKey(skill)
  if (!translationStates[key]) {
    translationStates[key] = {
      original: '', translated: '', error: '', translating: false, translationRequestId: 0, saving: false,
      channel: null, model: '', modelOptions: [], modelsLoading: false, modelsError: ''
    }
  }
  return translationStates[key]
}
const selectedSkillKey = computed(() => selectedSkill.value ? skillStateKey(selectedSkill.value) : '')
const activeTranslationState = computed(() => selectedSkill.value ? ensureTranslationState(selectedSkill.value) : inactiveTranslationState)
const originalContent = computed({ get: () => activeTranslationState.value.original, set: value => { activeTranslationState.value.original = value } })
const translatedContent = computed({ get: () => activeTranslationState.value.translated, set: value => { activeTranslationState.value.translated = value } })
const detailError = computed({ get: () => activeTranslationState.value.error, set: value => { activeTranslationState.value.error = value } })
const translating = computed(() => activeTranslationState.value.translating)
const savingBackup = computed(() => activeTranslationState.value.saving)
const translationChannel = computed({
  get: () => activeTranslationState.value.channel,
  set: value => {
    const skill = selectedSkill.value
    if (!skill) return
    const state = ensureTranslationState(skill)
    if (state.channel === value) return
    state.channel = value
    state.model = ''
    if (value) void loadTranslationModels(value, skill)
  }
})
const translationModel = computed({ get: () => activeTranslationState.value.model, set: value => { activeTranslationState.value.model = value } })
const translationModelOptions = computed(() => activeTranslationState.value.modelOptions)
const translationModelsLoading = computed(() => activeTranslationState.value.modelsLoading)
const translationModelsError = computed(() => activeTranslationState.value.modelsError)

const agentOptions = computed(() => [{ title: '全部来源', value: '' }, ...locations.value.map(location => ({ title: location.agent, value: location.key }))])
const writableLocations = computed(() => locations.value.filter(location => !location.readOnly))
const copyTargetLocations = computed(() => writableLocations.value.filter(location => location.key !== selectedSkill.value?.locationKey))
const filteredSkills = computed(() => {
  const keyword = query.value.toLowerCase()
  return skills.value.filter(skill => (!agentFilter.value || skill.locationKey === agentFilter.value) && (!keyword || `${skill.name} ${skill.description} ${skill.agent} ${skill.path}`.toLowerCase().includes(keyword)))
})
// 搜索覆盖全部技能，表格只挂载当前页，避免为每条记录同时创建多个浮层和按钮。
const skillsPage = ref(1)
const skillsPageSize = 30
const skillsPageCount = computed(() => Math.max(1, Math.ceil(filteredSkills.value.length / skillsPageSize)))
const visibleSkills = computed(() => filteredSkills.value.slice((skillsPage.value - 1) * skillsPageSize, skillsPage.value * skillsPageSize))
watch([query, agentFilter], () => { skillsPage.value = 1 })
watch(skillsPageCount, count => { skillsPage.value = Math.min(skillsPage.value, count) })
const displaySkillName = (skill: ManagedSkill) => showTranslatedNames.value && skill.translatedName ? skill.translatedName : skill.name
const translatedNameFromContent = (content: string) => {
  const frontmatter = content.match(/^---\r?\n([\s\S]*?)\r?\n---/)
  const name = frontmatter?.[1].match(/^display_name:\s*["']?([^\r\n"']+)["']?\s*$/m)?.[1]?.trim()
  return name || ''
}
const selectedDisplayName = computed(() => {
  if (!selectedSkill.value) return ''
  if (!showTranslatedNames.value) return selectedSkill.value.name
  return translatedNameFromContent(activeTranslationState.value.translated) || selectedSkill.value.translatedName || selectedSkill.value.name
})
const skillNote = computed({ get: () => selectedSkill.value?.note || '', set: value => { if (selectedSkill.value) selectedSkill.value.note = value } })

const loadSkills = async () => {
  loading.value = true
  error.value = ''
  try {
    const data = await api.getSkills()
    locations.value = data.locations
    skills.value = data.skills
    manifest.value = data.manifest ?? {}
    managedAgents.value = data.managedAgents ?? []
    try {
      const channelResults = await Promise.allSettled(TRANSLATION_PROTOCOLS.map(protocol => channelApiByType(protocol).getChannels()))
      translationChannels.value = channelResults.flatMap((result, index) => {
        if (result.status !== 'fulfilled') return []
        const protocol = TRANSLATION_PROTOCOLS[index]
        return result.value.channels
          .filter(channel => !channel.excludeFromConversation && channel.status !== 'deleted' && channel.status !== 'disabled' && channel.status !== 'suspended')
          .map(channel => ({ protocol, channel, key: JSON.stringify([protocol, channel.id || channel.index]) }))
      })
      translationChannelError.value = channelResults.flatMap((result, index) => result.status === 'rejected'
        ? [`${TRANSLATION_PROTOCOL_LABELS[TRANSLATION_PROTOCOLS[index]]} 渠道加载失败：${result.reason instanceof Error ? result.reason.message : String(result.reason)}`]
        : []).join('；')
      await loadTranslationAvailability()
    } catch (channelError) {
      translationChannels.value = []
      error.value = channelError instanceof Error ? `Skills 已加载，但翻译渠道不可用：${channelError.message}` : 'Skills 已加载，但翻译渠道不可用'
    }
  } catch (loadError) {
    error.value = loadError instanceof Error ? loadError.message : '加载 Skills 失败'
  } finally { loading.value = false }
}

const loadTranslationAvailability = async () => {
  modelAvailabilityController?.abort()
  const controller = new AbortController()
  modelAvailabilityController = controller
  modelAvailabilityLoading.value = true
  modelAvailabilityError.value = ''
  try {
    const results = await Promise.allSettled(TRANSLATION_PROTOCOLS.map(protocol => getModelAvailability(protocol, controller.signal)))
    if (modelAvailabilityController !== controller) return
    modelAvailabilityRows.value = results.flatMap((result, index) => result.status === 'fulfilled'
      ? result.value.rows.map(row => ({ protocol: TRANSLATION_PROTOCOLS[index], row }))
      : [])
    modelAvailabilityError.value = results.flatMap((result, index) => result.status === 'rejected'
      ? [`${TRANSLATION_PROTOCOL_LABELS[TRANSLATION_PROTOCOLS[index]]} 统计读取失败：${result.reason instanceof Error ? result.reason.message : String(result.reason)}`]
      : []).join('；')
  } catch (availabilityError) {
    if (controller.signal.aborted || modelAvailabilityController !== controller) return
    modelAvailabilityRows.value = []
    modelAvailabilityError.value = availabilityError instanceof Error ? availabilityError.message : '读取模型可用率失败'
  } finally {
    if (modelAvailabilityController === controller) modelAvailabilityLoading.value = false
  }
}

const AGENT_SHORT_LABELS: Record<string, string> = {
  agents: 'Agents',
  'claude-code': 'Claude',
  codex: 'Codex',
  opencode: 'OpenCode',
  pi: 'pi',
  cursor: 'Cursor'
}

const managedAgentItems = computed(() => {
  const keys = managedAgents.value.length > 0 ? managedAgents.value : ['agents', 'claude-code', 'codex', 'opencode', 'pi', 'cursor']
  return keys.map(key => ({
    key,
    label: AGENT_SHORT_LABELS[key] || locations.value.find(item => item.key === key)?.agent || key
  }))
})

const isAgentEnabled = (skillName: string, agentKey: string) =>
  (manifest.value[skillName] ?? []).includes(agentKey)

const toggleSkillAgent = async (skillName: string, agentKey: string) => {
  const lockKey = `${skillName}:${agentKey}`
  if (togglingKey.value) return
  const nextEnabled = !isAgentEnabled(skillName, agentKey)
  togglingKey.value = lockKey
  try {
    const res = await api.toggleSkillAgent(skillName, agentKey, nextEnabled)
    manifest.value = res.manifest ?? {}
    const label = AGENT_SHORT_LABELS[agentKey] || agentKey
    showToast(`${skillName} 已在 ${label} ${nextEnabled ? '启用并同步' : '禁用并移除'}`, 'success')
    await loadSkills()
  } catch (toggleError) {
    showToast(toggleError instanceof Error ? toggleError.message : '切换 Skill Agent 状态失败', 'error')
  } finally {
    togglingKey.value = ''
  }
}

const syncManifest = async () => {
  if (syncing.value) return
  syncing.value = true
  try {
    const res = await api.syncSkills()
    const { synced, removed, skipped, errors } = res.report
    const summary = `同步 ${synced}，清理 ${removed}，跳过 ${skipped}${errors?.length ? `，失败 ${errors.length}` : ''}`
    showToast(summary, errors?.length ? 'error' : 'success')
    await loadSkills()
  } catch (syncError) {
    showToast(syncError instanceof Error ? syncError.message : '同步 Skills 失败', 'error')
  } finally {
    syncing.value = false
  }
}

const defaultInstallTargets = () => writableLocations.value.filter(location => location.key === 'project').map(location => location.key)
const openImport = () => { importFile.value = null; importTargets.value = defaultInstallTargets(); importDialog.value = true }

const consolidateInstalledSkills = async () => {
  consolidating.value = true
  try {
    const result = await api.consolidateSkills()
    const conflictText = result.duplicates > 0 ? `，${result.duplicates} 个重复名称保留项目副本` : ''
    showToast(`已统一归档 ${result.total} 个 Skill，新导入 ${result.imported} 个${conflictText}`, 'success')
    await loadSkills()
  } catch (consolidateError) {
    showToast(consolidateError instanceof Error ? consolidateError.message : '导入现有 Skill 失败', 'error')
  } finally { consolidating.value = false }
}

const loadTranslationModels = async (channelKey: string, skill: ManagedSkill) => {
  const key = skillStateKey(skill)
  const requestId = (translationModelRequestIds.get(key) || 0) + 1
  translationModelRequestIds.set(key, requestId)
  const state = ensureTranslationState(skill)
  const selectedChannel = translationChannelByKey(channelKey)
  const channel = selectedChannel?.channel
  state.modelOptions = []
  state.modelsError = ''
  state.modelsLoading = false

  if (!channel || !selectedChannel) {
    state.modelsError = '未找到所选协议渠道'
    return
  }
  const apiKey = channel.apiKeys.find(key => key.trim())
  const observedModels = rankedAvailabilityRows.value
    .filter(item => item.target.key === channelKey)
    .map(item => item.row.model)
  const configuredModel = channel.defaultModel?.trim()
  const knownModels = [...new Set([...observedModels, ...(configuredModel ? [configuredModel] : [])])]
  state.modelOptions = knownModels.map(id => ({ title: availabilityModelTitle(id, channelKey), value: id }))
  state.model ||= knownModels[0] ?? ''
  if (!apiKey) {
    state.modelsError = '所选渠道未配置 API Key'
    return
  }

  state.modelsLoading = true
  try {
    const result = await fetchUpstreamModels(channel.baseUrl, apiKey, channel.serviceType, {
      baseUrls: channel.baseUrls,
      insecureSkipVerify: channel.insecureSkipVerify,
      proxyMode: channel.proxyMode,
      proxyUrl: channel.proxyMode === 'custom' ? channel.proxyUrl?.trim() : ''
    })
    if (translationModelRequestIds.get(key) !== requestId) return

    const models = new Set([...knownModels, ...result.data.map(model => model.id.trim()).filter(Boolean)])
    state.modelOptions = [...models].map(id => ({ title: availabilityModelTitle(id, channelKey), value: id }))
    state.model ||= state.modelOptions[0]?.value ?? ''
    if (!models.size) state.modelsError = '未找到可用模型，可手动输入模型 ID'
  } catch (modelError) {
    if (translationModelRequestIds.get(key) !== requestId) return
    state.modelsError = modelError instanceof Error ? modelError.message : '获取模型列表失败，可手动输入模型 ID'
  } finally {
    if (translationModelRequestIds.get(key) === requestId) state.modelsLoading = false
  }
}

const filterByAgent = (locationKey: string) => {
  agentFilter.value = agentFilter.value === locationKey ? '' : locationKey
}

const openDiscover = () => {
  discoverDialog.value = true
  discoverError.value = ''
  remoteSearched.value = false
  remoteResults.value = []
}

const searchRemote = async () => {
  if (!discoverQuery.value.trim()) {
    discoverError.value = '请输入搜索关键词'
    return
  }
  remoteSearching.value = true
  discoverError.value = ''
  remoteSearched.value = false
  try {
    const result = await api.searchSkills(discoverQuery.value.trim())
    remoteResults.value = result.skills || []
    remoteSearched.value = true
  } catch (searchError) {
    discoverError.value = searchError instanceof Error ? searchError.message : '搜索 Skill 失败'
  } finally { remoteSearching.value = false }
}

const inspectRemote = async (id: string) => {
  remoteInspectingId.value = id
  remoteError.value = ''
  try {
    remotePreview.value = await api.inspectRemoteSkill(id)
    remoteTargets.value = defaultInstallTargets()
    remotePreviewDialog.value = true
  } catch (inspectError) {
    discoverError.value = inspectError instanceof Error ? inspectError.message : '读取远程 Skill 失败'
  } finally { remoteInspectingId.value = '' }
}

const installRemote = async () => {
  if (!remotePreview.value || remoteTargets.value.length === 0) return
  remoteInstalling.value = true
  remoteError.value = ''
  try {
    const result = await api.installRemoteSkill(remotePreview.value.id, remoteTargets.value)
    showToast(`已安装 ${result.name}`, 'success')
    remotePreviewDialog.value = false
    await loadSkills()
  } catch (installError) {
    remoteError.value = installError instanceof Error ? installError.message : '远程安装失败'
  } finally { remoteInstalling.value = false }
}

const importSelected = async () => {
  if (!importFile.value) return
  importing.value = true
  try {
    const file = importFile.value
    const base64 = await fileToBase64(file)
    const result = await api.importSkill(file.name, base64, importTargets.value)
    showToast(`已安装 ${result.name}`, 'success')
    importDialog.value = false
    await loadSkills()
  } catch (importError) {
    showToast(importError instanceof Error ? importError.message : '导入 Skill 失败', 'error')
  } finally { importing.value = false }
}

const openSkill = async (skill: ManagedSkill, translateImmediately = false) => {
  selectedSkill.value = skill
  const state = ensureTranslationState(skill)
  if (!state.channel || !translationChannelByKey(state.channel)) {
    const recommendation = recommendedTranslation.value
    const fallbackChannel = translationChannels.value.find(item => item.channel.defaultModel?.trim()) ?? translationChannels.value[0]
    const defaultChannel = recommendation?.target ?? fallbackChannel
    state.channel = defaultChannel?.key ?? null
    state.model = recommendation?.row.model ?? ''
    state.modelOptions = []
  }
  const modelLoad = state.channel && !state.modelOptions.length && !state.modelsLoading
    ? loadTranslationModels(state.channel, skill)
    : undefined
  state.error = ''
  detailDialog.value = true
  try {
    const [contentResult, backupResult] = await Promise.all([
      api.getSkillContent(skill.locationKey, skill.name),
      api.getLatestSkillBackup(skill.locationKey, skill.name)
    ])
    const originalChanged = Boolean(state.original && state.original !== contentResult.content)
    if (originalChanged) {
      state.translationRequestId++
      state.translating = false
      state.translated = ''
    }
    state.original = contentResult.content
    if (backupResult.found && backupResult.translated) {
      state.translated = backupResult.translated
      if (selectedSkillKey.value === skillStateKey(skill)) showToast('已恢复最近保存的译文', 'success')
    } else if (originalChanged) {
      state.error = '原 Skill 已更新，旧译文已清除'
    }
  }
  catch (loadError) {
    state.error = loadError instanceof Error ? loadError.message : '读取 Skill 失败'
    return
  }
  if (translateImmediately) {
    if (!state.model) await modelLoad
    if (detailDialog.value && selectedSkillKey.value === skillStateKey(skill)) await translateSkill()
  }
}

const openCopy = (skill: ManagedSkill) => {
  if (skill.readOnly) return
  selectedSkill.value = skill
  copyTargets.value = []
  copyDialog.value = true
}
const openSelectedCopy = () => {
  if (selectedSkill.value) openCopy(selectedSkill.value)
}

const saveSkillNote = async () => {
  const skill = selectedSkill.value
  if (!skill) return
  savingNote.value = true
  try {
    const result = await api.updateSkillNote(skill.name, skill.note || '')
    skills.value.forEach(item => { if (item.name === skill.name) item.note = result.note })
    showToast(result.note ? '全局备注已保存' : '全局备注已清空', 'success')
  } catch (noteError) {
    showToast(noteError instanceof Error ? noteError.message : '保存备注失败', 'error')
  } finally { savingNote.value = false }
}

const copySelected = async () => {
  if (!selectedSkill.value || copyTargets.value.length === 0) return
  copying.value = true
  try {
    const result = await api.copySkill(selectedSkill.value.locationKey, selectedSkill.value.name, copyTargets.value)
    showToast(`已复制 ${result.name} 到 ${result.targets.length} 个 Agent`, 'success')
    copyDialog.value = false
    await loadSkills()
  } catch (copyError) {
    showToast(copyError instanceof Error ? copyError.message : '复制 Skill 失败', 'error')
  } finally { copying.value = false }
}

const normalizeTranslationModel = (value: unknown): string => {
  if (typeof value === 'string') return value.trim()
  if (!value || typeof value !== 'object') return ''

  const record = value as Record<string, unknown>
  for (const key of ['value', 'id', 'title']) {
    const candidate = record[key]
    if (typeof candidate === 'string' && candidate.trim()) return candidate.trim()
  }
  return ''
}

const translateSkill = async () => {
  const skill = selectedSkill.value
  if (!skill) return
  const state = ensureTranslationState(skill)
  if (!state.original || state.translating) return
  const original = state.original
  const requestId = ++state.translationRequestId
  const accessKey = useAuthStore().apiKey
  if (!accessKey) { state.error = '未找到管理界面访问密钥'; return }
  state.translating = true
  state.error = ''
  try {
    const model = normalizeTranslationModel(state.model)
    const selectedChannel = state.channel ? translationChannelByKey(state.channel) : undefined
    if (!selectedChannel) throw new Error('请选择可用的文本生成协议渠道后再翻译')
    if (!model) throw new Error('请选择或输入用于翻译的模型 ID')
    const protocol = selectedChannel.protocol
    const systemPrompt = '你是专业技术翻译。将用户提供的 Agent Skill 英文内容完整翻译为简体中文。保留 YAML frontmatter 的原有字段名、name 值、代码块、文件路径、命令、URL 和 Markdown 结构；在 YAML frontmatter 中新增 display_name 字段，值为该 Skill 的简体中文名称。只翻译可读的自然语言。仅输出翻译后的完整 Markdown，不要解释。'
    const metadata = { channel_index: selectedChannel.channel.index }
    let endpoint = '/v1/chat/completions'
    let body: Record<string, unknown>
    switch (protocol) {
      case 'messages':
        endpoint = '/v1/messages'
        body = { model, system: systemPrompt, max_tokens: 8192, stream: true, messages: [{ role: 'user', content: original }], metadata }
        break
      case 'responses':
        endpoint = '/v1/responses'
        body = { model, instructions: systemPrompt, input: original, stream: true, metadata }
        break
      case 'gemini':
        endpoint = `/v1beta/models/${encodeURIComponent(model.replace(/^models\//, ''))}:streamGenerateContent?alt=sse`
        body = { systemInstruction: { parts: [{ text: systemPrompt }] }, contents: [{ role: 'user', parts: [{ text: original }] }], metadata }
        break
      default:
        body = {
          model, stream: true,
          messages: [{ role: 'system', content: systemPrompt }, { role: 'user', content: original }],
          metadata
        }
        break
    }
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 10 * 60 * 1000)
    try {
      const response = await fetch(`${PROXY_BASE}${endpoint}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'x-api-key': accessKey }, body: JSON.stringify(body), signal: controller.signal })
      if (!response.ok) {
        const payload = await response.json().catch(() => null)
        const errorValue = payload?.error
        const errorMessage = typeof errorValue === 'string'
          ? errorValue
          : errorValue && typeof errorValue === 'object' && typeof errorValue.message === 'string'
            ? errorValue.message
            : ''
        throw new Error(errorMessage || `翻译请求失败 (${response.status})`)
      }
      const translated = await readTranslationStream(response, value => { if (state.translationRequestId === requestId) state.translated = value }, protocol)
      if (!translated.trim()) throw new Error(`翻译渠道未返回可用文本内容，请检查所选 ${TRANSLATION_PROTOCOL_LABELS[protocol]} 模型是否支持文本生成`)
      if (state.translationRequestId !== requestId) return
      state.translated = translated.trim()
    } finally {
      window.clearTimeout(timeout)
    }
    await saveBackup({ automatic: true, notify: true, skill, state, original })
  } catch (translationError) {
    if (state.translationRequestId !== requestId) return
    state.error = translationError instanceof DOMException && translationError.name === 'AbortError'
      ? '翻译超过 10 分钟未完成，已停止请求；已接收的译文仍保留在右侧，可手动保存'
      : translationError instanceof Error ? translationError.message : '翻译失败'
  }
  finally { if (state.translationRequestId === requestId) state.translating = false }
}

const translateSkillFromList = (skill: ManagedSkill) => openSkill(skill, true)

const readTranslationStream = async (response: Response, onUpdate: (translated: string) => void, protocol: TranslationApiTab): Promise<string> => {
  if (!response.body) throw new Error('翻译渠道未建立可读取的流式响应')
  if (response.headers.get('content-type')?.toLowerCase().includes('application/json')) {
    const payload = await response.json()
    const translated = extractTranslationText(payload, protocol)
    if (translated) onUpdate(translated)
    return translated
  }
  let translated = ''
  await readSSEStream<unknown>(response, payload => {
    const delta = extractTranslationText(payload, protocol)
    if (delta) {
      translated += delta
      onUpdate(translated)
    }
  }, { strict: true })
  return translated
}

const extractTranslationText = (payload: unknown, protocol: TranslationApiTab): string => {
  if (!payload || typeof payload !== 'object') return ''
  const record = payload as Record<string, unknown>
  if (record.error || record.type === 'response.failed' || record.type === 'response.incomplete') {
    const error = record.error
    const message = error && typeof error === 'object' ? (error as Record<string, unknown>).message : error
    throw new Error(typeof message === 'string' ? message : '模型未能完成翻译')
  }
  if (protocol === 'messages') {
    const delta = record.delta
    if (delta && typeof delta === 'object' && typeof (delta as Record<string, unknown>).text === 'string') return (delta as Record<string, string>).text
    return extractContent(record.content)
  }
  if (protocol === 'responses') {
    if (record.type === 'response.output_text.delta' && typeof record.delta === 'string') return record.delta
    if (typeof record.output_text === 'string') return record.output_text
    if (Array.isArray(record.output)) {
      return record.output.map(item => {
        if (!item || typeof item !== 'object') return ''
        return extractContent((item as Record<string, unknown>).content)
      }).join('')
    }
    return extractContent(record.content)
  }
  if (protocol === 'gemini') {
    const candidates = record.candidates
    const candidate = Array.isArray(candidates) ? candidates[0] : undefined
    if (candidate && typeof candidate === 'object') {
      const content = (candidate as Record<string, unknown>).content
      if (content && typeof content === 'object') return extractContent((content as Record<string, unknown>).parts)
    }
    return ''
  }
  const choice = Array.isArray(record.choices) ? record.choices[0] : undefined
  if (!choice || typeof choice !== 'object') return extractContent(record.output_text ?? record.content)
  const choiceRecord = choice as Record<string, unknown>
  const delta = choiceRecord.delta ?? choiceRecord.message
  if (!delta || typeof delta !== 'object') return ''
  return extractContent((delta as Record<string, unknown>).content)
}

const extractContent = (content: unknown): string => {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.map(part => {
    if (typeof part === 'string') return part
    if (!part || typeof part !== 'object') return ''
    const partRecord = part as Record<string, unknown>
    if (partRecord.thought === true || partRecord.type === 'thinking') return ''
    return typeof partRecord.text === 'string' ? partRecord.text : typeof partRecord.content === 'string' ? partRecord.content : ''
  }).join('')
}

const saveBackup = async ({ automatic = false, notify = true, skill = selectedSkill.value, state = skill ? ensureTranslationState(skill) : undefined, original = state?.original || '' }: { automatic?: boolean; notify?: boolean; skill?: ManagedSkill | null; state?: TranslationState; original?: string } = {}) => {
  if (!skill || !state?.translated.trim() || !original) {
    if (automatic) throw new Error('翻译完成，但没有可保存的译文内容')
    return
  }
  state.saving = true
  try {
    const channel = typeof state.channel === 'string' ? translationChannelByKey(state.channel) : undefined
    const channelName = channel ? `${TRANSLATION_PROTOCOL_LABELS[channel.protocol]} · ${channel.channel.name}` : undefined
    const result = await api.backupSkill({ locationKey: skill.locationKey, name: skill.name, original, translated: state.translated, model: state.model, channelName })
    const translatedName = translatedNameFromContent(state.translated)
    if (translatedName && translatedName !== skill.name) skill.translatedName = translatedName
    if (notify) showToast(automatic ? '翻译完成，已自动保存' : `翻译备份已保存到 ${result.path}`, 'success')
  } catch (saveError) {
    const message = saveError instanceof Error ? `译文已生成，但保存失败：${saveError.message}` : '译文已生成，但保存备份失败'
    if (automatic) throw new Error(message)
    state.error = message
  } finally { state.saving = false }
}

const confirmDelete = (skill: ManagedSkill) => { selectedSkill.value = skill; deleteDialog.value = true }
const deleteSelected = async () => {
  if (!selectedSkill.value) return
  deleting.value = true
  try { await api.deleteSkill(selectedSkill.value.locationKey, selectedSkill.value.name); showToast('Skill 已删除', 'success'); deleteDialog.value = false; await loadSkills() }
  catch (deleteError) { showToast(deleteError instanceof Error ? deleteError.message : '删除失败', 'error') }
  finally { deleting.value = false }
}

const fileToBase64 = (file: File) => new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onerror = () => reject(new Error('读取文件失败')); reader.onload = () => { const value = String(reader.result || ''); resolve(value.substring(value.indexOf(',') + 1)) }; reader.readAsDataURL(file) })
const copySkillPath = (path: string) => { void copyText(path, '路径已复制') }
const installCountFormatter = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const formatInstalls = (value: number) => installCountFormatter.format(value || 0).replace(/\s+/g, '')
const formatBytes = (value: number) => value < 1024 ? `${value} B` : value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KB` : `${(value / (1024 * 1024)).toFixed(1)} MB`
onMounted(loadSkills)
onUnmounted(() => modelAvailabilityController?.abort())
</script>

<style scoped>
@reference "@/styles/index.css";

.skills-page { @apply max-w-360 [margin:0_auto]; }
.skills-header-sticky {
  @apply sticky [top:var(--app-header-height,_0px)] [z-index:4] bg-background [box-shadow:0_-32px_0_0_var(--background)];
}
.locations-strip { @apply [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] rounded-card [padding:14px_16px]; }
.locations-heading { @apply flex items-center justify-between gap-3 mb-3; }
.location-grid { @apply grid gap-2 [grid-template-columns:repeat(auto-fill,_minmax(min(100%,_280px),_1fr))]; }
.location-card { @apply min-w-0 rounded-control [border:1px_solid_color-mix(in_srgb,_var(--outline)_22%,_transparent)] [background:color-mix(in_srgb,_var(--foreground)_2.5%,_transparent)] [padding:10px_12px] transition-colors; }
.location-card:hover { @apply [background:color-mix(in_srgb,_var(--primary)_5%,_transparent)]; }
.location-card--active { @apply [border-color:var(--primary)] [background:color-mix(in_srgb,_var(--primary)_7%,_transparent)]; }
.location-card-heading, .location-name, .location-path-row { @apply flex items-center min-w-0; }
.location-card-heading { @apply justify-between gap-2 mb-2; }
.location-name { @apply gap-2 [background:none] border-0 [color:inherit] cursor-pointer [font:inherit] font-medium p-0 text-left; }
.location-name:hover { @apply text-primary; }
.location-name span { @apply overflow-hidden text-ellipsis whitespace-nowrap; }
.location-path-row { @apply gap-1; }
.location-path { @apply min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-xs text-muted-foreground; }
.location-chip, .source-chip { @apply cursor-pointer; }
.location-chip:hover, .source-chip:hover { @apply [filter:brightness(0.96)]; }
.location-chip--active, .source-chip--active { @apply [outline:2px_solid_var(--primary)] [outline-offset:1px]; }
.skills-list-card { @apply overflow-hidden; }
.skills-table { @apply [max-height:min(72vh,_760px)] overflow-auto; }
.skills-table :deep(table) { @apply min-w-285 [table-layout:fixed]; }
.skills-table :deep(thead th) { @apply sticky top-0 [z-index:2] whitespace-nowrap [background:var(--surface)] [box-shadow:0_1px_0_color-mix(in_srgb,_var(--outline)_28%,_transparent)]; }
.skill-column { width: 27%; }
.source-column { width: 13%; }
.status-column { width: 9%; }
.agents-column { width: 22%; }
.path-column { width: 15%; }
.actions-column { width: 14%; }
.status-cell { @apply whitespace-nowrap; }
.skills-pagination { @apply flex items-center justify-between flex-wrap gap-3 [padding:12px_16px] [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)]; }
.skill-name-button { @apply [background:none] border-0 [color:inherit] cursor-pointer [font:inherit] p-0 text-left no-underline; }
.skill-name-button:hover { @apply text-primary underline; }
.skill-description { @apply max-w-110 mt-1 line-clamp-2 whitespace-normal leading-5; }
.agent-toggles-cell { @apply min-w-72; }
.agent-toggle-chip { @apply min-h-8 cursor-pointer whitespace-nowrap; }
.skill-path-cell { @apply items-center flex gap-0.5 min-w-0; }
.skill-path { @apply block min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap [font-size:12px]; }
.detail-card { @apply [height:min(900px,_calc(100vh_-_48px))] [max-height:calc(100vh_-_48px)] flex flex-col overflow-hidden; }
.detail-header, .detail-footer { @apply [flex:0_0_auto]; }
.detail-body { @apply [flex:1_1_auto] min-h-0 overflow-y-auto; }
.detail-footer { @apply [background:var(--surface)]; }
.detail-actions { @apply items-center flex gap-2 justify-end min-h-14; }
.skill-editor :deep(textarea) { @apply overflow-y-auto; }
.discover-card { @apply [max-height:calc(100vh_-_48px)] overflow-y-auto; }
.remote-results-table { @apply [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)]; }
.install-count-cell, .install-count { @apply min-w-21 whitespace-nowrap; }
.source-link { @apply text-primary no-underline; }
.source-link:hover { @apply underline; }
.file-list { @apply max-h-55 overflow-y-auto [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] rounded-badge p-3; }
@media (max-width: 600px) {
  .locations-strip { @apply [padding:12px]; }
  .locations-heading { @apply items-start; }
  .skill-description { @apply max-w-45; }
  .skills-table { @apply [max-height:70vh]; }
  .detail-card { @apply [height:calc(100vh_-_24px)] [max-height:calc(100vh_-_24px)]; }
  .detail-actions { @apply justify-start; }
}
</style>
