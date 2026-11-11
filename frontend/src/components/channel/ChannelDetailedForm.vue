<template>
<v-form ref="formRef" @submit.prevent="handleSubmit">
  <v-row>
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
        label="服务类型 *"
        :items="serviceTypeOptions"
        prepend-inner-icon="mdi-cog"
        variant="outlined"
        density="comfortable"
        :rules="[rules.required]"
        required
        :error-messages="errors.serviceType"
      />
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

    <v-col v-if="channelType === 'images'" cols="12">
      <ImagesOutputFields
        v-model:output-format="form.imagesOutputFormat"
        v-model:frame-count="form.imagesFrameCount"
        v-model:frame-duration-ms="form.imagesFrameDurationMs"
        :service-type="form.serviceType"
      />
      <div v-if="imagesOutputError" class="text-error text-caption mt-2">{{ imagesOutputError }}</div>
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
      <!-- 自动补全已关闭，请自行填写完整请求地址；不会追加版本号或接口路径。 -->
      <!-- 固定高度的提示区域，防止布局跳动；有错误时不显示 -->

    </v-col>

    <!-- 并发按 URL、上游协议和 Key 共享最小上限，默认 5，FIFO 最多排队 3 个、等待 10 秒。 -->
    <v-col cols="12" md="6">
      <v-text-field
        v-model="form.maxConcurrency"
        label="最大并发 *"
        type="number"
        min="1"
        step="1"
        variant="outlined"
        density="comfortable"
        :rules="[(value: unknown) => Number.isSafeInteger(Number(value)) && Number(value) > 0 || '请输入正整数']"
      />
    </v-col>
    <v-col v-if="supportsClientIdentity" cols="12" md="6">
      <v-select
        v-model="form.clientIdentityMode"
        label="是否伪装"
        :items="[
          { title: '继承全局设置', value: 'inherit' },
          { title: '不伪装（普通 API）', value: 'api' },
          { title: '伪装（中转兼容模式）', value: 'compatibility' }
        ]"
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
            <!-- 当前输出格式自动使用非流式生成。客户端要 SSE 时返回一条 completed 事件。 -->
            <!-- 发给上游的请求一律使用 stream: false。客户端要 JSON 就返回完整 JSON；要 SSE 时把完整结果包成一条 completed 事件，不含 partial。 -->
          </div>
        </div>
        <v-switch
          :model-value="form.imagesForceNonStream || isNativeSpecialOutput"
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
          <!-- 以参数名和参数值追加到上游生图请求。同名字段以这里配置的值为准，覆盖客户端传入的值。 true、false、null、JSON 数字、对象和数组按对应类型发送，其余按字符串发送。要强制字符串，值请写成带引号的 JSON，例如 "false"。 不能使用 model 和 stream。model 由模型重定向写入；stream 由强制非流式配置覆盖，未开启时跟随客户端请求。 输出格式和 GIF 帧参数优先使用上方的输出设置；选择 GIF/SVG 时会清除不支持的预览和压缩参数。 当前是 Responses 渠道：这些参数只会保存，不会注入到 /v1/responses。图片模型仍由上方模型重定向写入 image_generation 工具。 -->
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
          <div v-if="imagesExtraParamsError" class="text-error text-caption mt-2">
            {{ imagesExtraParamsError }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>

    <!-- API密钥管理 -->
    <v-col cols="12">
      <v-card variant="outlined" rounded="lg" :color="form.apiKeys.length === 0 ? 'error' : undefined">
        <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
          <div class="d-flex align-center ga-2">
            <v-icon :color="form.apiKeys.length > 0 ? 'primary' : 'error'">mdi-key</v-icon>
            <span class="text-body-1 font-weight-bold">API密钥管理 *</span>
            <v-chip v-if="form.apiKeys.length === 0" size="x-small" color="error" variant="tonal">
              至少需要一个密钥
            </v-chip>
          </div>
          <v-chip size="small" color="info" variant="tonal"> 可添加多个密钥用于负载均衡 </v-chip>
        </v-card-title>

        <v-card-text class="pt-2">
          <!-- 现有密钥列表 -->
          <div v-if="form.apiKeys.length" class="mb-4">
            <v-list density="compact" class="bg-transparent">
              <v-list-item
                v-for="(key, index) in form.apiKeys"
                :key="index"
                class="mb-2"
                rounded="lg"
                variant="tonal"
                :color="duplicateKeyIndex === index ? 'error' : 'surface-variant'"
                :class="{ 'animate-pulse': duplicateKeyIndex === index }"
              >
                <template #prepend>
                  <v-icon size="small" :color="duplicateKeyIndex === index ? 'error' : 'primary'">
                    {{ duplicateKeyIndex === index ? 'mdi-alert' : 'mdi-key' }}
                  </v-icon>
                </template>

                <v-list-item-title>
                  <div class="d-flex align-center justify-space-between">
                    <code class="text-caption">{{ maskApiKey(key) }}</code>
                    <div class="d-flex align-center ga-1">
                      <!-- Models 状态标签 -->
                      <v-chip
                        v-if="keyModelsStatus.get(key)?.loading"
                        size="x-small"
                        color="info"
                        variant="tonal"
                      >
                        <v-icon start size="12">mdi-loading</v-icon>
                        检测中...
                      </v-chip>
                      <v-chip
                        v-else-if="keyModelsStatus.get(key)?.success"
                        size="x-small"
                        color="success"
                        variant="tonal"
                      >
                        models {{ keyModelsStatus.get(key)?.statusCode }} ({{ keyModelsStatus.get(key)?.modelCount }} 个)
                      </v-chip>
                      <v-tooltip
                        v-else-if="keyModelsStatus.get(key)?.error"
                        :text="keyModelsStatus.get(key)?.error"
                        location="top"
                        max-width="300"
                      >
                        <template #activator="{ props: tooltipProps }">
                          <v-chip
                            v-bind="tooltipProps"
                            size="x-small"
                            color="error"
                            variant="tonal"
                          >
                            models {{ keyModelsStatus.get(key)?.statusCode || 'ERR' }}
                          </v-chip>
                        </template>
                      </v-tooltip>
                      <!-- 重复密钥标签 -->
                      <v-chip v-if="duplicateKeyIndex === index" size="x-small" color="error" variant="text">
                        重复密钥
                      </v-chip>
                    </div>
                  </div>
                </v-list-item-title>

                <template #append>
                  <div class="d-flex align-center ga-1">
                    <!-- 置顶/置底：仅首尾密钥显示 -->
                    <v-tooltip
                      v-if="index === form.apiKeys.length - 1 && form.apiKeys.length > 1"
                      text="置顶"
                      location="top"
                      :open-delay="150"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          color="warning"
                          icon
                          variant="text"
                          rounded="md"
                          @click="moveApiKeyToTop(index)"
                        >
                          <v-icon size="small">mdi-arrow-up-bold</v-icon>
                        </v-btn>
                      </template>
                    </v-tooltip>
                    <v-tooltip
                      v-if="index === 0 && form.apiKeys.length > 1"
                      text="置底"
                      location="top"
                      :open-delay="150"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          color="warning"
                          icon
                          variant="text"
                          rounded="md"
                          @click="moveApiKeyToBottom(index)"
                        >
                          <v-icon size="small">mdi-arrow-down-bold</v-icon>
                        </v-btn>
                      </template>
                    </v-tooltip>
                    <v-tooltip
                      :text="copiedKeyIndex === index ? '已复制!' : '复制密钥'"
                      location="top"
                      :open-delay="150"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          :color="copiedKeyIndex === index ? 'success' : 'primary'"
                          icon
                          variant="text"
                          @click="copyApiKey(key, index)"
                        >
                          <v-icon size="small">{{
                            copiedKeyIndex === index ? 'mdi-check' : 'mdi-content-copy'
                          }}</v-icon>
                        </v-btn>
                      </template>
                    </v-tooltip>
                    <v-tooltip text="删除密钥" location="top" :open-delay="150" content-class="key-tooltip">
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          color="error"
                          icon
                          variant="text"
                          @click="removeApiKey(index)"
                        >
                          <v-icon size="small" color="error">mdi-close</v-icon>
                        </v-btn>
                      </template>
                    </v-tooltip>
                  </div>
                </template>
              </v-list-item>
            </v-list>
          </div>

          <!-- 添加新密钥 -->
          <div class="d-flex align-start ga-3">
            <v-text-field
              v-model="newApiKey"
              label="添加新的API密钥"
              placeholder="输入完整的API密钥"
              prepend-inner-icon="mdi-plus"
              variant="outlined"
              density="comfortable"
              type="password"
              :error="!!apiKeyError"
              :error-messages="apiKeyError"
              class="flex-grow-1"
              @keyup.enter="addApiKey"
              @input="handleApiKeyInput"
            />
            <v-btn
              color="primary"
              variant="elevated"
              size="large"
              height="40"
              :disabled="!newApiKey.trim()"
              class="mt-1"
              @click="addApiKey"
            >
              添加
            </v-btn>
          </div>
        </v-card-text>
      </v-card>
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
          </div>
        </div>
        <v-switch v-model="form.insecureSkipVerify" inset color="warning" hide-details />
      </div>
    </v-col>

    <!-- 低质量渠道标记 -->
    <v-col v-if="channelType !== 'images'" cols="12">
      <div class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="info">mdi-speedometer-slow</v-icon>
          <div>
            <div class="text-body-1 font-weight-medium">低质量渠道</div>
            <!-- 启用后强制本地估算 token 数量，偏差超过 5% 时使用本地值 -->
          </div>
        </div>
        <v-switch v-model="form.lowQuality" inset color="info" hide-details />
      </div>
    </v-col>

    <!-- 图片理解能力与图片理解层 -->
    <v-col v-if="supportsConversationFeatures" cols="12">
      <v-card variant="outlined" rounded="lg">
        <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
          <v-icon color="primary">mdi-image-search-outline</v-icon>
          <span class="text-body-1 font-weight-bold">图片理解</span>
        </v-card-title>
        <v-card-text class="pt-2">
          <div class="d-flex align-center justify-space-between">
            <div>
              <div class="text-body-1 font-weight-medium">本渠道支持图片理解</div>
              <!-- 启用后，图片会直接发送给当前模型；该渠道也可被其他渠道的图片理解层调用。 -->
            </div>
            <v-switch v-model="form.visionCapable" inset color="primary" hide-details />
          </div>

          <v-divider class="my-4" />

          <div class="d-flex align-center justify-space-between">
            <div>
              <div class="text-body-1 font-weight-medium">指定图片理解渠道</div>
              <!-- 默认自动使用公共图片理解池；启用后优先使用指定渠道，再按公共池和当前分组回退。 -->
            </div>
            <v-switch v-model="form.visionLayerEnabled" inset color="secondary" hide-details />
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
    <v-col v-if="supportsConversationFeatures" cols="12">
      <v-card variant="outlined" rounded="lg">
        <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
          <v-icon color="primary">mdi-chat-outline</v-icon>
          <span class="text-body-1 font-weight-bold">对话与缓存</span>
        </v-card-title>
        <v-card-text class="pt-2">
          <div class="d-flex align-center justify-space-between">
            <div>
              <div class="text-body-1 font-weight-medium">不参与对话</div>
              <!-- 启用后不参与常规对话调度，仍可作为其他渠道的图片理解模型。 -->
            </div>
            <v-switch v-model="form.excludeFromConversation" inset color="primary" hide-details />
          </div>

          <template v-if="form.serviceType === 'openai' || form.serviceType === 'responses'">
            <v-divider class="my-4" />
            <div class="d-flex align-center justify-space-between">
              <div>
                <div class="text-body-1 font-weight-medium">不支持缓存键</div>
                <!-- 移除 prompt_cache_key，避免严格校验的兼容渠道返回参数不支持错误。 -->
              </div>
              <v-switch v-model="form.disablePromptCacheKey" inset color="secondary" hide-details />
            </div>
          </template>
          <v-expansion-panels
            v-if="channelType === 'messages' && form.serviceType === 'openai'"
            v-model="reasoningCompatibilityPanel"
            variant="accordion"
            class="mt-4"
          >
            <v-expansion-panel
              value="reasoning"
              :title="form.reasoningOutputMode === 'hide' || form.parseEmbeddedReasoning ? '高级兼容设置（已启用）' : '高级兼容设置'"
            >
              <v-expansion-panel-text>
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
              </v-expansion-panel-text>
            </v-expansion-panel>
          </v-expansion-panels>
        </v-card-text>
      </v-card>
    </v-col>

    <!-- 临时渠道标记 -->
    <v-col cols="12">
      <div class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="warning">mdi-timer-sand</v-icon>
          <div>
            <div class="text-body-1 font-weight-medium">临时渠道</div>
            <!-- 启用后 24 小时自动移入弃用池，弃用超过 3 天后由后台清理 -->
          </div>
        </div>
        <v-switch v-model="form.temporary" inset color="warning" hide-details />
      </div>
    </v-col>

    <!-- 注入 Dummy Thought Signature（仅 Gemini 渠道显示） -->
    <v-col v-if="channelType === 'gemini'" cols="12">
      <div class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="secondary">mdi-signature</v-icon>
          <div>
            <div class="text-body-1 font-weight-medium">注入 Dummy Thought Signature</div>
            <!-- 为 functionCall 注入 dummy signature，兼容需要该字段的第三方 API（官方 API 请关闭） -->
          </div>
        </div>
        <v-switch v-model="form.injectDummyThoughtSignature" inset color="secondary" hide-details />
      </div>
    </v-col>

    <!-- 移除 Thought Signature（仅 Gemini 渠道显示） -->
    <v-col v-if="channelType === 'gemini'" cols="12">
      <div class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="error">mdi-close-circle</v-icon>
          <div>
            <div class="text-body-1 font-weight-medium">移除 Thought Signature</div>
            <!-- 移除 functionCall 的 thought_signature 字段，兼容不支持该字段的旧版 Gemini API -->
          </div>
        </div>
        <v-switch v-model="form.stripThoughtSignature" inset color="error" hide-details />
      </div>
    </v-col>
  </v-row>
</v-form>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { useChannelModal } from '@/composables/useChannelModal'
import ModelMappingEditor from '../ModelMappingEditor.vue'
import ImagesChannelProtocolFields from '../ImagesChannelProtocolFields.vue'
import ImagesOutputFields from '../ImagesOutputFields.vue'

const props = defineProps<{ editor: ReturnType<typeof useChannelModal> }>()
const {
  formRef,
  reasoningCompatibilityPanel,
  serviceTypeOptions,
  sourceModelOptions,
  targetModelPlaceholder,
  autoCompleteUrl,
  imagesExtraParamsError,
  imagesOutputError,
  isNativeSpecialOutput,
  addImagesExtraParam,
  removeImagesExtraParam,
  addApiKey,
  channelStatusOptions,
  proxyModeOptions,
  baseUrlsText,
  newApiKey,
  apiKeyError,
  duplicateKeyIndex,
  handleApiKeyInput,
  copiedKeyIndex,
  errors,
  rules,
  isEditing,
  visionChannelsLoading,
  poolsLoading,
  poolLoadError,
  poolOptions,
  visionChannelOptions,
  visionLayerChannelError,
  maskApiKey,
  removeApiKey,
  moveApiKeyToTop,
  moveApiKeyToBottom,
  copyApiKey,
  handleSubmit,
  form,
  targetModelOptions,
  fetchingModels,
  fetchModelsError,
  targetChannelOptions,
  targetModelOptionsByChannel,
  targetStatusByChannel,
  keyModelsStatus,
  handleFetchModels,
  channelType,
  channel
} = props.editor

// 是否伪装只在 Messages / Responses 渠道可配置。
const supportsClientIdentity = computed(() => channelType.value === 'messages' || channelType.value === 'responses')
// 图片理解与对话缓存只属于对话类入口协议，Embeddings / Decisions 为纯透传。
const supportsConversationFeatures = computed(() =>
  ['messages', 'responses', 'gemini', 'chat'].includes(channelType.value)
)
</script>

<style scoped src="../../styles/channel-modal.css"></style>
