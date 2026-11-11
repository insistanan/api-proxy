<template>
  <div class="mcp-page">
    <div class="page-heading mb-5">
      <div>
        <h1 class="text-h5 font-weight-bold">MCP</h1>
        <div class="text-body-2 text-medium-emphasis mt-1">统一管理服务器，按应用启用</div>
      </div>
      <div class="d-flex flex-wrap ga-2 align-center">
        <v-chip v-if="dirty" color="warning" size="small" label>有未保存的修改</v-chip>
        <v-btn variant="text" prepend-icon="mdi-refresh" :loading="loading" :disabled="saving" @click="reloadList">刷新</v-btn>
        <v-btn variant="tonal" prepend-icon="mdi-plus" :disabled="!editable" @click="openCreate">新增 MCP</v-btn>
        <v-btn color="primary" prepend-icon="mdi-content-save" :disabled="!editable || (!dirty && !needsSync)" :loading="saving" @click="saveList">{{ dirty ? '保存' : needsSync ? '重新分发' : '保存' }}</v-btn>
      </div>
    </div>

    <v-alert v-if="loadError" type="error" variant="tonal" class="mb-5" closable @click:close="loadError = ''">{{ loadError }}</v-alert>
    <v-alert v-if="saveError" type="error" variant="tonal" class="mb-5" closable @click:close="saveError = ''">{{ saveError }}</v-alert>
    <v-alert v-if="toggleNotice" type="success" variant="tonal" density="compact" class="mb-5" closable @click:close="toggleNotice = ''">{{ toggleNotice }}</v-alert>
    <v-alert v-if="saveResults" :type="saveResultsAllOk ? 'success' : 'warning'" variant="tonal" class="mb-5" closable @click:close="saveResults = null">
      <div class="font-weight-medium mb-1">{{ saveResultsAllOk ? '列表已保存，各智能体配置已更新' : '列表已保存，但以下智能体配置文件未更新' }}</div>
      <div v-for="result in saveResults" :key="result.key" class="text-body-2">
        {{ result.name }}：{{ result.ok ? '已写入' : `未更新（${result.error || '后端未返回错误原因'}）` }}
        <code v-if="result.path" class="ml-1">{{ result.path }}</code>
      </div>
    </v-alert>

    <v-alert type="info" variant="tonal" density="compact" class="mb-5">
      点击应用开关即可启用或禁用，自动保存。所有条目均可直接编辑或删除，修改后点击“保存”更新应用配置。开关表示用户级配置状态，客户端可能需要重新加载配置。
    </v-alert>

    <div v-if="agentStatuses.length" class="agent-strip mb-5">
      <div class="text-caption text-medium-emphasis mb-2">智能体配置文件</div>
      <div class="agent-grid">
        <div v-for="status in agentStatuses" :key="status.key">
          <div class="d-flex align-center flex-wrap ga-2 mb-1">
            <span class="font-weight-medium">{{ status.name }}</span>
            <v-chip size="x-small" label :color="status.error ? 'error' : status.exists ? 'success' : undefined">{{ agentStatusLabel(status) }}</v-chip>
          </div>
          <code class="agent-path" :title="status.path">{{ status.path || '未解析到配置路径' }}</code>
          <div v-if="status.error" class="text-caption text-error mt-1">{{ status.error }}</div>
        </div>
      </div>
    </div>

    <div class="list-toolbar mb-4">
      <v-text-field v-model="search" class="mcp-search" label="搜索名称、命令或地址" prepend-inner-icon="mdi-magnify" clearable hide-details density="compact" />
      <span class="text-body-2 text-medium-emphasis" aria-live="polite">{{ servers.length }} 个已管理 · {{ discoveredRows.length }} 个从应用发现</span>
      <v-spacer />
      <span class="text-caption text-medium-emphasis">开关自动保存 · 内容编辑后保存</span>
    </div>

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />

    <v-card elevation="0" class="mcp-list-card">
      <v-table density="comfortable">
        <thead>
          <tr>
            <th class="server-column">名称 / 命令或地址</th>
            <th v-for="agent in AGENT_OPTIONS" :key="agent.key" class="text-center agent-column">
              <div>{{ agent.label }}</div>
              <div class="text-caption text-medium-emphasis mt-1">{{ activeLabel(agent.key) }}</div>
              <div v-if="dirty" class="text-caption text-primary">{{ draftCount(agent.key) }} 个已选</div>
            </th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && filteredManaged.length === 0 && filteredDiscovered.length === 0">
            <td :colspan="AGENT_OPTIONS.length + 2" class="text-center text-medium-emphasis py-10">{{ !loaded ? '列表未加载，请刷新重试' : search ? '没有匹配的 MCP' : '未发现 MCP，可点击“新增 MCP”添加' }}</td>
          </tr>
          <tr v-for="{ server, index } in filteredManaged" :key="`managed:${index}`">
            <td class="server-cell">
              <div class="d-flex align-center flex-wrap ga-2 mb-1">
                <span class="font-weight-bold">{{ server.name }}</span>
                <v-chip size="x-small" variant="outlined">{{ server.transport }}</v-chip>
                <v-chip v-if="!server.agents.length" size="x-small" variant="outlined">未启用</v-chip>
                <v-chip v-if="hasDrift(server)" size="x-small" color="warning" variant="tonal">应用配置有差异</v-chip>
              </div>
              <code class="mcp-target" :title="targetText(server)">{{ targetText(server) }}</code>
              <div v-if="server.agentConfigs" class="text-caption text-medium-emphasis mt-1" :title="variantDetails(server)">按应用保留启动方式（悬停查看）</div>
            </td>
            <td v-for="agent in AGENT_OPTIONS" :key="agent.key" class="text-center">
              <v-btn
                :icon="actualEnabled(server, agent.key) ? 'mdi-checkbox-marked' : 'mdi-checkbox-blank-outline'"
                :color="actualEnabled(server, agent.key) ? 'primary' : undefined"
                variant="text"
                :loading="toggling === toggleKey(server, agent.key)"
                :disabled="!editable || !!toggleBlockReason(server, agent.key)"
                :title="toggleBlockReason(server, agent.key) || `${actualEnabled(server, agent.key) ? '禁用' : '启用'} ${agent.label}`"
                :aria-label="`${server.name} 在 ${agent.label} 中${actualEnabled(server, agent.key) ? '禁用' : '启用'}`"
                :aria-pressed="actualEnabled(server, agent.key)"
                @click="setAgentEnabled(index, agent.key, !actualEnabled(server, agent.key))"
              />
            </td>
            <td class="text-right text-no-wrap">
              <v-tooltip text="编辑" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-pencil" variant="text" size="small" :aria-label="`编辑 ${server.name}`" :disabled="!editable" @click="openEdit(index)" /></template></v-tooltip>
              <v-tooltip text="删除" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-delete" color="error" variant="text" size="small" :aria-label="`删除 ${server.name}`" :disabled="!editable" @click="confirmDelete(index)" /></template></v-tooltip>
            </td>
          </tr>
          <tr v-for="row in filteredDiscovered" :key="row.key" class="discovered-row">
            <td class="server-cell">
              <div class="d-flex align-center flex-wrap ga-2 mb-1">
                <span class="font-weight-bold">{{ row.name }}</span>
                <v-chip v-if="row.server" size="x-small" variant="outlined">{{ row.server.transport }}</v-chip>
                <v-chip size="x-small" color="info" variant="tonal">从应用发现</v-chip>
                <v-chip v-if="row.conflict" size="x-small" color="warning" variant="tonal">同名配置不同</v-chip>
              </div>
              <code v-if="row.server" class="mcp-target" :title="targetText(row.server)">{{ targetText(row.server) }}</code>
              <div class="text-caption text-medium-emphasis mt-1">来源：{{ sourceLabels(row.sources) }}</div>
              <div v-if="row.server && row.sources.length > 1" class="text-caption text-medium-emphasis mt-1" :title="variantDetails(row.server)">按应用保留启动方式（悬停查看）</div>
              <div v-if="row.error" class="text-caption text-error mt-1">{{ row.error }}</div>
              <div v-if="row.warning" class="text-caption text-warning mt-1">{{ row.warning }}</div>
            </td>
            <td v-for="agent in AGENT_OPTIONS" :key="agent.key" class="text-center">
              <v-btn
                v-if="row.server"
                :icon="actualEnabled(row.server, agent.key) ? 'mdi-checkbox-marked' : 'mdi-checkbox-blank-outline'"
                :color="actualEnabled(row.server, agent.key) ? 'primary' : undefined"
                variant="text"
                :loading="toggling === toggleKey(row.server, agent.key)"
                :disabled="!editable || !!toggleBlockReason(row.server, agent.key)"
                :title="toggleBlockReason(row.server, agent.key) || `${actualEnabled(row.server, agent.key) ? '禁用' : '启用'} ${agent.label}`"
                :aria-label="`${row.name} 在 ${agent.label} 中${actualEnabled(row.server, agent.key) ? '禁用' : '启用'}`"
                :aria-pressed="actualEnabled(row.server, agent.key)"
                @click="toggleServer(row.server, agent.key, !actualEnabled(row.server, agent.key))"
              />
              <span v-else class="text-caption text-medium-emphasis">{{ row.sources.includes(agent.key) ? '无法识别' : '—' }}</span>
            </td>
            <td class="text-right text-no-wrap">
              <v-tooltip text="编辑" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-pencil" variant="text" size="small" :aria-label="`编辑 ${row.name}`" :disabled="!editable || !row.server" @click="openDetectedEdit(row)" /></template></v-tooltip>
              <v-tooltip text="删除" location="top" theme="dark"><template #activator="{ props }"><v-btn v-bind="props" icon="mdi-delete" color="error" variant="text" size="small" :aria-label="`删除 ${row.name}`" :disabled="!editable || !row.server" @click="confirmDetectedDelete(row)" /></template></v-tooltip>
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <v-dialog v-model="dialogOpen" max-width="720" persistent>
      <v-card>
        <v-card-title>{{ editingIndex === null && !editingSource ? '新增 MCP' : '编辑 MCP' }}</v-card-title>
        <v-card-text>
          <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-4">{{ formError }}</v-alert>
          <v-alert v-if="importHint" type="info" variant="tonal" density="compact" class="mb-4">{{ importHint }}</v-alert>
          <v-btn-toggle :model-value="inputMode" mandatory divided color="primary" density="comfortable" class="mb-4" @update:model-value="setInputMode">
            <v-btn value="command">粘贴命令</v-btn>
            <v-btn value="fields">分项填写</v-btn>
          </v-btn-toggle>
          <v-row>
            <v-col cols="12" md="6"><v-text-field v-model="form.name" label="名称" hint="仅允许字母、数字、下划线和连字符，1–64 个字符" persistent-hint /></v-col>
            <v-col cols="12" md="6"><v-select :model-value="form.transport" :items="TRANSPORT_OPTIONS" label="传输方式" @update:model-value="onTransportChange" /></v-col>
          </v-row>
          <template v-if="inputMode === 'command' && form.transport === 'stdio'">
            <v-textarea :model-value="commandText" label="MCP 启动命令" placeholder="npx -y @playwright/mcp@latest --extension" hint="粘贴完整命令，自动识别命令、参数和名称；支持带引号的路径与参数。" persistent-hint rows="3" :error-messages="commandParseError" @update:model-value="applyCommandText" />
            <div v-if="form.command && !commandParseError" class="command-preview mt-3 mb-4 text-body-2">
              <div>命令：<code>{{ form.command }}</code></div>
              <div>参数：<code>{{ form.argsText }}</code></div>
              <div v-if="form.envText !== '{}'">环境变量：<code>{{ form.envText }}</code></div>
            </div>
          </template>
          <template v-else-if="form.transport === 'stdio'">
            <v-text-field v-model="form.command" label="命令" placeholder="例如 npx" />
            <v-textarea v-model="form.argsText" label="参数" hint="支持 JSON 字符串数组，或每行一个参数（忽略空行）" persistent-hint rows="3" no-resize />
            <v-textarea v-model="form.envText" label="环境变量" hint="支持 JSON 对象，或每行一个 KEY=VALUE" persistent-hint rows="3" no-resize />
          </template>
          <template v-else>
            <v-text-field v-model="form.url" label="URL" placeholder="https://example.com/mcp" />
            <v-textarea v-model="form.headersText" label="请求头" hint="支持 JSON 对象，或每行一个 KEY=VALUE" persistent-hint rows="3" no-resize />
          </template>
          <div class="text-subtitle-2 mt-2 mb-1">分发到</div>
          <div class="d-flex flex-wrap ga-4">
            <v-checkbox v-for="agent in AGENT_OPTIONS" :key="agent.key" v-model="form.agents" :value="agent.key" :label="agent.label" density="compact" hide-details />
          </div>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="dialogOpen = false">取消</v-btn><v-btn color="primary" @click="submitForm">应用到列表</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="deleteDialog" max-width="480" persistent>
      <v-card>
        <v-card-title>删除 MCP</v-card-title>
        <v-card-text>将删除 <strong>{{ deletingServer?.name }}</strong><span v-if="deletingSource">（来源：{{ sourceLabels(deletingSource.sources) }}）</span>。点击页面“保存”后，会从对应应用中移除配置并清理停用记录。</v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="deleteDialog = false">取消</v-btn><v-btn color="error" @click="deleteServer">删除</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api, ApiError } from '@/services/api'
import { formatMcpCommand, parseMcpCommand } from '@/utils/mcpCommand'
import type { AgentResult, AgentStatus, DetectedMcpServer, McpAdoption, McpAgentKey, McpConnection, McpServer, McpTransport } from '@/types'

// 智能体顺序与展示名固定。保存时 agents 按此顺序输出，使草稿与服务端快照可直接比较。
const AGENT_OPTIONS: { key: McpAgentKey; label: string }[] = [
  { key: 'claude-code', label: 'Claude Code' },
  { key: 'codex', label: 'Codex' },
  { key: 'opencode', label: 'OpenCode' },
  { key: 'pi', label: 'pi' },
  { key: 'dsh', label: 'DSH' }
]
const TRANSPORT_OPTIONS: { title: string; value: McpTransport }[] = [
  { title: 'stdio（本地命令）', value: 'stdio' },
  { title: 'http（远程地址）', value: 'http' }
]
// 与后端契约一致：名称只允许 ASCII 字母、数字、下划线与连字符。
const NAME_PATTERN = /^[A-Za-z0-9_-]{1,64}$/

interface McpFormState {
  name: string
  transport: McpTransport
  command: string
  argsText: string
  envText: string
  url: string
  headersText: string
  agents: McpAgentKey[]
}

const emptyForm = (): McpFormState => ({
  name: '', transport: 'stdio', command: '', argsText: '', envText: '', url: '', headersText: '', agents: []
})

const loading = ref(false)
const saving = ref(false)
// 只有成功读取到 revision 后才允许编辑与保存；没有可信版本号时提交会绕过冲突检测。
const loaded = ref(false)
const loadError = ref('')
const saveError = ref('')
const revision = ref('')
// 最近一次服务端确认的列表（规范化后），用于判断草稿是否有未保存修改。
const baseline = ref<McpServer[]>([])
// 草稿列表：增删改只作用于此，点击保存后才通过 PUT 整份提交。
const servers = ref<McpServer[]>([])
const agentStatuses = ref<AgentStatus[]>([])
const saveResults = ref<AgentResult[] | null>(null)
const dialogOpen = ref(false)
const editingIndex = ref<number | null>(null)
const formError = ref('')
const form = reactive<McpFormState>(emptyForm())
const inputMode = ref<'command' | 'fields'>('command')
const commandText = ref('')
const commandParseError = ref('')
const suggestedCommandName = ref('')
const deleteDialog = ref(false)
const deletingIndex = ref<number | null>(null)
const search = ref<string | null>('')
const detected = ref<DetectedMcpServer[]>([])
const editingSource = ref<DiscoveredRow | null>(null)
const deletingSource = ref<DiscoveredRow | null>(null)
const pendingChanges = ref<McpAdoption[]>([])
const importHint = ref('')
const toggling = ref('')
const toggleNotice = ref('')

const editable = computed(() => loaded.value && !saving.value && !loading.value)
const dirty = computed(() => loaded.value && (pendingChanges.value.length > 0 || JSON.stringify(servers.value.map(toPayload)) !== JSON.stringify(baseline.value.map(toPayload))))
const saveResultsAllOk = computed(() => saveResults.value?.every(result => result.ok) ?? false)
const deletingServer = computed(() => deletingSource.value?.server ?? (deletingIndex.value === null ? null : servers.value[deletingIndex.value] ?? null))

// 用 JSON 回填已有数据，完整保留空参数、换行和首尾空格。
const recordToText = (record?: Record<string, string>) => JSON.stringify(record ?? {}, null, 2)

// 解析 KEY=VALUE 多行文本。格式错误或重复键直接抛错，由表单展示，不静默丢弃。
const parseKeyValueLines = (text: string, label: string): Record<string, string> => {
  if (text.trim().startsWith('{')) {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 对象`)
    const entries = Object.entries(parsed)
    if (entries.some(([key, value]) => !key || typeof value !== 'string')) throw new Error(`${label}的键不能为空，值必须是字符串`)
    return Object.fromEntries(entries)
  }
  const entries = new Map<string, string>()
  const lines = text.split(/\r?\n/)
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    if (!line.trim()) continue
    const separator = line.indexOf('=')
    const key = separator > 0 ? line.slice(0, separator).trim() : ''
    if (!key) throw new Error(`${label}第 ${index + 1} 行格式应为 KEY=VALUE`)
    if (entries.has(key)) throw new Error(`${label}中的 ${key} 重复出现`)
    entries.set(key, line.slice(separator + 1))
  }
  return Object.fromEntries(entries)
}

const parseArgs = (text: string): string[] => {
  if (text.trim().startsWith('[')) {
    const parsed: unknown = JSON.parse(text)
    if (!Array.isArray(parsed) || parsed.some(value => typeof value !== 'string')) throw new Error('参数必须是 JSON 字符串数组')
    return parsed
  }
  return text.split(/\r?\n/).filter(line => line.length > 0)
}

const validateHttpUrl = (value: string): void => {
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    throw new Error('URL 格式无效，请填写完整地址，例如 https://example.com/mcp')
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('URL 必须以 http:// 或 https:// 开头')
  }
}

// 统一生成提交与比较用的规范结构：字段顺序固定、空的可选字段省略、agents 按 AGENT_OPTIONS 排序。
// 切换传输方式时已清空的字段不会出现在结构中，避免残留值被提交。
const toPayload = (server: McpServer): McpServer => {
  const agents = AGENT_OPTIONS.map(option => option.key).filter(key => server.agents.includes(key))
  const agentConfigs: McpServer['agentConfigs'] = {}
  for (const { key } of AGENT_OPTIONS) {
    const config = server.agentConfigs?.[key]
    if (config) agentConfigs[key] = connectionOf(toPayload({ ...config, name: server.name, agents: [] }))
  }
  const variants = Object.keys(agentConfigs).length ? { agentConfigs } : {}
  if (server.transport === 'stdio') {
    const args = server.args ?? []
    const env = server.env ?? {}
    return {
      name: server.name,
      transport: 'stdio',
      command: server.command,
      ...(args.length > 0 ? { args: [...args] } : {}),
      ...(Object.keys(env).length > 0 ? { env: { ...env } } : {}),
      agents,
      ...variants
    }
  }
  if (server.transport === 'http') {
    const headers = server.headers ?? {}
    return {
      name: server.name,
      transport: 'http',
      url: server.url,
      ...(Object.keys(headers).length > 0 ? { headers: { ...headers } } : {}),
      agents,
      ...variants
    }
  }
  throw new Error(`MCP ${server.name} 的传输方式无效：${String(server.transport)}`)
}

const connectionOf = (server: McpConnection): McpConnection => ({
  transport: server.transport,
  ...(server.command !== undefined ? { command: server.command } : {}),
  ...(server.args ? { args: [...server.args] } : {}),
  ...(server.env ? { env: { ...server.env } } : {}),
  ...(server.url !== undefined ? { url: server.url } : {}),
  ...(server.headers ? { headers: { ...server.headers } } : {})
})
const configForAgent = (server: McpServer, agent: McpAgentKey): McpServer => ({
  ...connectionOf(server.agentConfigs?.[agent] ?? server), name: server.name, agents: [...server.agents]
})

interface DiscoveredRow {
  key: string
  name: string
  server?: McpServer
  sources: McpAgentKey[]
  error?: string
  warning?: string
  conflict: boolean
}

// 比较配置内容时忽略来源和可选集合的空值，并固定字典顺序，避免把相同配置误判为冲突。
const configKey = (server: McpServer): string => JSON.stringify({
  name: server.name,
  transport: server.transport,
  command: server.command ?? '',
  args: server.args ?? [],
  env: Object.entries(server.env ?? {}).sort(([a], [b]) => a.localeCompare(b)),
  url: server.url ?? '',
  headers: Object.entries(server.headers ?? {}).sort(([a], [b]) => a.localeCompare(b))
})

// 仅折叠已拆分参数、没有 shell 元字符的 npx 包装，复杂 shell 命令仍按不同配置处理。
const identityKey = (server: McpServer): string => {
  let command = server.command ?? ''
  let args = [...(server.args ?? [])]
  if (server.transport === 'stdio' && /^(cmd(?:\.exe)?)$/i.test(command)
    && args[0]?.toLowerCase() === '/c' && /^npx(?:\.cmd)?$/i.test(args[1] ?? '')
    && args.slice(2).every(arg => arg.length > 0 && !/[\s&|<>^%!"'()]/.test(arg))) {
    command = 'npx'
    args = args.slice(2)
  } else if (/^npx(?:\.cmd)?$/i.test(command)) command = 'npx'
  return configKey({ ...server, command, args })
}

const variantDetails = (server: McpServer): string => AGENT_OPTIONS
  .filter(agent => server.agentConfigs?.[agent.key])
  .map(agent => `${agent.label}：${targetText(configForAgent(server, agent.key))}`).join('\n')

const discoveredRows = computed<DiscoveredRow[]>(() => {
  const groups = new Map<string, DiscoveredRow>()
  for (const entry of detected.value) {
    if (pendingChanges.value.some(change => change.agent === entry.agent && change.server.name === entry.name)) continue
    // 已托管的来源在主列表显示，外部漂移由主列表标记，避免同一来源重复出现。
    const owned = baseline.value.some(server => server.name === entry.name && server.agents.includes(entry.agent))
    const candidate = entry.server
    const staged = candidate && servers.value.some(server => identityKey(server) === identityKey(candidate))
    if (owned || staged) continue
    const key = entry.server ? identityKey(entry.server) : `${entry.agent}:${entry.name}`
    const existing = groups.get(key)
    if (existing) {
      existing.sources.push(entry.agent)
      if (entry.enabled && !existing.server?.agents.includes(entry.agent)) existing.server?.agents.push(entry.agent)
      if (entry.warning) existing.warning = entry.warning
      if (existing.server && entry.server) {
        existing.server.agentConfigs = { ...existing.server.agentConfigs, [entry.agent]: connectionOf(entry.server) }
      }
    } else {
      groups.set(key, {
        key,
        name: entry.name,
        server: entry.server ? toPayload({ ...entry.server, agentConfigs: { [entry.agent]: connectionOf(entry.server) } }) : undefined,
        sources: [entry.agent],
        error: entry.error,
        warning: entry.warning,
        conflict: false
      })
    }
  }
  const rows = [...groups.values()]
  for (const row of rows) {
    row.conflict = servers.value.some(server => server.name === row.name && identityKey(server) !== row.key)
      || rows.some(other => other !== row && other.name === row.name && other.key !== row.key)
  }
  return rows.sort((a, b) => a.name.localeCompare(b.name))
})

const matchesSearch = (name: string, server?: McpServer) => {
  const query = (search.value ?? '').trim().toLocaleLowerCase()
  return !query || `${name} ${server ? `${targetText(server)} ${variantDetails(server)}` : ''}`.toLocaleLowerCase().includes(query)
}
// 保留原数组下标；搜索后编辑或勾选不能误操作其它行。
const filteredManaged = computed(() => servers.value.map((server, index) => ({ server, index }))
  .filter(({ server }) => matchesSearch(server.name, server)))
const filteredDiscovered = computed(() => discoveredRows.value.filter(row => matchesSearch(row.name, row.server)))
const sourceLabels = (sources: McpAgentKey[]) => AGENT_OPTIONS.filter(agent => sources.includes(agent.key)).map(agent => agent.label).join('、')
const draftCount = (agent: McpAgentKey) => servers.value.filter(server => server.agents.includes(agent)).length
const activeLabel = (agent: McpAgentKey) => {
  const status = agentStatuses.value.find(item => item.key === agent)
  if (!status || status.error) return '状态未知'
  return `${status.active} 个启用`
}
const actualEnabled = (server: McpServer, agent: McpAgentKey) => detected.value.some(entry =>
  entry.agent === agent && entry.enabled && entry.server && configKey(entry.server) === configKey(configForAgent(server, agent)))
const withDetectedConfigs = (server: McpServer, entries: DetectedMcpServer[]): McpServer => {
  const result = toPayload(server)
  for (const entry of entries) {
    if (!entry.server || entry.name !== server.name || identityKey(entry.server) !== identityKey(server)) continue
    // 已保存的应用差异优先，不能用外部漂移静默改写用户草稿。
    if (!result.agentConfigs?.[entry.agent]) {
      result.agentConfigs = { ...result.agentConfigs, [entry.agent]: connectionOf(entry.server) }
    }
  }
  return toPayload(result)
}
const toggleKey = (server: McpServer, agent: McpAgentKey) => `${agent}:${configKey(server)}`
const toggleBlockReason = (server: McpServer, agent: McpAgentKey) => {
  const status = agentStatuses.value.find(item => item.key === agent)
  if (!status || status.error || !status.path) return status?.error || '应用配置状态未知，请刷新'
  const entry = detected.value.find(item => item.agent === agent && item.name === server.name)
  if (entry && (!entry.server || configKey(entry.server) !== configKey(configForAgent(server, agent)))) return '该应用中存在不同的同名配置，请更名后再启用'
  return ''
}

const toggleServer = async (server: McpServer, agent: McpAgentKey, enabled: boolean) => {
  if (!editable.value) return
  const blocked = toggleBlockReason(server, agent)
  if (blocked) {
    saveError.value = blocked
    return
  }
  const status = agentStatuses.value.find(item => item.key === agent)
  if (!status) return
  saving.value = true
  toggling.value = toggleKey(server, agent)
  saveError.value = ''
  toggleNotice.value = ''
  try {
    const target = configForAgent(server, agent)
    const result = await api.toggleMcp({ revision: revision.value, agentRevision: status.revision, agent, server: toPayload(target), enabled })
    revision.value = result.revision
    const outcome = result.agents.find(item => item.key === agent)
    if (!outcome?.ok) saveError.value = outcome?.error || '后端未确认应用配置写入结果，请刷新查看'
    else toggleNotice.value = `${server.name} 已在 ${status.name} 中${enabled ? '启用' : '禁用'}`
    // 开关只更新对应的草稿选择，不能覆盖用户对其它条目或字段的编辑。
    if (outcome?.ok) {
      for (const draft of servers.value) {
        if (configKey(configForAgent(draft, agent)) !== configKey(target)) continue
        draft.agentConfigs = { ...draft.agentConfigs, [agent]: connectionOf(target) }
        const agents = draft.agents.filter(key => key !== agent)
        if (enabled) agents.push(agent)
        draft.agents = AGENT_OPTIONS.map(option => option.key).filter(key => agents.includes(key))
      }
    }
    try {
      const state = await api.getMcp()
      revision.value = state.revision
      baseline.value = state.servers.map(server => withDetectedConfigs(server, state.detected))
      agentStatuses.value = state.agents
      detected.value = state.detected
      // 本页开关改变了文件版本；只有原始条目仍一致时才推进草稿的来源版本。
      for (const change of pendingChanges.value) {
        const entry = state.detected.find(item => item.agent === change.agent && item.name === change.server.name)
        const status = state.agents.find(item => item.key === change.agent)
        if (entry?.server && status && !status.error && configKey(entry.server) === configKey(change.server)) change.agentRevision = status.revision
      }
    } catch (error) {
      loaded.value = false
      loadError.value = `开关请求已提交，但读取最新状态失败：${error instanceof Error ? error.message : '读取失败'}。当前草稿已保留，请刷新后再操作。`
    }
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : '切换 MCP 状态失败'
  } finally {
    toggling.value = ''
    saving.value = false
  }
}
const hasDrift = (server: McpServer) => {
  const saved = baseline.value.find(item => item.name === server.name)
  if (!saved) return false
  return saved.agents.some(agent => {
    if (agentStatuses.value.find(status => status.key === agent)?.error) return true
    const entry = detected.value.find(item => item.agent === agent && item.name === saved.name)
    return !entry?.server || !entry.enabled || configKey(entry.server) !== configKey(configForAgent(saved, agent))
  })
}
const needsSync = computed(() => (saveResults.value?.some(result => !result.ok) ?? false) || baseline.value.some(hasDrift))

// 只记录用户操作过的来源及原始版本，保存时才能接管这些具体条目。
const stageChanges = (server: McpServer, replacement: string, sources?: McpAgentKey[]) => {
  const changes = pendingChanges.value.map(change => ({ ...change }))
  for (const change of changes) {
    if (change.replacement === server.name) change.replacement = replacement
  }
  for (const entry of detected.value) {
    if (!entry.server || (sources && !sources.includes(entry.agent))
      || configKey(entry.server) !== configKey(configForAgent(server, entry.agent))) continue
    const existing = changes.find(change => change.agent === entry.agent && change.server.name === entry.name)
    if (existing) existing.replacement = replacement
    else {
      const status = agentStatuses.value.find(item => item.key === entry.agent)
      if (!status || status.error) throw new Error(status?.error || '无法取得来源应用版本，请刷新')
      changes.push({ agent: entry.agent, agentRevision: status.revision, server: toPayload(entry.server), replacement })
    }
  }
  pendingChanges.value = changes
}

const openDetectedEdit = (row: DiscoveredRow) => {
  if (!editable.value || !row.server) return
  editingIndex.value = null
  editingSource.value = row
  Object.assign(form, formFromServer(row.server))
  inputMode.value = 'fields'
  formError.value = ''
  importHint.value = `来源：${sourceLabels(row.sources)}。修改后点击页面“保存”更新应用配置。`
  if (servers.value.some(server => server.name === row.name)) {
    form.name = `${row.name.slice(0, 42)}-${row.sources[0]}`
    importHint.value += '统一列表中已有不同的同名配置，已建议新名称。'
  }
  dialogOpen.value = true
}

const confirmDetectedDelete = (row: DiscoveredRow) => {
  if (!editable.value || !row.server) return
  deletingIndex.value = null
  deletingSource.value = row
  deleteDialog.value = true
}

const applyCommandText = (text: string) => {
  commandText.value = text
  commandParseError.value = ''
  try {
    const parsed = parseMcpCommand(text)
    form.transport = 'stdio'
    form.command = parsed.command ?? ''
    form.argsText = JSON.stringify(parsed.args ?? [])
    form.envText = JSON.stringify(parsed.env ?? {})
    if (!form.name || form.name === suggestedCommandName.value) form.name = parsed.suggestedName
    suggestedCommandName.value = parsed.suggestedName
  } catch (error) {
    commandParseError.value = error instanceof Error ? error.message : '命令识别失败'
  }
}

const setInputMode = (mode: 'command' | 'fields') => {
  if (mode === inputMode.value) return
  if (mode === 'command') {
    // 用 JSON 引号回填，保证从分项模式切回来时不丢失空参数和路径。
    try {
      const text = formatMcpCommand(form.command, parseArgs(form.argsText))
      const env = parseKeyValueLines(form.envText, '环境变量')
      commandText.value = Object.entries(env).map(([key, value]) => `${key}=${formatMcpCommand(value)}`).concat(text).join(' ')
      form.transport = 'stdio'
      form.url = ''
      form.headersText = ''
      commandParseError.value = ''
    } catch (error) {
      formError.value = error instanceof Error ? error.message : '请先修正分项配置'
      return
    }
  }
  inputMode.value = mode
}

const formFromServer = (server: McpServer): McpFormState => ({
  name: server.name,
  transport: server.transport,
  command: server.command ?? '',
  argsText: JSON.stringify(server.args ?? [], null, 2),
  envText: recordToText(server.env),
  url: server.url ?? '',
  headersText: recordToText(server.headers),
  agents: [...server.agents]
})

const buildServerFromForm = (): McpServer => {
  const name = form.name.trim()
  if (!NAME_PATTERN.test(name)) throw new Error('名称只能包含字母、数字、下划线和连字符，长度为 1–64 个字符')
  if (servers.value.some((server, index) => server.name === name && index !== editingIndex.value)) {
    throw new Error(`名称 ${name} 已存在`)
  }
  const agents = [...form.agents]
  if (agents.includes('dsh') && name.length > 32) throw new Error('DSH 的 MCP 名称最长为 32 个字符')
  if (form.transport === 'stdio') {
    const command = form.command.trim()
    if (!command) throw new Error('stdio 传输必须填写命令')
    return toPayload({
      name,
      transport: 'stdio',
      command,
      args: parseArgs(form.argsText),
      env: parseKeyValueLines(form.envText, '环境变量'),
      agents
    })
  }
  const url = form.url.trim()
  if (!url) throw new Error('http 传输必须填写 URL')
  validateHttpUrl(url)
  return toPayload({
    name,
    transport: 'http',
    url,
    headers: parseKeyValueLines(form.headersText, '请求头'),
    agents
  })
}

const targetText = (server: McpServer) => server.transport === 'stdio'
  ? [server.command ?? '', ...(server.args ?? [])].join(' ').trim()
  : (server.url ?? '')

const agentStatusLabel = (status: AgentStatus) => {
  if (status.error) return '读取失败'
  if (!status.path) return '路径未知'
  return status.exists ? `${status.count} 个 MCP · ${status.active} 个启用` : '文件不存在'
}

const loadList = async () => {
  loading.value = true
  loadError.value = ''
  try {
    const data = await api.getMcp()
    const normalized = data.servers.map(server => withDetectedConfigs(server, data.detected))
    revision.value = data.revision
    baseline.value = normalized
    servers.value = normalized.map(toPayload)
    agentStatuses.value = data.agents
    detected.value = data.detected
    pendingChanges.value = []
    saveResults.value = null
    loaded.value = true
  } catch (error) {
    loaded.value = false
    loadError.value = error instanceof Error ? error.message : '加载 MCP 列表失败'
  } finally {
    loading.value = false
  }
}

const reloadList = async () => {
  if (dirty.value && !window.confirm('刷新会丢弃未保存的修改，是否继续？')) return
  await loadList()
}

const saveList = async () => {
  if (!editable.value || (!dirty.value && !needsSync.value)) return
  saving.value = true
  saveError.value = ''
  saveResults.value = null
  try {
    const payload = servers.value.map(toPayload)
    const result = await api.saveMcp(revision.value, payload, pendingChanges.value)
    revision.value = result.revision
    baseline.value = payload
    saveResults.value = result.agents
    // 部分应用写入失败时保留原始编辑信息，防止删除操作失去重试范围。
    pendingChanges.value = pendingChanges.value.filter(change => !result.agents.find(agent => agent.key === change.agent)?.ok)
    // 仅重新读取状态，不能把读取失败误报为保存失败，也不能触发再次写入。
    try {
      const state = await api.getMcp()
      agentStatuses.value = state.agents
      detected.value = state.detected
    } catch (error) {
      loadError.value = `列表已保存，但未能读取应用最新状态：${error instanceof Error ? error.message : '读取失败'}。请刷新查看。`
    }
  } catch (error) {
    // 冲突时保留用户草稿，交由用户选择刷新或更名，不自动丢弃编辑内容。
    saveError.value = error instanceof ApiError && error.code === 'revision_conflict'
      ? '列表已被外部修改，当前草稿已保留。请记录修改后刷新，再重新应用。'
      : error instanceof Error ? error.message : '保存 MCP 列表失败'
  } finally {
    saving.value = false
  }
}

const setAgentEnabled = (index: number, key: McpAgentKey, enabled: boolean) => toggleServer(servers.value[index], key, enabled)

const openCreate = () => {
  importHint.value = ''
  editingSource.value = null
  editingIndex.value = null
  Object.assign(form, emptyForm())
  inputMode.value = 'command'
  commandText.value = ''
  commandParseError.value = ''
  suggestedCommandName.value = ''
  formError.value = ''
  dialogOpen.value = true
}

const openEdit = (index: number) => {
  editingSource.value = null
  importHint.value = servers.value[index].agentConfigs
    ? '已保留各应用原有启动方式。修改命令、参数或连接信息后，将按新配置统一分发。'
    : ''
  editingIndex.value = index
  Object.assign(form, formFromServer(servers.value[index]))
  inputMode.value = 'fields'
  formError.value = ''
  dialogOpen.value = true
}

// 切换传输方式时清空另一种传输的字段，避免残留的 command 或 url 被误提交。
const onTransportChange = (value: McpTransport) => {
  if (value === form.transport) return
  form.transport = value
  if (value === 'http') inputMode.value = 'fields'
  if (value === 'stdio') {
    form.url = ''
    form.headersText = ''
  } else {
    form.command = ''
    form.argsText = ''
    form.envText = ''
  }
}

const submitForm = () => {
  try {
    if (inputMode.value === 'command') {
      applyCommandText(commandText.value)
      if (commandParseError.value) throw new Error(commandParseError.value)
    }
    const server = buildServerFromForm()
    const previous = editingIndex.value === null ? editingSource.value?.server : servers.value[editingIndex.value]
    if (previous && configKey({ ...server, name: previous.name }) === configKey(previous)) {
      server.agentConfigs = toPayload(previous).agentConfigs
    }
    // 已管理条目没有变化时不接管新来源，避免仅确认表单就产生待保存操作。
    if (editingIndex.value !== null && previous
      && JSON.stringify(toPayload(server)) === JSON.stringify(toPayload(previous))) {
      dialogOpen.value = false
      return
    }
    if (previous) stageChanges(previous, server.name, editingSource.value?.sources)
    if (editingIndex.value === null) servers.value.push(server)
    else servers.value[editingIndex.value] = server
    dialogOpen.value = false
  } catch (error) {
    formError.value = error instanceof Error ? error.message : '表单校验失败'
  }
}

const confirmDelete = (index: number) => {
  deletingSource.value = null
  deletingIndex.value = index
  deleteDialog.value = true
}

const deleteServer = () => {
  try {
    const server = deletingServer.value
    if (!server) return
    stageChanges(server, '', deletingSource.value?.sources)
    if (deletingIndex.value !== null) servers.value.splice(deletingIndex.value, 1)
    deletingIndex.value = null
    deletingSource.value = null
    deleteDialog.value = false
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : '删除 MCP 草稿失败'
  }
}

onMounted(loadList)
</script>

<style scoped>
.mcp-page { max-width: 1440px; margin: 0 auto; }
.page-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.list-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 16px; }
.mcp-search { flex: 0 1 400px; min-width: 220px; }
.agent-strip { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); border-radius: 6px; padding: 12px 16px; }
.agent-grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); }
.agent-path { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mcp-list-card { border: 1px solid rgba(var(--v-theme-on-surface), 0.12); border-radius: 12px; overflow: hidden; }
.mcp-list-card :deep(thead) { background: rgba(var(--v-theme-on-surface), 0.04); }
.mcp-list-card :deep(tbody tr:nth-child(even)) { background: rgba(var(--v-theme-on-surface), 0.025); }
.mcp-list-card :deep(tbody tr:hover) { background: rgba(var(--v-theme-primary), 0.04); }
.server-column { width: 100%; }
.agent-column { min-width: 108px; white-space: nowrap; }
.server-cell { padding-top: 16px !important; padding-bottom: 16px !important; }
.mcp-target { display: block; max-width: min(660px, 48vw); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; line-height: 1.6; }
.command-preview { padding: 12px; border-radius: 6px; background: rgba(var(--v-theme-on-surface), 0.04); overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .page-heading { align-items: flex-start; flex-direction: column; }
  .mcp-search { flex-basis: 100%; }
  .agent-column { min-width: 90px; }
  .mcp-target { max-width: 260px; }
}
</style>
