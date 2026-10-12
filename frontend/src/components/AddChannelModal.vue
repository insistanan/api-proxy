<template>
  <v-dialog :model-value="show" max-width="800" persistent @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="dialog-card-shell">
      <v-card-title class="d-flex align-center ga-3 pa-6" :class="headerClasses">
        <v-avatar :color="avatarColor" variant="flat" size="40">
          <v-icon :style="headerIconStyle" size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
        </v-avatar>
        <div class="flex-grow-1">
          <div class="text-h5 font-weight-bold">
            {{ isEditing ? '编辑渠道' : '添加新渠道' }}
          </div>
          <div class="text-body-2" :class="subtitleClasses">
            {{ isEditing ? '修改渠道配置信息' : isQuickMode ? '快速批量添加 API 密钥' : '配置API渠道信息和密钥' }}
          </div>
        </div>
        <div class="d-flex align-center ga-2 header-actions">
          <!-- 模式切换按钮（仅在添加模式显示） -->
          <v-btn v-if="!isEditing" variant="outlined" size="small" class="mode-toggle-btn" @click="toggleMode">
            <v-icon start size="16">{{ isQuickMode ? 'mdi-form-textbox' : 'mdi-lightning-bolt' }}</v-icon>
            {{ isQuickMode ? '详细配置' : '快速添加' }}
          </v-btn>
          <v-btn
            icon
            size="small"
            variant="text"
            class="header-action-btn"
            title="关闭"
            @click="handleCancel"
          >
            <v-icon size="18">mdi-close</v-icon>
          </v-btn>
        </div>
      </v-card-title>

      <v-card-text class="pa-6 dialog-card-body">
        <!-- 快速添加模式 -->
        <div v-if="!isEditing && isQuickMode">
          <v-textarea
            v-model="quickInput"
            label="输入内容"
            placeholder="每行输入一个 API Key、Base URL 或渠道名称&#10;&#10;示例:&#10;name: 备用 OpenAI 渠道&#10;sk-xxx-your-api-key&#10;https://api.example.com/v1"
            variant="outlined"
            rows="10"
            no-resize
            autofocus
            class="quick-input-textarea"
            @input="parseQuickInput"
          />

          <v-text-field
            v-model="quickChannelName"
            label="渠道名称 (可选)"
            placeholder="不填则从输入内容识别或自动生成"
            prepend-inner-icon="mdi-tag"
            variant="outlined"
            density="comfortable"
            class="mt-3"
          />

          <!-- 检测状态提示 -->
          <v-card variant="outlined" class="mt-4 detection-status-card" rounded="lg">
            <v-card-text class="pa-4">
              <div class="d-flex flex-column ga-3">
                <!-- Base URL 检测 -->
                <div class="d-flex align-start ga-3">
                  <v-icon :color="detectedBaseUrls.length > 0 ? 'success' : 'error'" size="20" class="mt-1">
                    {{ detectedBaseUrls.length > 0 ? 'mdi-check-circle' : 'mdi-alert-circle' }}
                  </v-icon>
                  <div class="flex-grow-1">
                    <div class="text-body-2 font-weight-medium">Base URL</div>
                    <div v-if="detectedBaseUrls.length === 0" class="text-caption text-error">
                      请输入一个有效的 URL (https://...)
                    </div>
                    <div v-else class="d-flex flex-column ga-2 mt-1">
                      <div v-for="url in detectedBaseUrls" :key="url" class="base-url-item">
                        <div class="text-caption text-success">{{ url }}</div>
                        <div class="text-caption text-medium-emphasis">预期请求: {{ getExpectedRequestUrl(url) }}</div>
                      </div>
                    </div>
                  </div>
                  <v-chip v-if="detectedBaseUrls.length > 0" size="x-small" color="success" variant="tonal">
                    {{ detectedBaseUrls.length }} 个
                  </v-chip>
                </div>

                <!-- API Keys 检测 -->
                <div class="d-flex align-center ga-3">
                  <v-icon :color="detectedApiKeys.length > 0 ? 'success' : 'error'" size="20">
                    {{ detectedApiKeys.length > 0 ? 'mdi-check-circle' : 'mdi-alert-circle' }}
                  </v-icon>
                  <div class="flex-grow-1">
                    <div class="text-body-2 font-weight-medium">API 密钥</div>
                    <div class="text-caption" :class="detectedApiKeys.length > 0 ? 'text-success' : 'text-error'">
                      {{
                        detectedApiKeys.length > 0
                          ? `已检测到 ${detectedApiKeys.length} 个密钥`
                          : '请至少输入一个 API Key'
                      }}
                    </div>
                  </div>
                  <v-chip v-if="detectedApiKeys.length > 0" size="x-small" color="success" variant="tonal">
                    {{ detectedApiKeys.length }} 个
                  </v-chip>
                </div>

                <!-- 渠道名称预览 -->
                <div class="d-flex align-center ga-3">
                  <v-icon color="primary" size="20">mdi-tag</v-icon>
                  <div class="flex-grow-1">
                    <div class="text-body-2 font-weight-medium">渠道名称</div>
                    <div class="text-caption text-primary font-weight-medium">
                      {{ generatedChannelName }}
                    </div>
                    <div v-if="detectedChannelName && !quickChannelName.trim()" class="text-caption text-medium-emphasis">
                      已从输入内容识别：{{ detectedChannelName }}
                    </div>
                  </div>
                  <v-chip size="x-small" color="primary" variant="tonal">
                    {{ quickChannelName.trim() || detectedChannelName ? '自定义' : '自动生成' }}
                  </v-chip>
                </div>

                <!-- 渠道类型提示 -->
                <div class="d-flex align-center ga-3">
                  <v-icon color="info" size="20">mdi-information</v-icon>
                  <div class="flex-grow-1">
                    <div class="text-body-2 font-weight-medium">渠道类型</div>
                    <div class="text-caption text-medium-emphasis">
                      {{ props.channelType === 'gemini' ? 'Gemini' : props.channelType === 'responses' ? 'Responses' : props.channelType === 'chat' ? 'Chat' : props.channelType === 'images' ? 'Images' : 'Messages' }} -
                      {{ getDefaultServiceType() }}
                    </div>
                  </div>
                </div>
              </div>
            </v-card-text>
          </v-card>
        </div>

        <!-- 详细表单模式（原有表单） -->
        <v-form v-else ref="formRef" @submit.prevent="handleSubmit">
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
              <v-select
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
                hint="该渠道在所选分组内参与故障转移；兜底分组仅在未匹配或命中分组不可用时使用"
                persistent-hint
                variant="outlined"
                density="comfortable"
              />
            </v-col>

            <!-- 基础URL -->
            <v-col cols="12">
              <v-textarea
                v-model="baseUrlsText"
                label="基础URL *"
                placeholder="每行一个 URL，支持多个 BaseURL&#10;例如：&#10;https://api.openai.com/v1&#10;https://api2.openai.com/v1"
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
              <!-- 固定高度的提示区域，防止布局跳动；有错误时不显示 -->
              <div v-show="formExpectedRequestUrls.length > 0 && !baseUrlHasError" class="base-url-hint">
                <div v-for="(item, index) in formExpectedRequestUrls" :key="index" class="expected-request-item">
                  <span class="text-caption text-medium-emphasis"> 预期请求: {{ item.expectedUrl }} </span>
                </div>
              </div>
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
                :hint="proxyModeHint"
                persistent-hint
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
                hint="支持 http、https、socks5 和 socks5h"
                persistent-hint
              />
            </v-col>

            <!-- Chat 默认模型 -->
            <v-col v-if="props.channelType === 'chat' && form.serviceType" cols="12">
              <v-card variant="outlined" rounded="lg">
                <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
                  <div class="d-flex align-center ga-2">
                    <v-icon color="primary">mdi-format-list-bulleted</v-icon>
                    <span class="text-body-1 font-weight-bold">默认模型 (可选)</span>
                  </div>
                  <v-chip size="small" color="secondary" variant="tonal"> 覆盖请求模型 </v-chip>
                </v-card-title>

                <v-card-text class="pt-2">
                  <div class="text-body-2 text-medium-emphasis mb-4">
                    设置后，Chat 请求会固定使用该渠道的默认模型；未设置时保留客户端请求中的模型。
                    <br/>
                    <span class="text-caption text-primary">点击输入框会自动获取上游支持的模型列表，每个 API Key 的检测状态会显示在密钥列表中</span>
                  </div>
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
                    @focus="handleFetchModels"
                  />
                  <div v-if="fetchModelsError" class="text-error text-caption mt-2">
                    {{ fetchModelsError }}
                  </div>
                </v-card-text>
              </v-card>
            </v-col>

            <!-- 模型重定向配置 -->
            <v-col v-if="form.serviceType && props.channelType !== 'chat'" cols="12">
              <ModelMappingEditor
                v-model="form.modelMapping"
                v-model:channel-value="form.modelMappingChannels"
                :hint="modelMappingHint"
                :source-options="sourceModelOptions"
                :target-options-by-channel="targetModelOptionsByChannel"
                :target-channel-options="targetChannelOptions"
                :target-status-by-channel="targetStatusByChannel"
                :target-placeholder="targetModelPlaceholder"
                @fetch-models="handleFetchModels"
              />
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
                hint="可选的渠道描述..."
                persistent-hint
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
                    <div class="text-caption text-medium-emphasis">
                      仅在自签名或域名不匹配时临时启用，生产环境请关闭
                    </div>
                  </div>
                </div>
                <v-switch v-model="form.insecureSkipVerify" inset color="warning" hide-details />
              </div>
            </v-col>

            <!-- 低质量渠道标记 -->
            <v-col cols="12">
              <div class="d-flex align-center justify-space-between">
                <div class="d-flex align-center ga-2">
                  <v-icon color="info">mdi-speedometer-slow</v-icon>
                  <div>
                    <div class="text-body-1 font-weight-medium">低质量渠道</div>
                    <div class="text-caption text-medium-emphasis">
                      启用后强制本地估算 token 数量，偏差超过 5% 时使用本地值
                    </div>
                  </div>
                </div>
                <v-switch v-model="form.lowQuality" inset color="info" hide-details />
              </div>
            </v-col>

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
                      <div class="text-caption text-medium-emphasis">
                        启用后，图片会直接发送给当前模型；该渠道也可被其他渠道的图片理解层调用。
                      </div>
                    </div>
                    <v-switch v-model="form.visionCapable" inset color="primary" hide-details />
                  </div>

                  <v-divider class="my-4" />

                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-body-1 font-weight-medium">指定图片理解渠道</div>
                      <div class="text-caption text-medium-emphasis">
                        默认自动使用公共图片理解池；启用后优先使用指定渠道，再按公共池和当前分组回退。
                      </div>
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
                    hint="该渠道优先处理图片；失败后仍会自动尝试公共池和当前分组。"
                    persistent-hint
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
                    hint="仅当需要让图片理解渠道固定使用某个模型时填写。"
                    persistent-hint
                    prepend-inner-icon="mdi-image-search-outline"
                    variant="outlined"
                    density="comfortable"
                  />

                </v-card-text>
              </v-card>
            </v-col>

            <!-- 对话与缓存兼容性 -->
            <v-col v-if="props.channelType !== 'images'" cols="12">
              <v-card variant="outlined" rounded="lg">
                <v-card-title class="d-flex align-center ga-2 pa-4 pb-2">
                  <v-icon color="primary">mdi-chat-outline</v-icon>
                  <span class="text-body-1 font-weight-bold">对话与缓存</span>
                </v-card-title>
                <v-card-text class="pt-2">
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-body-1 font-weight-medium">不参与对话</div>
                      <div class="text-caption text-medium-emphasis">
                        启用后不参与常规对话调度，仍可作为其他渠道的图片理解模型。
                      </div>
                    </div>
                    <v-switch v-model="form.excludeFromConversation" inset color="primary" hide-details />
                  </div>

                  <template v-if="form.serviceType === 'openai' || form.serviceType === 'responses'">
                    <v-divider class="my-4" />
                    <div class="d-flex align-center justify-space-between">
                      <div>
                        <div class="text-body-1 font-weight-medium">不支持缓存键</div>
                        <div class="text-caption text-medium-emphasis">
                          移除 prompt_cache_key，避免严格校验的兼容渠道返回参数不支持错误。
                        </div>
                      </div>
                      <v-switch v-model="form.disablePromptCacheKey" inset color="secondary" hide-details />
                    </div>
                  </template>
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
                    <div class="text-caption text-medium-emphasis">
                      启用后 24 小时自动移入弃用池，弃用超过 3 天后由后台清理
                    </div>
                  </div>
                </div>
                <v-switch v-model="form.temporary" inset color="warning" hide-details />
              </div>
            </v-col>

            <!-- 注入 Dummy Thought Signature（仅 Gemini 渠道显示） -->
            <v-col v-if="props.channelType === 'gemini'" cols="12">
              <div class="d-flex align-center justify-space-between">
                <div class="d-flex align-center ga-2">
                  <v-icon color="secondary">mdi-signature</v-icon>
                  <div>
                    <div class="text-body-1 font-weight-medium">注入 Dummy Thought Signature</div>
                    <div class="text-caption text-medium-emphasis">
                      为 functionCall 注入 dummy signature，兼容需要该字段的第三方 API（官方 API 请关闭）
                    </div>
                  </div>
                </div>
                <v-switch v-model="form.injectDummyThoughtSignature" inset color="secondary" hide-details />
              </div>
            </v-col>

            <!-- 移除 Thought Signature（仅 Gemini 渠道显示） -->
            <v-col v-if="props.channelType === 'gemini'" cols="12">
              <div class="d-flex align-center justify-space-between">
                <div class="d-flex align-center ga-2">
                  <v-icon color="error">mdi-close-circle</v-icon>
                  <div>
                    <div class="text-body-1 font-weight-medium">移除 Thought Signature</div>
                    <div class="text-caption text-medium-emphasis">
                      移除 functionCall 的 thought_signature 字段，兼容不支持该字段的旧版 Gemini API
                    </div>
                  </div>
                </div>
                <v-switch v-model="form.stripThoughtSignature" inset color="error" hide-details />
              </div>
            </v-col>
          </v-row>
        </v-form>
      </v-card-text>

      <v-card-actions class="pa-6 dialog-card-actions">
        <v-spacer />
        <v-btn variant="text" @click="handleCancel"> 取消 </v-btn>
        <v-btn
          v-if="!isEditing && isQuickMode"
          color="primary"
          variant="elevated"
          :disabled="!isQuickFormValid"
          prepend-icon="mdi-check"
          @click="handleQuickSubmit"
        >
          创建渠道
        </v-btn>
        <v-btn
          v-else
          color="primary"
          variant="elevated"
          :disabled="!isFormValid"
          prepend-icon="mdi-check"
          @click="handleSubmit"
        >
          {{ isEditing ? '更新渠道' : '创建渠道' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTheme } from 'vuetify'
import type { Channel, ChannelPool, ChannelStatus } from '../services/api'
import { api, fetchUpstreamModels, channelApiByType, ApiError } from '../services/api'
import ModelMappingEditor from './ModelMappingEditor.vue'
import {
  isValidApiKey as _isValidApiKey,
  isValidUrl as _isValidQuickInputUrl,
  parseQuickInput as parseQuickInputUtil
} from '../utils/quickInputParser'
import { useChannelEditor, cloneModelMapping, cloneModelMappingChannels } from '../composables/useChannelEditor'

interface Props {
  show: boolean
  channel?: Channel | null
  channelType?: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
}

const props = withDefaults(defineProps<Props>(), {
  channelType: 'messages'
})

const emit = defineEmits<{
  'update:show': [value: boolean]
  save: [channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }]
}>()

// 主题
const theme = useTheme()

// 表单引用
const formRef = ref()

// 模式切换: 快速添加 vs 详细表单
const isQuickMode = ref(true)

// 快速添加模式的数据
const quickInput = ref('')
const quickChannelName = ref('')
const detectedBaseUrl = ref('')
const detectedBaseUrls = ref<string[]>([])
const detectedApiKeys = ref<string[]>([])
const detectedChannelName = ref('')
const detectedServiceType = ref<'openai' | 'gemini' | 'claude' | 'responses' | 'chat' | null>(null)

// 详细表单预期请求 URL 预览（防止输入时抖动）
const formBaseUrlPreview = ref('')
let formBaseUrlPreviewTimer: number | null = null

// 切换模式时，将快速模式检测到的值同步到详细表单，但不清空快速模式输入
const toggleMode = () => {
  if (isQuickMode.value) {
    // 从快速模式切换到详细模式：始终用检测到的值覆盖表单
    if (detectedBaseUrls.value.length > 0) {
      // 多个 BaseURL
      form.baseUrl = detectedBaseUrls.value[0]
      form.baseUrls = [...detectedBaseUrls.value]
      baseUrlsText.value = detectedBaseUrls.value.join('\n')
    } else if (detectedBaseUrl.value) {
      // 单个 BaseURL
      form.baseUrl = detectedBaseUrl.value
      form.baseUrls = []
      baseUrlsText.value = detectedBaseUrl.value
    }
    if (detectedApiKeys.value.length > 0) {
      form.apiKeys = [...detectedApiKeys.value]
    }
    if (generatedChannelName.value) {
      form.name = generatedChannelName.value
    }
    form.serviceType = detectedServiceType.value || getDefaultServiceTypeValue()
  }
  // 切换回快速模式时不做任何清理，保留 quickInput 原有内容
  isQuickMode.value = !isQuickMode.value
}

// 解析快速输入内容
const parseQuickInput = () => {
  const result = parseQuickInputUtil(quickInput.value)
  detectedBaseUrl.value = result.detectedBaseUrl
  detectedBaseUrls.value = result.detectedBaseUrls
  detectedApiKeys.value = result.detectedApiKeys
  detectedChannelName.value = result.detectedChannelName
  detectedServiceType.value = result.detectedServiceType
}

// 获取默认服务类型
const getDefaultServiceType = (): string => {
  if (props.channelType === 'gemini') {
    return 'Gemini'
  }
  if (props.channelType === 'responses') {
    return 'Responses (原生接口)'
  }
  if (props.channelType === 'chat') {
    return 'Chat'
  }
  if (props.channelType === 'images') {
    return 'Images'
  }
  return 'Messages'
}

// 获取默认服务类型值
const getDefaultServiceTypeValue = (): 'openai' | 'gemini' | 'claude' | 'responses' | 'chat' => {
  if (props.channelType === 'gemini') {
    return 'gemini'
  }
  if (props.channelType === 'responses') {
    return 'responses'
  }
  if (props.channelType === 'chat' || props.channelType === 'images') {
    return 'openai'
  }
  return 'claude'
}

// 快速模式表单验证
const isQuickFormValid = computed(() => {
  return detectedBaseUrls.value.length > 0 && detectedApiKeys.value.length > 0
})

// 生成随机字符串
const generateRandomString = (length: number): string => {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let result = ''
  for (let i = 0; i < length; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length))
  }
  return result
}

// 从 URL 提取二级域名
const extractDomain = (url: string): string => {
  try {
    const hostname = new URL(url).hostname
    // 移除 www. 前缀
    const cleanHost = hostname.replace(/^www\./, '')
    const parts = cleanHost.split('.')

    // 处理特殊情况
    if (parts.length <= 1) {
      // localhost 等单段域名
      return cleanHost
    } else if (parts.length === 2) {
      // example.com → example
      return parts[0]
    } else {
      // api.openai.com → openai (取倒数第二段)
      return parts[parts.length - 2]
    }
  } catch {
    return 'channel'
  }
}

// 随机后缀和生成的渠道名称
const randomSuffix = ref(generateRandomString(6))

const generatedChannelName = computed(() => {
  const manualName = quickChannelName.value.trim()
  if (manualName) {
    return manualName
  }
  if (detectedChannelName.value) {
    return detectedChannelName.value
  }
  if (!detectedBaseUrl.value) {
    return `channel-${randomSuffix.value}`
  }
  const domain = extractDomain(detectedBaseUrl.value)
  return `${domain}-${randomSuffix.value}`
})

// 生成单个 URL 的预期请求地址
const getExpectedRequestUrl = (inputBaseUrl: string): string => {
  if (!inputBaseUrl) return ''

  let baseUrl = inputBaseUrl
  const skipVersion = baseUrl.endsWith('#')
  if (skipVersion) {
    baseUrl = baseUrl.slice(0, -1)
  }

  const hasVersion = /\/v\d+[a-z]*$/.test(baseUrl)

  const serviceType = detectedServiceType.value || getDefaultServiceTypeValue()
  let endpoint = ''
  if (props.channelType === 'responses') {
    if (serviceType === 'responses') {
      endpoint = '/responses'
    } else if (serviceType === 'claude') {
      endpoint = '/messages'
    } else {
      endpoint = '/chat/completions'
    }
  } else if (props.channelType === 'chat') {
    endpoint = '/chat/completions'
  } else {
    if (serviceType === 'claude') {
      endpoint = '/messages'
    } else if (serviceType === 'responses') {
      endpoint = '/responses'
    } else if (serviceType === 'gemini') {
      endpoint = '/models/{model}:generateContent'
    } else {
      endpoint = '/chat/completions'
    }
  }

  if (hasVersion || skipVersion) {
    return baseUrl + endpoint
  }
  // Gemini 使用 /v1beta，其他使用 /v1
  const versionPrefix = serviceType === 'gemini' ? '/v1beta' : '/v1'
  return baseUrl + versionPrefix + endpoint
}

// 检测 baseUrl 是否有验证错误
const baseUrlHasError = computed(() => {
  const value = form.baseUrl
  if (!value) return true
  try {
    new URL(value)
    return false
  } catch {
    return true
  }
})

// 详细模式所有 URL 的预期请求（支持多 BaseURL）
const formExpectedRequestUrls = computed(() => {
  if (!form.serviceType) return []

  // 收集所有 URL
  const urls: string[] = []
  if (form.baseUrls && form.baseUrls.length > 0) {
    urls.push(...form.baseUrls)
  } else if (form.baseUrl) {
    urls.push(form.baseUrl)
  }

  if (urls.length === 0) return []

  // 根据 serviceType 确定端点
  let endpoint = ''
  if (props.channelType === 'responses') {
    if (form.serviceType === 'responses') {
      endpoint = '/responses'
    } else if (form.serviceType === 'claude') {
      endpoint = '/messages'
    } else {
      endpoint = '/chat/completions'
    }
  } else if (props.channelType === 'chat') {
    endpoint = '/chat/completions'
  } else {
    // messages 渠道
    if (form.serviceType === 'claude') {
      endpoint = '/messages'
    } else if (form.serviceType === 'responses') {
      endpoint = '/responses'
    } else if (form.serviceType === 'gemini') {
      endpoint = '/models/{model}:generateContent'
    } else {
      endpoint = '/chat/completions'
    }
  }

  // 为每个 URL 生成预期请求
  return urls
    .filter(url => url && isValidUrl(url.replace(/#$/, '')))
    .map(rawUrl => {
      let baseUrl = rawUrl.trim()
      const skipVersion = baseUrl.endsWith('#')
      if (skipVersion) {
        baseUrl = baseUrl.slice(0, -1)
      }
      baseUrl = baseUrl.replace(/\/$/, '')

      const hasVersion = /\/v\d+[a-z]*$/.test(baseUrl)

      // Gemini 使用 /v1beta，其他使用 /v1
      const versionPrefix = form.serviceType === 'gemini' ? '/v1beta' : '/v1'
      const expectedUrl = hasVersion || skipVersion ? baseUrl + endpoint : baseUrl + versionPrefix + endpoint

      return { baseUrl: rawUrl, expectedUrl }
    })
})

// 处理快速添加提交
const handleQuickSubmit = () => {
  if (!isQuickFormValid.value) return

  const channelData = {
    name: generatedChannelName.value,
    serviceType: detectedServiceType.value || getDefaultServiceTypeValue(),
    baseUrl: detectedBaseUrl.value,
    baseUrls: detectedBaseUrls.value,
    apiKeys: detectedApiKeys.value,
    modelMapping: {},
    defaultModel: '',
    poolId: 'default',
    visionCapable: props.channelType === 'responses',
    excludeFromConversation: false,
    visionLayerEnabled: false,
    disablePromptCacheKey: false,
    proxyMode: 'inherit' as const,
    proxyUrl: ''
  }

  // 传递 isQuickAdd 标志，让 App.vue 知道需要进行后续处理
  emit('save', channelData, { isQuickAdd: true })
}

// 服务类型选项 - 根据渠道类型动态显示
const serviceTypeOptions = computed(() => {
  if (props.channelType === 'gemini') {
    return [
      { title: 'Gemini', value: 'gemini' },
      { title: 'Chat', value: 'openai' },
      { title: 'Messages', value: 'claude' }
    ]
  }
  if (props.channelType === 'chat') {
    return [
      { title: 'Chat', value: 'openai' }
    ]
  }
  if (props.channelType === 'responses') {
    return [
      { title: 'Responses (原生接口)', value: 'responses' },
      { title: 'Chat', value: 'openai' },
      { title: 'Messages', value: 'claude' }
    ]
  } else {
    return [
      { title: 'Chat', value: 'openai' },
      { title: 'Messages', value: 'claude' },
      { title: 'Responses', value: 'responses' },
      { title: 'Gemini', value: 'gemini' }
    ]
  }
})

// 全部源模型选项 - 根据渠道类型动态显示
const allSourceModelOptions = computed(() => {
  if (props.channelType === 'gemini') {
    // Gemini API 常用模型别名（模糊匹配，无需指定版本）
    return [
      { title: 'gemini-flash', value: 'gemini-flash' },
      { title: 'gemini-pro', value: 'gemini-pro' },
    ]
  }
  if (props.channelType === 'responses') {
    // Responses API (Codex) 常用模型名称（模糊匹配，无需指定版本）
    return [
      { title: 'codex', value: 'codex' },
      { title: 'gpt-5', value: 'gpt-5' },
      { title: 'gpt-5.2', value: 'gpt-5.2' },
      { title: 'gpt-5.3', value: 'gpt-5.3' },
      { title: 'gpt-5.3-codex', value: 'gpt-5.3-codex' },
      { title: 'gpt-5.4', value: 'gpt-5.4' },
      { title: 'gpt-5.4-mini', value: 'gpt-5.4-mini' },
      { title: 'gpt-5.5', value: 'gpt-5.5' },
      { title: 'gpt-5.6-luna', value: 'gpt-5.6-luna' },
      { title: 'gpt-5.6-sol', value: 'gpt-5.6-sol' },
      { title: 'gpt-5.6-terra', value: 'gpt-5.6-terra' },
    ]
  }
  if (props.channelType === 'chat') {
    return [
      { title: 'gpt-4o', value: 'gpt-4o' },
      { title: 'gpt-4.1', value: 'gpt-4.1' },
      { title: 'gpt-5', value: 'gpt-5' },
      { title: 'o3', value: 'o3' },
      { title: 'o4-mini', value: 'o4-mini' },
    ]
  } else {
    // Messages API (Claude) 常用模型别名（模糊匹配，无需指定版本）
    return [
      { title: 'opus', value: 'opus' },
      { title: 'sonnet', value: 'sonnet' },
      { title: 'haiku', value: 'haiku' }
    ]
  }
})

// 可选的源模型选项
// 允许重复选择同一个源模型，以便为其继续追加多个目标模型
const sourceModelOptions = computed(() => [
  { title: '*（任意源模型）', value: '*' },
  ...allSourceModelOptions.value
])

// 模型重定向的示例文本 - 根据渠道类型动态显示
const modelMappingHint = computed(() => {
  if (props.channelType === 'gemini') {
    return '配置模型名称映射，将请求中的模型名重定向到目标模型。使用模糊匹配（如 gemini-flash 可匹配 gemini-2.5-flash 等版本）；选择 "*（任意源模型）" 会将所有模型请求映射到指定目标。'
  }
  if (props.channelType === 'responses') {
    return '配置模型名称映射，将请求中的模型名重定向到目标模型。使用模糊匹配（如 gpt-5.5 可匹配 gpt-5.5-codex 等变体）；选择 "*（任意源模型）" 会将所有模型请求映射到指定目标。'
  }
  if (props.channelType === 'chat') {
    return '配置模型名称映射，将请求中的 Chat 模型名重定向到目标模型；选择 "*（任意源模型）" 会将所有模型请求映射到指定目标。'
  } else {
    return '配置模型名称映射，将请求中的模型名重定向到目标模型。使用模糊匹配（如 opus 可匹配 claude-opus-4-5-20251101 等版本）；选择 "*（任意源模型）" 会将所有模型请求映射到指定目标。'
  }
})

const targetModelPlaceholder = computed(() => {
  if (props.channelType === 'gemini') {
    return '例如：gemini-2.0-flash'
  }
  if (props.channelType === 'responses') {
    return '例如：gpt-5.1-codex-max'
  }
  if (props.channelType === 'chat') {
    return '例如：gpt-4.1'
  } else {
    return '例如：claude-3-5-sonnet'
  }
})

// 表单草域草稿经 useChannelEditor（R5，方案 8.1）：草稿独立于 store，取消不落盘。
const { form, resetForm: resetFormDraft, loadChannelData: loadChannelDataDraft } = useChannelEditor(props.channelType)

const addApiKey = () => {
  const key = newApiKey.value.trim()
  if (!key) return

  // 重置错误状态
  apiKeyError.value = ''
  duplicateKeyIndex.value = -1

  // 检查是否与现有密钥重复
  const duplicateIndex = findDuplicateKeyIndex(key)
  if (duplicateIndex !== -1) {
    apiKeyError.value = '该密钥已存在'
    duplicateKeyIndex.value = duplicateIndex
    // 清除输入框，让用户重新输入
    newApiKey.value = ''
    return
  }

  // 直接存储原始密钥
  form.apiKeys.push(key)
  newApiKey.value = ''
}

// resetForm：草稿重置 + 组件内 UI 状态（错误提示、模型缓存、快速添加输入）清理。
const resetForm = () => {
  resetFormDraft()
  newApiKey.value = ''
  baseUrlsText.value = ''
  originalKeyMap.value.clear()
  apiKeyError.value = ''
  duplicateKeyIndex.value = -1
  targetModelOptions.value = []
  fetchingModels.value = false
  fetchModelsError.value = ''
  targetChannelModelOptions.value = {}
  targetChannelModelStatus.value = {}
  keyModelsStatus.value.clear()
  errors.name = ''
  errors.serviceType = ''
  errors.baseUrl = ''
  quickInput.value = ''
  quickChannelName.value = ''
  detectedBaseUrl.value = ''
  detectedBaseUrls.value = []
  detectedApiKeys.value = []
  detectedChannelName.value = ''
  detectedServiceType.value = null
  randomSuffix.value = generateRandomString(6)
}

// loadChannelData：载入编辑目标 + 组件内 UI 状态同步（baseUrl 预览、模型缓存重置）。
const loadChannelData = (channel: Channel) => {
  loadChannelDataDraft(channel)
  if (channel.baseUrls && channel.baseUrls.length > 0) {
    baseUrlsText.value = channel.baseUrls.join('\n')
  } else {
    baseUrlsText.value = channel.baseUrl || ''
  }
  originalKeyMap.value.clear()
  formBaseUrlPreview.value = channel.baseUrl
  targetModelOptions.value = []
  fetchingModels.value = false
  fetchModelsError.value = ''
  targetChannelModelOptions.value = {}
  targetChannelModelStatus.value = {}
  keyModelsStatus.value.clear()
}

const channelStatusOptions = [
  { title: '活跃', value: 'active' },
  { title: '熔断', value: 'suspended' },
  { title: '禁用', value: 'disabled' }
] satisfies Array<{ title: string; value: ChannelStatus }>

// Channel.status 还可能包含健康检查结果（healthy/error/unknown），这些值不属于可持久化的渠道状态。
const normalizeFormStatus = (status: Channel['status']): ChannelStatus => {
  switch (status) {
    case 'active':
    case 'suspended':
    case 'disabled':
    case 'deprecated':
    case 'deleted':
      return status
    default:
      return 'active'
  }
}

const proxyModeOptions = [
  { title: '继承全局设置', value: 'inherit' },
  { title: '强制直连', value: 'direct' },
  { title: '使用独立代理', value: 'custom' }
]

const proxyModeHint = computed(() => {
  if (form.proxyMode === 'direct') return '忽略全局代理，该渠道始终直接连接上游'
  if (form.proxyMode === 'custom') return '仅该渠道使用下方填写的代理地址'
  return '跟随设置页面中的全局上游代理；全局未启用时直接连接'
})

// 多 BaseURL 文本输入（独立变量，保留用户输入的换行）
const baseUrlsText = ref('')

// 监听 baseUrlsText 变化，同步到 form（仅做基本同步，不修改用户输入）
watch(baseUrlsText, val => {
  const urls = val
    .split('\n')
    .map(s => s.trim())
    .filter(Boolean)
  if (urls.length === 0) {
    form.baseUrl = ''
    form.baseUrls = []
  } else if (urls.length === 1) {
    form.baseUrl = urls[0]
    form.baseUrls = []
  } else {
    form.baseUrl = urls[0]
    form.baseUrls = urls
  }
})

// 原始密钥映射 (掩码密钥 -> 原始密钥)
const originalKeyMap = ref<Map<string, string>>(new Map())

// 新API密钥输入
const newApiKey = ref('')

// 密钥重复检测状态
const apiKeyError = ref('')
const duplicateKeyIndex = ref(-1)

// 处理 API 密钥输入事件
const handleApiKeyInput = () => {
  apiKeyError.value = ''
  duplicateKeyIndex.value = -1
}

// 复制功能相关状态
const copiedKeyIndex = ref<number | null>(null)

// 目标模型列表（从上游获取）
  const targetModelOptions = ref<Array<{ title: string; value: string }>>([])
  const fetchingModels = ref(false)
  const fetchModelsError = ref('')

// 模型映射的目标渠道：候选渠道列表（同协议、可用、排除本渠道），以及各目标渠道的模型候选缓存。
// 键为目标渠道 ID；本渠道的候选沿用上面的 targetModelOptions（键为空串）。
const targetChannelOptions = ref<Array<{ title: string; value: string }>>([])
const targetChannelModelOptions = ref<Record<string, Array<{ title: string; value: string }>>>({})
const targetChannelModelStatus = ref<Record<string, { loading: boolean; error: string }>>({})
const targetModelOptionsByChannel = computed(() => ({
  '': targetModelOptions.value,
  ...targetChannelModelOptions.value
}))
const targetStatusByChannel = computed(() => ({
  '': { loading: fetchingModels.value, error: fetchModelsError.value },
  ...targetChannelModelStatus.value
}))

const getStringValue = (val: unknown): string => {
  if (!val) return ''
  if (typeof val === 'string') return val
  if (typeof val === 'object' && 'value' in val) {
    const value = (val as { value?: unknown }).value
    return typeof value === 'string' ? value : ''
  }
  return ''
}

// API Key 的 models 状态管理
interface KeyModelsStatus {
  loading: boolean
  success: boolean
  statusCode?: number
  error?: string
  modelCount?: number
}
const keyModelsStatus = ref<Map<string, KeyModelsStatus>>(new Map())

// 表单验证错误
const errors = reactive({
  name: '',
  serviceType: '',
  baseUrl: '',
  website: ''
})

// 验证规则
const rules = {
  required: (value: string) => !!value || '此字段为必填项',
  url: (value: string) => {
    try {
      new URL(value)
      return true
    } catch {
      return '请输入有效的URL'
    }
  },
  urlOptional: (value: string) => {
    if (!value) return true
    try {
      new URL(value)
      return true
    } catch {
      return '请输入有效的URL'
    }
  },
  baseUrls: (value: string) => {
    if (!value) return '此字段为必填项'
    const urls = value
      .split('\n')
      .map(s => s.trim())
      .filter(Boolean)
    if (urls.length === 0) return '请至少输入一个 URL'
    for (const url of urls) {
      try {
        new URL(url)
      } catch {
        return `无效的 URL: ${url}`
      }
    }
    return true
  },
  proxyUrl: (value: string) => {
    if (form.proxyMode !== 'custom') return true
    const raw = value?.trim()
    if (!raw) return '使用独立代理时必须填写代理地址'
    try {
      const parsed = new URL(raw)
      if (!['http:', 'https:', 'socks5:', 'socks5h:'].includes(parsed.protocol)) {
        return '仅支持 http、https、socks5 和 socks5h 协议'
      }
      if (!parsed.hostname) return '代理地址必须包含主机'
      if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) {
        return '代理地址不能包含路径、查询参数或片段'
      }
      return true
    } catch {
      return '请输入有效的代理地址'
    }
  }
}

// 计算属性
const isEditing = computed(() => !!props.channel)

// 动态header样式
const headerClasses = computed(() => {
  const isDark = theme.global.current.value.dark
  // Dark: keep neutral surface header; Light: use brand primary header
  return isDark ? 'bg-surface text-high-emphasis' : 'bg-primary text-white'
})

const avatarColor = computed(() => 'primary')

// Use Vuetify theme "on-primary" token so icon isn't fixed white
const headerIconStyle = computed(() => ({
  color: 'rgb(var(--v-theme-on-primary))'
}))

const subtitleClasses = computed(() => {
  const isDark = theme.global.current.value.dark
  // Dark mode: use medium emphasis; Light mode: use white with opacity for primary bg
  return isDark ? 'text-medium-emphasis' : 'text-white-subtitle'
})

const isFormValid = computed(() => {
  return (
    form.name.trim() && form.serviceType && form.baseUrl.trim() && isValidUrl(form.baseUrl) && form.apiKeys.length > 0 &&
    (!form.visionLayerEnabled || form.visionLayerChannelId) &&
    (form.proxyMode !== 'custom' || rules.proxyUrl(form.proxyUrl) === true)
  )
})

const visionChannels = ref<Channel[]>([])
const visionChannelsLoading = ref(false)
const pools = ref<ChannelPool[]>([])
const poolsLoading = ref(false)
const poolLoadError = ref('')
const poolOptions = computed(() => pools.value.map(pool => ({
  title: pool.id === 'default' ? pool.name : `${pool.name}（匹配 ${pool.modelMatcher}）`,
  value: pool.id
})))

const visionChannelOptions = computed(() => {
  const currentChannelID = props.channel?.id
  return visionChannels.value
    .filter(channel => channel.id && channel.id !== currentChannelID && channel.visionCapable && channel.status === 'active' &&
      (channel.excludeFromConversation || (channel.poolId || 'default') === form.poolId))
    .map(channel => ({ title: `${channel.name}（${channel.serviceType}）`, value: channel.id }))
})

const visionLayerChannelError = computed(() => {
  if (form.visionLayerEnabled && !form.visionLayerChannelId) {
    return '启用图片理解层后必须选择图片理解渠道'
  }
  return ''
})

const loadVisionChannels = async () => {
  visionChannelsLoading.value = true
  try {
    const response = await channelApiByType(props.channelType).getChannels()
    visionChannels.value = response.channels || []
  } catch (error) {
    console.error('加载图片理解渠道失败:', error)
    visionChannels.value = []
  } finally {
    visionChannelsLoading.value = false
  }
}

const loadPools = async () => {
	poolsLoading.value = true
	poolLoadError.value = ''
	try {
    const response = await api.getChannelPools(props.channelType)
    pools.value = response.pools || []
		if (!pools.value.some(pool => pool.id === form.poolId)) form.poolId = 'default'
	} catch (error) {
		pools.value = []
		poolLoadError.value = error instanceof Error ? error.message : '加载分组失败'
	} finally {
    poolsLoading.value = false
  }
}

watch(() => form.visionCapable, enabled => {
	if (enabled) {
    form.visionLayerEnabled = false
    form.visionLayerChannelId = ''
		form.visionLayerModel = ''
	} else {
		form.excludeFromConversation = false
	}
})

watch(() => form.excludeFromConversation, enabled => {
	if (enabled) {
		form.visionCapable = true
	}
})

watch([() => form.poolId, visionChannelOptions], () => {
	if (form.visionLayerChannelId && !visionChannelOptions.value.some(option => option.value === form.visionLayerChannelId)) {
		form.visionLayerChannelId = ''
	}
})

watch(() => form.visionLayerEnabled, enabled => {
  if (enabled) {
    form.visionCapable = false
  } else {
    form.visionLayerChannelId = ''
    form.visionLayerModel = ''
  }
})

// 工具函数
const isValidUrl = (url: string): boolean => {
  try {
    new URL(url)
    return true
  } catch {
    return false
  }
}

const maskApiKey = (key: string): string => {
  if (key.length <= 10) return key.slice(0, 3) + '***' + key.slice(-2)
  return key.slice(0, 8) + '***' + key.slice(-5)
}

// 表单操作
// 检查密钥是否重复，返回重复密钥的索引，如果没有重复返回-1
const findDuplicateKeyIndex = (newKey: string): number => {
  return form.apiKeys.findIndex(existingKey => existingKey === newKey)
}

const removeApiKey = (index: number) => {
  form.apiKeys.splice(index, 1)

  // 如果删除的是当前高亮的重复密钥，清除高亮状态
  if (duplicateKeyIndex.value === index) {
    duplicateKeyIndex.value = -1
    apiKeyError.value = ''
  } else if (duplicateKeyIndex.value > index) {
    // 如果删除的密钥在高亮密钥之前，调整高亮索引
    duplicateKeyIndex.value--
  }
}

// 将指定密钥移到最上方
const moveApiKeyToTop = (index: number) => {
  if (index <= 0 || index >= form.apiKeys.length) return
  const [key] = form.apiKeys.splice(index, 1)
  form.apiKeys.unshift(key)
  duplicateKeyIndex.value = -1
  copiedKeyIndex.value = null
}

// 将指定密钥移到最下方
const moveApiKeyToBottom = (index: number) => {
  if (index < 0 || index >= form.apiKeys.length - 1) return
  const [key] = form.apiKeys.splice(index, 1)
  form.apiKeys.push(key)
  duplicateKeyIndex.value = -1
  copiedKeyIndex.value = null
}

// 复制API密钥到剪贴板
const copyApiKey = async (key: string, index: number) => {
  try {
    await navigator.clipboard.writeText(key)
    copiedKeyIndex.value = index

    // 2秒后重置复制状态
    setTimeout(() => {
      copiedKeyIndex.value = null
    }, 2000)
  } catch (err) {
    console.error('复制密钥失败:', err)
    // 降级方案：使用传统的复制方法
    const textArea = document.createElement('textarea')
    textArea.value = key
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()

    try {
      document.execCommand('copy')
      copiedKeyIndex.value = index

      setTimeout(() => {
        copiedKeyIndex.value = null
      }, 2000)
    } catch (err) {
      console.error('降级复制方案也失败:', err)
    } finally {
      textArea.remove()
    }
  }
}

const handleFetchModels = (channelId: string) => {
  if (channelId) {
    void fetchChannelTargetModels(channelId)
    return
  }
  fetchTargetModels()
}

// 加载模型映射的目标渠道候选（同协议、可用，排除本渠道）
const loadMappingTargetChannels = async () => {
  try {
    const response = await channelApiByType(props.channelType).listMappingTargets(props.channel?.id)
    targetChannelOptions.value = (response.channels || []).map(channel => ({
      title: `${channel.name}（${channel.serviceType}）`,
      value: channel.id
    }))
  } catch (error) {
    console.error('加载模型映射目标渠道失败:', error)
    targetChannelOptions.value = []
  }
}

// 获取指定目标渠道的模型列表（使用目标渠道自身的密钥，服务端完成探测）。成功后按渠道缓存，失败不缓存以便重试。
const fetchChannelTargetModels = async (channelId: string) => {
  if (targetChannelModelOptions.value[channelId] || targetChannelModelStatus.value[channelId]?.loading) {
    return
  }

  targetChannelModelStatus.value = {
    ...targetChannelModelStatus.value,
    [channelId]: { loading: true, error: '' }
  }

  try {
    const response = await channelApiByType(props.channelType).listMappingTargetModels(channelId)
    const options = response.data
      .map(model => model.id)
      .sort()
      .map(id => ({ title: id, value: id }))
    targetChannelModelOptions.value = { ...targetChannelModelOptions.value, [channelId]: options }
    targetChannelModelStatus.value = {
      ...targetChannelModelStatus.value,
      [channelId]: { loading: false, error: '' }
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : '未知错误'
    targetChannelModelStatus.value = {
      ...targetChannelModelStatus.value,
      [channelId]: { loading: false, error: `无法获取目标渠道的模型列表: ${message}` }
    }
  }
}

const fetchTargetModels = async () => {
  if (!form.baseUrl) {
    fetchModelsError.value = '请先填写 Base URL'
    return
  }

  // 无 API Key 时仍尝试一次空密钥探测（Ollama 等本地上游）
  const keysToCheck = form.apiKeys.length > 0 ? form.apiKeys : ['']

  // 如果已经有模型列表且所有 key 都已检测过,直接返回(缓存)
  if (targetModelOptions.value.length > 0) {
    const allKeysChecked = keysToCheck.every(key => keyModelsStatus.value.has(key))
    if (allKeysChecked) {
      return
    }
  }

  fetchingModels.value = true
  fetchModelsError.value = ''

  const uncheckedKeys = keysToCheck.filter(key => !keyModelsStatus.value.has(key))

  if (uncheckedKeys.length === 0) {
    fetchingModels.value = false
    return
  }

  const keyPromises = uncheckedKeys.map(async (apiKey) => {
    keyModelsStatus.value.set(apiKey, { loading: true, success: false })

    try {
      const response = await fetchUpstreamModels(form.baseUrl, apiKey, form.serviceType, {
        baseUrls: form.baseUrls,
        insecureSkipVerify: form.insecureSkipVerify,
        proxyMode: form.proxyMode,
        proxyUrl: form.proxyMode === 'custom' ? form.proxyUrl.trim() : ''
      })

      keyModelsStatus.value.set(apiKey, {
        loading: false,
        success: true,
        statusCode: 200,
        modelCount: response.data.length
      })

      return response.data
    } catch (error) {
      let errorMsg = '未知错误'
      let statusCode = 0

      if (error instanceof ApiError) {
        errorMsg = error.message
        statusCode = error.status
      } else if (error instanceof Error) {
        errorMsg = error.message
      }

      keyModelsStatus.value.set(apiKey, {
        loading: false,
        success: false,
        statusCode,
        error: errorMsg
      })

      return []
    }
  })

  try {
    const results = await Promise.all(keyPromises)

    // 合并新获取的模型列表到现有列表
    const allModels = new Set<string>(targetModelOptions.value.map(opt => opt.value))
    results.forEach(models => {
      models.forEach(m => allModels.add(m.id))
    })

    targetModelOptions.value = Array.from(allModels)
      .sort()
      .map(id => ({ title: id, value: id }))

    // 如果所有 key 都失败了,显示错误
    const allFailed = keysToCheck.every(key => {
      const status = keyModelsStatus.value.get(key)
      return status && !status.success
    })

    if (allFailed) {
      const firstError = keysToCheck
        .map(key => keyModelsStatus.value.get(key)?.error)
        .find(Boolean)
      fetchModelsError.value = firstError
        ? `无法获取模型列表: ${firstError}`
        : '所有 API Key 都无法获取模型列表,请检查 API 密钥列表中的错误信息'
    }
  } finally {
    fetchingModels.value = false
  }
}

const handleSubmit = async () => {
  if (!formRef.value) return

  const { valid } = await formRef.value.validate()
  if (!valid) return

  // 直接使用原始密钥，不需要转换
  const processedApiKeys = form.apiKeys.filter(key => key.trim())

  // 处理 BaseURL：去重（忽略末尾 / 和 # 差异），并移除 UI 专用的尾部 #
  const seenUrls = new Set<string>()
  const deduplicatedUrls =
    form.baseUrls.length > 0
      ? form.baseUrls
          .map(url => url.trim().replace(/[#/]+$/, ''))
          .filter(Boolean)
          .filter(url => {
            const normalized = url.replace(/[#/]+$/, '')
            if (seenUrls.has(normalized)) return false
            seenUrls.add(normalized)
            return true
          })
      : [form.baseUrl.trim().replace(/[#/]+$/, '')].filter(Boolean)

  // 提交前统一用清理后的映射（去空白/去重/去空目标），目标渠道登记按其剪枝，保证两者一致
  const submitModelMapping = props.channelType === 'chat' ? {} : cloneModelMapping(form.modelMapping)

  // 构建渠道数据
  const channelData: Omit<Channel, 'id' | 'index' | 'latency'> = {
    name: form.name.trim(),
    serviceType: form.serviceType as 'openai' | 'gemini' | 'claude' | 'responses' | 'chat',
    baseUrl: deduplicatedUrls[0] || '',
    website: form.website.trim(), // 空字符串也需要传递，以便清除已有值
    insecureSkipVerify: form.insecureSkipVerify,
    proxyMode: form.proxyMode,
    proxyUrl: form.proxyMode === 'custom' ? form.proxyUrl.trim() : '',
    lowQuality: form.lowQuality,
    visionCapable: form.visionCapable,
    poolId: form.poolId,
    excludeFromConversation: form.excludeFromConversation,
    visionLayerEnabled: form.visionLayerEnabled,
    visionLayerChannelId: form.visionLayerChannelId || undefined,
    visionLayerModel: form.visionLayerModel.trim(),
    temporary: form.temporary,
    injectDummyThoughtSignature: form.injectDummyThoughtSignature,
    stripThoughtSignature: form.stripThoughtSignature,
    disablePromptCacheKey: form.disablePromptCacheKey,
    description: form.description.trim(),
    apiKeys: processedApiKeys,
    modelMapping: submitModelMapping,
    modelMappingChannels:
      props.channelType === 'chat' ? {} : cloneModelMappingChannels(submitModelMapping, form.modelMappingChannels),
    defaultModel: props.channelType === 'chat' ? getStringValue(form.defaultModel).trim() : ''
  }

  // 多 BaseURL 支持
  if (deduplicatedUrls.length > 1) {
    channelData.baseUrls = deduplicatedUrls
  }

  if (isEditing.value) {
    channelData.status = form.status
  }

  emit('save', channelData)
}

const handleCancel = () => {
  emit('update:show', false)
  resetForm()
}

// 监听props变化
watch(
  () => props.show,
  newShow => {
    if (newShow) {
      // 无论是编辑还是新增，都先清理密钥错误状态
      apiKeyError.value = ''
      duplicateKeyIndex.value = -1
      void loadVisionChannels()
      void loadPools()
      void loadMappingTargetChannels()

      if (props.channel) {
        // 编辑模式：使用表单模式
        isQuickMode.value = false
        loadChannelData(props.channel)
      } else {
        // 添加模式：默认使用快速模式
        isQuickMode.value = true
        resetForm()
      }
    }
  }
)

watch(
  () => props.channel,
  newChannel => {
    if (newChannel && props.show) {
      loadChannelData(newChannel)
    }
  }
)

watch(
  () => form.baseUrl,
  value => {
    if (formBaseUrlPreviewTimer !== null) {
      window.clearTimeout(formBaseUrlPreviewTimer)
    }
    formBaseUrlPreviewTimer = window.setTimeout(() => {
      formBaseUrlPreview.value = value
    }, 200)
  },
  { immediate: true }
)

// ESC键监听
const handleKeydown = (event: Event) => {
  const keyboardEvent = event as KeyboardEvent
  if (keyboardEvent.key === 'Escape' && props.show) {
    handleCancel()
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
  if (formBaseUrlPreviewTimer !== null) {
    window.clearTimeout(formBaseUrlPreviewTimer)
  }
})
</script>

<style scoped>
/* 基础URL下方的提示区域 - 固定高度防止布局跳动 */
.base-url-hint {
  min-height: 20px;
  padding: 4px 12px 8px;
  line-height: 1.25;
}

/* 多个预期请求项样式 */
.expected-request-item + .expected-request-item {
  margin-top: 2px;
}

/* 浅色模式下副标题使用白色带透明度 */
.text-white-subtitle {
  color: rgba(255, 255, 255, 0.85) !important;
}

.animate-pulse {
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}

:deep(.key-tooltip) {
  color: rgba(var(--v-theme-on-surface), 0.92);
  background-color: rgba(var(--v-theme-surface), 0.98);
  border: 1px solid rgba(var(--v-theme-primary), 0.45);
  font-weight: 600;
  letter-spacing: 0.2px;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.06);
}

/* 快速添加模式样式 */
.quick-input-textarea :deep(textarea) {
  font-family: 'SF Mono', Monaco, 'Cascadia Code', monospace;
  font-size: 13px;
  line-height: 1.6;
}

.detection-status-card {
  background: rgba(var(--v-theme-surface-variant), 0.3);
}

/* 多 Base URL 项目样式 */
.base-url-item {
  padding: 6px 10px;
  background: rgba(var(--v-theme-surface-variant), 0.4);
  border-radius: 6px;
  border-left: 2px solid rgb(var(--v-theme-success));
}

.base-url-item + .base-url-item {
  margin-top: 4px;
}

.mode-toggle-btn {
  text-transform: none;
}

.dialog-card-shell {
  display: flex;
  flex-direction: column;
  max-height: 90vh;
  overflow: hidden;
}

.dialog-card-body {
  flex: 1 1 auto;
  overflow-y: auto;
}

.dialog-card-actions {
  flex-shrink: 0;
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface));
}

.header-action-btn {
  color: inherit;
}

/* 亮色模式下按钮在 primary 背景上显示白色 */
.bg-primary .mode-toggle-btn {
  color: white !important;
  border-color: rgba(255, 255, 255, 0.7) !important;
}

.bg-primary .mode-toggle-btn:hover {
  background-color: rgba(255, 255, 255, 0.15) !important;
  border-color: white !important;
}

.bg-primary .header-action-btn {
  color: white !important;
}

.bg-primary .header-action-btn:hover {
  background-color: rgba(255, 255, 255, 0.15) !important;
}
</style>
