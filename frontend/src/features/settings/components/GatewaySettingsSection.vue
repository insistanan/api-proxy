<template>
  <section aria-labelledby="gateway-settings-title">
    <div class="section-heading">
      <h2 id="gateway-settings-title" class="text-h6 font-weight-bold">请求与日志</h2>
    </div>
    <v-form v-if="gateway" ref="gatewayFormRef" @submit.prevent="saveGatewaySettings">
      <v-select
        v-model="gateway.logCaptureMode"
        :items="[{ title: '受限正文', value: 'bounded' }, { title: '仅元数据', value: 'metadata' }]"
        label="采集档位" variant="outlined" density="comfortable"
        hint="仅元数据不保存新的请求、响应正文、头部及任务文本预览；关闭正文开关也会保留调用结果和采集状态。" persistent-hint
      />
      <div class="setting-row">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">记录请求正文</div>
          <div class="text-body-2 text-medium-emphasis">用于排查请求问题；关闭后不再记录新的请求正文。</div>
        </div>
        <v-switch v-model="gateway.enableRequestLogs" inset hide-details color="primary" aria-label="记录请求正文" />
      </div>
      <div class="setting-row">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">记录响应正文</div>
          <div class="text-body-2 text-medium-emphasis">用于排查上游响应；关闭后不再记录新的响应正文。</div>
        </div>
        <v-switch v-model="gateway.enableResponseLogs" inset hide-details color="primary" aria-label="记录响应正文" />
      </div>
      <v-text-field v-model.number="gateway.logBodyLimitKB" label="单条正文上限（KiB）" type="number" min="16" max="1024" :rules="[integerRangeRule(16, 1024)]" :disabled="gateway.logCaptureMode === 'metadata'" variant="outlined" density="comfortable" class="mt-4" hint="超过上限保存省略说明；入口摘要也受此上限约束，媒体始终省略。" persistent-hint />
      <v-row class="mt-2">
        <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logRuntimeRetentionDays" label="运行日志保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" variant="outlined" density="comfortable" /></v-col>
        <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logCallRetentionDays" label="调用摘要与尝试保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" variant="outlined" density="comfortable" /></v-col>
        <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logBodyRetentionDays" label="正文与头部保留天数" type="number" min="1" :max="gateway.logCallRetentionDays" :rules="[integerRangeRule(1, gateway.logCallRetentionDays)]" variant="outlined" density="comfortable" /></v-col>
      </v-row>
      <p class="text-body-2 text-medium-emphasis mb-4">保留天数包含今天，按服务器本地日历日清理。正文过期后保留调用摘要和工具 ID 索引，索引随调用记录删除；指标保留期独立设置。</p>
      <v-text-field v-model.number="gateway.logBudgetMB" label="日志占用预算（MiB，0 为不限）" type="number" min="0" max="1048576" :rules="[logBudgetRule]" variant="outlined" density="comfortable" hint="按 SQLite 已占用页检查，超过预算后新请求仅采集元数据；降至 90% 后恢复。每分钟检查，属于软上限，不提前删除历史日志。" persistent-hint />
      <p class="text-body-2 text-medium-emphasis mt-4">采集策略对新请求生效，在途请求沿用原快照。缩短保留期会在后续自动清理时删除过期内容，无法恢复。</p>
      <v-text-field
        v-model.number="gateway.metricsRetentionDays"
        label="指标保留天数"
        type="number"
        min="3"
        max="30"
        :rules="[integerRangeRule(3, 30)]"
        hint="缩短保留期会在下一次清理时删除更早的指标记录。"
        persistent-hint
        variant="outlined"
        density="comfortable"
        class="mt-4"
      />
      <v-expansion-panels variant="accordion" class="mt-5">
        <v-expansion-panel title="高级部署设置">
          <v-expansion-panel-text>
            <v-row>
              <v-col v-for="field in gatewayNumberFields" :key="field.key" cols="12" md="6">
                <v-text-field
                  v-model.number="gateway[field.key]"
                  :label="field.label"
                  type="number"
                  :min="field.min"
                  :max="field.max"
                  :rules="[integerRangeRule(field.min, field.max)]"
                  variant="outlined"
                  density="comfortable"
                />
              </v-col>
            </v-row>
            <v-switch v-model="gateway.enableCORS" label="允许跨域访问" color="primary" inset />
            <v-text-field
              v-model="gateway.corsOrigin"
              label="允许的跨域来源"
              hint="填写 * 或完整来源，例如 https://example.com"
              :rules="[nonEmptyHeaderRule]"
              variant="outlined"
              density="comfortable"
            />
            <v-divider class="my-4" />
            <p class="text-body-2 text-medium-emphasis mb-3">以下两项在重启后生效。更换日志路径不会搬迁旧日志。</p>
            <v-switch v-model="gateway.metricsPersistenceEnabled" label="持久化指标" color="primary" inset />
            <v-text-field
              v-model="gateway.logDBPath"
              label="日志数据库路径"
              :rules="[nonEmptyHeaderRule]"
              variant="outlined"
              density="comfortable"
              spellcheck="false"
            />
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>
      <v-alert v-if="gatewayNotice" :type="gatewayNotice.type" variant="tonal" density="compact" class="mt-5">
        {{ gatewayNotice.message }}
      </v-alert>
      <div class="settings-actions">
        <v-btn type="submit" color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="!gatewayDirty">
          保存请求与日志设置
        </v-btn>
      </div>
    </v-form>
    <LogStorageStatus class="mt-5" />
  </section>
</template>

<script setup lang="ts">
import type { useGatewaySettings } from '@/features/settings/composables/useGatewaySettings'
import { LogStorageStatus } from '@/features/observability'

const props = defineProps<{ editor: ReturnType<typeof useGatewaySettings> }>()
const {
  gatewayFormRef,
  gateway,
  gatewayNotice,
  gatewayDirty,
  gatewayNumberFields,
  integerRangeRule,
  logBudgetRule,
  nonEmptyHeaderRule,
  saveGatewaySettings,
  saving
} = props.editor
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
