<template>
  <section aria-labelledby="gateway-settings-title">
    <div class="section-heading">
      <h2 id="gateway-settings-title" class="text-h6 font-weight-bold">请求与日志</h2>
    </div>
    <v-form v-if="gateway" ref="gatewayFormRef" @submit.prevent="saveGatewaySettings">
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
  </section>
</template>

<script setup lang="ts">
import type { useGatewaySettings } from '@/composables/settings/useGatewaySettings'

const props = defineProps<{ editor: ReturnType<typeof useGatewaySettings> }>()
const {
  gatewayFormRef,
  gateway,
  gatewayNotice,
  gatewayDirty,
  gatewayNumberFields,
  integerRangeRule,
  nonEmptyHeaderRule,
  saveGatewaySettings,
  saving
} = props.editor
</script>

<style scoped src="../../styles/settings.css"></style>
