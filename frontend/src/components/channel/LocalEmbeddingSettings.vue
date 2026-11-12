<template>
  <div class="local-embedding">
    <p class="text-body-2">仅选择本地向量匹配时需要安装。自动规则、LLM 或决策模型判断无需下载；使用 API 向量也无需安装本地助手。</p>
    <p class="text-body-2 text-medium-emphasis">当前提供手动安装，尚无经过兼容性实测的一键预设包。资源会下载到运行网关的机器。</p>
    <ol class="install-steps text-body-2"><li>从发布网站取得对应平台的 CPU 运行时 ZIP、GGUF 向量模型及 SHA-256。</li><li>展开手动安装，填写资源信息，点击“下载并校验”。</li><li>下载完成后点击“启动并预热”，状态就绪后再构建数据集索引。</li></ol>
    <div class="d-flex flex-wrap ga-3">
      <v-btn variant="text" size="small" href="https://github.com/ggml-org/llama.cpp/releases" target="_blank" rel="noopener noreferrer">获取 llama.cpp 运行时</v-btn>
      <v-btn variant="text" size="small" href="https://huggingface.co/models?library=gguf&amp;search=bge-small-zh-v1.5" target="_blank" rel="noopener noreferrer">查找 BGE small 中文模型</v-btn>
      <v-btn variant="text" size="small" href="https://huggingface.co/models?library=gguf&amp;search=multilingual-e5-small" target="_blank" rel="noopener noreferrer">查找 E5 small 多语言模型</v-btn>
    </div>
    <p class="text-caption text-medium-emphasis">以上按钮打开发布网站，不会直接安装。BGE small 中文通常使用 cls，E5 small 多语言通常使用 mean；具体以所选模型说明为准。</p>
    <p class="text-body-2">{{ status?.platform }} · {{ status?.ready ? '已预热，可调用' : status?.running ? '正在启动' : status?.installed ? '已安装，未运行' : '尚未安装' }}</p>
    <v-alert v-if="error || status?.error" type="error" variant="tonal">{{ error || status?.error }}</v-alert>
    <v-expansion-panels variant="accordion"><v-expansion-panel title="手动安装或更换模型（高级）"><v-expansion-panel-text>
    <div class="local-install-fields">
    <h4 class="text-subtitle-2">运行时资源</h4>
    <v-text-field v-model="manifest.runtimeUrl" label="CPU 运行时 ZIP 下载地址（HTTPS）" :disabled="busy" />
    <v-text-field v-model="manifest.runtimeSha256" label="运行时 SHA-256" :disabled="busy" />
    <v-text-field v-model="manifest.executable" label="ZIP 内 llama-server 相对路径" placeholder="llama-server.exe" :disabled="busy" />
    <h4 class="text-subtitle-2">向量模型资源</h4>
    <v-text-field v-model="manifest.modelUrl" label="小型 GGUF 向量模型下载地址（HTTPS）" :disabled="busy" />
    <v-text-field v-model="manifest.modelSha256" label="模型 SHA-256" :disabled="busy" />
    <v-text-field v-model="manifest.modelName" label="模型名称（与语义配置一致）" :disabled="busy" />
    <v-select v-model="manifest.pooling" :items="['cls', 'mean', 'last']" label="模型池化方式" :disabled="busy" />
    <p class="text-caption text-medium-emphasis">请使用文件直链和完整校验值，而不是网页地址。模型名称是本地调用标识，需与数据集构建配置一致。</p>
    <div><v-btn variant="tonal" :loading="action === 'install'" :disabled="busy || status?.running || !manifestComplete" @click="run('install')">下载并校验</v-btn></div>
    </div>
    </v-expansion-panel-text></v-expansion-panel></v-expansion-panels>
    <p class="text-caption text-medium-emphasis">本地助手使用 2 个 CPU 线程、1 个执行位和 2 个等待位。网关重启后需重新启动助手；实际内存和延迟取决于模型。</p>
    <div class="d-flex flex-wrap ga-2">
      <v-btn color="primary" :loading="action === 'start'" :disabled="busy || !status?.installed || status.running" @click="run('start')">启动并预热</v-btn>
      <v-btn variant="outlined" :loading="action === 'stop'" :disabled="busy || !status?.running" @click="run('stop')">停止</v-btn>
      <v-btn variant="text" :disabled="!!action" @click="refresh">刷新状态</v-btn>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { intentApi, intentErrorText, type LocalManifest, type LocalStatus } from '@/services/intent'

const status = ref<LocalStatus>()
const error = ref('')
const action = ref('')
const busy = computed(() => !!action.value || !!status.value?.busy)
const manifest = ref<LocalManifest>({ runtimeUrl: '', runtimeSha256: '', executable: '', modelUrl: '', modelSha256: '', modelName: '', pooling: 'cls' })
const manifestComplete = computed(() => Object.values(manifest.value).every(value => value.trim() !== ''))
async function refresh() {
  try {
    status.value = await intentApi.local()
    if (status.value.installed) manifest.value = { ...status.value.manifest }
    error.value = ''
  } catch (cause) { error.value = intentErrorText(cause) }
}
async function run(next: 'install' | 'start' | 'stop') {
  action.value = next
  error.value = ''
  try { status.value = next === 'install' ? await intentApi.install(manifest.value) : await intentApi.localAction(next) }
  catch (cause) { error.value = intentErrorText(cause) }
  finally { action.value = '' }
}
onMounted(refresh)
</script>

<style scoped>
.local-embedding { display: grid; gap: 20px; padding-top: 8px; }
.local-install-fields { display: grid; gap: 22px; padding-top: 8px; }
.install-steps { padding-left: 24px; display: grid; gap: 10px; }
</style>
