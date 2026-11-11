<template>
  <div class="skills-page">
    <PageHeader title="Skills" description="发现、安装并管理各客户端使用的技能。">
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadSkills">刷新</v-btn>
        <v-btn variant="text" prepend-icon="mdi-archive-outline" :loading="consolidating" @click="consolidateInstalledSkills">导入现有</v-btn>
        <v-btn variant="text" prepend-icon="mdi-swap-horizontal" @click="showTranslatedNames = !showTranslatedNames">{{ showTranslatedNames ? '显示原名称' : '显示译名' }}</v-btn>
        <v-btn variant="tonal" prepend-icon="mdi-magnify" @click="openDiscover">发现 Skill</v-btn>
        <v-btn color="primary" prepend-icon="mdi-plus" @click="openImport">本地安装</v-btn>
    </PageHeader>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{ error }}</v-alert>

    <DisclosureSection title="来源与安装位置" description="查看各客户端的技能目录、读写状态和备份位置。" icon="mdi-folder-open" class="mb-5">
      <v-alert type="info" variant="tonal" density="compact" class="mb-4">
        项目 <code>skills/</code> 目录是统一可恢复来源：导入、远程安装和翻译保存都会同步保留 Skill；翻译备份位于 <code>skills/&lt;名称&gt;/.backups/&lt;时间&gt;/</code>。插件缓存与 Codex 内置 Skill 仅供查看，删除请回到原 Agent 的插件管理。
      </v-alert>

      <div class="locations-strip">
        <div class="text-caption text-medium-emphasis mb-2">已检查的本机目录</div>
        <div class="d-flex flex-wrap ga-2">
          <v-chip v-for="location in locations" :key="location.key" size="small" label :title="`${location.path}（点击筛选）`" :color="location.exists ? (location.readOnly ? 'info' : 'success') : undefined" :class="['location-chip', { 'location-chip--active': agentFilter === location.key }]" @click="filterByAgent(location.key)">
            {{ location.agent }} · {{ location.exists ? (location.readOnly ? '只读' : '可安装') : '目录不存在' }}
          </v-chip>
        </div>
      </div>
    </DisclosureSection>

    <v-row class="mb-1">
      <v-col cols="12" md="7">
        <v-text-field v-model.trim="query" density="comfortable" variant="outlined" prepend-inner-icon="mdi-magnify" label="搜索名称、描述或来源" hide-details />
      </v-col>
      <v-col cols="12" md="5">
        <v-select v-model="agentFilter" :items="agentOptions" item-title="title" item-value="value" label="来源" density="comfortable" variant="outlined" hide-details />
      </v-col>
    </v-row>

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />

    <v-card elevation="0" class="skills-list-card">
      <v-table density="comfortable">
        <thead>
          <tr><th>Skill</th><th>来源</th><th>状态</th><th>路径</th><th class="text-right">操作</th></tr>
        </thead>
        <tbody>
          <tr v-if="!loading && filteredSkills.length === 0"><td colspan="5" class="text-center text-medium-emphasis py-10">未发现符合条件的 Skill</td></tr>
          <tr v-for="skill in visibleSkills" :key="`${skill.locationKey}:${skill.name}`">
            <td>
              <v-tooltip :text="skill.note || ''" :disabled="!skill.note" location="top" theme="dark"><template #activator="{ props }"><button v-bind="props" type="button" class="skill-name-button font-weight-medium" @click="openSkill(skill)">{{ displaySkillName(skill) }}<v-tooltip v-if="skill.translatedName" text="已翻译" location="top" theme="dark"><template #activator="{ props: tipProps }"><v-icon v-bind="tipProps" icon="mdi-translate" size="x-small" color="primary" class="ml-1" /></template></v-tooltip></button></template></v-tooltip>
              <div class="text-caption text-medium-emphasis skill-description">{{ skill.description || '未读取到说明' }}</div>
            </td>
            <td>
              <v-chip size="small" label :color="skill.readOnly ? 'info' : undefined" :class="['source-chip', { 'source-chip--active': agentFilter === skill.locationKey }]" title="点击筛选此来源" @click="filterByAgent(skill.locationKey)">{{ skill.agent }}</v-chip>
              <div v-if="skill.readOnly" class="text-caption text-medium-emphasis mt-1">只读来源</div>
            </td>
            <td><v-chip size="small" :color="skill.valid ? 'success' : 'warning'" label>{{ skill.valid ? '有效' : skill.issue || '需检查' }}</v-chip></td>
            <td><div class="skill-path-cell"><code class="skill-path">{{ skill.path }}</code><v-tooltip text="复制路径" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-content-copy" variant="text" size="x-small" :aria-label="`复制 ${skill.name} 的路径`" @click="copySkillPath(skill.path)" /></template></v-tooltip></div></td>
            <td class="text-right text-no-wrap">
              <v-tooltip text="查看与翻译" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-text-box-search-outline" variant="text" size="small" @click="openSkill(skill)" /></template></v-tooltip>
              <v-tooltip :text="skill.readOnly ? '只读来源不可复制' : '复制到其他 Agent'" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-content-copy" :disabled="skill.readOnly" variant="text" size="small" @click="openCopy(skill)" /></template></v-tooltip>
              <v-tooltip :text="skill.readOnly ? '只读来源不可删除' : '删除'" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-delete" :disabled="skill.readOnly" color="error" variant="text" size="small" @click="confirmDelete(skill)" /></template></v-tooltip>
            </td>
          </tr>
        </tbody>
      </v-table>
      <div class="skills-pagination">
        <span class="text-body-2 text-medium-emphasis" role="status">共 {{ filteredSkills.length }} 项 · 每页 {{ skillsPageSize }} 项</span>
        <v-pagination v-if="skillsPageCount > 1" v-model="skillsPage" :length="skillsPageCount" :total-visible="3" density="compact" aria-label="技能列表分页" />
      </div>
    </v-card>

    <v-dialog v-model="importDialog" max-width="700" persistent>
      <v-card>
        <v-card-title>安装 Skill</v-card-title>
        <v-card-text>
          <v-file-input v-model="importFile" accept=".zip,.md,text/markdown,application/zip" label="ZIP 压缩包或 SKILL.md" variant="outlined" prepend-icon="mdi-paperclip" show-size />
          <v-select v-model="importTargets" :items="writableLocations" item-title="agent" item-value="key" label="安装目标" variant="outlined" multiple chips />
          <v-alert type="warning" density="compact" variant="tonal">项目 Skills 目录会始终同步保留一份。其他同名目标会被覆盖；ZIP 只接受单一 Skill 目录，且必须包含有效的 <code>SKILL.md</code>。</v-alert>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="importDialog = false">取消</v-btn><v-btn color="primary" :disabled="!importFile || importTargets.length === 0" :loading="importing" @click="importSelected">安装</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="discoverDialog" max-width="1100" persistent>
      <v-card class="discover-card">
        <div class="d-flex align-center justify-space-between px-5 py-4">
          <!-- 发现 Skill 数据来自 skills.sh，安装前会从 GitHub 校验 SKILL.md 和文件清单 -->
          <v-btn icon="mdi-close" variant="text" @click="discoverDialog = false" />
        </div>
        <v-divider />
        <v-card-text class="pa-5">
          <v-text-field v-model.trim="discoverQuery" label="搜索 Skill，例如 ui、testing、react" variant="outlined" hide-details="auto" clearable @keyup.enter="searchRemote">
            <template #append-inner><v-btn icon="mdi-magnify" variant="text" size="small" :loading="remoteSearching" @click="searchRemote" /></template>
          </v-text-field>
          <v-alert v-if="discoverError" type="error" variant="tonal" class="mt-4">{{ discoverError }}</v-alert>
          <v-progress-linear v-if="remoteSearching" indeterminate color="primary" class="mt-4" />
          <div v-if="!remoteSearching && remoteResults.length === 0 && remoteSearched" class="text-center text-medium-emphasis py-10">没有找到匹配的 Skill</div>
          <v-table v-if="remoteResults.length" density="comfortable" class="mt-4 remote-results-table">
            <thead><tr><th>Skill</th><th>来源</th><th>安装量</th><th class="text-right">操作</th></tr></thead>
            <tbody>
              <tr v-for="result in remoteResults" :key="result.id">
                <td><div class="font-weight-medium">{{ result.name || result.skillId }}</div><div class="text-caption text-medium-emphasis">{{ result.skillId }}</div></td>
                <td><a v-if="result.repositoryUrl" :href="result.repositoryUrl" target="_blank" rel="noreferrer" class="source-link">{{ result.source }}</a><span v-else>{{ result.source }}</span></td>
                <td class="install-count-cell"><span class="install-count">{{ formatInstalls(result.installs) }}</span></td>
                <td class="text-right"><v-btn size="small" variant="tonal" color="primary" :loading="remoteInspectingId === result.id" @click="inspectRemote(result.id)">查看并安装</v-btn></td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog v-model="remotePreviewDialog" max-width="820" persistent>
      <v-card>
        <div class="d-flex align-center justify-space-between px-5 py-4">
          <div><div class="text-h6">安装 {{ remotePreview?.name }}</div><div class="text-caption text-medium-emphasis">{{ remotePreview?.source }}</div></div>
          <v-btn icon="mdi-close" variant="text" @click="remotePreviewDialog = false" />
        </div>
        <v-divider />
        <v-card-text class="pa-5">
          <v-alert v-if="remoteError" type="error" variant="tonal" class="mb-4">{{ remoteError }}</v-alert>
          <div v-if="remotePreview" class="text-body-2 mb-4">{{ remotePreview.description }}</div>
          <div v-if="remotePreview" class="d-flex flex-wrap ga-2 mb-4">
            <v-chip size="small" label>{{ remotePreview.files.length }} 个文件</v-chip>
            <v-chip v-if="remotePreview.license" size="small" label>许可证：{{ remotePreview.license }}</v-chip>
            <v-chip v-if="remotePreview.containsScripts" size="small" color="warning" label>包含 scripts/，请先审阅</v-chip>
            <v-btn v-if="remotePreview.repositoryUrl" :href="remotePreview.repositoryUrl" target="_blank" rel="noreferrer" size="small" variant="text" prepend-icon="mdi-open-in-new">GitHub 仓库</v-btn>
          </div>
          <v-select v-model="remoteTargets" :items="writableLocations" item-title="agent" item-value="key" label="安装目标" variant="outlined" multiple chips :disabled="remoteInstalling" />
          <v-alert type="warning" density="compact" variant="tonal" class="mb-4">项目 Skills 目录会始终同步保留一份；其他目标中的同名 Skill 会被覆盖。远程内容只会写入文件，不会在服务端执行脚本。</v-alert>
          <div v-if="remotePreview" class="file-list"><div class="text-caption text-medium-emphasis mb-2">将安装的文件</div><div v-for="file in remotePreview.files" :key="file.path" class="d-flex justify-space-between text-body-2"><code>{{ file.path }}</code><span class="text-medium-emphasis">{{ formatBytes(file.size) }}</span></div></div>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="remotePreviewDialog = false">取消</v-btn><v-btn color="primary" :disabled="!remotePreview || remoteTargets.length === 0" :loading="remoteInstalling" @click="installRemote">安装</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="detailDialog" max-width="1280" persistent>
      <v-card class="detail-card">
        <div class="detail-header d-flex align-center justify-space-between px-5 py-4"><div><div class="text-h6">{{ selectedDisplayName }}</div><div class="text-caption text-medium-emphasis">{{ selectedSkill?.path }}</div></div><v-btn icon="mdi-close" variant="text" @click="detailDialog = false" /></div>
        <v-divider />
        <v-card-text class="detail-body pa-5">
          <v-alert v-if="detailError" type="error" variant="tonal" class="mb-4">{{ detailError }}</v-alert>
          <v-row>
            <v-col cols="12" lg="6"><v-textarea :model-value="originalContent" label="原 Skill（只读）" variant="outlined" rows="19" readonly no-resize class="skill-editor" /></v-col>
            <v-col cols="12" lg="6"><v-textarea v-model="translatedContent" label="中文翻译" variant="outlined" rows="19" no-resize class="skill-editor" /></v-col>
          </v-row>
        </v-card-text>
        <v-divider />
        <div class="detail-footer px-5 py-4">
          <v-row align="end" class="detail-controls">
            <v-col cols="12" md="6" lg="3"><v-text-field v-model="skillNote" label="全局备注" variant="outlined" clearable hide-details="auto" @keyup.enter="saveSkillNote"><template #append-inner><v-tooltip text="保存备注" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-content-save" variant="text" size="small" :loading="savingNote" :disabled="savingNote" @click="saveSkillNote" /></template></v-tooltip></template></v-text-field></v-col>
            <v-col cols="12" md="6" lg="3"><v-select v-model="translationChannel" :items="chatChannels" item-title="name" item-value="index" label="Chat 渠道（可选）" variant="outlined" clearable /></v-col>
            <v-col cols="12" md="6" lg="4"><v-combobox v-model="translationModel" :items="translationModelOptions" item-title="title" item-value="value" label="模型 ID（可选）" variant="outlined" clearable :loading="translationModelsLoading" :error-messages="translationModelsError || undefined" /></v-col>
            <v-col cols="12" md="6" lg="2"><div class="detail-actions"><v-tooltip :text="selectedSkill?.readOnly ? '只读来源不可复制' : '复制到其他 Agent'" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-content-copy" :disabled="!selectedSkill || selectedSkill.readOnly" variant="tonal" @click="openSelectedCopy" /></template></v-tooltip><v-tooltip text="翻译并自动保存" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-translate" color="primary" :loading="translating" @click="translateSkill" /></template></v-tooltip><v-tooltip text="保存当前译文" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-content-save" :disabled="!translatedContent || savingBackup" :loading="savingBackup" @click="() => saveBackup()" /></template></v-tooltip></div></v-col>
          </v-row>
        </div>
      </v-card>
    </v-dialog>

    <v-dialog v-model="copyDialog" max-width="650" persistent>
      <v-card>
        <v-card-title>复制 Skill 到其他 Agent</v-card-title>
        <v-card-text>
          <div class="text-body-2 mb-4">来源：<strong>{{ selectedSkill?.name }}</strong>（{{ selectedSkill?.agent }}）</div>
          <v-select v-model="copyTargets" :items="copyTargetLocations" item-title="agent" item-value="key" label="复制目标" variant="outlined" multiple chips :disabled="copying" />
          <v-alert type="warning" density="compact" variant="tonal">将复制 SKILL.md 以及 references、scripts 等附属文件。同名 Skill 会覆盖目标用户目录；内置和插件来源不可作为复制源或目标。</v-alert>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="copyDialog = false">取消</v-btn><v-btn color="primary" :disabled="copyTargets.length === 0" :loading="copying" @click="copySelected">复制</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="deleteDialog" max-width="500" persistent>
      <v-card><v-card-title>删除 Skill</v-card-title><v-card-text>确认删除 <strong>{{ selectedSkill?.name }}</strong> 吗？将移除 <code>{{ selectedSkill?.path }}</code> 下的整个 Skill 目录。</v-card-text><v-card-actions><v-spacer /><v-btn variant="text" @click="deleteDialog = false">取消</v-btn><v-btn color="error" :loading="deleting" @click="deleteSelected">删除</v-btn></v-card-actions></v-card>
    </v-dialog>
    <v-snackbar v-model="notice.visible" :color="notice.type" location="bottom right" :timeout="3000" variant="elevated">
      <div class="d-flex align-center ga-3">
        <span>{{ notice.message }}</span>
        <v-btn icon="mdi-close" variant="text" size="small" aria-label="关闭通知" @click="notice.visible = false" />
      </div>
    </v-snackbar>
  </div>
</template>

<script setup lang="ts">
import PageHeader from '@/components/common/PageHeader.vue'
import DisclosureSection from '@/components/common/DisclosureSection.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api, fetchUpstreamModels, channelApiByType, type Channel, type ManagedSkill, type RemoteSkillPreview, type SkillLocation, type SkillSearchResult } from '@/services/api'
import { useAuthStore } from '@/stores/auth'

const PROXY_BASE = import.meta.env.PROD
  ? ''
  : import.meta.env.VITE_BACKEND_URL || ''

const loading = ref(false)
const consolidating = ref(false)
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
const chatChannels = ref<Channel[]>([])
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
const notice = ref({ visible: false, type: 'success', message: '' })
const savingNote = ref(false)

type TranslationState = {
  original: string
  translated: string
  error: string
  translating: boolean
  translationRequestId: number
  saving: boolean
  channel: number | null
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
const translationChannel = computed({ get: () => activeTranslationState.value.channel, set: value => { activeTranslationState.value.channel = value } })
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
    try {
      const channels = await channelApiByType('chat').getChannels()
      chatChannels.value = channels.channels.filter(channel => channel.status !== 'deleted')
    } catch (channelError) {
      chatChannels.value = []
      error.value = channelError instanceof Error ? `Skills 已加载，但 Chat 渠道不可用：${channelError.message}` : 'Skills 已加载，但 Chat 渠道不可用'
    }
  } catch (loadError) {
    error.value = loadError instanceof Error ? loadError.message : '加载 Skills 失败'
  } finally { loading.value = false }
}

const defaultInstallTargets = () => writableLocations.value.filter(location => location.key === 'project' || location.key === 'agents').map(location => location.key)
const openImport = () => { importFile.value = null; importTargets.value = defaultInstallTargets(); importDialog.value = true }

const consolidateInstalledSkills = async () => {
  consolidating.value = true
  try {
    const result = await api.consolidateSkills()
    const conflictText = result.duplicates > 0 ? `，${result.duplicates} 个重复名称保留项目副本` : ''
    notice.value = { visible: true, type: 'success', message: `已统一归档 ${result.total} 个 Skill，新导入 ${result.imported} 个${conflictText}` }
    await loadSkills()
  } catch (consolidateError) {
    notice.value = { visible: true, type: 'error', message: consolidateError instanceof Error ? consolidateError.message : '导入现有 Skill 失败' }
  } finally { consolidating.value = false }
}

const loadTranslationModels = async (channelIndex: number, skill: ManagedSkill) => {
  const key = skillStateKey(skill)
  const requestId = (translationModelRequestIds.get(key) || 0) + 1
  translationModelRequestIds.set(key, requestId)
  const state = ensureTranslationState(skill)
  const channel = chatChannels.value.find(item => item.index === channelIndex)
  state.modelOptions = []
  state.modelsError = ''

  if (!channel) {
    state.modelsError = '未找到所选 Chat 渠道'
    return
  }
  const apiKey = channel.apiKeys.find(key => key.trim())
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

    const options = (result.data || [])
      .map(model => model.id.trim())
      .filter(Boolean)
      .map(id => ({ title: id, value: id }))
    state.modelOptions = options
    const configuredModel = channel.defaultModel?.trim()
    state.model = configuredModel || options[0]?.value || ''
    if (options.length === 0) state.modelsError = '未找到可用模型，可手动输入模型 ID'
  } catch (modelError) {
    if (translationModelRequestIds.get(key) !== requestId) return
    state.modelsError = modelError instanceof Error ? modelError.message : '获取模型列表失败，可手动输入模型 ID'
  } finally {
    if (translationModelRequestIds.get(key) === requestId) state.modelsLoading = false
  }
}

watch([selectedSkillKey, translationChannel], ([key, channelIndex], [previousKey, previousChannel]) => {
  if (!selectedSkill.value || !key || key !== previousKey || channelIndex === previousChannel) return
  const state = ensureTranslationState(selectedSkill.value)
  const requestId = (translationModelRequestIds.get(key) || 0) + 1
  translationModelRequestIds.set(key, requestId)
  state.model = ''
  state.modelOptions = []
  state.modelsError = ''
  state.modelsLoading = false
  if (typeof channelIndex === 'number') void loadTranslationModels(channelIndex, selectedSkill.value)
})

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
    notice.value = { visible: true, type: 'success', message: `已安装 ${result.name}` }
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
    notice.value = { visible: true, type: 'success', message: `已安装 ${result.name}` }
    importDialog.value = false
    await loadSkills()
  } catch (importError) {
    notice.value = { visible: true, type: 'error', message: importError instanceof Error ? importError.message : '导入 Skill 失败' }
  } finally { importing.value = false }
}

const openSkill = async (skill: ManagedSkill) => {
  selectedSkill.value = skill
  const state = ensureTranslationState(skill)
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
      if (selectedSkillKey.value === skillStateKey(skill)) notice.value = { visible: true, type: 'success', message: '已恢复最近保存的译文' }
    } else if (originalChanged) {
      state.error = '原 Skill 已更新，旧译文已清除'
    }
  }
  catch (loadError) { state.error = loadError instanceof Error ? loadError.message : '读取 Skill 失败' }
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
    notice.value = { visible: true, type: 'success', message: result.note ? '全局备注已保存' : '全局备注已清空' }
  } catch (noteError) {
    notice.value = { visible: true, type: 'error', message: noteError instanceof Error ? noteError.message : '保存备注失败' }
  } finally { savingNote.value = false }
}

const copySelected = async () => {
  if (!selectedSkill.value || copyTargets.value.length === 0) return
  copying.value = true
  try {
    const result = await api.copySkill(selectedSkill.value.locationKey, selectedSkill.value.name, copyTargets.value)
    notice.value = { visible: true, type: 'success', message: `已复制 ${result.name} 到 ${result.targets.length} 个 Agent` }
    copyDialog.value = false
    await loadSkills()
  } catch (copyError) {
    notice.value = { visible: true, type: 'error', message: copyError instanceof Error ? copyError.message : '复制 Skill 失败' }
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

const normalizeTranslationChannelIndex = (value: unknown): number | null => {
  if (value === null || value === undefined || value === '') return null
  const parsed = typeof value === 'number' ? value : Number(String(value).trim())
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : null
}

const translateSkill = async () => {
  const skill = selectedSkill.value
  if (!skill) return
  const state = ensureTranslationState(skill)
  if (!state.original) return
  const original = state.original
  const requestId = ++state.translationRequestId
  const accessKey = useAuthStore().apiKey
  if (!accessKey) { state.error = '未找到管理界面访问密钥'; return }
  state.translating = true
  state.error = ''
  try {
    const model = normalizeTranslationModel(state.model)
    const channelIndex = normalizeTranslationChannelIndex(state.channel)
    const body: Record<string, unknown> = {
      model: model || 'translate-skill', stream: true,
      messages: [
        { role: 'system', content: '你是专业技术翻译。将用户提供的 Agent Skill 英文内容完整翻译为简体中文。保留 YAML frontmatter 的原有字段名、name 值、代码块、文件路径、命令、URL 和 Markdown 结构；在 YAML frontmatter 中新增 display_name 字段，值为该 Skill 的简体中文名称。只翻译可读的自然语言。仅输出翻译后的完整 Markdown，不要解释。' },
        { role: 'user', content: original }
      ]
    }
    if (channelIndex !== null) body.metadata = { channel_index: channelIndex }
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 10 * 60 * 1000)
    try {
      const response = await fetch(`${PROXY_BASE}/v1/chat/completions`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'x-api-key': accessKey }, body: JSON.stringify(body), signal: controller.signal })
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
      const translated = await readTranslationStream(response, value => { if (state.translationRequestId === requestId) state.translated = value })
      if (!translated.trim()) throw new Error('翻译渠道未返回可用文本内容，请检查所选模型是否支持 Chat Completions 输出')
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

const readTranslationStream = async (response: Response, onUpdate: (translated: string) => void): Promise<string> => {
  if (!response.body) throw new Error('翻译渠道未建立可读取的流式响应')
  if (response.headers.get('content-type')?.toLowerCase().includes('application/json')) {
    const payload = await response.json().catch(() => null)
    const translated = extractTranslationText(payload)
    if (translated) onUpdate(translated)
    return translated
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let pending = ''
  let translated = ''
  const appendDelta = (payload: unknown) => {
    const delta = extractTranslationText(payload)
    if (delta) {
      translated += delta
      onUpdate(translated)
    }
  }
  const appendJSON = (data: string) => {
    try { appendDelta(JSON.parse(data)) } catch { /* 仅处理符合 Chat Completions 结构的 JSON */ }
  }
  while (true) {
    const { done, value } = await reader.read()
    pending += decoder.decode(value || new Uint8Array(), { stream: !done })
    const lines = pending.split(/\r?\n/)
    pending = lines.pop() || ''
    for (const line of lines) {
      if (line.startsWith('data:')) {
        const data = line.slice(5).trim()
        if (data && data !== '[DONE]') appendJSON(data)
      } else if (line.trim()) {
        appendJSON(line.trim())
      }
    }
    if (done) break
  }
  if (pending.startsWith('data:')) {
    const data = pending.slice(5).trim()
    if (data && data !== '[DONE]') {
      appendJSON(data)
    }
  } else if (pending.trim()) {
    appendJSON(pending.trim())
  }
  if (!translated) {
    const fallback = extractTranslationTextFromResponse(pending)
    if (fallback) {
      translated = fallback
      onUpdate(translated)
    }
  }
  return translated
}

const extractTranslationText = (payload: unknown): string => {
  if (!payload || typeof payload !== 'object') return ''
  const record = payload as Record<string, unknown>
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
    return typeof partRecord.text === 'string' ? partRecord.text : typeof partRecord.content === 'string' ? partRecord.content : ''
  }).join('')
}

const extractTranslationTextFromResponse = (data: string): string => {
  try { return extractTranslationText(JSON.parse(data)) } catch { return '' }
}

const saveBackup = async ({ automatic = false, notify = true, skill = selectedSkill.value, state = skill ? ensureTranslationState(skill) : undefined, original = state?.original || '' }: { automatic?: boolean; notify?: boolean; skill?: ManagedSkill | null; state?: TranslationState; original?: string } = {}) => {
  if (!skill || !state?.translated.trim() || !original) {
    if (automatic) throw new Error('翻译完成，但没有可保存的译文内容')
    return
  }
  state.saving = true
  try {
    const channel = chatChannels.value.find(item => item.index === state.channel)
    const result = await api.backupSkill({ locationKey: skill.locationKey, name: skill.name, original, translated: state.translated, model: state.model, channelName: channel?.name })
    const translatedName = translatedNameFromContent(state.translated)
    if (translatedName && translatedName !== skill.name) skill.translatedName = translatedName
    if (notify) notice.value = { visible: true, type: 'success', message: automatic ? '翻译完成，已自动保存' : `翻译备份已保存到 ${result.path}` }
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
  try { await api.deleteSkill(selectedSkill.value.locationKey, selectedSkill.value.name); notice.value = { visible: true, type: 'success', message: 'Skill 已删除' }; deleteDialog.value = false; await loadSkills() }
  catch (deleteError) { notice.value = { visible: true, type: 'error', message: deleteError instanceof Error ? deleteError.message : '删除失败' } }
  finally { deleting.value = false }
}

const fileToBase64 = (file: File) => new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onerror = () => reject(new Error('读取文件失败')); reader.onload = () => { const value = String(reader.result || ''); resolve(value.substring(value.indexOf(',') + 1)) }; reader.readAsDataURL(file) })
const copySkillPath = async (path: string) => {
  try {
    await navigator.clipboard.writeText(path)
    notice.value = { visible: true, type: 'success', message: '路径已复制' }
  } catch {
    notice.value = { visible: true, type: 'error', message: '复制路径失败，请检查浏览器权限' }
  }
}
const installCountFormatter = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const formatInstalls = (value: number) => installCountFormatter.format(value || 0).replace(/\s+/g, '')
const formatBytes = (value: number) => value < 1024 ? `${value} B` : value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KB` : `${(value / (1024 * 1024)).toFixed(1)} MB`
onMounted(loadSkills)
</script>

<style scoped>
.skills-page { max-width: 1440px; margin: 0 auto; }
.locations-strip { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); border-radius: 6px; padding: 12px 16px; }
.location-chip, .source-chip { cursor: pointer; }
.location-chip:hover, .source-chip:hover { filter: brightness(0.96); }
.location-chip--active, .source-chip--active { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 1px; }
.skills-list-card { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); overflow-x: auto; }
.skills-pagination { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; padding: 12px 16px; border-top: 1px solid rgba(var(--v-theme-outline), 0.2); }
.skill-name-button { background: none; border: 0; color: inherit; cursor: pointer; font: inherit; padding: 0; text-align: left; text-decoration: none; }
.skill-name-button:hover { color: rgb(var(--v-theme-primary)); text-decoration: underline; }
.skill-description { max-width: 440px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.skill-path-cell { align-items: center; display: flex; gap: 2px; min-width: 0; }
.skill-path { display: block; max-width: 310px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.detail-card { height: min(900px, calc(100vh - 48px)); max-height: calc(100vh - 48px); display: flex; flex-direction: column; overflow: hidden; }
.detail-header, .detail-footer { flex: 0 0 auto; }
.detail-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; }
.detail-footer { background: rgb(var(--v-theme-surface)); }
.detail-actions { align-items: center; display: flex; gap: 8px; justify-content: flex-end; min-height: 56px; }
.skill-editor :deep(.v-field__input) { overflow-y: auto; }
.discover-card { max-height: calc(100vh - 48px); overflow-y: auto; }
.remote-results-table { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); }
.install-count-cell, .install-count { min-width: 84px; white-space: nowrap; }
.source-link { color: rgb(var(--v-theme-primary)); text-decoration: none; }
.source-link:hover { text-decoration: underline; }
.file-list { max-height: 220px; overflow-y: auto; border: 1px solid rgba(var(--v-theme-on-surface), 0.12); border-radius: 6px; padding: 12px; }
@media (max-width: 600px) { .skill-description, .skill-path { max-width: 180px; } .detail-card { height: calc(100vh - 24px); max-height: calc(100vh - 24px); } .detail-actions { justify-content: flex-start; } }
</style>
