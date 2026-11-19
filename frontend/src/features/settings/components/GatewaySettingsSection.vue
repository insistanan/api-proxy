<template>
  <section aria-labelledby="gateway-settings-title">
    <div class="section-heading">
      <h2 id="gateway-settings-title" class="text-h6 font-weight-bold">请求与日志</h2>
      <v-btn type="button" variant="outlined" color="error" prepend-icon="mdi-delete" :loading="logs.purging" @click="confirmClearLogs">
        清空日志
      </v-btn>
    </div>
    <LogStorageStatus />
    <v-alert v-if="purgeNotice" :type="purgeNotice.type" variant="tonal" density="compact" class="mb-4" role="status">
      {{ purgeNotice.message }}
    </v-alert>
    <v-form v-if="gateway" ref="gatewayFormRef" @submit.prevent="saveGatewaySettings">
      <v-select
        v-model="gateway.logCaptureMode"
        :items="[{ title: '受限正文', value: 'bounded' }, { title: '仅元数据', value: 'metadata' }]"
        label="采集档位" variant="outlined" density="comfortable"
        hint="仅元数据保留调用结果，不保存新请求的正文、头部或任务预览。" persistent-hint
      />
      <div v-if="gateway.logCaptureMode === 'bounded'" class="setting-row mt-4">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">记录请求正文</div>
          <div class="text-body-2 text-medium-emphasis">用于排查请求问题；关闭后不再记录新的请求正文。</div>
        </div>
        <v-switch v-model="gateway.enableRequestLogs" inset hide-details color="primary" aria-label="记录请求正文" />
      </div>
      <div v-if="gateway.logCaptureMode === 'bounded'" class="setting-row">
        <div class="setting-copy">
          <div class="text-body-1 font-weight-medium">记录响应正文</div>
          <div class="text-body-2 text-medium-emphasis">用于排查上游响应；关闭后不再记录新的响应正文。</div>
        </div>
        <v-switch v-model="gateway.enableResponseLogs" inset hide-details color="primary" aria-label="记录响应正文" />
      </div>
      <v-expansion-panels variant="accordion" class="mt-5">
        <v-expansion-panel title="日志保留与容量">
          <v-expansion-panel-text eager>
            <div class="d-flex flex-column ga-5">
              <v-text-field v-model.number="gateway.logBodyLimitKB" label="单条正文上限（KiB）" type="number" min="16" max="1024" :rules="[integerRangeRule(16, 1024)]" :disabled="gateway.logCaptureMode === 'metadata'" hint="超过上限的正文只保存省略说明。" persistent-hint />
              <v-row>
                <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logRuntimeRetentionDays" label="运行日志保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" /></v-col>
                <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logCallRetentionDays" label="调用摘要与尝试保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" /></v-col>
                <v-col cols="12" md="4"><v-text-field v-model.number="gateway.logBodyRetentionDays" label="正文与头部保留天数" type="number" min="1" :max="gateway.logCallRetentionDays" :rules="[integerRangeRule(1, gateway.logCallRetentionDays)]" /></v-col>
              </v-row>
              <p class="text-body-2 text-medium-emphasis">保留期包含今天。缩短后，下次自动清理会删除过期记录。</p>
              <v-text-field v-model.number="gateway.logBudgetMB" label="日志占用预算（MiB，0 为不限）" type="number" min="0" max="1048576" :rules="[logBudgetRule]" hint="超过预算时，新请求只保存元数据；占用降到预算的 90% 后恢复。" persistent-hint />
              <v-text-field v-model.number="gateway.metricsRetentionDays" label="指标保留天数" type="number" min="3" max="30" :rules="[integerRangeRule(3, 30)]" hint="指标独立清理。缩短后，下次清理会删除更早的指标。" persistent-hint />
            </div>
          </v-expansion-panel-text>
        </v-expansion-panel>
        <v-expansion-panel title="高级部署设置">
          <v-expansion-panel-text eager>
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
    <ConfirmDialog
      :model-value="!!pending"
      :title="pending?.title"
      :message="pending?.message ?? ''"
      :confirm-text="pending?.confirmText"
      @update:model-value="value => { if (!value) cancelConfirm() }"
      @confirm="acceptConfirm"
    />
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { useGatewaySettings } from '@/features/settings/composables/useGatewaySettings'
import type { SettingsNotice } from '@/features/settings/composables/types'
import { LogStorageStatus, useLogsStore } from '@/features/observability'
import ConfirmDialog from '@/shared/components/common/ConfirmDialog.vue'
import { useConfirm } from '@/shared/composables/useConfirm'

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

const logs = useLogsStore()
const purgeNotice = ref<SettingsNotice>(null)
const { pending, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()

function confirmClearLogs() {
  if (logs.purging) return
  askConfirm({
    title: '清空全部日志？',
    message: '将删除现有的运行日志、请求记录、请求与响应正文，无法恢复。以后产生的日志仍会按当前设置记录。',
    confirmText: '清空全部',
    action: async () => {
      purgeNotice.value = null
      try {
        const total = await logs.purgeLogs({ scope: 'all' })
        purgeNotice.value = { type: 'success', message: `已清理 ${total.toLocaleString()} 条日志记录。` }
      } catch (error) {
        purgeNotice.value = { type: 'error', message: error instanceof Error ? error.message : '清空日志失败，请查看存储状态。' }
      }
    }
  })
}
</script>

<style scoped src="@/features/settings/styles/settings.css"></style>
