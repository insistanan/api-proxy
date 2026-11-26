<template>
  <AppDialog v-model="open" title="Skills 仓库同步" description="推送 Skill 资源与启用清单；MCP 配置、备注和翻译备份保留在本机。" size="lg" :busy="action !== null">
    <UiAlert v-if="error" type="error" class="mb-4">{{ error }}</UiAlert>
    <p v-if="loading" role="status" class="text-sm">正在读取仓库状态…</p>
    <template v-if="status">
      <div class="mb-4 flex flex-wrap items-center gap-2">
        <UiBadge :color="status.isGitRepo ? 'success' : 'warning'">{{ status.isGitRepo ? status.branch || 'Git 仓库' : '未初始化 Git' }}</UiBadge>
        <UiBadge v-if="status.dirty" color="warning">{{ status.changedFiles?.length }} 项未提交修改</UiBadge>
        <UiBadge v-if="status.ahead">领先 {{ status.ahead }} 提交</UiBadge>
        <UiBadge v-if="status.behind">落后 {{ status.behind }} 提交</UiBadge>
        <UiButton icon="mdi-folder-marker-outline" variant="text" size="small" aria-label="复制仓库路径" title="复制仓库路径" @click="copyText(status.skillsDir, '仓库路径已复制')" />
      </div>
      <p v-if="status.remoteUrl || status.repoUrl" class="mb-4 text-sm [overflow-wrap:anywhere]">远程仓库：{{ status.remoteUrl || status.repoUrl }}</p>
      <UiAlert v-if="!status.gitAvailable" type="error" class="mb-4">未检测到 Git，请先在网关所在机器安装 Git。</UiAlert>
      <UiAlert v-else-if="!status.isGitRepo || (!status.remoteUrl && !status.repoUrl)" type="info" class="mb-4">请先在设置 → 技能与 MCP 中配置仓库，并初始化或克隆。</UiAlert>
      <UiInput v-if="status.isGitRepo" v-model="commitMessage" label="提交说明（可选）" :disabled="action !== null" />
      <UiAlert v-if="status.exportError" type="error" class="mt-4">{{ status.exportError }}</UiAlert>
      <details v-else-if="status.exportRevision" class="mt-4 text-sm">
        <summary>将导出 {{ status.exportFiles?.length || 0 }} 个 Skill 资源及清单文件</summary>
        <p class="my-2 text-muted-foreground">MCP 配置、本机托管记录和翻译备份不在导出范围内。</p>
        <ul><li v-for="path in status.exportFiles" :key="path" class="break-all">{{ path }}</li></ul>
      </details>
    </template>
    <div class="mt-4 space-y-2 text-sm leading-6 text-muted-foreground">
      <p>远程领先且本地无修改：先「拉取并同步」，再推送。本地和远程都新增了提交：在仓库目录执行 <code>git fetch origin</code>，再 <code>git merge '@{upstream}'</code> 合并历史。</p>
      <p>有未提交修改时先在本地提交。合并产生冲突：逐个修改冲突文件，保留需要的内容，移除冲突标记；确认 JSON 和 SKILL.md 格式正确后，执行 <code>git add &lt;已解决的文件&gt;</code> 与 <code>git commit</code>，再回到这里推送。未建立上游时，用 <code>origin/&lt;分支名&gt;</code> 代替 <code>@{upstream}</code>。</p>
      <p>界面只做快进拉取和普通推送；分叉与冲突会明确报错，不会自动选择一方内容。领先/落后数量来自最近一次获取的远程引用，推送前会重新获取远程状态。</p>
    </div>
    <template #actions>
      <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" :disabled="action !== null" @click="loadStatus">刷新状态</UiButton>
      <UiButton variant="tonal" prepend-icon="mdi-cloud-download-outline" :loading="action === 'pull'" :disabled="!canSync || !!status?.dirty || action !== null" @click="runAction('pull')">拉取并同步</UiButton>
      <UiButton color="primary" prepend-icon="mdi-cloud-upload-outline" :loading="action === 'push'" :disabled="!canSync || !status?.exportRevision || action !== null" @click="runAction('push')">提交并推送</UiButton>
    </template>
  </AppDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api'
import type { ExtensionsGitStatus } from '@/types'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import { useClipboard } from '@/shared/composables/useClipboard'
import { useToast } from '@/shared/composables/useToast'

const open = defineModel<boolean>({ default: false })
const emit = defineEmits<{ updated: [] }>()
const status = ref<ExtensionsGitStatus | null>(null)
const loading = ref(false)
const error = ref('')
const action = ref<'pull' | 'push' | null>(null)
const commitMessage = ref('')
const { copyText } = useClipboard()
const { showToast } = useToast()
const canSync = computed(() => !loading.value && !!status.value?.gitAvailable && status.value.isGitRepo && !!(status.value.remoteUrl || status.value.repoUrl))
async function loadStatus() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  status.value = null
  try { status.value = await api.getExtensionsGitStatus() }
  catch (err) { error.value = err instanceof Error ? err.message : String(err) }
  finally { loading.value = false }
}
async function runAction(next: 'pull' | 'push') {
  if (action.value || !canSync.value) return
  action.value = next
  error.value = ''
  try {
    const response = next === 'pull' ? await api.pullExtensionsGit() : await api.pushExtensionsGit(commitMessage.value.trim(), status.value?.exportRevision || '')
    status.value = response.status
    const failures = [...(response.skillsReport?.errors || []), ...(response.mcpAgents || []).filter(item => !item.ok).map(item => `${item.name}：${item.error || '分发失败'}`)]
    showToast(`${response.message || '仓库操作完成'}${failures.length ? `\n${failures.join('\n')}` : ''}`, failures.length ? 'warning' : 'success')
    if (next === 'push') commitMessage.value = ''
    if (next === 'pull') emit('updated')
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    await loadStatus()
    error.value = `${message}${error.value ? `；状态刷新失败：${error.value}` : ''}`
    showToast(error.value, 'error')
  } finally { action.value = null }
}
watch(open, value => { if (value) void loadStatus() })
</script>
