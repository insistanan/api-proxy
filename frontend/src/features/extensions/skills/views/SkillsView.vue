<template>
  <div class="skills-page">
    <div class="skills-header-sticky">
      <PageHeader title="Skills" description="发现、安装并管理各客户端使用的技能。">
        <UiButton variant="text" prepend-icon="mdi-refresh" :loading="loading" @click="loadSkills">刷新</UiButton>
        <UiButton variant="text" prepend-icon="mdi-sync" :loading="syncing" @click="syncManifest"
          >同步到 Agent</UiButton
        >
        <UiButton
          variant="text"
          prepend-icon="mdi-source-branch"
          :disabled="batchRunning || translationInProgress"
          @click="gitDialog = true"
          >仓库同步</UiButton
        >
        <UiButton
          variant="text"
          prepend-icon="mdi-archive-outline"
          :loading="consolidating"
          :disabled="batchRunning || translationInProgress"
          @click="consolidateInstalledSkills"
          >导入现有</UiButton
        >
        <UiButton
          variant="text"
          prepend-icon="mdi-swap-horizontal"
          @click="displayLanguage = displayLanguage === 'zh' ? 'en' : 'zh'"
          >{{ displayLanguage === 'zh' ? '显示英文' : '显示中文' }}</UiButton
        >
        <UiButton
          variant="text"
          prepend-icon="mdi-translate"
          :disabled="loading || translationInProgress || batchRunning || batchStarting || !batchTargets.length"
          @click="openBatchTranslation"
          >批量翻译项目技能</UiButton
        >
        <UiButton
          variant="tonal"
          prepend-icon="mdi-magnify"
          :disabled="batchRunning || translationInProgress"
          @click="openDiscover"
          >发现 Skill</UiButton
        >
        <UiButton
          color="primary"
          prepend-icon="mdi-plus"
          :disabled="batchRunning || translationInProgress"
          @click="openImport"
          >本地安装</UiButton
        >
      </PageHeader>
    </div>

    <div v-if="batchRunning || batchResult" class="mb-4 flex items-center gap-3 text-sm text-muted-foreground">
      <span role="status">{{ batchRunning ? batchProgress : batchResultLabel }}</span>
      <UiButton v-if="batchRunning" variant="text" size="small" @click="cancelSkillTranslation">取消翻译</UiButton>
      <UiButton v-if="!batchRunning && batchResult" variant="text" size="small" @click="batchResultDialog = true"
        >查看翻译结果</UiButton
      >
    </div>

    <UiAlert v-if="batchStatusError" type="warning" class="mb-4">读取翻译任务状态失败。{{ batchStatusError }}</UiAlert>
    <UiAlert v-if="error" type="error" variant="tonal" class="mb-5" closable @click:close="error = ''">{{
      error
    }}</UiAlert>

    <ExtensionsGitDialog v-model="gitDialog" @updated="loadSkills" />
    <DisclosureSection
      v-model="sourcesExpanded"
      title="来源与安装位置"
      :count="locations.length"
      icon="mdi-folder-open"
      class="mb-4"
    >
      <div class="locations-strip">
        <div class="locations-heading">
          <div>
            <div class="text-xs text-muted-foreground">
              {{ locations.filter(location => location.exists).length }} 个目录可用 · 点击卡片筛选来源
            </div>
          </div>
          <UiButton
            v-if="agentFilter"
            variant="text"
            size="small"
            prepend-icon="mdi-filter-remove-outline"
            @click="agentFilter = ''"
            >清除来源筛选</UiButton
          >
        </div>
        <div class="location-grid">
          <div
            v-for="location in locations"
            :key="location.key"
            :class="['location-card', { 'location-card--active': agentFilter === location.key }]"
          >
            <button
              type="button"
              class="location-filter"
              :aria-pressed="agentFilter === location.key"
              :aria-label="`筛选 ${sourceName(location.agent)} 来源`"
              @click="filterByAgent(location.key)"
            >
              <UiIcon icon="mdi-folder-open" size="small" />
              <span class="location-name">{{ sourceName(location.agent) }}</span>
              <UiBadge
                size="x-small"
                label
                :color="location.exists ? (location.readOnly ? 'info' : 'success') : 'secondary'"
              >
                {{
                  location.error ? '读取失败' : location.exists ? (location.readOnly ? '只读' : '可安装') : '目录不存在'
                }}
              </UiBadge>
            </button>
            <p v-if="location.error" class="text-xs text-destructive">{{ location.error }}</p>
            <UiTooltip text="复制目录路径" location="top" theme="dark"
              ><template #activator="{ props }"
                ><UiButton
                  v-bind="props"
                  class="location-path-button"
                  icon="mdi-folder-marker-outline"
                  variant="text"
                  size="x-small"
                  :disabled="!location.path"
                  :aria-label="`复制 ${sourceName(location.agent)} 目录路径`"
                  @click="copySkillPath(location.path)" /></template
            ></UiTooltip>
          </div>
        </div>
      </div>
    </DisclosureSection>

    <div class="panel-surface grid grid-cols-12 gap-4 mb-4 p-4">
      <div class="min-w-0 col-span-12 md:col-span-7">
        <UiInput
          v-model.trim="query"
          density="comfortable"
          variant="outlined"
          prepend-inner-icon="mdi-magnify"
          label="搜索名称、描述或来源"
          hide-details
        />
      </div>
      <div class="min-w-0 col-span-12 md:col-span-5">
        <UiSelect
          v-model="agentFilter"
          :items="agentOptions"
          item-title="title"
          item-value="value"
          label="来源"
          density="comfortable"
          variant="outlined"
          hide-details
        />
      </div>
    </div>

    <UiProgress v-if="loading" indeterminate color="primary" class="mb-4" />

    <UiCard elevation="0" class="skills-list-card">
      <UiTable fixed-header density="comfortable" class="skills-table">
        <colgroup>
          <col class="skill-column" />
          <col class="source-column" />
          <col class="status-column" />
          <col class="agents-column" />
          <col class="path-column" />
          <col class="actions-column" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">Skill</th>
            <th scope="col">来源</th>
            <th scope="col">状态</th>
            <th scope="col">启用 Agent</th>
            <th scope="col">路径</th>
            <th scope="col" class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && filteredSkills.length === 0">
            <td colspan="6" class="text-center text-muted-foreground py-10">未发现符合条件的 Skill</td>
          </tr>
          <tr v-for="skill in visibleSkills" :key="`${skill.locationKey}:${skill.name}`">
            <td>
              <UiTooltip :text="skill.note || ''" :disabled="!skill.note" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><button v-bind="props" type="button" class="skill-name-button font-medium" @click="openSkill(skill)">
                    {{ displaySkillName(skill)
                    }}<UiTooltip v-if="skill.hasTranslation" text="已翻译" location="top" theme="dark"
                      ><template #activator="{ props: tipProps }"
                        ><UiIcon
                          v-bind="tipProps"
                          icon="mdi-translate"
                          size="x-small"
                          color="primary"
                          class="ml-1" /></template
                    ></UiTooltip></button></template
              ></UiTooltip>
              <div class="text-xs text-muted-foreground skill-description">
                {{ displaySkillDescription(skill) || '未读取到说明' }}
              </div>
            </td>
            <td>
              <UiBadge
                size="small"
                label
                :color="skill.readOnly ? 'info' : undefined"
                :class="['source-chip', { 'source-chip--active': agentFilter === skill.locationKey }]"
                title="点击筛选此来源"
                @click="filterByAgent(skill.locationKey)"
                >{{ sourceName(skill.agent) }}</UiBadge
              >
              <div v-if="skill.readOnly" class="text-xs text-muted-foreground mt-1">只读来源</div>
            </td>
            <td class="status-cell">
              <UiBadge size="small" :color="skill.valid ? 'success' : 'warning'" label>{{
                skill.valid ? '有效' : skill.issue || '需检查'
              }}</UiBadge>
            </td>
            <td class="agent-toggles-cell">
              <div v-if="skill.locationKey === 'project'" class="flex flex-wrap gap-1">
                <UiBadge
                  v-for="agent in managedAgentItems"
                  :key="agent.key"
                  size="x-small"
                  label
                  :color="isAgentEnabled(skill.name, agent.key) ? 'primary' : undefined"
                  :variant="isAgentEnabled(skill.name, agent.key) ? 'flat' : 'outlined'"
                  :prepend-icon="isAgentEnabled(skill.name, agent.key) ? 'mdi-check' : undefined"
                  :disabled="togglingKey === `${skill.name}:${agent.key}`"
                  class="agent-toggle-chip"
                  :title="`${isAgentEnabled(skill.name, agent.key) ? '禁用' : '启用'} ${agent.label}`"
                  @click="toggleSkillAgent(skill.name, agent.key)"
                >
                  {{ agent.label }}
                </UiBadge>
              </div>
              <span v-else class="text-xs text-muted-foreground">—</span>
            </td>
            <td>
              <UiTooltip text="复制路径" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><UiButton
                    v-bind="props"
                    icon="mdi-folder-marker-outline"
                    variant="text"
                    size="small"
                    :aria-label="`复制 ${skill.name} 的路径`"
                    @click="copySkillPath(skill.path)" /></template
              ></UiTooltip>
            </td>
            <td class="text-right whitespace-nowrap">
              <UiTooltip text="查看与翻译" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><UiButton
                    v-bind="props"
                    icon="mdi-text-box-search-outline"
                    variant="text"
                    size="small"
                    :disabled="loading"
                    @click="openSkill(skill)" /></template
              ></UiTooltip>
              <UiTooltip text="一键翻译名称和内容" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><UiButton
                    v-bind="props"
                    icon="mdi-translate"
                    color="primary"
                    variant="text"
                    size="small"
                    :loading="translationStates[skillStateKey(skill)]?.translating"
                    :disabled="
                      loading ||
                      !skill.valid ||
                      skill.hasTranslation ||
                      translationInProgress ||
                      batchStarting ||
                      batchRunning
                    "
                    :aria-label="`一键翻译 ${skill.name} 的名称和内容`"
                    @click="translateSkillFromList(skill)" /></template
              ></UiTooltip>
              <UiTooltip :text="skill.readOnly ? '只读来源不可复制' : '复制到其他 Agent'" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><UiButton
                    v-bind="props"
                    icon="mdi-content-copy"
                    :disabled="skill.readOnly || batchRunning || translationInProgress"
                    variant="text"
                    size="small"
                    @click="openCopy(skill)" /></template
              ></UiTooltip>
              <UiTooltip :text="skill.readOnly ? '只读来源不可删除' : '删除'" location="top" theme="dark"
                ><template #activator="{ props }"
                  ><UiButton
                    v-bind="props"
                    icon="mdi-delete"
                    :disabled="skill.readOnly || batchRunning || translationInProgress"
                    color="error"
                    variant="text"
                    size="small"
                    @click="confirmDelete(skill)" /></template
              ></UiTooltip>
            </td>
          </tr>
        </tbody>
      </UiTable>
      <div class="skills-pagination">
        <span class="text-sm text-muted-foreground" role="status"
          >共 {{ filteredSkills.length }} 项 · 每页 {{ skillsPageSize }} 项</span
        >
        <UiPagination
          v-if="skillsPageCount > 1"
          v-model="skillsPage"
          :length="skillsPageCount"
          :total-visible="3"
          density="compact"
          aria-label="技能列表分页"
        />
      </div>
    </UiCard>

    <AppDialog
      v-model="importDialog"
      title="安装 Skill"
      description="上传 ZIP 压缩包或 SKILL.md，选择要写入的 Agent 目录。"
      size="md"
      :busy="importing"
    >
      <UiFileInput
        v-model="importFile"
        accept=".zip,.md,text/markdown,application/zip"
        label="ZIP 压缩包或 SKILL.md"
        variant="outlined"
        prepend-icon="mdi-paperclip"
        show-size
      />
      <UiSelect
        v-model="importTargets"
        :items="writableLocations"
        item-title="agent"
        item-value="key"
        label="安装目标"
        variant="outlined"
        multiple
        chips
      />
      <UiAlert type="warning" density="compact" variant="tonal"
        >项目目录会保留一份来源，并更新已启用的托管副本。同名目录未托管或已在本地修改时会报告冲突；ZIP
        必须包含单一、有效的 <code>SKILL.md</code>。</UiAlert
      >
      <template #actions>
        <UiButton variant="text" :disabled="importing" @click="importDialog = false">取消</UiButton>
        <UiButton
          color="primary"
          :disabled="!importFile || importTargets.length === 0"
          :loading="importing"
          @click="importSelected"
          >安装</UiButton
        >
      </template>
    </AppDialog>

    <AppDialog
      v-model="discoverDialog"
      title="发现 Skill"
      description="从 skills.sh 搜索并安装到本机，安装前会从 GitHub 校验 SKILL.md 和文件清单。"
      size="xl"
      :busy="remoteSearching"
    >
      <UiInput
        v-model.trim="discoverQuery"
        label="搜索 Skill，例如 ui、testing、react"
        variant="outlined"
        hide-details="auto"
        clearable
        @keyup.enter="searchRemote"
      >
        <template #append-inner
          ><UiButton icon="mdi-magnify" variant="text" size="small" :loading="remoteSearching" @click="searchRemote"
        /></template>
      </UiInput>
      <UiAlert v-if="discoverError" type="error" variant="tonal" class="mt-4">{{ discoverError }}</UiAlert>
      <UiProgress v-if="remoteSearching" indeterminate color="primary" class="mt-4" />
      <EmptyState
        v-if="!remoteSearching && remoteResults.length === 0 && remoteSearched"
        variant="inline"
        icon="mdi-magnify-close"
        title="没有找到匹配的 Skill"
        description="换个关键词再搜一次，例如 ui、testing、react。"
      />
      <UiTable v-if="remoteResults.length" density="comfortable" class="mt-4 remote-results-table">
        <thead>
          <tr>
            <th>Skill</th>
            <th>来源</th>
            <th>安装量</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="result in remoteResults" :key="result.id">
            <td>
              <div class="font-medium">{{ result.name || result.skillId }}</div>
              <div class="text-xs text-muted-foreground">{{ result.skillId }}</div>
            </td>
            <td>
              <a
                v-if="result.repositoryUrl"
                :href="result.repositoryUrl"
                target="_blank"
                rel="noreferrer"
                class="source-link"
                >{{ result.source }}</a
              ><span v-else>{{ result.source }}</span>
            </td>
            <td class="install-count-cell">
              <span class="install-count">{{ formatInstalls(result.installs) }}</span>
            </td>
            <td class="text-right">
              <UiButton
                size="small"
                variant="tonal"
                color="primary"
                :loading="remoteInspectingId === result.id"
                @click="inspectRemote(result.id)"
                >查看并安装</UiButton
              >
            </td>
          </tr>
        </tbody>
      </UiTable>
    </AppDialog>

    <AppDialog
      v-model="remotePreviewDialog"
      :title="`安装 ${remotePreview?.name || ''}`"
      :description="remotePreview?.source"
      size="lg"
      :busy="remoteInstalling"
    >
      <UiAlert v-if="remoteError" type="error" variant="tonal" class="mb-4">{{ remoteError }}</UiAlert>
      <div v-if="remotePreview" class="text-sm mb-4">{{ remotePreview.description }}</div>
      <div v-if="remotePreview" class="flex flex-wrap gap-2 mb-4">
        <UiBadge size="small" label>{{ remotePreview.files.length }} 个文件</UiBadge>
        <UiBadge v-if="remotePreview.license" size="small" label>许可证：{{ remotePreview.license }}</UiBadge>
        <UiBadge v-if="remotePreview.commit" size="small" label :title="remotePreview.commit"
          >提交 {{ remotePreview.commit.slice(0, 7) }}</UiBadge
        >
        <UiBadge v-if="remotePreview.containsScripts" size="small" color="warning" label
          >包含 scripts/，请先审阅</UiBadge
        >
        <UiButton
          v-if="remotePreview.repositoryUrl"
          :href="remotePreview.repositoryUrl"
          target="_blank"
          rel="noreferrer"
          size="small"
          variant="text"
          prepend-icon="mdi-open-in-new"
          >GitHub 仓库</UiButton
        >
      </div>
      <UiSelect
        v-model="remoteTargets"
        :items="writableLocations"
        item-title="agent"
        item-value="key"
        label="安装目标"
        variant="outlined"
        multiple
        chips
        :disabled="remoteInstalling"
      />
      <UiAlert type="warning" density="compact" variant="tonal" class="mb-4"
        >安装当前预览的版本，并更新已启用的托管副本。同名冲突会停止安装；预览过期后需重新预览。远程脚本仅保存为文件。</UiAlert
      >
      <div v-if="remotePreview" class="file-list">
        <div class="text-xs text-muted-foreground mb-2">将安装的文件</div>
        <div v-for="file in remotePreview.files" :key="file.path" class="flex justify-between text-sm">
          <code>{{ file.path }}</code
          ><span class="text-muted-foreground">{{ formatBytes(file.size) }}</span>
        </div>
      </div>
      <template #actions>
        <UiButton variant="text" :disabled="remoteInstalling" @click="remotePreviewDialog = false">取消</UiButton>
        <UiButton
          color="primary"
          :disabled="!remotePreview || remoteTargets.length === 0"
          :loading="remoteInstalling"
          @click="installRemote"
          >安装</UiButton
        >
      </template>
    </AppDialog>

    <AppDialog v-model="batchDialog" title="批量翻译项目技能" size="md">
      <p class="mb-4 text-sm text-muted-foreground">
        将翻译
        {{ batchTargets.length }} 项尚无译文的项目技能。首轮结束后重试失败项一次。关闭弹窗或浏览器后，服务端会继续翻译。
      </p>
      <UiSelect
        v-model="batchChannel"
        :items="translationChannelOptions"
        item-title="title"
        item-value="value"
        label="翻译协议 / 渠道"
        :disabled="batchRunning"
      />
      <UiSelect
        v-model="batchModel"
        searchable
        creatable
        :items="batchModelOptions"
        item-title="title"
        item-value="value"
        label="翻译模型"
        :disabled="batchRunning"
        :loading="batchConfigLoading"
        :error-messages="!batchModel ? batchConfigError || undefined : undefined"
      />
      <p v-if="batchModel && batchConfigError" class="mt-2 text-xs text-muted-foreground">
        上游模型列表未能获取，可以继续使用已选模型。
      </p>
      <UiAlert v-if="batchStartError" type="error" class="mt-3">{{ batchStartError }}</UiAlert>
      <template #actions>
        <UiButton variant="text" @click="batchDialog = false">关闭</UiButton>
        <UiButton
          color="primary"
          :disabled="!batchChannel || !batchModel || !batchTargets.length || translationInProgress || batchRunning"
          :loading="batchStarting"
          @click="translateAllSkills"
          >后台翻译</UiButton
        >
      </template>
    </AppDialog>
    <AppDialog v-model="batchResultDialog" title="批量翻译完成" size="md">
      <p v-if="batchResult" class="text-sm">
        成功 {{ batchResult.succeeded }} 条，失败 {{ batchResult.failures.length }} 条，跳过
        {{ batchResult.skipped }} 条。
      </p>
      <p v-if="batchResult?.failures.length" class="mt-2 text-xs text-muted-foreground">
        失败项最多重试一次，不再自动重试；已落盘的操作不会重复提交。
      </p>
      <ul class="mt-3 space-y-2 text-sm">
        <li v-for="failure in batchResult?.failures" :key="failure.name" class="[overflow-wrap:anywhere]">
          {{ failure.name }}：{{ failure.error }}
        </li>
      </ul>
      <template #actions><UiButton color="primary" @click="batchResultDialog = false">知道了</UiButton></template>
    </AppDialog>

    <AppDialog
      v-model="detailDialog"
      :title="selectedDisplayName"
      :description="selectedSkill ? sourceName(selectedSkill.agent) : undefined"
      size="xl"
    >
      <UiAlert v-if="detailError" type="error" variant="tonal" class="mb-4">{{ detailError }}</UiAlert>
      <UiAlert v-else type="info" variant="tonal" density="compact" class="mb-4">{{
        translationRecommendation
      }}</UiAlert>
      <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-3">
        <UiInput
          v-model="skillNote"
          label="全局备注"
          variant="outlined"
          clearable
          hide-details="auto"
          @keyup.enter="saveSkillNote"
          ><template #append-inner
            ><UiButton
              icon="mdi-content-save"
              title="保存备注"
              aria-label="保存备注"
              variant="text"
              size="small"
              :loading="savingNote"
              :disabled="savingNote"
              @click="saveSkillNote" /></template
        ></UiInput>
        <UiSelect
          v-model="translationChannel"
          :items="translationChannelOptions"
          item-title="title"
          item-value="value"
          label="翻译协议 / 渠道"
          variant="outlined"
          :disabled="translationInProgress || batchRunning"
        />
        <UiSelect
          v-model="translationModel"
          searchable
          creatable
          :items="translationModelOptions"
          item-title="title"
          item-value="value"
          label="翻译模型"
          variant="outlined"
          clearable
          :disabled="translationInProgress || batchRunning"
          :loading="translationModelsLoading"
          :error-messages="!translationModel ? translationModelsError || undefined : undefined"
        />
      </div>
      <div class="grid grid-cols-12 gap-4">
        <div class="min-w-0 col-span-12 lg:col-span-6">
          <UiTextarea
            :model-value="originalContent"
            label="原 Skill（只读）"
            variant="outlined"
            rows="19"
            readonly
            no-resize
            class="skill-editor"
          />
        </div>
        <div class="min-w-0 col-span-12 lg:col-span-6">
          <UiTextarea
            v-model="translatedContent"
            :label="translationLabel"
            variant="outlined"
            rows="19"
            no-resize
            class="skill-editor"
            :readonly="translating || batchRunning"
          />
        </div>
      </div>
      <template #actions>
        <UiButton
          v-if="selectedSkill"
          icon="mdi-folder-marker-outline"
          title="复制路径"
          aria-label="复制 Skill 路径"
          variant="text"
          @click="copySkillPath(selectedSkill.path)"
        />
        <UiButton
          prepend-icon="mdi-content-copy"
          :disabled="!selectedSkill || selectedSkill.readOnly || batchRunning || translationInProgress"
          variant="tonal"
          @click="openSelectedCopy"
          >复制到 Agent</UiButton
        >
        <UiButton
          prepend-icon="mdi-translate"
          color="primary"
          :loading="translating"
          :disabled="
            translationInProgress ||
            batchStarting ||
            batchRunning ||
            !selectedSkill?.valid ||
            selectedSkill.hasTranslation ||
            !originalContent ||
            !translationModel
          "
          @click="translateSkill"
          >翻译并保存</UiButton
        >
        <UiButton
          prepend-icon="mdi-content-save"
          :disabled="!translatedContent || savingBackup || translating || batchRunning"
          :loading="savingBackup"
          @click="() => saveBackup()"
          >保存译文</UiButton
        >
      </template>
    </AppDialog>

    <AppDialog v-model="copyDialog" title="复制 Skill 到其他 Agent" size="md" :busy="copying">
      <div class="text-sm mb-4">
        来源：<strong>{{ selectedSkill?.name }}</strong
        >（{{ selectedSkill ? sourceName(selectedSkill.agent) : '' }}）
      </div>
      <UiSelect
        v-model="copyTargets"
        :items="copyTargetLocations"
        item-title="agent"
        item-value="key"
        label="复制目标"
        variant="outlined"
        multiple
        chips
        :disabled="copying"
      />
      <UiAlert type="warning" density="compact" variant="tonal"
        >将复制 SKILL.md 以及 references、scripts 等附属文件。同名 Skill
        会覆盖目标用户目录；内置和插件来源不可作为复制源或目标。</UiAlert
      >
      <template #actions>
        <UiButton variant="text" :disabled="copying" @click="copyDialog = false">取消</UiButton>
        <UiButton color="primary" :disabled="copyTargets.length === 0" :loading="copying" @click="copySelected"
          >复制</UiButton
        >
      </template>
    </AppDialog>

    <ConfirmDialog
      v-model="deleteDialog"
      title="删除 Skill"
      :message="`确认删除「${selectedSkill?.name || ''}」吗？将移除 ${selectedSkill?.path || ''} 下的整个 Skill 目录。`"
      confirm-text="删除"
      :loading="deleting"
      @confirm="deleteSelected"
    />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiPagination from '@/shared/ui/data/Pagination.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiFileInput from '@/shared/ui/field/FileInput.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import DisclosureSection from '@/shared/components/layout/DisclosureSection.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import {
  api,
  fetchUpstreamModels,
  channelApiByType,
  type ApiTab,
  type AvailabilityCounts,
  type Channel,
  type ManagedSkill,
  type ModelAvailabilityRow,
  type RemoteSkillPreview,
  type SkillLanguage,
  type SkillLocation,
  type SkillSearchResult
} from '@/api'
import { CONVERSATION_PROTOCOLS, getModelAvailability } from '@/features/channels'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'
import { useClipboard } from '@/shared/composables/useClipboard'
import ExtensionsGitDialog from '@/features/extensions/components/ExtensionsGitDialog.vue'
import { translateSkillDocument } from '@/features/extensions/skills/services/translation'
import {
  batchProgress,
  batchResult,
  batchResultLabel,
  batchRunning,
  batchStarting,
  batchStatusError,
  cancelSkillTranslation,
  pendingProjectTranslations,
  refreshSkillTranslation,
  startSkillTranslation,
  translationInProgress
} from '@/features/extensions/skills/composables/translationBatch'

const { showToast } = useToast()
const { copyText } = useClipboard()

type TranslationApiTab = Extract<ApiTab, 'messages' | 'responses' | 'gemini' | 'chat'>
type TranslationChannel = { protocol: TranslationApiTab; channel: Channel; key: string }
type TranslationAvailability = { protocol: TranslationApiTab; row: ModelAvailabilityRow }
const TRANSLATION_PROTOCOLS: readonly TranslationApiTab[] = CONVERSATION_PROTOCOLS
const TRANSLATION_PROTOCOL_LABELS: Record<TranslationApiTab, string> = {
  messages: 'Messages',
  responses: 'Responses',
  gemini: 'Gemini',
  chat: 'Chat'
}

let disposed = false
let translationController: AbortController | null = null
let searchRevision = 0
let inspectRevision = 0
const sourcesExpanded = ref(true)
const loading = ref(false)
const syncing = ref(false)
const consolidating = ref(false)
const togglingKey = ref('')
const importing = ref(false)
const copying = ref(false)
const remoteSearching = ref(false)
const remoteInstalling = ref(false)
const remoteInspectingId = ref('')
const deleting = ref(false)
const error = ref('')
const query = ref('')
const agentFilter = ref('project')
const gitDialog = ref(false)
const batchDialog = ref(false)
const batchStartError = ref('')
const batchChannel = ref<string | null>(null)
const batchResultDialog = ref(false)
const locations = ref<SkillLocation[]>([])
const skills = ref<ManagedSkill[]>([])
const manifest = ref<Record<string, string[]>>({})
const managedAgents = ref<string[]>([])
const translationChannels = ref<TranslationChannel[]>([])
const modelAvailabilityRows = ref<TranslationAvailability[]>([])
const modelAvailabilityLoading = ref(false)
const modelAvailabilityError = ref('')
const translationChannelError = ref('')
let modelAvailabilityController: AbortController | null = null
const importDialog = ref(false)
const importFile = ref<File | null>(null)
const importTargets = ref<string[]>([])
const copyDialog = ref(false)
const copyTargets = ref<string[]>([])
const discoverDialog = ref(false)
const discoverQuery = ref('')
const remoteResults = ref<SkillSearchResult[]>([])
const remoteSearched = ref(false)
const discoverError = ref('')
const remotePreviewDialog = ref(false)
const remotePreview = ref<RemoteSkillPreview | null>(null)
const remoteTargets = ref<string[]>([])
const remoteError = ref('')
const detailDialog = ref(false)
const deleteDialog = ref(false)
const selectedSkill = ref<ManagedSkill | null>(null)
const savingNote = ref(false)

type TranslationState = {
  original: string
  language: SkillLanguage
  translated: string
  error: string
  translating: boolean
  translationRequestId: number
  saving: boolean
  channel: string | null
  model: string
  modelOptions: Array<{ title: string; value: string }>
  modelsLoading: boolean
  modelsError: string
}

const translationStates = reactive<Record<string, TranslationState>>({})
const inactiveTranslationState: TranslationState = {
  original: '',
  language: 'en',
  translated: '',
  error: '',
  translating: false,
  translationRequestId: 0,
  saving: false,
  channel: null,
  model: '',
  modelOptions: [],
  modelsLoading: false,
  modelsError: ''
}
const batchConfig = reactive<TranslationState>({ ...inactiveTranslationState })
const translationModelRequestIds = new WeakMap<TranslationState, number>()
const displayLanguage = ref<SkillLanguage>('zh')

const availabilitySampleSize = (row: AvailabilityCounts) =>
  row.success + row.failure + row.unsupported + row.credential + row.rateLimited
const availabilitySample = (row: ModelAvailabilityRow) => {
  const recentTotal = availabilitySampleSize(row.recent)
  if (recentTotal > 0) return { counts: row.recent, total: recentTotal, period: 0, label: '近 1 小时' }
  const total = availabilitySampleSize(row)
  return total > 0 ? { counts: row, total, period: 1, label: '24 小时' } : null
}
const rankedAvailabilityRows = computed(() =>
  modelAvailabilityRows.value
    .flatMap(({ protocol, row }) => {
      const target = translationChannels.value.find(
        item => item.protocol === protocol && item.channel.id === row.channelId
      )
      const sample = availabilitySample(row)
      return target && sample ? [{ target, row, sample }] : []
    })
    .sort(
      (left, right) =>
        left.sample.period - right.sample.period ||
        right.sample.counts.success / right.sample.total - left.sample.counts.success / left.sample.total ||
        right.sample.total - left.sample.total ||
        right.row.lastSeen - left.row.lastSeen
    )
)
const recommendedTranslation = computed(() => rankedAvailabilityRows.value[0] ?? null)
const translationRecommendation = computed(() => {
  if (modelAvailabilityLoading.value) return '正在读取模型观测数据，并为翻译推荐可用率最高的模型。'
  const best = recommendedTranslation.value
  if (!best) {
    const errors = [translationChannelError.value, modelAvailabilityError.value].filter(Boolean).join('；')
    return errors
      ? `无法读取完整的模型统计：${errors}。将使用渠道默认模型或首个可用模型。`
      : '暂无有效请求样本，将使用渠道默认模型或首个可用模型。'
  }
  const rate = ((100 * best.sample.counts.success) / best.sample.total).toFixed(1)
  const partialError = [translationChannelError.value, modelAvailabilityError.value].filter(Boolean).join('；')
  return `按模型观测推荐：${TRANSLATION_PROTOCOL_LABELS[best.target.protocol]} · ${best.target.channel.name} · ${best.row.model}（${best.sample.label}成功率 ${rate}%，${best.sample.total} 次有效请求）${partialError ? `。部分协议统计不可用：${partialError}` : '。'}`
})
const translationChannelOptions = computed(() =>
  translationChannels.value.map(item => ({
    title: `${TRANSLATION_PROTOCOL_LABELS[item.protocol]} · ${item.channel.name}`,
    value: item.key
  }))
)
const translationChannelByKey = (key: string) => translationChannels.value.find(item => item.key === key)
const availabilityModelTitle = (model: string, channelKey: string) => {
  const item = rankedAvailabilityRows.value.find(
    candidate => candidate.target.key === channelKey && candidate.row.model === model
  )
  if (!item) return model
  const rate = ((100 * item.sample.counts.success) / item.sample.total).toFixed(1)
  return `${model} · ${item.sample.label} ${rate}%（${item.sample.total} 次）`
}

const skillStateKey = (skill: ManagedSkill) => `${skill.locationKey}:${skill.name}`
const ensureTranslationState = (skill: ManagedSkill): TranslationState => {
  const key = skillStateKey(skill)
  if (!translationStates[key]) {
    translationStates[key] = {
      original: '',
      language: 'en',
      translated: '',
      error: '',
      translating: false,
      translationRequestId: 0,
      saving: false,
      channel: null,
      model: '',
      modelOptions: [],
      modelsLoading: false,
      modelsError: ''
    }
  }
  return translationStates[key]
}
const selectedSkillKey = computed(() => (selectedSkill.value ? skillStateKey(selectedSkill.value) : ''))
const activeTranslationState = computed(() =>
  selectedSkill.value ? ensureTranslationState(selectedSkill.value) : inactiveTranslationState
)
const translationLabel = computed(() => {
  const language = selectedSkill.value?.language
  const target = selectedSkill.value?.translationLanguage ?? (language === 'zh' ? 'en' : 'zh')
  return target === 'en' ? '英文译文' : '中文译文'
})
const originalContent = computed({
  get: () => activeTranslationState.value.original,
  set: value => {
    activeTranslationState.value.original = value
  }
})
const translatedContent = computed({
  get: () => activeTranslationState.value.translated,
  set: value => {
    activeTranslationState.value.translated = value
  }
})
const detailError = computed({
  get: () => activeTranslationState.value.error,
  set: value => {
    activeTranslationState.value.error = value
  }
})
const translating = computed(() => activeTranslationState.value.translating)
const savingBackup = computed(() => activeTranslationState.value.saving)
const translationChannel = computed({
  get: () => activeTranslationState.value.channel,
  set: value => {
    const skill = selectedSkill.value
    if (!skill) return
    const state = ensureTranslationState(skill)
    if (state.channel === value) return
    state.channel = value
    state.model = ''
    if (value) void loadTranslationModels(value, state)
  }
})
const translationModel = computed({
  get: () => activeTranslationState.value.model,
  set: value => {
    activeTranslationState.value.model = value
  }
})
const translationModelOptions = computed(() => activeTranslationState.value.modelOptions)
const translationModelsLoading = computed(() => activeTranslationState.value.modelsLoading)
const translationModelsError = computed(() => activeTranslationState.value.modelsError)

const sourceName = (name: string) => name.replace(/\s*用户目录$/, '').replace(/^项目 Skills 目录$/, '项目 Skills')
const agentOptions = computed(() => [
  { title: '全部来源', value: '' },
  ...locations.value.map(location => ({ title: sourceName(location.agent), value: location.key }))
])
const writableLocations = computed(() =>
  locations.value
    .filter(location => !location.readOnly)
    .map(location => ({ ...location, agent: sourceName(location.agent) }))
)
const copyTargetLocations = computed(() =>
  writableLocations.value.filter(location => location.key !== selectedSkill.value?.locationKey)
)
const filteredSkills = computed(() => {
  const keyword = query.value.toLowerCase()
  return skills.value.filter(
    skill =>
      (!agentFilter.value || skill.locationKey === agentFilter.value) &&
      (!keyword ||
        `${skill.name} ${skill.displayName ?? ''} ${skill.description} ${skill.translatedName ?? ''} ${skill.translatedDescription ?? ''} ${skill.agent} ${skill.path}`
          .toLowerCase()
          .includes(keyword))
  )
})
// 搜索覆盖全部技能，表格只挂载当前页，避免为每条记录同时创建多个浮层和按钮。
const skillsPage = ref(1)
const skillsPageSize = 30
const skillsPageCount = computed(() => Math.max(1, Math.ceil(filteredSkills.value.length / skillsPageSize)))
const visibleSkills = computed(() =>
  filteredSkills.value.slice((skillsPage.value - 1) * skillsPageSize, skillsPage.value * skillsPageSize)
)
watch([query, agentFilter], () => {
  skillsPage.value = 1
})
watch(skillsPageCount, count => {
  skillsPage.value = Math.min(skillsPage.value, count)
})
const batchTargets = computed(() => pendingProjectTranslations(skills.value))
const showCounterpart = (skill: ManagedSkill) =>
  skill.language !== displayLanguage.value && skill.translationLanguage === displayLanguage.value
const displaySkillName = (skill: ManagedSkill) =>
  (showCounterpart(skill) ? skill.translatedName : skill.displayName) || skill.name
const displaySkillDescription = (skill: ManagedSkill) =>
  (showCounterpart(skill) ? skill.translatedDescription : undefined) || skill.description
const selectedDisplayName = computed(() => (selectedSkill.value ? displaySkillName(selectedSkill.value) : ''))
const skillNote = computed({
  get: () => selectedSkill.value?.note || '',
  set: value => {
    if (selectedSkill.value) selectedSkill.value.note = value
  }
})

const loadSkills = async () => {
  loading.value = true
  error.value = ''
  try {
    const data = await api.getSkills()
    locations.value = data.locations
    skills.value = data.skills
    manifest.value = data.manifest ?? {}
    managedAgents.value = data.managedAgents ?? []
    try {
      const channelResults = await Promise.allSettled(
        TRANSLATION_PROTOCOLS.map(protocol => channelApiByType(protocol).getChannels())
      )
      translationChannels.value = channelResults.flatMap((result, index) => {
        if (result.status !== 'fulfilled') return []
        const protocol = TRANSLATION_PROTOCOLS[index]
        return result.value.channels
          .filter(
            channel =>
              !channel.excludeFromConversation &&
              channel.status !== 'deleted' &&
              channel.status !== 'disabled' &&
              channel.status !== 'suspended'
          )
          .map(channel => ({ protocol, channel, key: JSON.stringify([protocol, channel.id || channel.index]) }))
      })
      translationChannelError.value = channelResults
        .flatMap((result, index) =>
          result.status === 'rejected'
            ? [
                `${TRANSLATION_PROTOCOL_LABELS[TRANSLATION_PROTOCOLS[index]]} 渠道加载失败：${result.reason instanceof Error ? result.reason.message : String(result.reason)}`
              ]
            : []
        )
        .join('；')
      await loadTranslationAvailability()
    } catch (channelError) {
      translationChannels.value = []
      error.value =
        channelError instanceof Error
          ? `Skills 已加载，但翻译渠道不可用：${channelError.message}`
          : 'Skills 已加载，但翻译渠道不可用'
    }
  } catch (loadError) {
    error.value = loadError instanceof Error ? loadError.message : '加载 Skills 失败'
  } finally {
    loading.value = false
  }
}

const loadTranslationAvailability = async () => {
  modelAvailabilityController?.abort()
  const controller = new AbortController()
  modelAvailabilityController = controller
  modelAvailabilityLoading.value = true
  modelAvailabilityError.value = ''
  try {
    const results = await Promise.allSettled(
      TRANSLATION_PROTOCOLS.map(protocol => getModelAvailability(protocol, controller.signal))
    )
    if (modelAvailabilityController !== controller) return
    modelAvailabilityRows.value = results.flatMap((result, index) =>
      result.status === 'fulfilled'
        ? result.value.rows.map(row => ({ protocol: TRANSLATION_PROTOCOLS[index], row }))
        : []
    )
    modelAvailabilityError.value = results
      .flatMap((result, index) =>
        result.status === 'rejected'
          ? [
              `${TRANSLATION_PROTOCOL_LABELS[TRANSLATION_PROTOCOLS[index]]} 统计读取失败：${result.reason instanceof Error ? result.reason.message : String(result.reason)}`
            ]
          : []
      )
      .join('；')
  } catch (availabilityError) {
    if (controller.signal.aborted || modelAvailabilityController !== controller) return
    modelAvailabilityRows.value = []
    modelAvailabilityError.value = availabilityError instanceof Error ? availabilityError.message : '读取模型可用率失败'
  } finally {
    if (modelAvailabilityController === controller) modelAvailabilityLoading.value = false
  }
}

const AGENT_SHORT_LABELS: Record<string, string> = {
  agents: 'Agents',
  'claude-code': 'Claude',
  codex: 'Codex',
  opencode: 'OpenCode',
  pi: 'pi',
  cursor: 'Cursor'
}

const managedAgentItems = computed(() => {
  const keys =
    managedAgents.value.length > 0
      ? managedAgents.value
      : ['agents', 'claude-code', 'codex', 'opencode', 'pi', 'cursor']
  return keys.map(key => ({
    key,
    label: AGENT_SHORT_LABELS[key] || locations.value.find(item => item.key === key)?.agent || key
  }))
})

const isAgentEnabled = (skillName: string, agentKey: string) => (manifest.value[skillName] ?? []).includes(agentKey)

const toggleSkillAgent = async (skillName: string, agentKey: string) => {
  const lockKey = `${skillName}:${agentKey}`
  if (togglingKey.value) return
  const nextEnabled = !isAgentEnabled(skillName, agentKey)
  togglingKey.value = lockKey
  try {
    const res = await api.toggleSkillAgent(skillName, agentKey, nextEnabled)
    manifest.value = res.manifest ?? {}
    const label = AGENT_SHORT_LABELS[agentKey] || agentKey
    showToast(`${skillName} 已在 ${label} ${nextEnabled ? '启用并同步' : '禁用并移除'}`, 'success')
    await loadSkills()
  } catch (toggleError) {
    showToast(toggleError instanceof Error ? toggleError.message : '切换 Skill Agent 状态失败', 'error')
  } finally {
    togglingKey.value = ''
  }
}

const syncManifest = async () => {
  if (syncing.value) return
  syncing.value = true
  try {
    const res = await api.syncSkills()
    const { synced, removed, skipped, errors } = res.report
    const summary = `同步 ${synced}，清理 ${removed}，跳过 ${skipped}${errors?.length ? `，失败 ${errors.length}` : ''}`
    showToast(summary, errors?.length ? 'error' : 'success')
    await loadSkills()
  } catch (syncError) {
    showToast(syncError instanceof Error ? syncError.message : '同步 Skills 失败', 'error')
  } finally {
    syncing.value = false
  }
}

const defaultInstallTargets = () =>
  writableLocations.value.filter(location => location.key === 'project').map(location => location.key)
const openImport = () => {
  importFile.value = null
  importTargets.value = defaultInstallTargets()
  importDialog.value = true
}

const consolidateInstalledSkills = async () => {
  consolidating.value = true
  try {
    const result = await api.consolidateSkills()
    const conflictText = result.duplicates > 0 ? `，${result.duplicates} 个重复名称保留项目副本` : ''
    showToast(`已统一归档 ${result.total} 个 Skill，新导入 ${result.imported} 个${conflictText}`, 'success')
    await loadSkills()
  } catch (consolidateError) {
    showToast(consolidateError instanceof Error ? consolidateError.message : '导入现有 Skill 失败', 'error')
  } finally {
    consolidating.value = false
  }
}

const loadTranslationModels = async (channelKey: string | null, state: TranslationState) => {
  const requestId = (translationModelRequestIds.get(state) || 0) + 1
  translationModelRequestIds.set(state, requestId)
  const selectedChannel = channelKey ? translationChannelByKey(channelKey) : undefined
  const channel = selectedChannel?.channel
  state.modelOptions = []
  state.modelsError = ''
  state.modelsLoading = false

  if (!channelKey || !channel || !selectedChannel) {
    state.modelsError = '未找到所选协议渠道'
    return
  }
  const apiKey = channel.apiKeys.find(key => key.trim())
  const observedModels = rankedAvailabilityRows.value
    .filter(item => item.target.key === channelKey)
    .map(item => item.row.model)
  const configuredModel = channel.defaultModel?.trim()
  const knownModels = [...new Set([...observedModels, ...(configuredModel ? [configuredModel] : [])])]
  state.modelOptions = knownModels.map(id => ({ title: availabilityModelTitle(id, channelKey), value: id }))
  state.model ||= knownModels[0] ?? ''
  if (!apiKey) {
    state.modelsError = '所选渠道未配置 API Key'
    return
  }

  state.modelsLoading = true
  try {
    const result = await fetchUpstreamModels(channel.baseUrl, apiKey, channel.serviceType, {
      baseUrls: channel.baseUrls,
      insecureSkipVerify: channel.insecureSkipVerify,
      proxyMode: channel.proxyMode,
      proxyUrl: channel.proxyMode === 'custom' ? channel.proxyUrl?.trim() : ''
    })
    if (translationModelRequestIds.get(state) !== requestId) return

    const models = new Set([...knownModels, ...result.data.map(model => model.id.trim()).filter(Boolean)])
    state.modelOptions = [...models].map(id => ({ title: availabilityModelTitle(id, channelKey), value: id }))
    state.model ||= state.modelOptions[0]?.value ?? ''
    if (!models.size) state.modelsError = '未找到可用模型，可手动输入模型 ID'
  } catch (modelError) {
    if (translationModelRequestIds.get(state) !== requestId) return
    state.modelsError = modelError instanceof Error ? modelError.message : '获取模型列表失败，可手动输入模型 ID'
  } finally {
    if (translationModelRequestIds.get(state) === requestId) state.modelsLoading = false
  }
}

const filterByAgent = (locationKey: string) => {
  agentFilter.value = agentFilter.value === locationKey ? '' : locationKey
}

const openDiscover = () => {
  discoverDialog.value = true
  discoverError.value = ''
  remoteSearched.value = false
  remoteResults.value = []
}

const searchRemote = async () => {
  if (!discoverQuery.value.trim()) {
    discoverError.value = '请输入搜索关键词'
    return
  }
  const revision = ++searchRevision
  inspectRevision++
  remoteInspectingId.value = ''
  remotePreviewDialog.value = false
  remotePreview.value = null
  remoteSearching.value = true
  discoverError.value = ''
  remoteSearched.value = false
  try {
    const result = await api.searchSkills(discoverQuery.value.trim())
    if (disposed || revision !== searchRevision) return
    remoteResults.value = result.skills
    remoteSearched.value = true
  } catch (searchError) {
    if (disposed || revision !== searchRevision) return
    discoverError.value = searchError instanceof Error ? searchError.message : '搜索 Skill 失败'
  } finally {
    if (revision === searchRevision) remoteSearching.value = false
  }
}

const inspectRemote = async (id: string) => {
  if (remoteInstalling.value) return
  const revision = ++inspectRevision
  remoteInspectingId.value = id
  remoteError.value = ''
  try {
    const preview = await api.inspectRemoteSkill(id)
    if (disposed || revision !== inspectRevision) return
    remotePreview.value = preview
    remoteTargets.value = defaultInstallTargets()
    remotePreviewDialog.value = true
  } catch (inspectError) {
    if (disposed || revision !== inspectRevision) return
    discoverError.value = inspectError instanceof Error ? inspectError.message : '读取远程 Skill 失败'
  } finally {
    if (revision === inspectRevision) remoteInspectingId.value = ''
  }
}

const installRemote = async () => {
  if (remoteInstalling.value || !remotePreview.value || remoteTargets.value.length === 0) return
  const preview = remotePreview.value
  const targets = [...remoteTargets.value]
  remoteInstalling.value = true
  remoteError.value = ''
  try {
    const result = await api.installRemoteSkill(preview.id, targets, preview.commit)
    showToast(`已安装 ${result.name}`, 'success')
    remotePreviewDialog.value = false
    await loadSkills()
  } catch (installError) {
    remoteError.value = installError instanceof Error ? installError.message : '远程安装失败'
  } finally {
    remoteInstalling.value = false
  }
}

const importSelected = async () => {
  if (!importFile.value) return
  importing.value = true
  try {
    const file = importFile.value
    const base64 = await fileToBase64(file)
    const result = await api.importSkill(file.name, base64, importTargets.value)
    showToast(`已安装 ${result.name}`, 'success')
    importDialog.value = false
    await loadSkills()
  } catch (importError) {
    showToast(importError instanceof Error ? importError.message : '导入 Skill 失败', 'error')
  } finally {
    importing.value = false
  }
}

const openSkill = async (skill: ManagedSkill, translateImmediately = false) => {
  selectedSkill.value = skill
  const state = ensureTranslationState(skill)
  if (state.translating) {
    detailDialog.value = true
    return
  }
  if (!state.channel || !translationChannelByKey(state.channel)) {
    const recommendation = recommendedTranslation.value
    const fallbackChannel =
      translationChannels.value.find(item => item.channel.defaultModel?.trim()) ?? translationChannels.value[0]
    const defaultChannel = recommendation?.target ?? fallbackChannel
    state.channel = defaultChannel?.key ?? null
    state.model = recommendation?.row.model ?? ''
    state.modelOptions = []
  }
  const modelLoad =
    state.channel && !state.modelOptions.length && !state.modelsLoading
      ? loadTranslationModels(state.channel, state)
      : undefined
  state.error = ''
  detailDialog.value = true
  try {
    const [contentResult, backupResult] = await Promise.all([
      api.getSkillContent(skill.locationKey, skill.name),
      api.getLatestSkillBackup(skill.locationKey, skill.name)
    ])
    const originalChanged = Boolean(state.original && state.original !== contentResult.content)
    if (originalChanged) {
      state.translationRequestId++
      state.translating = false
      state.translated = ''
    }
    state.original = contentResult.content
    state.language = contentResult.language
    skill.language = contentResult.language
    skill.hasTranslation = backupResult.found
    skill.translatedName = backupResult.translatedName
    skill.translatedDescription = backupResult.translatedDescription
    skill.translationLanguage = backupResult.translationLanguage
    if (backupResult.found && backupResult.translated) {
      state.translated = backupResult.translated
    } else if (originalChanged) {
      state.error = '原 Skill 已更新，旧译文已清除'
    }
  } catch (loadError) {
    state.error = loadError instanceof Error ? loadError.message : '读取 Skill 失败'
    return
  }
  if (translateImmediately && !skill.hasTranslation && !batchRunning.value && !translationInProgress.value) {
    if (!state.model) await modelLoad
    if (detailDialog.value && selectedSkillKey.value === skillStateKey(skill)) await translateSkill()
  }
}

const openCopy = (skill: ManagedSkill) => {
  if (skill.readOnly) return
  selectedSkill.value = skill
  copyTargets.value = []
  copyDialog.value = true
}
const openSelectedCopy = () => {
  if (selectedSkill.value) openCopy(selectedSkill.value)
}

const saveSkillNote = async () => {
  const skill = selectedSkill.value
  if (!skill) return
  savingNote.value = true
  try {
    const result = await api.updateSkillNote(skill.name, skill.note || '')
    skills.value.forEach(item => {
      if (item.name === skill.name) item.note = result.note
    })
    showToast(result.note ? '全局备注已保存' : '全局备注已清空', 'success')
  } catch (noteError) {
    showToast(noteError instanceof Error ? noteError.message : '保存备注失败', 'error')
  } finally {
    savingNote.value = false
  }
}

const copySelected = async () => {
  if (!selectedSkill.value || copyTargets.value.length === 0) return
  copying.value = true
  try {
    const result = await api.copySkill(selectedSkill.value.locationKey, selectedSkill.value.name, copyTargets.value)
    showToast(`已复制 ${result.name} 到 ${result.targets.length} 个 Agent`, 'success')
    copyDialog.value = false
    await loadSkills()
  } catch (copyError) {
    showToast(copyError instanceof Error ? copyError.message : '复制 Skill 失败', 'error')
  } finally {
    copying.value = false
  }
}

const normalizeTranslationModel = (value: unknown): string => {
  if (typeof value === 'string') return value.trim()
  if (!value || typeof value !== 'object') return ''

  const record = value as Record<string, unknown>
  for (const key of ['value', 'id', 'title']) {
    const candidate = record[key]
    if (typeof candidate === 'string' && candidate.trim()) return candidate.trim()
  }
  return ''
}

const performTranslation = async (skill: ManagedSkill): Promise<void> => {
  const state = ensureTranslationState(skill)
  if (!state.original || state.translating || translationInProgress.value) {
    state.error = !state.original ? 'Skill 内容为空，无法翻译' : '已有翻译任务正在执行'
    throw new Error(state.error)
  }
  const original = state.original
  const requestId = ++state.translationRequestId
  const accessKey = useAuthStore().apiKey
  if (!accessKey) {
    state.error = '未找到管理界面访问密钥'
    throw new Error(state.error)
  }
  translationInProgress.value = true
  state.translating = true
  state.error = ''
  try {
    const model = normalizeTranslationModel(state.model)
    const selectedChannel = state.channel ? translationChannelByKey(state.channel) : undefined
    if (!selectedChannel) throw new Error('请选择可用的文本生成协议渠道后再翻译')
    if (!model) throw new Error('请选择或输入用于翻译的模型 ID')
    const protocol = selectedChannel.protocol
    const controller = new AbortController()
    translationController = controller
    const auth = useAuthStore()
    const generation = auth.generation
    const translated = await translateSkillDocument({
      protocol,
      channelIndex: selectedChannel.channel.index,
      model,
      original,
      language: state.language === 'zh' ? '英文' : '简体中文',
      signal: controller.signal,
      onUpdate: value => {
        if (!disposed && state.translationRequestId === requestId) state.translated = value
      }
    })
    controller.signal.throwIfAborted()
    if (disposed || auth.generation !== generation || state.translationRequestId !== requestId)
      throw new Error('页面、会话或原文已变更，本次翻译未保存')
    state.translated = translated
    await saveBackup({ automatic: true, skill, state, original })
  } catch (translationError) {
    if (disposed) return
    if (state.translationRequestId !== requestId) throw new Error('原 Skill 已变更，本次翻译未保存')
    state.error =
      translationError instanceof DOMException && translationError.name === 'AbortError'
        ? '翻译已取消；已接收内容仅保留为草稿'
        : translationError instanceof DOMException && translationError.name === 'TimeoutError'
          ? '翻译超过等待时间；已接收内容仅保留为草稿'
          : translationError instanceof Error
            ? translationError.message
            : '翻译失败'
    showToast(`${skill.name}：${state.error}`, 'error')
    throw new Error(state.error)
  } finally {
    translationController = null
    translationInProgress.value = false
    if (state.translationRequestId === requestId) state.translating = false
  }
}

const translateSkill = async () => {
  const skill = selectedSkill.value
  if (!skill?.valid || skill.hasTranslation || batchStarting.value || batchRunning.value || translationInProgress.value)
    return
  try {
    await performTranslation(skill)
    return true
  } catch (error) {
    ensureTranslationState(skill).error = error instanceof Error ? error.message : String(error)
    return false
  }
}
const translateSkillFromList = (skill: ManagedSkill) => {
  if (!skill.valid || skill.hasTranslation || batchStarting.value || batchRunning.value || translationInProgress.value)
    return
  return openSkill(skill, true)
}

const batchModel = computed({
  get: () => batchConfig.model,
  set: value => {
    batchConfig.model = value
  }
})
const batchModelOptions = computed(() => batchConfig.modelOptions)
const batchConfigLoading = computed(() => batchConfig.modelsLoading)
const batchConfigError = computed(() => batchConfig.modelsError)
watch([batchDialog, batchChannel], ([open, channel]) => {
  if (!open) return
  const recommended = recommendedTranslation.value
  batchConfig.model = recommended && recommended.target.key === channel ? recommended.row.model : ''
  void loadTranslationModels(channel, batchConfig)
})

const openBatchTranslation = () => {
  if (translationInProgress.value || batchRunning.value || !batchTargets.value.length) return
  const recommendation = recommendedTranslation.value
  batchStartError.value = ''
  batchChannel.value =
    recommendation?.target.key ??
    translationChannels.value.find(item => item.channel.defaultModel?.trim())?.key ??
    translationChannels.value[0]?.key ??
    null
  batchDialog.value = true
}

const translateAllSkills = async () => {
  const selected = batchChannel.value ? translationChannelByKey(batchChannel.value) : undefined
  const model = normalizeTranslationModel(batchModel.value)
  if (!selected?.channel.id) {
    batchStartError.value = '所选渠道缺少稳定标识，请刷新渠道后重试'
    return
  }
  if (batchStarting.value || batchRunning.value || translationInProgress.value || !model || !batchTargets.value.length)
    return
  batchStartError.value = ''
  try {
    await startSkillTranslation(selected.channel.id, selected.protocol, model)
    batchDialog.value = false
  } catch (error) {
    batchStartError.value = error instanceof Error ? error.message : String(error)
  }
}

const saveBackup = async ({
  automatic = false,
  skill = selectedSkill.value,
  state = skill ? ensureTranslationState(skill) : undefined,
  original = state?.original || ''
}: { automatic?: boolean; skill?: ManagedSkill | null; state?: TranslationState; original?: string } = {}) => {
  if (!skill || !state?.translated.trim() || !original) {
    if (automatic) throw new Error('翻译完成，但没有可保存的译文内容')
    return
  }
  state.saving = true
  try {
    const channel = typeof state.channel === 'string' ? translationChannelByKey(state.channel) : undefined
    const channelName = channel
      ? `${TRANSLATION_PROTOCOL_LABELS[channel.protocol]} · ${channel.channel.name}`
      : undefined
    const result = await api.backupSkill({
      locationKey: skill.locationKey,
      name: skill.name,
      original,
      translated: state.translated,
      model: state.model,
      channelName,
      automatic
    })
    const current = skills.value.find(item => skillStateKey(item) === skillStateKey(skill))
    const presentation = {
      hasTranslation: true,
      language: state.language,
      translatedName: result.translatedName,
      translatedDescription: result.translatedDescription,
      translationLanguage: result.translationLanguage
    }
    Object.assign(skill, presentation)
    if (current && current !== skill) Object.assign(current, presentation)
    showToast(automatic ? '翻译完成，已自动保存' : `翻译备份已保存到 ${result.path}`, 'success')
  } catch (saveError) {
    const message =
      saveError instanceof Error ? `译文已生成，但保存失败：${saveError.message}` : '译文已生成，但保存备份失败'
    if (automatic) throw new Error(message)
    state.error = message
  } finally {
    state.saving = false
  }
}

const confirmDelete = (skill: ManagedSkill) => {
  selectedSkill.value = skill
  deleteDialog.value = true
}
const deleteSelected = async () => {
  if (!selectedSkill.value) return
  deleting.value = true
  try {
    await api.deleteSkill(selectedSkill.value.locationKey, selectedSkill.value.name)
    showToast('Skill 已删除', 'success')
    deleteDialog.value = false
    await loadSkills()
  } catch (deleteError) {
    showToast(deleteError instanceof Error ? deleteError.message : '删除失败', 'error')
  } finally {
    deleting.value = false
  }
}

const fileToBase64 = (file: File) =>
  new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.onload = () => {
      const value = String(reader.result || '')
      resolve(value.substring(value.indexOf(',') + 1))
    }
    reader.readAsDataURL(file)
  })
const copySkillPath = (path: string) => {
  void copyText(path, '路径已复制')
}
const installCountFormatter = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const formatInstalls = (value: number) => installCountFormatter.format(value || 0).replace(/\s+/g, '')
const formatBytes = (value: number) =>
  value < 1024
    ? `${value} B`
    : value < 1024 * 1024
      ? `${(value / 1024).toFixed(1)} KB`
      : `${(value / (1024 * 1024)).toFixed(1)} MB`
watch(batchResult, (result, previous) => {
  if (!result || result.id === previous?.id) return
  void loadSkills()
})
onMounted(() => {
  void loadSkills()
  void refreshSkillTranslation()
})
watch(
  remotePreviewDialog,
  open => {
    if (!open) {
      inspectRevision++
      remoteInspectingId.value = ''
    }
  },
  { flush: 'sync' }
)
watch(
  discoverDialog,
  open => {
    if (!open) {
      searchRevision++
      inspectRevision++
      remoteSearching.value = false
      remoteInspectingId.value = ''
    }
  },
  { flush: 'sync' }
)
onUnmounted(() => {
  disposed = true
  searchRevision++
  inspectRevision++
  translationController?.abort()
  modelAvailabilityController?.abort()
})
</script>

<style scoped>
@reference "@/styles/index.css";

.skills-page {
  @apply max-w-360 [margin:0_auto];
}
.skills-header-sticky {
  @apply sticky [top:var(--app-header-height,_0px)] [z-index:4] bg-background [box-shadow:0_-32px_0_0_var(--background)];
}
.locations-strip {
  @apply min-w-0;
}
.locations-heading {
  @apply flex items-center justify-between gap-3 mb-3;
}
.location-grid {
  @apply grid gap-2 [grid-template-columns:repeat(auto-fill,_minmax(min(100%,_240px),_1fr))];
}
.location-card {
  @apply relative min-w-0 min-h-12 rounded-control [border:1px_solid_color-mix(in_srgb,_var(--outline)_22%,_transparent)] [background:color-mix(in_srgb,_var(--foreground)_2.5%,_transparent)] transition-colors;
}
.location-card:hover {
  @apply [background:color-mix(in_srgb,_var(--primary)_5%,_transparent)];
}
.location-card--active {
  @apply [border-color:var(--primary)] [background:color-mix(in_srgb,_var(--primary)_7%,_transparent)];
}
.location-filter {
  @apply absolute inset-0 flex items-center gap-2 rounded-control pl-3 pr-9 text-left text-xs;
}
.location-name {
  @apply min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap font-medium;
}
.location-path-button {
  @apply absolute right-1 top-1/2 z-1 -translate-y-1/2;
}
.location-chip,
.source-chip {
  @apply cursor-pointer;
}
.location-chip:hover,
.source-chip:hover {
  @apply [filter:brightness(0.96)];
}
.location-chip--active,
.source-chip--active {
  @apply [outline:2px_solid_var(--primary)] [outline-offset:1px];
}
.skills-list-card {
  @apply overflow-hidden;
}
.skills-table {
  @apply [max-height:min(72vh,_760px)] overflow-auto;
}
.skills-table :deep(table) {
  @apply min-w-285 [table-layout:fixed];
}
.skills-table :deep(thead th) {
  @apply sticky top-0 [z-index:2] whitespace-nowrap [background:var(--surface)] [box-shadow:0_1px_0_color-mix(in_srgb,_var(--outline)_28%,_transparent)];
}
.skill-column {
  width: 30%;
}
.source-column {
  width: 13%;
}
.status-column {
  width: 9%;
}
.agents-column {
  width: 22%;
}
.path-column {
  width: 5%;
}
.actions-column {
  width: 21%;
}
.status-cell {
  @apply whitespace-nowrap;
}
.skills-pagination {
  @apply flex items-center justify-between flex-wrap gap-3 [padding:12px_16px] [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)];
}
.skill-name-button {
  @apply [background:none] border-0 [color:inherit] cursor-pointer [font:inherit] p-0 text-left no-underline;
}
.skill-name-button:hover {
  @apply text-primary underline;
}
.skill-description {
  @apply max-w-110 mt-1 line-clamp-2 whitespace-normal leading-5;
}
.agent-toggles-cell {
  @apply min-w-72;
}
.agent-toggle-chip {
  @apply min-h-8 cursor-pointer whitespace-nowrap;
}
.skill-editor :deep(textarea) {
  @apply overflow-y-auto;
}
.remote-results-table {
  @apply [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)];
}
.install-count-cell,
.install-count {
  @apply min-w-21 whitespace-nowrap;
}
.source-link {
  @apply text-primary no-underline;
}
.source-link:hover {
  @apply underline;
}
.file-list {
  @apply max-h-55 overflow-y-auto [border:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] rounded-badge p-3;
}
@media (max-width: 600px) {
  .locations-heading {
    @apply items-start;
  }
  .skill-description {
    @apply max-w-45;
  }
  .skills-table {
    @apply [max-height:70vh];
  }
}
</style>
