package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

var dshSettingsMu sync.Mutex

// DSHModel 表示 llm-pi-ai 下某个 provider 的一个模型条目。
type DSHModel struct {
	ID               string      `yaml:"id"               json:"id"`
	Name             string      `yaml:"name,omitempty"    json:"name,omitempty"`
	Input            []string    `yaml:"input,omitempty"   json:"input,omitempty"`
	ReasoningEfforts interface{} `yaml:"reasoningEfforts,omitempty" json:"reasoningEfforts,omitempty"`
	ContextWindow    *int        `yaml:"contextWindow,omitempty" json:"contextWindow,omitempty"`
	MaxTokens        *int        `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
}

// DSHProvider 表示 llm-pi-ai.providers 下一个路由的配置。
type DSHProvider struct {
	APIKeyEnv   string     `yaml:"apiKeyEnv,omitempty"  json:"apiKeyEnv,omitempty"`
	API         string     `yaml:"api,omitempty"        json:"api,omitempty"`
	BaseURL     string     `yaml:"baseURL,omitempty"    json:"baseURL,omitempty"`
	DisplayName string     `yaml:"displayName,omitempty" json:"displayName,omitempty"`
	Models      []DSHModel `yaml:"models,omitempty"     json:"models,omitempty"`
}

// DSHDefaultModel 对应 settings.yaml 顶层 agent-default-model 字段。
type DSHDefaultModel struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model"    json:"model"`
}

// dshSettingsResponse 是 GET /settings/dsh 的响应。
type dshSettingsResponse struct {
	DSHHome      string                 `json:"dshHome"`
	Path         string                 `json:"path"`
	Exists       bool                   `json:"exists"`
	Writable     bool                   `json:"writable"`
	Providers    map[string]DSHProvider `json:"providers"`
	ProviderKeys []string               `json:"providerKeys"` // 有序列表，保持文件顺序
	DefaultModel *DSHDefaultModel       `json:"defaultModel"`
	Theme        string                 `json:"theme"`
}

// saveDSHSettingsRequest 是 PUT /settings/dsh 的请求体。
type saveDSHSettingsRequest struct {
	Providers    map[string]DSHProvider `json:"providers"`
	ProviderKeys []string               `json:"providerKeys"` // 可选；为空时用 map 随机序
	DefaultModel *DSHDefaultModel       `json:"defaultModel"`
}

// ---- 路径解析 ----

func resolveDSHHome() string {
	if home := os.Getenv("DSH_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".dsh")
	}
	return filepath.Join(home, ".dsh")
}

func resolveDSHSettingsPath() string {
	return filepath.Join(resolveDSHHome(), "settings.yaml")
}

// ---- 读取 ----

// readDSHSettingsFile 读取 DSH settings.yaml，返回根 map 和原始字节。
// 如果文件不存在，返回空 map（不报错）。
func readDSHSettingsFile(path string) (map[string]interface{}, []byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil, false, nil
		}
		return nil, nil, false, err
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, raw, true, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	if root == nil {
		root = make(map[string]interface{})
	}
	return root, raw, true, nil
}

// extractDSHProviders 从根 map 中提取 llm-pi-ai.providers 部分。
func extractDSHProviders(root map[string]interface{}) map[string]DSHProvider {
	providers := make(map[string]DSHProvider)

	piAI, ok := root["llm-pi-ai"].(map[string]interface{})
	if !ok {
		return providers
	}
	provMap, ok := piAI["providers"].(map[string]interface{})
	if !ok {
		return providers
	}
	for k, v := range provMap {
		pm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		providers[k] = mapToDSHProvider(pm)
	}
	return providers
}

func mapToDSHProvider(m map[string]interface{}) DSHProvider {
	p := DSHProvider{}
	if v, ok := m["apiKeyEnv"].(string); ok {
		p.APIKeyEnv = v
	}
	if v, ok := m["api"].(string); ok {
		p.API = v
	}
	if v, ok := m["baseURL"].(string); ok {
		p.BaseURL = v
	}
	if v, ok := m["displayName"].(string); ok {
		p.DisplayName = v
	}
	if models, ok := m["models"].([]interface{}); ok {
		for _, mi := range models {
			mm, ok := mi.(map[string]interface{})
			if !ok {
				continue
			}
			p.Models = append(p.Models, mapToDSHModel(mm))
		}
	}
	return p
}

func mapToDSHModel(m map[string]interface{}) DSHModel {
	model := DSHModel{}
	if v, ok := m["id"].(string); ok {
		model.ID = v
	}
	if v, ok := m["name"].(string); ok {
		model.Name = v
	}
	if v, ok := m["input"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				model.Input = append(model.Input, s)
			}
		}
	}
	// reasoningEfforts 可以是 map 或 bool，原样保留
	if v, ok := m["reasoningEfforts"]; ok {
		model.ReasoningEfforts = v
	}
	if v, ok := m["contextWindow"]; ok {
		if n := toInt(v); n > 0 {
			model.ContextWindow = &n
		}
	}
	if v, ok := m["maxTokens"]; ok {
		if n := toInt(v); n > 0 {
			model.MaxTokens = &n
		}
	}
	return model
}

// extractDSHDefaultModel 从根 map 中提取 agent-default-model。
func extractDSHDefaultModel(root map[string]interface{}) *DSHDefaultModel {
	dm, ok := root["agent-default-model"].(map[string]interface{})
	if !ok {
		return nil
	}
	result := &DSHDefaultModel{}
	if v, ok := dm["provider"].(string); ok {
		result.Provider = v
	}
	if v, ok := dm["model"].(string); ok {
		result.Model = v
	}
	if result.Provider == "" && result.Model == "" {
		return nil
	}
	return result
}

// extractDSHTheme 提取 ui-theme.preference。
func extractDSHTheme(root map[string]interface{}) string {
	theme, ok := root["ui-theme"].(map[string]interface{})
	if !ok {
		return ""
	}
	if v, ok := theme["preference"].(string); ok {
		return v
	}
	return ""
}

// orderedDSHProviderKeys 统一 provider 列表顺序：requested 里实际存在的 key 优先
// 按传入顺序保留，其余按字母序补齐。requested 为 nil/空或含脏 key 时依然稳定，
// 且返回值永不为 nil（空 providers 返回空 slice），这样 json.Marshal 会序列化成
// "providerKeys":[] 而不是 "null"——否则前端 [...providerKeys] 会抛
// "undefined is not iterable"（见 DshView.vue 的 providerKeys 读取）。
// 读取（GetDSHSettings）与保存（SaveDSHSettings）共用这一条顺序规则。
func orderedDSHProviderKeys(requested []string, providers map[string]DSHProvider) []string {
	if len(providers) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(requested))
	keys := []string{}
	for _, k := range requested {
		if _, ok := providers[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	fallback := make([]string, 0, len(providers)-len(keys))
	for k := range providers {
		if !seen[k] {
			fallback = append(fallback, k)
		}
	}
	sort.Strings(fallback)
	keys = append(keys, fallback...)
	return keys
}

// GetDSHSettings 返回 DSH settings.yaml 的解析视图。
func GetDSHSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		dshSettingsMu.Lock()
		defer dshSettingsMu.Unlock()

		path := resolveDSHSettingsPath()
		root, _, exists, err := readDSHSettingsFile(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 DSH 配置失败: %v", err)})
			return
		}

		providers := extractDSHProviders(root)
		// 文件顺序优先（extractDSHProviderKeysOrdered），脏 key 过滤 + 字母序兜底。
		providerKeys := orderedDSHProviderKeys(extractDSHProviderKeysOrdered(path), providers)

		writable := checkDSHWritable(path)

		c.JSON(200, dshSettingsResponse{
			DSHHome:      resolveDSHHome(),
			Path:         path,
			Exists:       exists,
			Writable:     writable,
			Providers:    providers,
			ProviderKeys: providerKeys,
			DefaultModel: extractDSHDefaultModel(root),
			Theme:        extractDSHTheme(root),
		})
	}
}

// extractDSHProviderKeysOrdered 用 yaml.Node 按文件顺序提取 provider key。
func extractDSHProviderKeysOrdered(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil
	}
	root := mappingNode(&node)
	if root == nil {
		return nil
	}
	piAINode := nodeValue(root, "llm-pi-ai")
	if piAINode == nil {
		return nil
	}
	providersNode := nodeValue(piAINode, "providers")
	if providersNode == nil || providersNode.Kind != yaml.MappingNode {
		return nil
	}
	keys := []string{}
	for i := 0; i+1 < len(providersNode.Content); i += 2 {
		if providersNode.Content[i].Kind == yaml.ScalarNode {
			keys = append(keys, providersNode.Content[i].Value)
		}
	}
	return keys
}

// mappingNode 取 DocumentNode 的第一个 MappingNode 子节点。
func mappingNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	if n.Kind == yaml.MappingNode {
		return n
	}
	return nil
}

// nodeValue 在 MappingNode 中按键名取值节点。
func nodeValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// checkDSHWritable 检查 DSH home 目录是否可写（尝试创建临时文件）。
func checkDSHWritable(path string) bool {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return false
	}
	// 尝试创建临时文件来确认可写
	tmp, err := os.CreateTemp(dir, ".dsh-writable-test-*")
	if err != nil {
		return false
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return true
}

// ---- 保存 ----

// SaveDSHSettings 更新 DSH settings.yaml 中的 llm-pi-ai 和 agent-default-model 部分，
// 保留其他顶层字段不变。
func SaveDSHSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveDSHSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "DSH 配置请求无效"})
			return
		}

		dshSettingsMu.Lock()
		defer dshSettingsMu.Unlock()

		path := resolveDSHSettingsPath()
		root, _, _, err := readDSHSettingsFile(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 DSH 配置失败: %v", err)})
			return
		}

		// 构建 llm-pi-ai.providers 部分
		piAI, ok := root["llm-pi-ai"].(map[string]interface{})
		if !ok {
			piAI = make(map[string]interface{})
		}
		// 将 typed providers 转回 generic map，顺序由 SaveDSHSettings 统一决定。
		orderedKeys := orderedDSHProviderKeys(req.ProviderKeys, req.Providers)
		provMap := make(map[string]interface{})
		for _, k := range orderedKeys {
			provMap[k] = dshProviderToMap(req.Providers[k])
		}
		piAI["providers"] = provMap
		root["llm-pi-ai"] = piAI

		// 构建 agent-default-model 部分
		if req.DefaultModel != nil && req.DefaultModel.Provider != "" {
			root["agent-default-model"] = map[string]interface{}{
				"provider": req.DefaultModel.Provider,
				"model":    req.DefaultModel.Model,
			}
		} else {
			delete(root, "agent-default-model")
		}

		// 用 yaml.Node 按有序方式写入（保持 key 顺序）
		data, err := marshalDSHYAML(root, req.ProviderKeys, req.Providers)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("序列化 DSH 配置失败: %v", err)})
			return
		}

		if err := writeDSHSettings(path, data); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("写入 DSH 配置失败: %v", err)})
			return
		}

		c.JSON(200, gin.H{"success": true, "path": path})
	}
}

// marshalDSHYAML 将 root map 序列化为 YAML 字节，同时保证 llm-pi-ai.providers
// 下的 key 顺序与 providerKeys 一致（如果提供）。
func marshalDSHYAML(root map[string]interface{}, providerKeys []string, providers map[string]DSHProvider) ([]byte, error) {
	rootNode := &yaml.Node{Kind: yaml.MappingNode}

	for _, key := range orderedRootKeys(root) {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}

		if key == "llm-pi-ai" {
			piAI, _ := root["llm-pi-ai"].(map[string]interface{})
			piAINode := buildPiAINode(piAI, providerKeys, providers)
			rootNode.Content = append(rootNode.Content, keyNode, piAINode)
		} else if key == "agent-default-model" {
			val := root[key]
			if m, ok := val.(map[string]interface{}); ok {
				dmNode := &yaml.Node{Kind: yaml.MappingNode}
				if pv, ok := m["provider"].(string); ok {
					addScalar(dmNode, "provider", pv)
				}
				if mv, ok := m["model"].(string); ok {
					addScalar(dmNode, "model", mv)
				}
				rootNode.Content = append(rootNode.Content, keyNode, dmNode)
			}
		} else {
			val := root[key]
			if vNode := toYAMLNode(val); vNode != nil {
				rootNode.Content = append(rootNode.Content, keyNode, vNode)
			}
		}
	}

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(rootNode); err != nil {
		// 回退到简单 marshal
		return yaml.Marshal(root)
	}
	enc.Close()
	return []byte(buf.String()), nil
}

// buildPiAINode 构建 llm-pi-ai 部分，保证字段顺序和 provider key 顺序。
func buildPiAINode(piAI map[string]interface{}, providerKeys []string, providers map[string]DSHProvider) *yaml.Node {
	piAINode := &yaml.Node{Kind: yaml.MappingNode}
	for _, pk := range orderedPiAIKeys(piAI) {
		if pk == "providers" {
			providersNode := &yaml.Node{Kind: yaml.MappingNode}
			for _, rk := range orderedDSHProviderKeys(providerKeys, providers) {
				p, ok := providers[rk]
				if !ok {
					continue
				}
				pNode := buildProviderNode(p)
				providersNode.Content = append(providersNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: rk},
					pNode)
			}
			piAINode.Content = append(piAINode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "providers"},
				providersNode)
		} else {
			if vNode := toYAMLNode(piAI[pk]); vNode != nil {
				piAINode.Content = append(piAINode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: pk},
					vNode)
			}
		}
	}
	return piAINode
}

// buildProviderNode 构建单个 provider 的 yaml.Node，保证字段顺序。
func buildProviderNode(p DSHProvider) *yaml.Node {
	pNode := &yaml.Node{Kind: yaml.MappingNode}
	pMap := dshProviderToMap(p)
	for _, fkey := range []string{"apiKeyEnv", "api", "baseURL", "displayName", "models"} {
		if fval, ok := pMap[fkey]; ok {
			addScalar(pNode, fkey, fval)
		}
	}
	for fkey, fval := range pMap {
		if !contains([]string{"apiKeyEnv", "api", "baseURL", "displayName", "models"}, fkey) {
			addScalar(pNode, fkey, fval)
		}
	}
	return pNode
}

// addScalar 向 mapping node 添加一个 key-value 对，value 会递归转换为合适的 node。
func addScalar(mapping *yaml.Node, key string, val interface{}) {
	vNode := toYAMLNode(val)
	if vNode == nil {
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		vNode)
}

// toYAMLNode 将 interface{} 转换为 yaml.Node。
func toYAMLNode(val interface{}) *yaml.Node {
	if val == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	}
	switch v := val.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: fmt.Sprintf("%g", v)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprintf("%t", v)}
	case map[string]interface{}:
		mNode := &yaml.Node{Kind: yaml.MappingNode}
		for k, vv := range v {
			addScalar(mNode, k, vv)
		}
		return mNode
	case map[interface{}]interface{}:
		mNode := &yaml.Node{Kind: yaml.MappingNode}
		for k, vv := range v {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			addScalar(mNode, ks, vv)
		}
		return mNode
	case []interface{}:
		sNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range v {
			if n := toYAMLNode(item); n != nil {
				sNode.Content = append(sNode.Content, n)
			}
		}
		return sNode
	case []string:
		sNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range v {
			sNode.Content = append(sNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item})
		}
		return sNode
	default:
		// 最后手段：marshal 然后嵌入
		data, err := yaml.Marshal(val)
		if err != nil {
			return nil
		}
		var n yaml.Node
		if err := yaml.Unmarshal(data, &n); err != nil {
			return nil
		}
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			return n.Content[0]
		}
		return &n
	}
}

// orderedRootKeys 返回 root map 的 key 顺序，优先使用文件原始顺序。
func orderedRootKeys(root map[string]interface{}) []string {
	// 固定的 key 优先顺序
	preferred := []string{
		"ui-onboarding", "llm-pi-ai", "llm-deepseek",
		"agent-default-model", "ui-theme",
	}
	seen := make(map[string]bool)
	keys := []string{}
	for _, k := range preferred {
		if _, ok := root[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	// 添加其他未列出的 key
	for k := range root {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

// orderedPiAIKeys 返回 llm-pi-ai map 的 key 顺序。
func orderedPiAIKeys(piAI map[string]interface{}) []string {
	preferred := []string{"apiKeyEnv", "api", "baseURL", "displayName", "models", "providers"}
	seen := make(map[string]bool)
	keys := []string{}
	for _, k := range preferred {
		if _, ok := piAI[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range piAI {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

func dshProviderToMap(p DSHProvider) map[string]interface{} {
	m := make(map[string]interface{})
	if p.APIKeyEnv != "" {
		m["apiKeyEnv"] = p.APIKeyEnv
	}
	if p.API != "" {
		m["api"] = p.API
	}
	if p.BaseURL != "" {
		m["baseURL"] = p.BaseURL
	}
	if p.DisplayName != "" {
		m["displayName"] = p.DisplayName
	}
	if len(p.Models) > 0 {
		models := make([]interface{}, len(p.Models))
		for i, model := range p.Models {
			models[i] = dshModelToMap(model)
		}
		m["models"] = models
	}
	return m
}

func dshModelToMap(m DSHModel) map[string]interface{} {
	result := make(map[string]interface{})
	result["id"] = m.ID
	if m.Name != "" {
		result["name"] = m.Name
	}
	if len(m.Input) > 0 {
		inputs := make([]interface{}, len(m.Input))
		for i, v := range m.Input {
			inputs[i] = v
		}
		result["input"] = inputs
	}
	if m.ReasoningEfforts != nil {
		result["reasoningEfforts"] = m.ReasoningEfforts
	}
	if m.ContextWindow != nil {
		result["contextWindow"] = *m.ContextWindow
	}
	if m.MaxTokens != nil {
		result["maxTokens"] = *m.MaxTokens
	}
	return result
}

func writeDSHSettings(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// 备份
	if prev, err := os.ReadFile(path); err == nil && len(prev) > 0 {
		backup := path + "." + time.Now().Format("20060102-150405") + ".bak"
		_ = os.WriteFile(backup, prev, 0600)
	}
	// 原子写入
	tempFile, err := os.CreateTemp(dir, ".dsh-settings-*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if err := tempFile.Chmod(0600); err != nil {
		tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// ---- 辅助函数 ----

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
