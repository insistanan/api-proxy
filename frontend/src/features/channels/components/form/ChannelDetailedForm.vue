<template>
<UiForm ref="formRef" @submit.prevent="handleSubmit">
  <UiAlert v-if="submitError" type="error" variant="tonal" class="mb-4" role="alert">{{ submitError }}</UiAlert>
  <UiAlert v-if="mappingTargetChannelsError" type="error" variant="tonal" class="mb-4" role="alert">{{ mappingTargetChannelsError }}</UiAlert>
  <div class="grid grid-cols-12 gap-4">
    <div class="min-w-0 col-span-12" >
      <div class="form-section-heading">
        <h3>连接信息</h3>
        <p>入口接收客户端请求；上游服务类型决定网关如何连接供应商。</p>
      </div>
    </div>
    <!-- 基本信息 -->
    <div class="min-w-0 col-span-12 md:col-span-6"  >
      <UiInput
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
    </div>

    <div class="min-w-0 col-span-12 md:col-span-6"  >
      <ImagesChannelProtocolFields
        v-if="channelType === 'images'"
        v-model:service-type="form.serviceType"
        v-model:responses-model="form.responsesModel"
        :model-options="targetModelOptions"
        :models-loading="fetchingModels"
        :models-error="fetchModelsError"
        @fetch-models="handleFetchModels('')"
      />
      <UiSelect
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
    </div>

    <!-- 基础URL -->
    <div class="min-w-0 col-span-12" >
      <div class="flex items-center justify-between flex-wrap gap-2 mb-2">
        <span class="text-sm font-medium">请求地址</span>
        <UiSwitch
          v-model="autoCompleteUrl"
          label="自动补全请求地址"
          color="primary"
          density="compact"
          inset
          hide-details
          class="grow-0"
        />
      </div>
      <UiTextarea
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
      <div class="text-xs text-muted-foreground mt-2">{{ form.useFullUrl ? '按填写的完整地址请求，不再追加版本号或接口路径。' : '填写上游基础地址，每行一个；网关自动补全对应接口路径。' }}</div>

    </div>

    <div class="min-w-0 col-span-12" >
      <ChannelApiKeysFields :editor="editor" />
    </div>

    <div class="min-w-0 col-span-12" >
      <div class="form-section-heading">
        <h3>模型与路由</h3>
        <p>{{ channelType === 'chat' ? '默认模型可覆盖客户端请求；留空时保留原模型。' : '将客户端请求的模型名称映射到上游模型；连接信息无需重复填写。' }}</p>
      </div>
    </div>

    <!-- 渠道状态（仅编辑时显示） -->
    <div v-if="isEditing" class="min-w-0 col-span-12" >
      <UiSelect
        v-model="form.status"
        label="渠道状态"
        :items="channelStatusOptions"
        variant="outlined"
        density="comfortable"
      />
    </div>

    <!-- 渠道分组 -->
    <div :class="supportsConversationFeatures || channelType === 'embeddings' ? 'md:col-span-6' : ''" class="min-w-0 col-span-12" >
      <UiSelect
        v-model="form.poolId"
        label="渠道分组"
        :items="poolOptions"
        :loading="poolsLoading"
        :error-messages="poolLoadError"
        variant="outlined"
        density="comfortable"
      />
    </div>

    <div v-if="supportsConversationFeatures" class="min-w-0 col-span-12 md:col-span-6">
      <UiSelect
        v-model="form.reasoningEffort"
        label="思考控制"
        :items="reasoningEffortItems"
        variant="outlined"
        density="comfortable"
        :hint="reasoningEffortHint"
        persistent-hint
      />
    </div>

    <div v-if="channelType === 'images'" class="min-w-0 col-span-12"  :data-form-error="!!imagesOutputError" tabindex="-1">
      <ImagesOutputFields
        v-model:output-format="form.imagesOutputFormat"
        v-model:frame-count="form.imagesFrameCount"
        v-model:frame-duration-ms="form.imagesFrameDurationMs"
        :service-type="form.serviceType"
      />
      <div v-if="imagesOutputError" class="text-destructive text-xs mt-2" role="alert">{{ imagesOutputError }}</div>
    </div>

    <!-- Embeddings 输出维度：覆盖请求体中的 dimensions，0 表示不覆盖 -->
    <div v-if="channelType === 'embeddings'" class="min-w-0 col-span-12 md:col-span-6"  >
      <UiInput
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
    </div>

    <!-- Chat 默认模型 -->
    <div v-if="channelType === 'chat' && form.serviceType" class="min-w-0 col-span-12" >
      <UiCard variant="outlined" rounded="lg">
        <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center justify-between p-4 pb-2">
          <div class="flex items-center gap-2">
            <UiIcon color="primary">mdi-format-list-bulleted</UiIcon>
            <span class="text-base font-bold">默认模型 (可选)</span>
          </div>
          <UiBadge size="small" color="secondary" variant="tonal"> 覆盖请求模型 </UiBadge>
        </div>

        <div class="p-4 pt-2">
          <!-- 设置默认模型后，Chat 请求会使用该模型；未设置时保留客户端传入的模型。
               上游模型列表的探测由父级编辑器负责。 -->
          <UiSelect
v-model="form.defaultModel" searchable
            creatable
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
          <div v-if="fetchModelsError" class="text-destructive text-xs mt-2">
            {{ fetchModelsError }}
          </div>
        </div>
      </UiCard>
    </div>

    <!-- 模型重定向配置 -->
    <div v-if="form.serviceType && channelType !== 'chat'" class="min-w-0 col-span-12" >
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
    </div>

    <div v-if="channelType === 'images'" class="min-w-0 col-span-12" >
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <UiIcon color="primary">mdi-lightning-bolt</UiIcon>
          <div>
            <div class="text-base font-medium">强制非流式</div>
            <div class="text-sm text-muted-foreground mt-1">上游一次性生成完整图片；客户端请求 SSE 时返回一条完成事件。</div>
          </div>
        </div>
        <UiSwitch
          :model-value="form.imagesForceNonStream || isNativeSpecialOutput"
          aria-label="图片生成使用非流式请求"
          :disabled="isNativeSpecialOutput"
          inset
          color="primary"
          hide-details
          @update:model-value="form.imagesForceNonStream = Boolean($event)"
        />
      </div>
    </div>

    <div v-if="channelType === 'images'" class="min-w-0 col-span-12" >
      <UiCard variant="outlined" rounded="lg">
        <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center justify-between p-4 pb-2">
          <div class="flex items-center gap-2">
            <UiIcon color="primary">mdi-code-json</UiIcon>
            <span class="text-base font-bold">自定义请求参数（仅 Images API）</span>
          </div>
          <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="addImagesExtraParam">
            添加
          </UiButton>
        </div>
        <div class="p-4 pt-2">
          <p class="text-sm text-muted-foreground mb-3">{{ form.serviceType === 'responses' ? '当前使用 Responses API：这些参数只会保存，不会写入上游请求；图片模型由上方模型重定向设置。' : '同名参数覆盖客户端传入的值；model、stream 由上方专用设置控制。值支持 JSON 类型，纯文本按字符串发送。' }}</p>
          <div v-if="form.imagesExtraParams.length" class="flex flex-col gap-3">
            <div
              v-for="(row, index) in form.imagesExtraParams"
              :key="index"
              class="flex items-center gap-2"
            >
              <UiInput
                v-model="row.key"
                label="参数名"
                placeholder="例如：watermark"
                variant="outlined"
                density="comfortable"
                hide-details
                class="flex-1-1"
                @update:model-value="imagesExtraParamsError = ''"
              />
              <UiInput
                v-model="row.value"
                label="参数值"
                placeholder="例如：false 或 &quot;false&quot;"
                variant="outlined"
                density="comfortable"
                hide-details
                class="flex-1-1"
                @update:model-value="imagesExtraParamsError = ''"
              />
              <UiButton
                size="small"
                color="error"
                icon
                variant="text"
                title="删除参数"
                @click="removeImagesExtraParam(index)"
              >
                <UiIcon size="small" color="error">mdi-delete</UiIcon>
              </UiButton>
            </div>
          </div>
          <div v-if="imagesExtraParamsError" class="text-destructive text-xs mt-2" data-form-error="true" tabindex="-1" role="alert">
            {{ imagesExtraParamsError }}
          </div>
        </div>
      </UiCard>
    </div>

    <div class="min-w-0 col-span-12" >
      <DisclosureSection v-model="detailedSections.connection" title="连接与并发" :description="connectionSummary" icon="mdi-web" data-form-section="connection">
        <div class="grid grid-cols-12 gap-4 pt-3">
          <!-- 并发按 URL、上游协议和 Key 共享最小上限，默认 5，FIFO 最多排队 3 个、等待 10 秒。 -->
          <div class="min-w-0 col-span-12 md:col-span-6"  >
            <UiInput
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
          </div>
          <div v-if="supportsClientIdentity" class="min-w-0 col-span-12 md:col-span-6"  >
            <UiSelect
              v-model="form.clientIdentityMode"
              label="客户端身份"
              :items="clientIdentityModeItems"
              :hint="clientIdentityHint"
              persistent-hint
              variant="outlined"
              density="comfortable"
            />
          </div>

          <div v-if="channel?.effectiveConcurrency?.length" class="min-w-0 col-span-12" >
            <details class="text-sm">
              <summary class="text-primary" style="cursor: pointer">已保存配置的生效并发</summary>
              <div class="text-xs text-muted-foreground mt-2">
                相同地址、上游协议和 Key 共用最小并发上限；保存修改后重新计算。
              </div>
              <div
                v-for="limit in channel.effectiveConcurrency"
                :key="`${limit.baseUrl}:${limit.protocol}:${limit.keyIndex}`"
                class="text-sm mt-1"
                style="overflow-wrap: anywhere"
              >
                {{ limit.baseUrl }} · {{ limit.protocol }} · Key {{ limit.keyIndex + 1 }}：{{ limit.maxConcurrency }} 并发
              </div>
            </details>
          </div>

          <!-- 出站代理策略 -->
          <div class="min-w-0 col-span-12 md:col-span-6"  >
            <UiSelect
              v-model="form.proxyMode"
              label="出站代理"
              :items="proxyModeOptions"
              prepend-inner-icon="mdi-web"
              variant="outlined"
              density="comfortable"
            />
          </div>

          <div v-if="form.proxyMode === 'custom'" class="min-w-0 col-span-12 md:col-span-6"  >
            <UiInput
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
          </div>

        </div>
      </DisclosureSection>
    </div>
    <div v-if="supportsConversationFeatures" class="min-w-0 col-span-12" >
      <DisclosureSection v-model="detailedSections.compatibility" title="图片理解与对话兼容" :description="compatibilitySummary" icon="mdi-image-search-outline" data-form-section="compatibility">
        <div class="grid grid-cols-12 gap-4 pt-3">
          <!-- 图片理解能力与图片理解层 -->
          <div class="min-w-0 col-span-12" >
            <UiCard variant="outlined" rounded="lg">
              <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center gap-2 p-4 pb-2">
                <UiIcon color="primary">mdi-image-search-outline</UiIcon>
                <span class="text-base font-bold">图片理解</span>
              </div>
              <div class="p-4 pt-2">
                <div class="flex items-center justify-between">
                  <div>
                    <div class="text-base font-medium">本渠道支持图片理解</div>
                    <div class="text-sm text-muted-foreground mt-1">图片直接发送给当前模型，也可供其他渠道调用。</div>
                  </div>
                  <UiSwitch v-model="form.visionCapable" aria-label="本渠道支持图片理解" inset color="primary" hide-details />
                </div>

                <hr class="border-0 border-t border-border my-4" />

                <div class="flex items-center justify-between">
                  <div>
                    <div class="text-base font-medium">指定图片理解渠道</div>
                    <div class="text-sm text-muted-foreground mt-1">默认使用公共池；指定后优先使用该渠道，再按公共池和当前分组回退。</div>
                  </div>
                  <UiSwitch v-model="form.visionLayerEnabled" aria-label="指定图片理解渠道" inset color="secondary" hide-details />
                </div>

                <UiSelect
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

                <UiInput
                  v-if="form.visionLayerEnabled"
                  v-model="form.visionLayerModel"
                  class="mt-4"
                  label="图片理解模型（可选）"
                  placeholder="留空则原样透传用户请求的模型名"
                  prepend-inner-icon="mdi-image-search-outline"
                  variant="outlined"
                  density="comfortable"
                />

              </div>
            </UiCard>
          </div>

          <!-- 对话与缓存兼容性 -->
          <div class="min-w-0 col-span-12" >
            <UiCard variant="outlined" rounded="lg">
              <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center gap-2 p-4 pb-2">
                <UiIcon color="primary">mdi-chat-outline</UiIcon>
                <span class="text-base font-bold">对话与缓存</span>
              </div>
              <div class="p-4 pt-2">
                <div class="flex items-center justify-between">
                  <div>
                    <div class="text-base font-medium">不参与对话</div>
                    <div class="text-sm text-muted-foreground mt-1">仅用作图片理解，退出常规对话调度。</div>
                  </div>
                  <UiSwitch v-model="form.excludeFromConversation" aria-label="不参与对话调度" inset color="primary" hide-details />
                </div>

                <template v-if="form.serviceType === 'openai' || form.serviceType === 'responses'">
                  <hr class="border-0 border-t border-border my-4" />
                  <div class="flex items-center justify-between">
                    <div>
                      <div class="text-base font-medium">不支持缓存键</div>
                      <div class="text-sm text-muted-foreground mt-1">移除 prompt_cache_key，用于拒绝此参数的上游。</div>
                    </div>
                    <UiSwitch v-model="form.disablePromptCacheKey" aria-label="上游不支持缓存键" inset color="secondary" hide-details />
                  </div>
                </template>
                <template v-if="channelType === 'messages' && form.serviceType === 'openai'">
                  <hr class="border-0 border-t border-border my-4" />
                  <div class="text-base font-medium mb-2">思考内容兼容</div>
                  <p class="text-sm text-muted-foreground mb-3">默认保留上游思考，正文保持原样。</p>
                  <UiSwitch
                    v-model="form.reasoningOutputMode"
                    true-value="hide"
                    false-value="preserve"
                    label="隐藏思考"
                    :hint="form.reasoningOutputMode === 'hide' ? '隐藏后客户端无法保存思考；缓存失效时，严格上游可能无法续接历史。' : undefined"
                    :persistent-hint="form.reasoningOutputMode === 'hide'"
                    inset
                    color="secondary"
                  />
                  <UiSwitch
                    v-model="form.parseEmbeddedReasoning"
                    label="解析正文中的思考标签"
                    hint="仅用于上游通过 <think> 或 <thinking> 标签返回思考的情况。"
                    persistent-hint
                    inset
                    color="secondary"
                  />
                </template>
              </div>
            </UiCard>
          </div>

        </div>
      </DisclosureSection>
    </div>
    <div class="min-w-0 col-span-12" >
      <DisclosureSection v-model="detailedSections.advanced" title="高级选项与备注" :description="advancedSummary" icon="mdi-tune" data-form-section="advanced">
        <div class="grid grid-cols-12 gap-4 pt-3">
          <!-- 官网/控制台（可选） -->
          <div class="min-w-0 col-span-12" >
            <UiInput
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
          </div>

          <!-- 描述 -->
          <div class="min-w-0 col-span-12" >
            <UiTextarea
              v-model="form.description"
              label="描述 (可选)"
              prepend-inner-icon="mdi-text"
              variant="outlined"
              density="comfortable"
              rows="3"
              no-resize
            />
          </div>

          <!-- 跳过 TLS 证书验证 -->
          <div class="min-w-0 col-span-12" >
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <UiIcon color="warning">mdi-shield-alert</UiIcon>
                <div>
                  <div class="text-base font-medium">跳过 TLS 证书验证</div>
                    <div class="text-sm text-muted-foreground mt-1">用于受信任的自签名服务，开启后不再校验上游证书。</div>
                </div>
              </div>
              <UiSwitch v-model="form.insecureSkipVerify" aria-label="跳过 TLS 证书验证" inset color="warning" hide-details />
            </div>
          </div>

          <!-- 低质量渠道标记 -->
          <div v-if="channelType !== 'images'" class="min-w-0 col-span-12" >
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <UiIcon color="info">mdi-speedometer-slow</UiIcon>
                <div>
                  <div class="text-base font-medium">低质量渠道</div>
                  <div class="text-sm text-muted-foreground mt-1">强制本地估算 Token，偏差超过 5% 时使用本地值。</div>
                </div>
              </div>
              <UiSwitch v-model="form.lowQuality" aria-label="使用本地 Token 估算" inset color="info" hide-details />
            </div>
          </div>

          <!-- 临时渠道标记 -->
          <div class="min-w-0 col-span-12" >
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <UiIcon color="warning">mdi-timer-sand</UiIcon>
                <div>
                  <div class="text-base font-medium">临时渠道</div>
                  <div class="text-sm text-muted-foreground mt-1">24 小时后自动弃用，再过 3 天由后台清理。</div>
                </div>
              </div>
              <UiSwitch v-model="form.temporary" aria-label="设为临时渠道" inset color="warning" hide-details />
            </div>
          </div>

          <!-- 注入 Dummy Thought Signature（仅 Gemini 渠道显示） -->
          <div v-if="channelType === 'gemini'" class="min-w-0 col-span-12" >
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <UiIcon color="secondary">mdi-signature</UiIcon>
                <div>
                  <div class="text-base font-medium">注入 Dummy Thought Signature</div>
                  <div class="text-sm text-muted-foreground mt-1">为工具调用补充占位签名，仅用于要求此字段的第三方 API。</div>
                </div>
              </div>
              <UiSwitch v-model="form.injectDummyThoughtSignature" aria-label="注入 Dummy Thought Signature" inset color="secondary" hide-details />
            </div>
          </div>

          <!-- 移除 Thought Signature（仅 Gemini 渠道显示） -->
          <div v-if="channelType === 'gemini'" class="min-w-0 col-span-12" >
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2">
                <UiIcon color="error">mdi-close-circle</UiIcon>
                <div>
                  <div class="text-base font-medium">移除 Thought Signature</div>
                  <div class="text-sm text-muted-foreground mt-1">移除工具调用的签名字段，用于不支持该字段的旧版 API。</div>
                </div>
              </div>
              <UiSwitch v-model="form.stripThoughtSignature" aria-label="移除 Thought Signature" inset color="error" hide-details />
            </div>
          </div>
        </div>
      </DisclosureSection>
    </div>
  </div>
</UiForm>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiForm from '@/shared/ui/form/Form.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed } from 'vue'
import DisclosureSection from '@/shared/components/layout/DisclosureSection.vue'
import ChannelApiKeysFields from '@/features/channels/components/form/ChannelApiKeysFields.vue'
import type { useChannelModal } from '@/features/channels/composables/useChannelModal'
import ModelMappingEditor from '@/features/channels/components/form/ModelMappingEditor.vue'
import ImagesChannelProtocolFields from '@/features/channels/components/form/ImagesChannelProtocolFields.vue'
import ImagesOutputFields from '@/features/channels/components/form/ImagesOutputFields.vue'
import { validateEmbeddingsDimensions } from '@/features/channels/composables/useChannelEditor'
import { isConversationProtocol } from '@/features/channels/services/channel-api'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
// 字段校验规则返回 true 或错误文案。
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
  mappingTargetChannelsError,
  targetModelOptionsByChannel,
  targetStatusByChannel,
  handleFetchModels,
  channelType,
  channel,
  supportsClientIdentity,
  clientIdentityModeItems,
  clientIdentityHint
} = props.editor

// 图片理解与对话缓存只属于对话类入口协议，Embeddings / Decisions 为纯透传。
const supportsConversationFeatures = computed(() =>
  isConversationProtocol(channelType.value)
)
const reasoningEffortItems = [
  { title: '跟随请求', value: 'inherit' },
  { title: '自动（模型默认）', value: 'auto' },
  { title: '关闭', value: 'none' },
  { title: 'low', value: 'low' },
  { title: 'high', value: 'high' },
  { title: 'xhigh', value: 'xhigh' },
  { title: 'max', value: 'max' }
]
const reasoningEffortHint = computed(() => {
  switch (form.reasoningEffort) {
    case 'inherit': return '按客户端请求决定是否思考。'
    case 'auto': return '不发送思考参数，由上游模型决定。'
    case 'none': return '向上游发送关闭参数；模型须支持关闭。'
    default:
      return form.serviceType === 'gemini'
        ? '覆盖请求等级。Gemini 2.5 的 xhigh 和 max 均为 16384 tokens。'
        : '覆盖请求等级；模型须支持所选等级。'
  }
})

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

<style scoped src="@/features/channels/styles/channel-modal.css"></style>
