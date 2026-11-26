<template>
  <section aria-labelledby="gateway-settings-title">
    <div class="section-heading">
      <h2 id="gateway-settings-title" class="text-lg font-bold">请求与日志</h2>
      <UiButton type="button" variant="outlined" color="error" prepend-icon="mdi-delete" :loading="logs.purging" @click="confirmClearLogs">
        清空日志
      </UiButton>
    </div>
    <LogStorageStatus />
    <UiAlert v-if="logs.compactionError" type="warning" variant="tonal" class="mb-4">
      日志已删除，空间回收未完成：{{ logs.compactionError }}
      <UiButton variant="text" :loading="logs.purging" @click="logs.retryCompaction">仅重试压缩</UiButton>
    </UiAlert>
    <UiAlert v-if="purgeNotice" :type="purgeNotice.type" variant="tonal" density="compact" class="mb-4" role="status">
      {{ purgeNotice.message }}
    </UiAlert>
    <UiForm v-if="gateway" ref="gatewayFormRef" @submit.prevent="saveGatewaySettings">
      <UiSelect
        v-model="gateway.logCaptureMode"
        :items="[{ title: '受限正文', value: 'bounded' }, { title: '仅元数据', value: 'metadata' }]"
        label="采集档位" variant="outlined" density="comfortable"
        hint="仅元数据保留调用结果，不保存新请求的正文、头部或任务预览。" persistent-hint
      />
      <div v-if="gateway.logCaptureMode === 'bounded'" class="setting-row mt-4">
        <div class="setting-copy">
          <div class="text-base font-medium">记录请求正文</div>
          <div class="text-sm text-muted-foreground">用于排查请求问题；关闭后不再记录新的请求正文。</div>
        </div>
        <UiSwitch v-model="gateway.enableRequestLogs" inset hide-details color="primary" aria-label="记录请求正文" />
      </div>
      <div v-if="gateway.logCaptureMode === 'bounded'" class="setting-row">
        <div class="setting-copy">
          <div class="text-base font-medium">记录响应正文</div>
          <div class="text-sm text-muted-foreground">用于排查上游响应；关闭后不再记录新的响应正文。</div>
        </div>
        <UiSwitch v-model="gateway.enableResponseLogs" inset hide-details color="primary" aria-label="记录响应正文" />
      </div>
      <UiAccordion variant="accordion" class="mt-5">
        <UiAccordionItem title="日志保留与容量">
          <div class="px-4 pb-4" >
            <div class="flex flex-col gap-5">
              <UiInput v-model.number="gateway.logBodyLimitKB" label="单条正文上限（KiB）" type="number" min="16" max="1024" :rules="[integerRangeRule(16, 1024)]" :disabled="gateway.logCaptureMode === 'metadata'" hint="超过上限的正文只保存省略说明。" persistent-hint />
              <div class="grid grid-cols-12 gap-4">
                <div class="min-w-0 col-span-12 md:col-span-4"  ><UiInput v-model.number="gateway.logRuntimeRetentionDays" label="运行日志保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" /></div>
                <div class="min-w-0 col-span-12 md:col-span-4"  ><UiInput v-model.number="gateway.logCallRetentionDays" label="调用摘要与尝试保留天数" type="number" min="1" max="365" :rules="[integerRangeRule(1, 365)]" /></div>
                <div class="min-w-0 col-span-12 md:col-span-4"  ><UiInput v-model.number="gateway.logBodyRetentionDays" label="正文与头部保留天数" type="number" min="1" :max="gateway.logCallRetentionDays" :rules="[integerRangeRule(1, gateway.logCallRetentionDays)]" /></div>
              </div>
              <p class="text-sm text-muted-foreground">保留期包含今天。缩短后，下次自动清理会删除过期记录。</p>
              <UiInput v-model.number="gateway.logBudgetMB" label="日志占用预算（MiB，0 为不限）" type="number" min="0" max="1048576" :rules="[logBudgetRule]" hint="超过预算时，新请求只保存元数据；占用降到预算的 90% 后恢复。" persistent-hint />
              <UiInput v-model.number="gateway.metricsRetentionDays" label="指标保留天数" type="number" min="3" max="30" :rules="[integerRangeRule(3, 30)]" hint="指标独立清理。缩短后，下次清理会删除更早的指标。" persistent-hint />
            </div>
          </div>
        </UiAccordionItem>
        <UiAccordionItem title="高级部署设置">
          <div class="px-4 pb-4" >
            <div class="grid grid-cols-12 gap-4">
              <div v-for="field in gatewayNumberFields" :key="field.key" class="min-w-0 col-span-12 md:col-span-6"  >
                <UiInput
                  v-model.number="gateway[field.key]"
                  :label="field.label"
                  type="number"
                  :min="field.min"
                  :max="field.max"
                  :rules="[integerRangeRule(field.min, field.max)]"
                  variant="outlined"
                  density="comfortable"
                />
              </div>
            </div>
            <UiSwitch v-model="gateway.enableCORS" label="允许跨域访问" color="primary" inset />
            <UiInput
              v-model="gateway.corsOrigin"
              label="允许的跨域来源"
              hint="填写 * 或完整来源，例如 https://example.com"
              :rules="[nonEmptyHeaderRule]"
              variant="outlined"
              density="comfortable"
            />
            <hr class="border-0 border-t border-border my-4" />
            <p class="text-sm text-muted-foreground mb-3">以下两项在重启后生效。更换日志路径不会搬迁旧日志。</p>
            <UiSwitch v-model="gateway.metricsPersistenceEnabled" label="持久化指标" color="primary" inset />
            <UiInput
              v-model="gateway.logDBPath"
              label="日志数据库路径"
              :rules="[nonEmptyHeaderRule]"
              variant="outlined"
              density="comfortable"
              spellcheck="false"
            />
          </div>
        </UiAccordionItem>
      </UiAccordion>
      <UiAlert v-if="gatewayNotice" :type="gatewayNotice.type" variant="tonal" density="compact" class="mt-5">
        {{ gatewayNotice.message }}
      </UiAlert>
      <div class="settings-actions">
        <UiButton type="submit" color="primary" prepend-icon="mdi-content-save" :loading="saving" :disabled="!gatewayDirty">
          保存请求与日志设置
        </UiButton>
      </div>
    </UiForm>
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
import UiButton from '@/shared/ui/button/Button.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAccordion from '@/shared/ui/disclosure/Accordion.vue'
import UiAccordionItem from '@/shared/ui/disclosure/AccordionItem.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { ref } from 'vue'
import type { useGatewaySettings } from '@/features/settings/composables/useGatewaySettings'
import type { SettingsNotice } from '@/features/settings/composables/types'
import { LogStorageStatus, useLogsStore } from '@/features/observability'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
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
