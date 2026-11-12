// Codex models.json 条目解析与保留未知字段的读写。
package clients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
)

// codexReasoningLevel 与 codexDefaultReasoningLevels 是自定义模型的固定推理档位骨架
// （与 DeepSeek 官方 Codex 接入文档的 supported_reasoning_levels 一致）。
type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

var codexDefaultReasoningLevels = []codexReasoningLevel{
	{Effort: "low", Description: "Fast responses with lighter reasoning"},
	{Effort: "high", Description: "Extra high reasoning depth for complex problems"},
	{Effort: "max", Description: "Maximum reasoning depth for the hardest problems"},
}

// codexEntryModelMessages 只解析自定义条目关心的指令字段，其余子结构原样透传。
type codexEntryModelMessages struct {
	InstructionsTemplate string `json:"instructions_template"`
}

// codexFieldsHaveInstructions 判断条目是否满足 Codex 的指令硬性要求：
// 携带 base_instructions 或含非空 instructions_template 的 model_messages。
func codexFieldsHaveInstructions(fields map[string]json.RawMessage) bool {
	if bi, ok := fields["base_instructions"]; ok && !bytes.Equal(bytes.TrimSpace(bi), []byte("null")) {
		return true
	}
	mm, ok := fields["model_messages"]
	if !ok || bytes.Equal(bytes.TrimSpace(mm), []byte("null")) {
		return false
	}
	var parsed codexEntryModelMessages
	return json.Unmarshal(mm, &parsed) == nil && parsed.InstructionsTemplate != ""
}

// codexCatalogModelEntry 是本功能管理的 models.json 条目字段全集。
// ModelMessages 原样透传（从内置清单复制，结构随 codex 版本演进，不逐字段建模）。
type codexCatalogModelEntry struct {
	Slug                           string                `json:"slug"`
	PreferWebsockets               bool                  `json:"prefer_websockets"`
	SupportVerbosity               bool                  `json:"support_verbosity"`
	DefaultVerbosity               string                `json:"default_verbosity"`
	ApplyPatchToolType             string                `json:"apply_patch_tool_type"`
	WebSearchToolType              string                `json:"web_search_tool_type"`
	InputModalities                []string              `json:"input_modalities"`
	SupportsImageDetailOriginal    bool                  `json:"supports_image_detail_original"`
	TruncationPolicy               codexTruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls      bool                  `json:"supports_parallel_tool_calls"`
	ToolMode                       *string               `json:"tool_mode"`
	MultiAgentVersion              string                `json:"multi_agent_version"`
	UseResponsesLite               bool                  `json:"use_responses_lite"`
	IncludeSkillsUsageInstructions bool                  `json:"include_skills_usage_instructions"`
	AutoReviewModelOverride        *string               `json:"auto_review_model_override"`
	ContextWindow                  int64                 `json:"context_window"`
	MaxContextWindow               int64                 `json:"max_context_window"`
	EffectiveContextWindowPercent  int                   `json:"effective_context_window_percent"`
	AutoCompactTokenLimit          *int64                `json:"auto_compact_token_limit"`
	CompHash                       string                `json:"comp_hash"`
	ReasoningSummaryFormat         string                `json:"reasoning_summary_format"`
	DefaultReasoningSummary        string                `json:"default_reasoning_summary"`
	DisplayName                    string                `json:"display_name"`
	Description                    string                `json:"description"`
	DefaultReasoningLevel          string                `json:"default_reasoning_level"`
	SupportedReasoningLevels       []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                      string                `json:"shell_type"`
	Visibility                     string                `json:"visibility"`
	MinimalClientVersion           string                `json:"minimal_client_version"`
	SupportedInAPI                 bool                  `json:"supported_in_api"`
	AvailabilityNux                *string               `json:"availability_nux"`
	Upgrade                        *string               `json:"upgrade"`
	Priority                       int                   `json:"priority"`
	ExperimentalSupportedTools     []string              `json:"experimental_supported_tools"`
	SupportsSearchTool             bool                  `json:"supports_search_tool"`
	DefaultServiceTier             *string               `json:"default_service_tier"`
	SupportsReasoningSummaries     bool                  `json:"supports_reasoning_summaries"`
	ModelMessages                  json.RawMessage       `json:"model_messages"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// codexModelEntryMeta 从条目中提取的最小元数据集合，用于列表展示与更新校验。
type codexModelEntryMeta struct {
	Slug                     string                `json:"slug"`
	DisplayName              string                `json:"display_name"`
	Description              string                `json:"description"`
	InputModalities          []string              `json:"input_modalities"`
	ContextWindow            int64                 `json:"context_window"`
	MaxContextWindow         int64                 `json:"max_context_window"`
	DefaultReasoningLevel    string                `json:"default_reasoning_level"`
	SupportedReasoningLevels []codexReasoningLevel `json:"supported_reasoning_levels"`
	Priority                 int                   `json:"priority"`
	Visibility               string                `json:"visibility"`
	SupportsSearchTool       bool                  `json:"supports_search_tool"`
}

func (meta codexModelEntryMeta) hasEffort(effort string) bool {
	for _, level := range meta.SupportedReasoningLevels {
		if level.Effort == effort {
			return true
		}
	}
	return false
}

func (meta codexModelEntryMeta) view(builtin bool) codexModelView {
	levels := make([]string, 0, len(meta.SupportedReasoningLevels))
	for _, level := range meta.SupportedReasoningLevels {
		levels = append(levels, level.Effort)
	}
	return codexModelView{
		Slug:                  meta.Slug,
		DisplayName:           meta.DisplayName,
		Description:           meta.Description,
		SupportsImage:         slices.Contains(meta.InputModalities, "image"),
		ContextWindow:         meta.ContextWindow,
		DefaultReasoningLevel: meta.DefaultReasoningLevel,
		ReasoningLevels:       levels,
		SupportsSearchTool:    meta.SupportsSearchTool,
		Priority:              meta.Priority,
		Visibility:            meta.Visibility,
		Builtin:               builtin,
	}
}

// codexModelView 是 GET /settings/codex 返回的模型条目视图。
type codexModelView struct {
	Slug                  string   `json:"slug"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsImage         bool     `json:"supportsImage"`
	ContextWindow         int64    `json:"contextWindow"`
	DefaultReasoningLevel string   `json:"defaultReasoningLevel"`
	ReasoningLevels       []string `json:"reasoningLevels"`
	SupportsSearchTool    bool     `json:"supportsSearchTool"`
	Priority              int      `json:"priority"`
	Visibility            string   `json:"visibility"`
	Builtin               bool     `json:"builtin"`
}

type codexCatalogWriteResult struct {
	previous []byte
	data     []byte
}

func upsertCodexModelCatalog(modelsPath string, input codexModelUpsertInput, builtin *codexBuiltinCatalog) (codexCatalogWriteResult, error) {
	catalog, err := readCodexModelCatalog(modelsPath)
	if err != nil {
		return codexCatalogWriteResult{}, err
	}
	if err := catalog.upsertModel(input, builtin); err != nil {
		return codexCatalogWriteResult{}, err
	}
	catalog.ensureBuiltinEntries(builtin)
	encoded, err := catalog.encode()
	if err != nil {
		return codexCatalogWriteResult{}, fmt.Errorf("编码 models.json 失败: %w", err)
	}
	return codexCatalogWriteResult{previous: catalog.raw, data: encoded}, nil
}

// deleteCodexModelCatalog 删除条目，第二返回值表示删除后目录是否为空
// （空目录会让 Codex 拒绝启动，调用方需同步回退 config.toml 的 model_catalog_json）。
func deleteCodexModelCatalog(modelsPath, slug string) (codexCatalogWriteResult, bool, error) {
	catalog, err := readCodexModelCatalog(modelsPath)
	if err != nil {
		return codexCatalogWriteResult{}, false, err
	}
	if err := catalog.deleteModel(slug); err != nil {
		return codexCatalogWriteResult{}, false, err
	}
	empty := len(catalog.entries) == 0
	encoded, err := catalog.encode()
	if err != nil {
		return codexCatalogWriteResult{}, false, fmt.Errorf("编码 models.json 失败: %w", err)
	}
	return codexCatalogWriteResult{previous: catalog.raw, data: encoded}, empty, nil
}

// codexModelCatalog 是 models.json 的内存表示：
// doc 保留顶层未知字段，entries 为 models 数组的原始条目。
type codexModelCatalog struct {
	doc     map[string]json.RawMessage
	entries []json.RawMessage
	raw     []byte
	exists  bool
}

func readCodexModelCatalog(path string) (*codexModelCatalog, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &codexModelCatalog{doc: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, newCodexInputError("models.json JSON 格式无效: %v", err)
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	catalog := &codexModelCatalog{doc: doc, raw: raw, exists: true}
	if modelsRaw, ok := doc["models"]; ok && !bytes.Equal(bytes.TrimSpace(modelsRaw), []byte("null")) {
		if err := json.Unmarshal(modelsRaw, &catalog.entries); err != nil {
			return nil, newCodexInputError("models.json 的 models 字段无效: %v", err)
		}
	}
	return catalog, nil
}

func codexCatalogEntryMeta(raw json.RawMessage) (codexModelEntryMeta, error) {
	var meta codexModelEntryMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return codexModelEntryMeta{}, newCodexInputError("models.json 中存在无法解析的模型条目: %v", err)
	}
	return meta, nil
}

func (catalog *codexModelCatalog) findModel(slug string) (codexModelEntryMeta, error) {
	for _, entry := range catalog.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			return codexModelEntryMeta{}, err
		}
		if meta.Slug == slug {
			return meta, nil
		}
	}
	return codexModelEntryMeta{}, newCodexInputError("模型 %s 不存在", slug)
}

func (catalog *codexModelCatalog) modelViews(builtin *codexBuiltinCatalog) []codexModelView {
	views := make([]codexModelView, 0, len(catalog.entries))
	for _, entry := range catalog.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			continue
		}
		views = append(views, meta.view(builtin != nil && builtin.has(meta.Slug)))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Slug < views[j].Slug })
	return views
}

// ensureBuiltinEntries 补齐缺失的内置条目，返回是否有新增。
// model_catalog_json 对 Codex 是整体替换语义：设置后未写入的内置模型将不可见，
// 因此每次写入都保证内置清单完整；已存在的同 slug 条目保持不动（尊重用户手工修改）。
func (catalog *codexModelCatalog) ensureBuiltinEntries(builtin *codexBuiltinCatalog) bool {
	if builtin == nil {
		return false
	}
	existing := make(map[string]struct{}, len(catalog.entries))
	for _, entry := range catalog.entries {
		if meta, err := codexCatalogEntryMeta(entry); err == nil {
			existing[meta.Slug] = struct{}{}
		}
	}
	changed := false
	for _, entry := range builtin.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			continue
		}
		if _, ok := existing[meta.Slug]; ok {
			continue
		}
		catalog.entries = append(catalog.entries, entry)
		changed = true
	}
	return changed
}

// upsertModel 按 slug 新增或更新条目：
// 新增时从内置清单复制 model_messages 并套用固定骨架；更新时仅覆盖可调字段，保留其余字段，
// 缺失指令字段的坏条目（旧版本写入）会顺带补全。
func (catalog *codexModelCatalog) upsertModel(input codexModelUpsertInput, builtin *codexBuiltinCatalog) error {
	for index, entry := range catalog.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			return err
		}
		if meta.Slug != input.Slug {
			continue
		}
		return catalog.updateModel(index, meta, input, builtin)
	}

	if builtin == nil {
		return fmt.Errorf("内置模型清单不可用，无法为新条目复制 model_messages")
	}
	messages, err := builtin.modelMessages()
	if err != nil {
		return fmt.Errorf("获取指令模板失败: %w", err)
	}
	entry := buildCodexModelEntry(input, messages)
	// 统一走 map 序列化，保证多次保存的键序稳定（按字母序）。
	fields, err := codexEntryFieldMap(entry)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("编码模型条目失败: %w", err)
	}
	catalog.entries = append(catalog.entries, encoded)
	return nil
}

func (catalog *codexModelCatalog) updateModel(index int, meta codexModelEntryMeta, input codexModelUpsertInput, builtin *codexBuiltinCatalog) error {
	if input.DefaultReasoningLevel != "" && !meta.hasEffort(input.DefaultReasoningLevel) {
		return newCodexInputError("模型 %s 不支持推理档位 %q", input.Slug, input.DefaultReasoningLevel)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(catalog.entries[index], &fields); err != nil {
		return newCodexInputError("解析模型 %s 条目失败: %v", input.Slug, err)
	}
	if !codexFieldsHaveInstructions(fields) {
		if builtin == nil {
			return fmt.Errorf("模型 %s 缺少指令字段且内置清单不可用，无法修复条目", input.Slug)
		}
		messages, err := builtin.modelMessages()
		if err != nil {
			return fmt.Errorf("获取指令模板失败: %w", err)
		}
		fields["model_messages"] = messages
	}
	displayName := input.DisplayName
	if displayName == "" {
		displayName = input.Slug
	}
	maxContextWindow := meta.MaxContextWindow
	if maxContextWindow < input.ContextWindow {
		maxContextWindow = input.ContextWindow
	}
	overrides := map[string]any{
		"display_name":                   displayName,
		"input_modalities":               codexInputModalities(input.SupportsImage),
		"supports_image_detail_original": input.SupportsImage,
		"context_window":                 input.ContextWindow,
		"max_context_window":             maxContextWindow,
	}
	if input.Description != "" {
		overrides["description"] = input.Description
	}
	if input.DefaultReasoningLevel != "" {
		overrides["default_reasoning_level"] = input.DefaultReasoningLevel
	}
	for key, value := range overrides {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("编码模型字段 %s 失败: %w", key, err)
		}
		fields[key] = encoded
	}
	updated, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("编码模型条目失败: %w", err)
	}
	catalog.entries[index] = updated
	return nil
}

func (catalog *codexModelCatalog) deleteModel(slug string) error {
	for index, entry := range catalog.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			return err
		}
		if meta.Slug == slug {
			catalog.entries = append(catalog.entries[:index], catalog.entries[index+1:]...)
			return nil
		}
	}
	return newCodexInputError("模型 %s 不存在", slug)
}

func (catalog *codexModelCatalog) encode() ([]byte, error) {
	if catalog.doc == nil {
		catalog.doc = map[string]json.RawMessage{}
	}
	entries := catalog.entries
	if entries == nil {
		entries = []json.RawMessage{}
	}
	encodedModels, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	catalog.doc["models"] = encodedModels
	encoded, err := json.MarshalIndent(catalog.doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func codexEntryFieldMap(entry codexCatalogModelEntry) (map[string]json.RawMessage, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func codexInputModalities(supportsImage bool) []string {
	if supportsImage {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

// buildCodexModelEntry 基于用户输入与固定骨架生成完整条目；messages 为从内置清单复制的指令模板。
// 固定骨架决策：supports_search_tool=false（web_search 是 OpenAI 特有工具，第三方模型默认不启用）、
// priority=99（自定义模型排在内置模型之后）、推理档位固定 low/high/max、默认档位 high。
func buildCodexModelEntry(input codexModelUpsertInput, messages json.RawMessage) codexCatalogModelEntry {
	displayName := input.DisplayName
	if displayName == "" {
		displayName = input.Slug
	}
	reasoningLevel := input.DefaultReasoningLevel
	if reasoningLevel == "" {
		reasoningLevel = "high"
	}
	return codexCatalogModelEntry{
		Slug:                           input.Slug,
		PreferWebsockets:               false,
		SupportVerbosity:               true,
		DefaultVerbosity:               "low",
		ApplyPatchToolType:             "freeform",
		WebSearchToolType:              "text",
		InputModalities:                codexInputModalities(input.SupportsImage),
		SupportsImageDetailOriginal:    input.SupportsImage,
		TruncationPolicy:               codexTruncationPolicy{Mode: "tokens", Limit: 10000},
		SupportsParallelToolCalls:      true,
		ToolMode:                       nil,
		MultiAgentVersion:              "v2",
		UseResponsesLite:               false,
		IncludeSkillsUsageInstructions: false,
		AutoReviewModelOverride:        nil,
		ContextWindow:                  input.ContextWindow,
		MaxContextWindow:               input.ContextWindow,
		EffectiveContextWindowPercent:  95,
		AutoCompactTokenLimit:          nil,
		CompHash:                       "3000",
		ReasoningSummaryFormat:         "experimental",
		DefaultReasoningSummary:        "none",
		DisplayName:                    displayName,
		Description:                    input.Description,
		DefaultReasoningLevel:          reasoningLevel,
		SupportedReasoningLevels:       codexDefaultReasoningLevels,
		ShellType:                      "shell_command",
		Visibility:                     "list",
		MinimalClientVersion:           "0.144.0",
		SupportedInAPI:                 true,
		AvailabilityNux:                nil,
		Upgrade:                        nil,
		Priority:                       99,
		ExperimentalSupportedTools:     []string{},
		SupportsSearchTool:             false,
		DefaultServiceTier:             nil,
		SupportsReasoningSummaries:     true,
		ModelMessages:                  messages,
	}
}
