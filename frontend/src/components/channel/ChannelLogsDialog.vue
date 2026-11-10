<template>
<v-dialog v-model="showLogsDialog" max-width="1280">
      <v-card class="channel-logs-dialog-card">
        <v-card-title class="channel-logs-title">
          <div class="channel-logs-title-main">
            <v-icon size="20" color="primary">mdi-text-box-search-outline</v-icon>
            <div class="channel-logs-title-text">
              <span>{{ logsChannel?.name || '渠道' }} 请求日志</span>
              <span class="channel-logs-subtitle">{{ channelLogs.length }} 条记录</span>
            </div>
          </div>
          <v-btn icon variant="text" size="small" @click="closeLogsDialog">
            <v-icon>mdi-close</v-icon>
          </v-btn>
        </v-card-title>
        <v-divider />
        <v-card-text class="channel-logs-dialog-body">
          <div v-if="isLoadingLogs" class="d-flex align-center justify-center py-8">
            <v-progress-circular indeterminate color="primary" />
          </div>
          <v-alert v-else-if="logsError" type="error" variant="tonal" density="compact">
            {{ logsError }}
          </v-alert>
          <div v-else>
            <div v-if="channelLogs.length > 0" class="log-trend-panel">
              <div class="log-trend-header">
                <div class="text-subtitle-2 font-weight-bold">调用用量趋势</div>
                <!-- 按分钟聚合，展示请求数、Token I/O 和缓存 R/W -->
              </div>
              <apexchart
                type="line"
                height="220"
                :options="logTrendChartOptions"
                :series="logTrendSeries"
              />
            </div>

            <div class="channel-logs-table-shell">
              <v-table density="compact" class="channel-logs-table">
                <colgroup>
                  <col class="log-col-time" />
                  <col class="log-col-status" />
                  <col class="log-col-model" />
                  <col class="log-col-token" />
                  <col class="log-col-cache" />
                  <col class="log-col-upstream" />
                  <col class="log-col-key" />
                  <col class="log-col-duration" />
                  <col class="log-col-error" />
                </colgroup>
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>状态</th>
                    <th>模型</th>
                    <th>Token I/O</th>
                    <th>缓存 R/W</th>
                    <th>上游</th>
                    <th>Key</th>
                    <th>耗时</th>
                    <th>失败明细</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="channelLogs.length === 0">
                    <td colspan="9" class="text-center text-medium-emphasis py-6">暂无请求日志</td>
                  </tr>
                  <tr v-for="log in channelLogs" :key="log.attemptId">
                    <td class="log-time-cell">{{ formatLogTime(log.timestamp) }}</td>
                    <td>
                      <v-chip size="x-small" :color="getLogStatusColor(log.status)" variant="tonal">
                        {{ formatLogStatus(log.status, log.statusCode) }}
                      </v-chip>
                    </td>
                    <td>
                      <div class="log-model" :title="log.model || '--'">{{ log.model || '--' }}</div>
                    </td>
                    <td class="log-number-cell">
                      {{ formatLogTokenPair(log.inputTokens, log.outputTokens) }}
                    </td>
                    <td class="log-number-cell">
                      <div>{{ formatLogTokenPair(log.cacheCreationTokens, log.cacheReadTokens) }}</div>
                      <div v-if="(log.cacheCreation5mTokens || 0) + (log.cacheCreation1hTokens || 0) > 0" class="log-cell-note">
                        5m {{ formatNumber(log.cacheCreation5mTokens) }} / 1h {{ formatNumber(log.cacheCreation1hTokens) }}
                      </div>
                    </td>
                    <td>
                      <div class="log-base-url" :title="log.baseUrl">{{ formatLogBaseUrl(log.baseUrl) }}</div>
                    </td>
                    <td class="log-key-cell">{{ log.keyMask }}</td>
                    <td class="log-duration-cell">{{ formatDurationMs(log.durationMs) }}</td>
                    <td>
                      <div v-if="log.errorType || log.errorMessage" class="log-error" :title="formatLogError(log)">
                        <div class="log-error-type">{{ log.errorType || 'error' }}</div>
                        <div class="log-error-message">{{ log.errorMessage || '--' }}</div>
                      </div>
                      <span v-else class="text-medium-emphasis">--</span>
                    </td>
                  </tr>
                </tbody>
              </v-table>
            </div>
          </div>
        </v-card-text>
      </v-card>
    </v-dialog>
</template>

<script setup lang="ts">
// 日志展示与图表挂载独立于渠道资源池布局。
import VueApexCharts from 'vue3-apexcharts'
import type { useChannelLogs } from '@/composables/useChannelLogs'

const apexchart = VueApexCharts
const props = defineProps<{ viewer: ReturnType<typeof useChannelLogs> }>()
const {
  showLogsDialog, logsChannel, channelLogs, isLoadingLogs, logsError,
  logTrendSeries, logTrendChartOptions, closeLogsDialog,
  formatLogTime, getLogStatusColor, formatLogStatus, formatLogBaseUrl,
  formatDurationMs, formatLogError, formatNumber, formatLogTokenPair
} = props.viewer
</script>

<style scoped src="../../styles/channel-logs.css"></style>
