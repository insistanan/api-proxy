package handlers

// DSH 凭据文件的读写与脱敏视图。
//
// DSH 的 settings.yaml 只保存 apiKeyEnv 引用，实际密钥由
// $DSH_HOME/.credentials.yaml 的 refs 节保存。本文件只处理凭据文件，
// 不把明文密钥并入 DSHProvider，避免后续 YAML 序列化时误写进 settings。

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"gopkg.in/yaml.v3"
)

const (
	dshCredentialsFileName = ".credentials.yaml"
	dshCredentialActionKeep = "keep"
	dshCredentialActionReplace = "replace"
	dshCredentialActionRemove = "remove"
)

var dshCredentialRefPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DSHCredentialStatus 是前端可见的凭据状态；永远不包含明文值。
type DSHCredentialStatus struct {
	Reference     string `json:"reference"`
	APIKeyMasked  string `json:"apiKeyMasked"`
	APIKeyPresent bool   `json:"apiKeyPresent"`
	Writable      bool   `json:"writable"`
}

// dshCredentialUpdate 是保存请求中的一次性凭据操作。
// APIKey 只在 replace 时使用，keep/remove 都不会落盘明文请求字段。
type dshCredentialUpdate struct {
	Action string `json:"action"`
	APIKey string `json:"apiKey"`
}

func resolveDSHCredentialsPath() string {
	return filepath.Join(resolveDSHHome(), dshCredentialsFileName)
}

func validateDSHCredentialReference(reference string) error {
	if reference == "" {
		return fmt.Errorf("DSH API Key 引用名不能为空")
	}
	if !dshCredentialRefPattern.MatchString(reference) {
		return fmt.Errorf("DSH API Key 引用名格式无效（只能包含字母、数字、下划线，且不能以数字开头）")
	}
	return nil
}

// validateDSHCredentialUpdates 在读取磁盘前校验请求语义，便于把用户输入
// 错误明确返回 400，而不是混同为凭据文件解析错误。
func validateDSHCredentialUpdates(updates map[string]dshCredentialUpdate, providers map[string]DSHProvider) error {
	if len(updates) == 0 {
		return nil
	}

	routes := make([]string, 0, len(updates))
	for route := range updates {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	seen := make(map[string]dshCredentialUpdate, len(routes))
	for _, route := range routes {
		provider, ok := providers[route]
		if !ok {
			return fmt.Errorf("DSH 凭据更新引用了不存在的 provider: %s", route)
		}
		update := updates[route]
		if update.Action == "" {
			update.Action = dshCredentialActionKeep
		}
		switch update.Action {
		case dshCredentialActionKeep:
			// keep 不需要引用名，也不应把请求中的明文误当作替换。
			continue
		case dshCredentialActionReplace:
			if err := validateDSHCredentialReference(strings.TrimSpace(provider.APIKeyEnv)); err != nil {
				return fmt.Errorf("provider %s 的 DSH 凭据引用无效: %w", route, err)
			}
			if strings.TrimSpace(update.APIKey) == "" {
				return fmt.Errorf("provider %s 替换 API Key 时密钥不能为空", route)
			}
		case dshCredentialActionRemove:
			if err := validateDSHCredentialReference(strings.TrimSpace(provider.APIKeyEnv)); err != nil {
				return fmt.Errorf("provider %s 的 DSH 凭据引用无效: %w", route, err)
			}
		default:
			return fmt.Errorf("provider %s 的 DSH 凭据操作无效: %s", route, update.Action)
		}

		reference := strings.TrimSpace(provider.APIKeyEnv)
		if previous, exists := seen[reference]; exists && (previous.Action != update.Action || strings.TrimSpace(previous.APIKey) != strings.TrimSpace(update.APIKey)) {
			return fmt.Errorf("DSH 凭据引用 %s 在多个 provider 中收到冲突更新", reference)
		}
		seen[reference] = update
	}
	return nil
}

// newDSHCredentialsDocument 创建当前官方格式的空凭据文档。
func newDSHCredentialsDocument() *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}
	addScalar(root, "version", 1)
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "refs"},
		&yaml.Node{Kind: yaml.MappingNode},
	)
	document := &yaml.Node{Kind: yaml.DocumentNode}
	document.Content = append(document.Content, root)
	return document
}

// readDSHCredentialsDocument 读取并规范化 DSH credentials 文档。
// 官方当前格式为 version: 1 + refs/records；同时兼容官方曾经使用的
// “无 version 的扁平 refs”格式，首次写入时升级为 versioned 文档。
func readDSHCredentialsDocument(path string) (*yaml.Node, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newDSHCredentialsDocument(), false, nil
		}
		return nil, false, fmt.Errorf("读取 DSH 凭据文件失败: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return newDSHCredentialsDocument(), true, nil
	}

	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, true, fmt.Errorf("解析 DSH 凭据文件失败: %w", err)
	}
	root := mappingNode(&document)
	if root == nil {
		return nil, true, fmt.Errorf("解析 DSH 凭据文件失败: 根节点必须是 YAML 对象")
	}

	if version := nodeValue(root, "version"); version != nil {
		if version.Kind != yaml.ScalarNode || strings.TrimSpace(version.Value) != "1" {
			return nil, true, fmt.Errorf("不支持的 DSH 凭据文件版本: %s", version.Value)
		}
	} else {
		if err := migrateLegacyDSHCredentials(root); err != nil {
			return nil, true, err
		}
		root = mappingNode(&document)
	}
	if err := validateDSHCredentialsTopLevel(root); err != nil {
		return nil, true, err
	}

	refs := nodeValue(root, "refs")
	if refs == nil {
		refs = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "refs"},
			refs,
		)
	} else if refs.Kind != yaml.MappingNode {
		return nil, true, fmt.Errorf("DSH 凭据文件的 refs 必须是 YAML 对象")
	}
	if err := validateDSHCredentialRefs(refs); err != nil {
		return nil, true, err
	}
	if records := nodeValue(root, "records"); records != nil && records.Kind != yaml.MappingNode {
		return nil, true, fmt.Errorf("DSH 凭据文件的 records 必须是 YAML 对象")
	}
	return &document, true, nil
}

// validateDSHCredentialsTopLevel 与官方存储格式保持一致：凭据文件不是
// 通用配置容器，未知顶层键会让 DSH 拒绝加载，因此不能静默保留并继续写入。
func validateDSHCredentialsTopLevel(root *yaml.Node) error {
	if root == nil || root.Kind != yaml.MappingNode || len(root.Content)%2 != 0 {
		return fmt.Errorf("解析 DSH 凭据文件失败: 顶层映射结构无效")
	}
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index+1 < len(root.Content); index += 2 {
		key := root.Content[index]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("解析 DSH 凭据文件失败: 顶层键必须是字符串")
		}
		if _, exists := seen[key.Value]; exists {
			return fmt.Errorf("解析 DSH 凭据文件失败: 顶层键重复: %s", key.Value)
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "version", "refs", "records":
		default:
			return fmt.Errorf("解析 DSH 凭据文件失败: 不支持的顶层键: %s", key.Value)
		}
	}
	return nil
}

func validateDSHCredentialRefs(refs *yaml.Node) error {
	if refs == nil || refs.Kind != yaml.MappingNode || len(refs.Content)%2 != 0 {
		return fmt.Errorf("解析 DSH 凭据文件失败: refs 映射结构无效")
	}
	seen := make(map[string]struct{}, len(refs.Content)/2)
	for index := 0; index+1 < len(refs.Content); index += 2 {
		key := refs.Content[index]
		value := refs.Content[index+1]
		if key.Kind != yaml.ScalarNode || !dshCredentialRefPattern.MatchString(key.Value) {
			return fmt.Errorf("解析 DSH 凭据文件失败: refs 中存在无效引用名")
		}
		if _, exists := seen[key.Value]; exists {
			return fmt.Errorf("解析 DSH 凭据文件失败: refs 引用名重复: %s", key.Value)
		}
		seen[key.Value] = struct{}{}
		if value.Kind != yaml.ScalarNode || value.Value == "" {
			return fmt.Errorf("解析 DSH 凭据文件失败: refs 引用 %s 的值必须是非空标量", key.Value)
		}
	}
	return nil
}

// migrateLegacyDSHCredentials 将早期的裸引用映射包进 refs，拒绝无法证明
// 是凭据引用的其他结构，避免把未知文件静默当成空凭据覆盖。
func migrateLegacyDSHCredentials(root *yaml.Node) error {
	if root == nil || root.Kind != yaml.MappingNode || len(root.Content)%2 != 0 {
		return fmt.Errorf("解析 DSH 凭据文件失败: 根节点必须是 YAML 对象")
	}
	legacyRefs := &yaml.Node{Kind: yaml.MappingNode}
	for index := 0; index+1 < len(root.Content); index += 2 {
		key := root.Content[index]
		value := root.Content[index+1]
		if key.Kind != yaml.ScalarNode || !dshCredentialRefPattern.MatchString(key.Value) || value.Kind != yaml.ScalarNode {
			return fmt.Errorf("无法识别无版本 DSH 凭据文件格式，请先用 DSH 官方界面迁移")
		}
		legacyRefs.Content = append(legacyRefs.Content, key, value)
	}
	root.Content = nil
	addScalar(root, "version", 1)
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "refs"},
		legacyRefs,
	)
	return nil
}

func dshCredentialsRefs(document *yaml.Node) (*yaml.Node, error) {
	root := mappingNode(document)
	if root == nil {
		return nil, fmt.Errorf("DSH 凭据文件根节点无效")
	}
	refs := nodeValue(root, "refs")
	if refs == nil {
		refs = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "refs"},
			refs,
		)
	}
	if refs.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("DSH 凭据文件的 refs 必须是 YAML 对象")
	}
	return refs, nil
}

func dshCredentialValue(refs *yaml.Node, reference string) (string, bool, error) {
	for index := 0; index+1 < len(refs.Content); index += 2 {
		if refs.Content[index].Value != reference {
			continue
		}
		value := refs.Content[index+1]
		if value.Kind != yaml.ScalarNode {
			return "", true, fmt.Errorf("DSH 凭据引用 %s 的值必须是标量", reference)
		}
		return value.Value, true, nil
	}
	return "", false, nil
}

func setDSHCredentialValue(refs *yaml.Node, reference, value string) {
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	for index := 0; index+1 < len(refs.Content); index += 2 {
		if refs.Content[index].Value == reference {
			old := refs.Content[index+1]
			valueNode.Style = old.Style
			valueNode.HeadComment = old.HeadComment
			valueNode.LineComment = old.LineComment
			valueNode.FootComment = old.FootComment
			refs.Content[index+1] = valueNode
			return
		}
	}
	refs.Content = append(refs.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: reference},
		valueNode,
	)
}

func deleteDSHCredentialValue(refs *yaml.Node, reference string) bool {
	for index := 0; index+1 < len(refs.Content); index += 2 {
		if refs.Content[index].Value != reference {
			continue
		}
		refs.Content = append(refs.Content[:index], refs.Content[index+2:]...)
		return true
	}
	return false
}

func marshalDSHCredentialsDocument(document *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("序列化 DSH 凭据文件失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("关闭 DSH 凭据文件编码器失败: %w", err)
	}
	return buf.Bytes(), nil
}

// prepareDSHCredentialDocument 只在有 replace/remove 操作时读取并生成新内容。
// 返回 changed=false 时不写盘，避免无意义地触碰凭据文件时间戳。
func prepareDSHCredentialDocument(path string, updates map[string]dshCredentialUpdate, providers map[string]DSHProvider) ([]byte, bool, error) {
	if len(updates) == 0 {
		return nil, false, nil
	}
	document, _, err := readDSHCredentialsDocument(path)
	if err != nil {
		return nil, false, err
	}
	refs, err := dshCredentialsRefs(document)
	if err != nil {
		return nil, false, err
	}

	routes := make([]string, 0, len(updates))
	for route := range updates {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	changed := false
	for _, route := range routes {
		provider := providers[route]
		update := updates[route]
		if update.Action == "" || update.Action == dshCredentialActionKeep {
			continue
		}
		reference := strings.TrimSpace(provider.APIKeyEnv)
		current, exists, err := dshCredentialValue(refs, reference)
		if err != nil {
			return nil, false, err
		}
		switch update.Action {
		case dshCredentialActionReplace:
			value := strings.TrimSpace(update.APIKey)
			if !exists || current != value {
				setDSHCredentialValue(refs, reference, value)
				changed = true
			}
		case dshCredentialActionRemove:
			if exists && deleteDSHCredentialValue(refs, reference) {
				changed = true
			}
		}
	}
	if !changed {
		return nil, false, nil
	}
	data, err := marshalDSHCredentialsDocument(document)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func readDSHCredentialStatuses(path string, providers map[string]DSHProvider) (map[string]DSHCredentialStatus, error) {
	statuses := make(map[string]DSHCredentialStatus, len(providers))
	document, _, err := readDSHCredentialsDocument(path)
	if err != nil {
		return nil, err
	}
	refs, err := dshCredentialsRefs(document)
	if err != nil {
		return nil, err
	}
	writable := checkDSHWritable(path)
	for route, provider := range providers {
		reference := strings.TrimSpace(provider.APIKeyEnv)
		status := DSHCredentialStatus{Reference: reference, Writable: writable}
		if reference != "" {
			value, exists, valueErr := dshCredentialValue(refs, reference)
			if valueErr != nil {
				return nil, valueErr
			}
			if exists && strings.TrimSpace(value) != "" {
				status.APIKeyPresent = true
				status.APIKeyMasked = utils.MaskAPIKey(value)
			}
		}
		statuses[route] = status
	}
	return statuses, nil
}

func writeDSHCredentials(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建 DSH 凭据目录失败: %w", err)
	}
	// 凭据文件不创建备份：备份同样包含明文密钥，容易扩大泄露面。
	return atomicfile.Write(path, data, atomicfile.Options{
		TempPrefix: ".dsh-credentials-",
		Mode:       0o600,
	})
}
