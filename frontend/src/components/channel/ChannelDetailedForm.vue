<template>
<v-form ref="formRef" @submit.prevent="handleSubmit">
  <v-alert v-if="submitError" type="error" variant="tonal" class="mb-4" role="alert">{{ submitError }}</v-alert>
  <v-row>
    <v-col cols="12">
      <div class="form-section-heading">
        <h3>连接信息</h3>
        <p>入口接收客户端请求；上游服务类型决定网关如何连接供应商。</p>
      </div>
    </v-col>
    <!-- 基本信息 -->
    <v-col cols="12" md="6">
      <v-text-field
        v-model="form.name"
        label="渠道名称 *"
        placeholder="例如：GPT-4 渠道"
        prepend-inner-icon="mdi-tag"
        variant="outlined"
        density="comfortable"
        :rules="[rules.required]"
        required
        :error-messages="errors.name"
      />
    </v-col>

    <v-col cols="12" md="6">
      <ImagesChannelProtocolFields
        v-if="channelType === 'images'"
        v-model:service-type="form.serviceType"
        v-model:responses-model="form.responsesModel"
        :model-options="targetModelOptions"
        :models-loading="fetchingModels"
        :models-error="fetchModelsError"
        @fetch-models="handleFetchModels('')"
      />
      <v-select
        v-else
        v-model="form.serviceType"
        label="上游服务类型 *"
        :items="serviceTypeOptions"
        prepend-inner-icon="mdi-cog"
        variant="outlined"
        density="comfortable"
        :rules="[rules.required]"
        required
        :error-messages="errors.serviceType"
      />
    </v-col>

    <!-- 基础URL -->
    <v-col cols="12">
      <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-2">
        <span class="text-body-2 font-weight-medium">请求地址</span>
        <v-switch
          v-model="autoCompleteUrl"
          label="自动补全请求地址"
          color="primary"
          density="compact"
          inset
          hide-details
          class="flex-grow-0"
        />
      </div>
      <v-textarea
        v-model="baseUrlsText"
        :label="form.useFullUrl ? '完整请求地址 *' : '基础URL *'"
        :placeholder="form.useFullUrl ? '每行一个完整请求地址，包含接口路径和所需查询参数' : '每行一个 URL，支持多个 BaseURL\n例如：\nhttps://api.openai.com/v1\nhttps://api2.openai.com/v1'"
        prepend-inner-icon="mdi-web"
        variant="outlined"
        density="comfortable"
        rows="3"
        no-resize
        :rules="[rules.required, rules.baseUrls]"
        required
        :error-messages="errors.baseUrl"
        hide-details="auto"
      />
      <div class="text-caption text-medium-emphasis mt-2">{{ form.useFullUrl ? '按填写的完整地址请求，不再追加版本号或接口路径。' : '填写上游基础地址，每行一个；网关自动补全对应接口路径。' }}</div>

    </v-col>

    <v-col cols="12">
      <ChannelApiKeysFields :editor="editor" />
    </v-col>

    <v-col cols="12">
      <div class="form-section-heading">
        <h3>模型与路由</h3>
        <p>{{ channelType === 'chat' ? '默认模型可覆盖客户端请求；留空时保留原模型。' : '将客户端请求的模型名称映射到上游模型；连接信息无需重复填写。' }}</p>
      </div>
    </v-col>

    <!-- 渠道状态（仅编辑时显示） -->
    <v-col v-if="isEditing" cols="12" md="6">
      <v-select
        v-model="form.status"
        label="渠道状态"
        :items="channelStatusOptions"
        variant="outlined"
        density="comfortable"
      />
    </v-col>

    <!-- 渠道分组 -->
    <v-col cols="12" :md="isEditing ? 6 : 12">
      <v-select
        v-model="form.poolId"
        label="渠道分组"
        :items="poolOptions"
        :loading="poolsLoading"
        :error-messages="poolLoadError"
        variant="outlined"
        density="comfortable"
      />
    </v-col>

    <v-col v-if="channelType === 'images'" cols="12" :data-form-error="!!imagesOutputError" tabindex="-1">
      <ImagesOutputFields
        v-model:output-format="form.imagesOutputFormat"
        v-model:frame-count="form.imagesFrameCount"
        v-model:frame-duration-ms="form.imagesFrameDurationMs"
        :service-type="form.serviceType"
      />
      <div v-if="imagesOutputError" class="text-error text-caption mt-2" role="alert">{{ imagesOutputError }}</div>
    </v-col>

    <!-- Embeddings 输出维度：覆盖请求体中的 dimensions，0 表示不覆盖 -->
    <v-col v-if="channelType === 'embeddings'" cols="12" md="6">
      <v-text-field
        v-model="form.embeddingsDimensions"
        label="输出维度 *"
        type="number"
        min="0"
        step="1"
        hint="覆盖请求中的 dimensions，需与上游模型支持的维度一致；0 表示不覆盖"
        persistent-hint
        variant="outlined"
        density="comfortable"
        :rules="[embeddingsDimensionsRule]"
      />
    </v-col>

    <!-- Chat 默认模型 -->
    <v-col v-if="channelType === 'chat' && form.serviceType" cols="12">
      <v-card variant="outlined" rounded="lg">
        <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary">mdi-format-list-bulleted</v-icon>
            <span class="text-body-1 font-weight-bold">默认模型 (可选)</span>
          </div>
          <v-chip size="small" color="secondary" variant="tonal"> 覆盖请求模型 </v-chip>
        </v-card-title>

        <v-card-text class="pt-2">
          <!-- 设置默认模型后，Chat 请求会使用该模型；未设置时保留客户端传入的模型。
               上游模型列表的探测由父级编辑器负责。 -->
          <v-combobox
            v-model="form.defaultModel"
            label="默认模型"
            placeholder="例如：gpt-4.1"
            :items="targetModelOptions"
            item-title="title"
            item-value="value"
            :return-object="false"
            :loading="fetchingModels"
            variant="outlined"
            density="comfortable"
            clearable
            @focus="handleFetchModels('')"
          />
          <div v-if="fetchModelsError" class="text-error text-caption mt-2">
            {{ fetchModelsError }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>

    <!-- 模型重定向配置 -->
    <v-col v-if="form.serviceType && channelType !== 'chat'" cols="12">
      <ModelMappingEditor
        v-model="form.modelMapping"
        v-model:channel-value="form.modelMappingChannels"
        :source-options="sourceModelOptions"
        :target-options-by-channel="targetModelOptionsByChannel"
        :target-channel-options="targetChannelOptions"
        :target-status-by-channel="targetStatusByChannel"
        :target-placeholder="targetModelPlaceholder"
        :strict-source-selection="channelType === 'images'"
        :strict-target-selection="channelType === 'images'"
        :allow-wildcard="true"
        :allow-target-channel="channelType !== 'images'"
        @fetch-models="handleFetchModels"
      />
    </v-col>

    <v-col v-if="channelType === 'images'" cols="12">
      <div class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-lightning-bolt</v-icon>
          <div>
            <div class="text-body-1 font-weight-medium">强制非流式</div>
            <div class="text-body-2 text-medium-emphasis mt-1">上游一次性生成完整图片；客户端请求 SSE 时返回一条完成事件。</div>
          </div>
        </div>
        <v-switch
          :model-value="form.imagesForceNonStream || isNativeSpecialOutput"
          aria-label="图片生成使用非流式请求"
          :disabled="isNativeSpecialOutput"
          inset
          color="primary"
          hide-details
          @update:model-value="form.imagesForceNonStream = Boolean($event)"
        />
      </div>
    </v-col>

    <v-col v-if="channelType === 'images'" cols="12">
      <v-card variant="outlined" rounded="lg">
        <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary">mdi-code-json</v-icon>
            <span class="text-body-1 font-weight-bold">自定义请求参数（仅 Images API）</span>
          </div>
          <v-btn size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addImagesExtraParam">
            添加
          </v-btn>
        </v-card-title>
        <v-card-text class="pt-2">
          <p class="text-body-2 text-medium-emphasis mb-3">{{ form.serviceType === 'responses' ? '当前使用 Responses API：这些参数只会保存，不会写入上游请求；图片模型由上方模型重定向设置。' : '同名参数覆盖客户端传入的值；model、stream 由上方专用设置控制。值支持 JSON 类型，纯文本按字符串发送。' }}</p>
          <div v-if="form.imagesExtraParams.length" class="d-flex flex-column ga-3">
            <div
              v-for="(row, index) in form.imagesExtraParams"
              :key="index"
              class="d-flex align-center ga-2"
            >
              <v-text-field
                v-model="row.key"
                label="参数名"
                placeholder="例如：watermark"
                variant="outlined"
                density="comfortable"
                hide-details
                class="flex-1-1"
                @update:model-value="imagesExtraParamsError = ''"
              />
              <v-text-field
                v-model="row.value"
                label="参数值"
                placeholder="例如：false 或 &quot;false&quot;"
                variant="outlined"
                density="comfortable"
                hide-details
                class="flex-1-1"
                @update:model-value="imagesExtraParamsError = ''"
              />
              <v-btn
                size="small"
                color="error"
                icon
                variant="text"
                title="删除参数"
                @click="removeImagesExtraParam(index)"
              >
                <v-icon size="small" color="error">mdi-delete</v-icon>
              </v-btn>
            </div>
          </div>
          <div v-if="imagesExtraParamsError" class="text-error text-caption mt-2" data-form-error="true" tabindex="-1" role="alert">
            {{ imagesExtraParamsError }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>

    <v-col cols="12">
      <DisclosureSection v-model="detailedSections.connection" title="连接与并发" :description="connectionSummary" icon="mdi-web" data-form-section="connection">
        <v-row class="pt-3">
          <!-- 并发按 URL、上游协议和 Key 共享最小上限，默认 5，FIFO 最多排队 3 个、等待 10 秒。 -->
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.maxConcurrency"
              label="最大并发 *"
              type="number"
              min="1"
              step="1"
              hint="相同地址、上游协议和密钥共用最小并发上限。"
              persistent-hint
              variant="outlined"
              density="comfortable"
              :rules="[(value: unknown) => Number.isSafeInteger(Number(value)) && Number(value) > 0 || '请输入正整数']"
            />
          </v-col>
          <v-col v-if="supportsClientIdentity" cols="12" md="6">
            <v-select
              v-model="form.clientIdentityMode"
              label="客户端身份"
              :items="clientIdentityModeItems"
              :hint="clientIdentityHint"
              persistent-hint
              variant="outlined"
              density="comfortable"
            />
          </v-col>

          <v-col v-if="channel?.effectiveConcurrency?.length" cols="12">
            <details class="text-body-2">
              <summary class="text-primary" style="cursor: pointer">已保存配置的生效并发</summary>
              <div class="text-caption text-medium-emphasis mt-2">
                相同地址、上游协议和 Key 共用最小并发上限；保存修改后重新计算。
              </div>
              <div
                v-for="limit in channel.effectiveConcurrency"
                :key="`${limit.baseUrl}:${limit.protocol}:${limit.keyIndex}`"
                class="text-body-2 mt-1"
                style="overflow-wrap: anywhere"
              >
                {{ limit.baseUrl }} · {{ limit.protocol }} · Key {{ limit.keyIndex + 1 }}：{{ limit.maxConcurrency }} 并发
              </div>
            </details>
          </v-col>

          <!-- 出站代理策略 -->
          <v-col cols="12" md="6">
            <v-select
              v-model="form.proxyMode"
              label="出站代理"
              :items="proxyModeOptions"
              prepend-inner-icon="mdi-web"
              variant="outlined"
              density="comfortable"
            />
          </v-col>

          <v-col v-if="form.proxyMode === 'custom'" cols="12" md="6">
            <v-text-field
              v-model="form.proxyUrl"
              label="独立代理地址 *"
              placeholder="http://127.0.0.1:7897"
              prepend-inner-icon="mdi-web"
              variant="outlined"
              density="comfortable"
              autocomplete="off"
              spellcheck="false"
              :rules="[rules.proxyUrl]"
            />
          </v-col>

        </v-row>
      </DisclosureSection>
    </v-col>
    <v-col v-if="supportsConversationFeatures" cols="12">
      <DisclosureSection v-model="detailedSections.compatibility" title="图片理解与对话兼容" :description="compatibilitySummary" icon="mdi-image-search-outline" data-form-section="compatibility">
        <v-row class="pt-3">
          <!-- 图片理解能力与图片理解层 -->
          <v-col cols="12">
            <v-card variant="outlined" rounded="lg">
              <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
                <v-icon color="primary">mdi-image-search-outline</v-icon>
                <span class="text-body-1 font-weight-bold">图片理解</span>
              </v-card-title>
              <v-card-text class="pt-2">
                <div class="d-flex align-center justify-space-between">
                  <div>
                    <div class="text-body-1 font-weight-medium">本渠道支持图片理解</div>
                    <div class="text-body-2 text-medium-emphasis mt-1">图片直接发送给当前模型，也可供其他渠道调用。</div>
                  </div>
                  <v-switch v-model="form.visionCapable" aria-label="本渠道支持图片理解" inset color="primary" hide-details />
                </div>

                <v-divider class="my-4" />

                <div class="d-flex align-center justify-space-between">
                  <div>
                    <div class="text-body-1 font-weight-medium">指定图片理解渠道</div>
                    <div class="text-body-2 text-medium-emphasis mt-1">默认使用公共池；指定后优先使用该渠道，再按公共池和当前分组回退。</div>
                  </div>
                  <v-switch v-model="form.visionLayerEnabled" aria-label="指定图片理解渠道" inset color="secondary" hide-details />
                </div>

                <v-select
                  v-if="form.visionLayerEnabled"
                  v-model="form.visionLayerChannelId"
                  class="mt-4"
                  label="图片理解渠道 *"
                  :items="visionChannelOptions"
                  :loading="visionChannelsLoading"
                  :disabled="visionChannelOptions.length === 0"
                  placeholder="选择一个支持图片理解的渠道"
                  prepend-inner-icon="mdi-image-search-outline"
                  variant="outlined"
                  density="comfortable"
                  :error-messages="visionLayerChannelError"
                />

                <v-text-field
                  v-if="form.visionLayerEnabled"
                  v-model="form.visionLayerModel"
                  class="mt-4"
                  label="图片理解模型（可选）"
                  placeholder="留空则原样透传用户请求的模型名"
                  prepend-inner-icon="mdi-image-search-outline"
                  variant="outlined"
                  density="comfortable"
                />

              </v-card-text>
            </v-card>
          </v-col>

          <!-- 对话与缓存兼容性 -->
          <v-col cols="12">
            <v-card variant="outlined" rounded="lg">
              <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
                <v-icon color="primary">mdi-chat-outline</v-icon>
                <span class="text-body-1 font-weight-bold">对话与缓存</span>
              </v-card-title>
              <v-card-text class="pt-2">
                <div class="d-flex align-center justify-space-between">
                  <div>
                    <div class="text-body-1 font-weight-medium">不参与对话</div>
                    <div class="text-body-2 text-medium-emphasis mt-1">仅用作图片理解，退出常规对话调度。</div>
                  </div>
                  <v-switch v-model="form.excludeFromConversation" aria-label="不参与对话调度" inset color="primary" hide-details />
                </div>

                <template v-if="form.serviceType === 'openai' || form.serviceType === 'responses'">
                  <v-divider class="my-4" />
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-body-1 font-weight-medium">不支持缓存键</div>
                      <div class="text-body-2 text-medium-emphasis mt-1">移除 prompt_cache_key，用于拒绝此参数的上游。</div>
                    </div>
                    <v-switch v-model="form.disablePromptCacheKey" aria-label="上游不支持缓存键" inset color="secondary" hide-details />
                  </div>
                </template>
                <template v-if="channelType === 'messages' && form.serviceType === 'openai'">
                  <v-divider class="my-4" />
                  <div class="text-body-1 font-weight-medium mb-2">思考内容兼容</div>
                  <p class="text-body-2 text-medium-emphasis mb-3">默认保留上游思考，正文保持原样。</p>
                  <v-switch
                    v-model="form.reasoningOutputMode"
                    true-value="hide"
                    false-value="preserve"
                    label="隐藏思考"
                    :hint="form.reasoningOutputMode === 'hide' ? '隐藏后客户端无法保存思考；缓存失效时，严格上游可能无法续接历史。' : undefined"
                    :persistent-hint="form.reasoningOutputMode === 'hide'"
                    inset
                    color="secondary"
                  />
                  <v-switch
                    v-model="form.parseEmbeddedReasoning"
                    label="解析正文中的思考标签"
                    hint="仅用于上游通过 <think> 或 <thinking> 标签返回思考的情况。"
                    persistent-hint
                    inset
                    color="secondary"
                  />
                </template>
              </v-card-text>
            </v-card>
          </v-col>

        </v-row>
      </DisclosureSection>
    </v-col>
    <v-col cols="12">
      <DisclosureSection v-model="detailedSections.advanced" title="高级选项与备注" :description="advancedSummary" icon="mdi-tune" data-form-section="advanced">
        <v-row class="pt-3">
          <!-- 官网/控制台（可选） -->
          <v-col cols="12">
            <v-text-field
              v-model="form.website"
              label="官网/控制台 (可选)"
              placeholder="例如：https://platform.openai.com"
              prepend-inner-icon="mdi-open-in-new"
              variant="outlined"
              density="comfortable"
              type="url"
              :rules="[rules.urlOptional]"
              :error-messages="errors.website"
            />
          </v-col>

          <!-- 描述 -->
          <v-col cols="12">
            <v-textarea
              v-model="form.description"
              label="描述 (可选)"
              prepend-inner-icon="mdi-text"
              variant="outlined"
              density="comfortable"
              rows="3"
              no-resize
            />
          </v-col>

          <!-- 跳过 TLS 证书验证 -->
          <v-col cols="12">
            <div class="d-flex align-center justify-space-between">
              <div class="d-flex align-center ga-2">
                <v-icon color="warning">mdi-shield-alert</v-icon>
                <div>
                  <div class="text-body-1 font-weight-medium">跳过 TLS 证书验证</div>
                    <div class="text-body-2 text-medium-emphasis mt-1">用于受信任的自签名服务，开启后不再校验上游证书。</div>
                </div>
              </div>
              <v-switch v-model="form.insecureSkipVerify" aria-label="跳过 TLS 证书验证" inset color="warning" hide-details />
            </div>
          </v-col>

          <!-- 低质量渠道标记 -->
          <v-col v-if="channelType !== 'images'" cols="12">
            <div class="d-flex align-center justify-space-between">
              <div class="d-flex align-center ga-2">
                <v-icon color="info">mdi-speedometer-slow</v-icon>
                <div>
                  <div class="text-body-1 font-weight-medium">低质量渠道</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">强制本地估算 Token，偏差超过 5% 时使用本地值。</div>
                </div>
              </div>
              <v-switch v-model="form.lowQuality" aria-label="使用本地 Token 估算" inset color="info" hide-details />
            </div>
          </v-col>

          <!-- 临时渠道标记 -->
          <v-col cols="12">
            <div class="d-flex align-center justify-space-between">
              <div class="d-flex align-center ga-2">
                <v-icon color="warning">mdi-timer-sand</v-icon>
                <div>
                  <div class="text-body-1 font-weight-medium">临时渠道</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">24 小时后自动弃用，再过 3 天由后台清理。</div>
                </div>
              </div>
              <v-switch v-model="form.temporary" aria-label="设为临时渠道" inset color="warning" hide-details />
            </div>
          </v-col>

          <!-- 注入 Dummy Thought Signature（仅 Gemini 渠道显示） -->
          <v-col v-if="channelType === 'gemini'" cols="12">
            <div class="d-flex align-center justify-space-between">
              <div class="d-flex align-center ga-2">
                <v-icon color="secondary">mdi-signature</v-icon>
                <div>
                  <div class="text-body-1 font-weight-medium">注入 Dummy Thought Signature</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">为工具调用补充占位签名，仅用于要求此字段的第三方 API。</div>
                </div>
              </div>
              <v-switch v-model="form.injectDummyThoughtSignature" aria-label="注入 Dummy Thought Signature" inset color="secondary" hide-details />
            </div>
          </v-col>

          <!-- 移除 Thought Signature（仅 Gemini 渠道显示） -->
          <v-col v-if="channelType === 'gemini'" cols="12">
            <div class="d-flex align-center justify-space-between">
              <div class="d-flex align-center ga-2">
                <v-icon color="error">mdi-close-circle</v-icon>
                <div>
                  <div class="text-body-1 font-weight-medium">移除 Thought Signature</div>
                  <div class="text-body-2 text-medium-emphasis mt-1">移除工具调用的签名字段，用于不支持该字段的旧版 API。</div>
                </div>
              </div>
              <v-switch v-model="form.stripThoughtSignature" aria-label="移除 Thought Signature" inset color="error" hide-details />
            </div>
          </v-col>
        </v-row>
      </DisclosureSection>
    </v-col>
  </v-row>
</v-form>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DisclosureSection from '../common/DisclosureSection.vue'
import ChannelApiKeysFields from './ChannelApiKeysFields.vue'
import type { useChannelModal } from '@/composables/useChannelModal'
import ModelMappingEditor from '../ModelMappingEditor.vue'
import ImagesChannelProtocolFields from '../ImagesChannelProtocolFields.vue'
import ImagesOutputFields from '../ImagesOutputFields.vue'
import { supportsChannelClientIdentity, validateEmbeddingsDimensions } from '@/composables/useChannelEditor'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
// Vuetify 规则要求返回 true 或错误文案
const embeddingsDimensionsRule = (value: unknown) => validateEmbeddingsDimensions(value) || true
const {
  formRef,
  detailedSections,
  submitError,
  serviceTypeOptions,
  sourceModelOptions,
  targetModelPlaceholder,
  autoCompleteUrl,
  imagesExtraParamsError,
  imagesOutputError,
  isNativeSpecialOutput,
  addImagesExtraParam,
  removeImagesExtraParam,
  channelStatusOptions,
  proxyModeOptions,
  baseUrlsText,
  errors,
  rules,
  isEditing,
  visionChannelsLoading,
  poolsLoading,
  poolLoadError,
  poolOptions,
  visionChannelOptions,
  visionLayerChannelError,
  handleSubmit,
  form,
  targetModelOptions,
  fetchingModels,
  fetchModelsError,
  targetChannelOptions,
  targetModelOptionsByChannel,
  targetStatusByChannel,
  handleFetchModels,
  channelType,
  channel
} = props.editor

// 伪装配置只在会真正注入身份的组合中显示：Messages→Claude/Responses，Responses→Responses。
const supportsClientIdentity = computed(() => supportsChannelClientIdentity(channelType.value, form.serviceType))
// 继承全局时实际跟随哪个全局开关由上游协议决定，直接写在下拉项里避免误判。
const clientIdentityFollowsClaude = computed(() => form.serviceType === 'claude')
const clientIdentityModeItems = computed(() => [
  { title: `继承全局设置（跟随 ${clientIdentityFollowsClaude.value ? 'Claude' : 'Codex'} 伪装开关）`, value: 'inherit' },
  { title: '不伪装（普通 API）', value: 'api' },
  { title: '伪装（中转兼容模式）', value: 'compatibility' }
])
const clientIdentityHint = computed(() =>
  clientIdentityFollowsClaude.value
    ? '继承时跟随 Messages 页顶部的 Claude 伪装开关。'
    : '继承时跟随 Responses 页顶部的 Codex 伪装开关。'
)
// 图片理解与对话缓存只属于对话类入口协议，Embeddings / Decisions 为纯透传。
const supportsConversationFeatures = computed(() =>
  ['messages', 'responses', 'gemini', 'chat'].includes(channelType.value)
)

// 摘要读取当前草稿，让收起的分区仍能表达已选择的策略，不额外维护状态。
const connectionSummary = computed(() => {
  const proxy = proxyModeOptions.find(option => option.value === form.proxyMode)?.title
  const identity = supportsClientIdentity.value
    ? clientIdentityModeItems.value.find(option => option.value === form.clientIdentityMode)?.title
    : undefined
  return [`并发上限 ${form.maxConcurrency}`, proxy ? `代理：${proxy}` : undefined, identity].filter(Boolean).join(' · ')
})
const compatibilitySummary = computed(() => {
  const items = [form.excludeFromConversation ? '仅用于图片理解' : '参与对话调度']
  if (form.visionCapable) items.push('支持图片理解')
  if (form.visionLayerEnabled) items.push('指定图片理解渠道')
  if (['openai', 'responses'].includes(form.serviceType) && form.disablePromptCacheKey) items.push('移除缓存键')
  if (channelType.value === 'messages' && form.serviceType === 'openai') {
    if (form.reasoningOutputMode === 'hide') items.push('隐藏思考')
    if (form.parseEmbeddedReasoning) items.push('解析思考标签')
  }
  return items.join(' · ')
})
const advancedSummary = computed(() => {
  const items: string[] = []
  if (form.insecureSkipVerify) items.push('跳过证书验证')
  if (channelType.value !== 'images' && form.lowQuality) items.push('本地估算 Token')
  if (form.temporary) items.push('临时渠道')
  if (channelType.value === 'gemini') {
    if (form.injectDummyThoughtSignature) items.push('注入占位签名')
    if (form.stripThoughtSignature) items.push('移除签名')
  }
  if (form.website.trim()) items.push('已填官网')
  if (form.description.trim()) items.push('已填备注')
  return items.length ? items.join(' · ') : '未开启高级选项；可填写官网和备注。'
})
</script>

<style scoped src="../../styles/channel-modal.css"></style>
