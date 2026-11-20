<template>
  <AppDialog
    :model-value="showLogsDialog"
    :title="(logsChannel?.name || '渠道') + ' 请求日志'"
    :description="channelLogs.length ? `最近 ${channelLogs.length} 次尝试` : undefined"
    size="xl"
    @update:model-value="value => { if (!value) closeLogsDialog() }"
  >
    <div v-if="isLoadingLogs" class="d-flex align-center justify-center py-8">
      <v-progress-circular indeterminate color="primary" />
    </div>
    <v-alert v-else-if="logsError" type="error" variant="tonal" density="compact">
      {{ logsError }}
    </v-alert>
    <div v-else class="channel-log-content" :style="statusColors">
      <template v-if="channelLogs.length">
        <dl class="log-summary">
          <div><dt>成功</dt><dd>{{ formatNumber(logSummary.success) }}</dd></div>
          <div><dt>未成功</dt><dd :class="{ 'log-failure-value': logSummary.unfinished > 0 }">{{ formatNumber(logSummary.unfinished) }}</dd></div>
          <div><dt>重试</dt><dd>{{ formatNumber(logSummary.retried) }}</dd></div>
          <div><dt>P95 耗时</dt><dd>{{ formatDurationMs(logSummary.p95) }}</dd></div>
        </dl>
        <section class="log-trend-panel">
          <div class="log-trend-header">
            <div class="text-caption text-medium-emphasis">最近记录 · 按分钟聚合</div>
            <v-btn-toggle v-model="trendMode" mandatory density="compact" variant="tonal" color="primary" aria-label="趋势指标">
              <v-btn v-for="mode in trendModes" :key="mode.value" :value="mode.value" size="small">{{ mode.label }}</v-btn>
            </v-btn-toggle>
          </div>
          <apexchart type="bar" height="168" :options="logTrendChartOptions" :series="logTrendSeries" />
        </section>
      </template>

      <div class="log-records-header">
        <span class="text-subtitle-2">最近记录</span>
        <v-btn v-if="logsChannel" :to="allLogsRoute" variant="text" color="primary" size="small">筛选历史日志</v-btn>
      </div>

      <div class="channel-logs-table-shell">
        <v-table density="compact" class="channel-logs-table">
          <colgroup>
            <col class="log-col-time" />
            <col class="log-col-status" />
            <col class="log-col-model" />
            <col class="log-col-token" />
            <col class="log-col-cache" />
            <col class="log-col-duration" />
            <col class="log-col-detail" />
          </colgroup>
          <thead>
            <tr>
              <th>时间</th>
              <th>状态</th>
              <th>模型</th>
              <th class="log-number-heading">Token<span class="log-heading-note">输入 / 输出</span></th>
              <th class="log-number-heading">缓存 Token<span class="log-heading-note">读取 / 写入</span></th>
              <th class="log-number-heading">耗时</th>
              <th class="log-detail-heading">详情</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="channelLogs.length === 0">
              <td colspan="7"><EmptyState variant="table" icon="mdi-text-box-search-outline" title="暂无请求日志" description="该渠道目前没有可展示的最近记录。" /></td>
            </tr>
            <template v-for="log in channelLogs" :key="log.attemptId">
              <tr :class="{ 'log-row-expanded': expandedAttemptId === log.attemptId }">
                <td class="log-time-cell" :title="formatDateTime(log.timestamp)">{{ formatLogTime(log.timestamp) }}</td>
                <td>
                  <span class="log-status" :class="`log-status--${getLogStatusColor(log.status)}`">
                    {{ formatLogStatus(log.status, log.statusCode) }}
                  </span>
                </td>
                <td>
                  <div class="log-model" :title="log.model || '--'">{{ log.model || '--' }}</div>
                  <div class="log-cell-note">{{ log.stream ? '流式' : '非流式' }}<span v-if="log.retried"> · 重试</span></div>
                </td>
                <td class="log-number-cell">
                  <span class="log-token-pair"><span>{{ formatNumber(log.inputTokens) }}</span><span class="log-pair-divider">/</span><span>{{ formatNumber(log.outputTokens) }}</span></span>
                </td>
                <td class="log-number-cell">
                  <span class="log-token-pair"><span>{{ formatNumber(log.cacheReadTokens) }}</span><span class="log-pair-divider">/</span><span>{{ formatNumber(log.cacheCreationTokens) }}</span></span>
                </td>
                <td class="log-duration-cell">{{ formatDurationMs(log.durationMs) }}</td>
                <td class="log-detail-cell">
                  <v-btn
                    :icon="expandedAttemptId === log.attemptId ? 'mdi-chevron-up' : 'mdi-chevron-down'"
                    size="x-small" variant="text"
                    :title="log.errorType || log.errorMessage ? '查看错误与连接详情' : '查看连接详情'"
                    :aria-label="`${expandedAttemptId === log.attemptId ? '收起' : '展开'} ${formatLogTime(log.timestamp)} 的请求详情`"
                    :aria-expanded="expandedAttemptId === log.attemptId"
                    :aria-controls="`channel-log-${log.attemptId}`"
                    @click="expandedAttemptId = expandedAttemptId === log.attemptId ? null : log.attemptId"
                  />
                </td>
              </tr>
              <tr v-if="expandedAttemptId === log.attemptId" :id="`channel-log-${log.attemptId}`" class="log-detail-row">
                <td colspan="7">
                  <dl class="log-detail-grid">
                    <div class="log-detail-wide"><dt>上游地址</dt><dd>{{ log.baseUrl || '未记录' }}</dd></div>
                    <div><dt>密钥掩码</dt><dd>{{ log.keyMask || '未记录' }}</dd></div>
                    <div><dt>请求时间</dt><dd>{{ formatDateTime(log.timestamp) }}</dd></div>
                    <div><dt>请求 ID</dt><dd>{{ log.requestId || '未记录' }}</dd></div>
                    <div><dt>尝试 ID</dt><dd>{{ log.attemptId }}</dd></div>
                    <div v-if="(log.cacheCreation5mTokens || 0) + (log.cacheCreation1hTokens || 0) > 0">
                      <dt>缓存写入明细</dt><dd>5m {{ formatNumber(log.cacheCreation5mTokens) }} · 1h {{ formatNumber(log.cacheCreation1hTokens) }}</dd>
                    </div>
                    <div v-if="log.errorType || log.errorMessage" class="log-detail-wide log-error-detail">
                      <dt>错误信息</dt><dd>{{ formatLogError(log) }}</dd>
                    </div>
                  </dl>
                </td>
              </tr>
            </template>
          </tbody>
        </v-table>
      </div>
    </div>
  </AppDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CHART_STATUS_COLORS } from '@/shared/utils/chartPalette'
import { formatDateTime } from '@/shared/utils/datetime'
import { defineLazyChart } from '@/shared/utils/lazyChart'
import type { useChannelLogs } from '@/features/channels/composables/useChannelLogs'
import AppDialog from '@/shared/components/common/AppDialog.vue'
import EmptyState from '@/shared/components/common/EmptyState.vue'

const apexchart = defineLazyChart(() => import('vue3-apexcharts'))
const props = defineProps<{ viewer: ReturnType<typeof useChannelLogs>; channelType: string }>()
const {
  showLogsDialog, logsChannel, channelLogs, isLoadingLogs, logsError,
  trendMode, trendModes, logSummary, logTrendSeries, logTrendChartOptions, closeLogsDialog,
  formatLogTime, getLogStatusColor, formatLogStatus,
  formatDurationMs, formatLogError, formatNumber
} = props.viewer

const expandedAttemptId = ref<string | null>(null)
watch([showLogsDialog, logsChannel], () => { expandedAttemptId.value = null })
const statusColors = {
  '--log-success': CHART_STATUS_COLORS.success,
  '--log-failure': CHART_STATUS_COLORS.failure
}

const allLogsRoute = computed(() => ({
  name: 'request-logs',
  query: { type: logsChannel.value?.kind ?? props.channelType, channel: logsChannel.value?.name }
}))
</script>

<style scoped src="@/features/channels/styles/channel-logs.css"></style>
