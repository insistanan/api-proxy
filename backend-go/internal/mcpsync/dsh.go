package mcpsync

import (
	"bytes"
	"fmt"
	"maps"
	"strings"

	"gopkg.in/yaml.v3"
)

const dshMCPModule = "@deepseek-ai/dsh-mcp-client"

// dshAdapter 管理所有 profile 都会读取的用户全局 Cordis 补丁。
// 直接操作 YAML 节点，保留其它插件、注释与 !!js 表达式；不执行表达式。
type dshAdapter struct{ path func() (string, error) }

func NewDSHAdapter(path func() (string, error)) Adapter { return dshAdapter{path: path} }
func (dshAdapter) Key() AgentKey                        { return AgentDSH }
func (dshAdapter) Name() string                         { return "DSH" }
func (a dshAdapter) Path() (string, error)              { return a.path() }

type dshRow struct {
	row            *yaml.Node
	parent         *yaml.Node
	groupPath      string
	idPath         string
	parentDisabled bool
	patches        []*yaml.Node
}

func yamlField(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func yamlSet(node *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value != key {
			continue
		}
		if value == nil {
			node.Content = append(node.Content[:index], node.Content[index+2:]...)
		} else {
			node.Content[index+1] = value
		}
		return
	}
	if value != nil {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
}

func yamlString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}

func dshDocument(raw []byte) (*yaml.Node, error) {
	if len(raw) == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("解析 DSH cordis.patch.yml 失败: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("DSH cordis.patch.yml 必须是补丁数组（空配置请使用 []）")
	}
	return &document, nil
}

func dshRows(document *yaml.Node) ([]dshRow, map[string]*yaml.Node, error) {
	var rows []dshRow
	groups := map[string]*yaml.Node{}
	entryIDs := map[string]bool{}
	var visit func(*yaml.Node, string, bool) error
	visit = func(list *yaml.Node, groupPath string, disabled bool) error {
		if list == nil || list.Kind != yaml.SequenceNode {
			return fmt.Errorf("DSH 插件列表必须是数组")
		}
		for _, row := range list.Content {
			if row.Kind != yaml.MappingNode {
				return fmt.Errorf("DSH 插件条目必须是对象")
			}
			id := yamlString(yamlField(row, "id"))
			path := id
			if groupPath != "" {
				path = groupPath + ":" + id
			}
			if id != "" {
				if entryIDs[path] {
					return fmt.Errorf("DSH 插件 id 重复: %s", path)
				}
				entryIDs[path] = true
			}
			name := yamlString(yamlField(row, "name"))
			if name == dshMCPModule {
				if id == "" {
					return fmt.Errorf("DSH MCP 插件缺少稳定 id，无法安全管理")
				}
				rows = append(rows, dshRow{row: row, parent: list, groupPath: groupPath, idPath: path, parentDisabled: disabled})
			}
			if yamlString(yamlField(row, "group")) == "true" || name == "cordis:group" {
				if id == "" {
					return fmt.Errorf("DSH 插件组缺少 id，无法安全管理嵌套 MCP")
				}
				config := yamlField(row, "config")
				groups[path] = config
				if err := visit(config, path, disabled || yamlString(yamlField(row, "disabled")) == "true"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, patch := range document.Content[0].Content {
		if patch.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("DSH 补丁必须是对象")
		}
		if insert := yamlField(patch, "insert"); insert != nil {
			path := yamlString(yamlField(patch, "id"))
			if err := visit(insert, path, false); err != nil {
				return nil, nil, err
			}
		}
	}
	seenPaths := map[string]bool{}
	for index := range rows {
		if seenPaths[rows[index].idPath] {
			return nil, nil, fmt.Errorf("DSH MCP 插件 id 重复: %s", rows[index].idPath)
		}
		seenPaths[rows[index].idPath] = true
		for _, patch := range document.Content[0].Content {
			if yamlField(patch, "insert") == nil && yamlString(yamlField(patch, "id")) == rows[index].idPath {
				if name := yamlString(yamlField(patch, "name")); name != "" && name != dshMCPModule {
					return nil, nil, fmt.Errorf("DSH MCP 补丁的插件名称断言不匹配: %s", rows[index].idPath)
				}
				rows[index].patches = append(rows[index].patches, patch)
			}
		}
	}
	return rows, groups, nil
}

func effectiveDSHRow(row dshRow) (*yaml.Node, error) {
	raw, err := yaml.Marshal(row.row)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	result := doc.Content[0]
	for _, patch := range row.patches {
		for index := 0; index < len(patch.Content); index += 2 {
			key := patch.Content[index].Value
			if key == "id" || key == "name" {
				continue
			}
			yamlSet(result, key, patch.Content[index+1])
		}
	}
	return result, nil
}

// dshEntries 投影为通用连接字段，同时携带原始 YAML 以供停用恢复与编辑保留。
func dshEntries(raw []byte) (map[string]any, error) {
	document, err := dshDocument(raw)
	if err != nil {
		return nil, err
	}
	rows, _, err := dshRows(document)
	if err != nil {
		return nil, err
	}
	entries := map[string]any{}
	for _, row := range rows {
		effective, err := effectiveDSHRow(row)
		if err != nil {
			return nil, err
		}
		config := yamlField(effective, "config")
		name := yamlString(yamlField(config, "serverName"))
		missingName := name == ""
		if name == "" {
			name = yamlString(yamlField(row.row, "id"))
		}
		if _, exists := entries[name]; exists {
			return nil, fmt.Errorf("DSH 中存在多个名为 %s 的 MCP，请先处理重复项", name)
		}
		entry := map[string]any{"enabled": !row.parentDisabled && yamlString(yamlField(effective, "disabled")) != "true", "_dsh_group": row.groupPath, "_dsh_parent_disabled": row.parentDisabled}
		if missingName {
			entry["_dsh_error"] = "DSH MCP 缺少 serverName，请先在 DSH 配置中补全"
		}
		payload, err := yaml.Marshal(effective)
		if err != nil {
			return nil, err
		}
		entry["_dsh_yaml"] = string(payload)
		transport := yamlString(yamlField(config, "transport"))
		switch transport {
		case "stdio":
			entry["type"] = "stdio"
		case "streamable-http":
			entry["type"] = "http"
		default:
			entry["type"] = "unsupported"
		}
		for _, field := range []string{"command", "args", "env", "url", "headers"} {
			value := yamlField(config, field)
			if value == nil {
				continue
			}
			if hasYAMLExpression(value) {
				entry["_dsh_error"] = "连接字段包含 YAML 表达式或引用，无法安全解析；请在 DSH 中编辑"
				continue
			}
			var decoded any
			if err := value.Decode(&decoded); err != nil {
				return nil, fmt.Errorf("读取 DSH MCP %s 的 %s 失败: %w", name, field, err)
			}
			entry[field] = decoded
		}
		entries[name] = entry
	}
	return entries, nil
}

func hasYAMLExpression(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || (node.Tag != "" && !strings.HasPrefix(node.Tag, "!!") && !strings.HasPrefix(node.Tag, "tag:yaml.org,2002:")) || node.Tag == "!!js" {
		return true
	}
	for _, child := range node.Content {
		if hasYAMLExpression(child) {
			return true
		}
	}
	return false
}

func (dshAdapter) Read(current []byte) ([]DetectedServer, error) {
	entries, err := dshEntries(current)
	if err != nil {
		return nil, err
	}
	result, err := readEntries(map[string]any{"entries": entries}, "entries", AgentDSH)
	for index := range result {
		result[index].Warning = "来自 DSH 用户全局补丁；profile 或插件包另行提供的 MCP 不包含在此文件中"
	}
	return result, err
}

func dshEntry(server Server) map[string]any { return piEntry(server) }

func marshalDSH(document *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func replaceDSHEntry(current []byte, name string, entry map[string]any) ([]byte, bool, error) {
	document, err := dshDocument(current)
	if err != nil {
		return nil, false, err
	}
	rows, groups, err := dshRows(document)
	if err != nil {
		return nil, false, err
	}
	var target *dshRow
	for index := range rows {
		effective, err := effectiveDSHRow(rows[index])
		if err != nil {
			return nil, false, err
		}
		if yamlString(yamlField(yamlField(effective, "config"), "serverName")) == name {
			if target != nil {
				return nil, false, fmt.Errorf("DSH MCP %s 重复", name)
			}
			target = &rows[index]
		}
	}
	if entry == nil && target == nil {
		return current, false, nil
	}
	var row *yaml.Node
	if target != nil {
		row, err = effectiveDSHRow(*target)
		if err != nil {
			return nil, false, err
		}
	} else if payload, ok := entry["_dsh_yaml"].(string); ok {
		var snapshot yaml.Node
		if err := yaml.Unmarshal([]byte(payload), &snapshot); err != nil {
			return nil, false, err
		}
		if len(snapshot.Content) != 1 || snapshot.Content[0].Kind != yaml.MappingNode {
			return nil, false, fmt.Errorf("DSH 停用快照不是插件对象")
		}
		row = snapshot.Content[0]
	} else {
		row = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if entry != nil {
		originalName := yamlString(yamlField(yamlField(row, "config"), "serverName"))
		if enabled, _ := entry["enabled"].(bool); enabled {
			if blocked, _ := entry["_dsh_parent_disabled"].(bool); blocked {
				return nil, false, fmt.Errorf("DSH 父插件组已禁用，请先在 DSH 中启用该组")
			}
		}
		server, _, err := decodeEntry(name, entry, AgentDSH)
		if err != nil {
			return nil, false, err
		}
		if len(name) > 32 {
			return nil, false, fmt.Errorf("DSH MCP 名称不能超过 32 个字符")
		}
		config := yamlField(row, "config")
		if config == nil {
			config = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			yamlSet(row, "config", config)
		}
		if config.Kind != yaml.MappingNode {
			return nil, false, fmt.Errorf("DSH MCP config 必须是对象")
		}
		fields := maps.Clone(claudeEntry(server))
		delete(fields, "type")
		fields["serverName"] = name
		if server.Transport == TransportHTTP {
			fields["transport"] = "streamable-http"
		} else {
			fields["transport"] = "stdio"
		}
		for _, key := range []string{"command", "args", "env", "url", "headers", "serverName", "transport"} {
			value, exists := fields[key]
			if !exists {
				yamlSet(config, key, nil)
				continue
			}
			var node yaml.Node
			if err := node.Encode(value); err != nil {
				return nil, false, err
			}
			yamlSet(config, key, &node)
		}
		if yamlField(row, "id") == nil || (target == nil && originalName != name) {
			yamlSet(row, "id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "mcp-" + name})
		}
		yamlSet(row, "name", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: dshMCPModule})
		yamlSet(row, "disabled", nil)
	}
	if target != nil {
		for index, original := range target.parent.Content {
			if original != target.row {
				continue
			}
			if entry == nil {
				target.parent.Content = append(target.parent.Content[:index], target.parent.Content[index+1:]...)
			} else {
				target.parent.Content[index] = row
			}
			break
		}
		for _, patch := range target.patches {
			for index, original := range document.Content[0].Content {
				if original == patch {
					document.Content[0].Content = append(document.Content[0].Content[:index], document.Content[0].Content[index+1:]...)
					break
				}
			}
		}
	} else if entry != nil {
		group, _ := entry["_dsh_group"].(string)
		if group != "" {
			list := groups[group]
			if list != nil && list.Kind == yaml.SequenceNode {
				list.Content = append(list.Content, row)
			} else {
				// 目标组也可能来自 profile；保留原补丁 id，不把它移动到全局根作用域。
				patch := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				yamlSet(patch, "id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: group})
				yamlSet(patch, "insert", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{row}})
				document.Content[0].Content = append(document.Content[0].Content, patch)
			}
		} else {
			patch := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			yamlSet(patch, "insert", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{row}})
			document.Content[0].Content = append(document.Content[0].Content, patch)
		}
	}
	if _, _, err := dshRows(document); err != nil {
		return nil, false, err
	}
	next, err := marshalDSH(document)
	return next, !bytes.Equal(current, next), err
}

func (a dshAdapter) Render(current []byte, desired map[string]Server, managed map[string]struct{}) ([]byte, bool, error) {
	entries, err := dshEntries(current)
	if err != nil {
		return nil, false, err
	}
	next := current
	for _, name := range sortedKeys(managed) {
		server, wanted := desired[name]
		entry, exists := entries[name].(map[string]any)
		if !wanted {
			if exists {
				next, _, err = replaceDSHEntry(next, name, nil)
			}
		} else {
			original, enabled, decodeErr := decodeEntry(name, entry, AgentDSH)
			if exists && decodeErr == nil && enabled && sameConfig(original, server) {
				continue
			}
			merged := mergeEntry(entry, server, AgentDSH, dshEntry)
			next, _, err = replaceDSHEntry(next, name, merged)
		}
		if err != nil {
			return nil, false, err
		}
	}
	return next, !bytes.Equal(current, next), nil
}
