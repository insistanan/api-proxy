<template>
  <div class="pa-4" :class="error ? 'text-error' : 'text-medium-emphasis'" :role="error ? 'alert' : 'status'">
    <template v-if="error">图表加载失败，请刷新页面后重试。{{ error.message }}</template>
    <template v-else>正在加载图表…</template>
  </div>
</template>

<script setup lang="ts">
// 异步图表只传入加载错误，不把渠道参数和事件透传到提示节点。
defineOptions({ inheritAttrs: false })
defineProps<{ error?: Error }>()
</script>
