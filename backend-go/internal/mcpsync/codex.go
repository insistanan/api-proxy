package mcpsync

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// codexAdapter 写入 Codex 的 config.toml（$CODEX_HOME/config.toml，默认 ~/.codex/config.toml）中的 [mcp_servers.<name>] 表。
//
// 不做 TOML 解析后整体重序列化：那会丢失用户的注释与排版。改为文本级补丁：
// 删除托管名称对应的表（[mcp_servers.<name>] 及其子表 .env、.http_headers 等），再在末尾追加生成的表。
// 生成内容的字符串使用 JSON 转义（JSON 字符串是合法的 TOML 基本字符串），键名统一加引号。
// 补丁结果必须能被 TOML 解析，否则不写入。
type codexAdapter struct {
	path func() (string, error)
}

// NewCodexAdapter 创建 Codex 适配器。path 由调用方解析 config.toml 路径。
func NewCodexAdapter(path func() (string, error)) Adapter {
	return codexAdapter{path: path}
}

func (codexAdapter) Key() AgentKey { return AgentCodex }

func (codexAdapter) Name() string { return "Codex" }

func (a codexAdapter) Path() (string, error) { return a.path() }

func (codexAdapter) Read(current []byte) ([]DetectedServer, error) {
	var root map[string]any
	if err := toml.Unmarshal(current, &root); err != nil {
		return nil, fmt.Errorf("解析 config.toml 失败: %w", err)
	}
	return readEntries(root, "mcp_servers", AgentCodex)
}

func (codexAdapter) Render(current []byte, desired map[string]Server, managed map[string]struct{}) ([]byte, bool, error) {
	var root map[string]any
	if len(current) > 0 {
		// 原文件必须先能解析；否则补丁结果不可信，直接拒绝。
		if err := toml.Unmarshal(current, &root); err != nil {
			return nil, false, fmt.Errorf("解析 config.toml 失败: %w", err)
		}
	}
	entries, err := objectField(root, "mcp_servers")
	if err != nil {
		return nil, false, err
	}
	// 未改动的条目保持原始文本；需要更新时仅合并统一模型字段，保留客户端附加设置。
	expectedEntries := maps.Clone(entries)
	if expectedEntries == nil {
		expectedEntries = map[string]any{}
	}
	patches := make(map[string]struct{})
	var blocks []string
	for _, name := range sortedKeys(managed) {
		server, wanted := desired[name]
		currentEntry, exists := entries[name]
		if !wanted {
			if exists {
				patches[name] = struct{}{}
				delete(expectedEntries, name)
			}
			continue
		}
		entry, _ := currentEntry.(map[string]any)
		if existing, enabled, err := decodeEntry(name, entry, AgentCodex); err == nil && enabled && sameConfig(existing, server) {
			continue
		}
		generated := map[string]any{}
		if err := toml.Unmarshal([]byte(strings.Join(codexBlock(server), "\n")), &generated); err != nil {
			return nil, false, fmt.Errorf("生成 MCP 配置失败: %w", err)
		}
		mcp := generated["mcp_servers"].(map[string]any)
		merged := mergeEntry(entry, server, AgentCodex, func(Server) map[string]any {
			return mcp[name].(map[string]any)
		})
		block, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{name: merged}})
		if err != nil {
			return nil, false, fmt.Errorf("编码 MCP 配置失败: %w", err)
		}
		// 每个块只保留服务器自身及其子表，避免多条更新重复声明父表。
		var lines []string
		for line := range strings.SplitSeq(string(block), "\n") {
			if strings.TrimSpace(line) != "[mcp_servers]" {
				lines = append(lines, line)
			}
		}
		blocks = append(blocks, strings.TrimSpace(strings.Join(lines, "\n")))
		expectedEntries[name] = merged
		patches[name] = struct{}{}
	}
	if len(patches) == 0 {
		return current, false, nil
	}
	return patchCodexEntries(current, root, expectedEntries, patches, blocks)
}

// patchCodexEntries 只替换指定表，并校验其它配置的解析结果没有变化。
func patchCodexEntries(current []byte, root, expectedEntries map[string]any, patches map[string]struct{}, blocks []string) ([]byte, bool, error) {
	eol := "\n"
	if strings.Contains(string(current), "\r\n") {
		eol = "\r\n"
	}

	var kept []string
	skipping := false
	for line := range strings.SplitSeq(string(current), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if keys, isHeader := parseTOMLHeader(line); isHeader {
			skipping = isManagedCodexTable(keys, patches)
		}
		if !skipping {
			kept = append(kept, line)
		}
	}

	for index, block := range blocks {
		blocks[index] = strings.ReplaceAll(block, "\n", eol)
	}

	var parts []string
	if body := strings.TrimRight(strings.Join(kept, eol), " \t\r\n"); body != "" {
		parts = append(parts, body)
	}
	parts = append(parts, blocks...)
	next := ""
	if len(parts) > 0 {
		next = strings.Join(parts, eol+eol) + eol
	}

	if next == string(current) {
		return current, false, nil
	}
	var validated map[string]any
	if err := toml.Unmarshal([]byte(next), &validated); err != nil {
		return nil, false, fmt.Errorf("生成的 config.toml 无法解析，已取消写入: %w", err)
	}
	if validated == nil {
		validated = map[string]any{}
	}
	expected := maps.Clone(root)
	if expected == nil {
		expected = map[string]any{}
	}
	if len(expectedEntries) > 0 {
		expected["mcp_servers"] = expectedEntries
	}
	if actualEntries, ok := validated["mcp_servers"].(map[string]any); ok && len(actualEntries) == 0 && len(expectedEntries) == 0 {
		delete(validated, "mcp_servers")
		delete(expected, "mcp_servers")
	} else if len(expectedEntries) == 0 {
		delete(expected, "mcp_servers")
	}
	if !reflect.DeepEqual(expected, validated) {
		return nil, false, fmt.Errorf("config.toml 补丁会改变预期之外的配置，已取消写入")
	}
	return []byte(next), true, nil
}

// isManagedCodexTable 判断表头路径是否属于某个托管 MCP 名称：[mcp_servers.<name>] 或其子表。
func isManagedCodexTable(keys []string, managed map[string]struct{}) bool {
	if len(keys) < 2 || keys[0] != "mcp_servers" {
		return false
	}
	_, ok := managed[keys[1]]
	return ok
}

// codexBlock 生成单个 MCP 服务器的 TOML 表。env 与 http_headers 作为子表输出。
func codexBlock(server Server) []string {
	header := "[mcp_servers." + server.Name + "]"
	lines := []string{header}
	if server.Transport == TransportHTTP {
		lines = append(lines, "url = "+quoteTOML(server.URL))
		if len(server.Headers) > 0 {
			lines = append(lines, "", "[mcp_servers."+server.Name+".http_headers]")
			lines = append(lines, quoteTOMLPairs(server.Headers)...)
		}
		return lines
	}
	lines = append(lines, "command = "+quoteTOML(server.Command))
	if len(server.Args) > 0 {
		quoted := make([]string, 0, len(server.Args))
		for _, arg := range server.Args {
			quoted = append(quoted, quoteTOML(arg))
		}
		lines = append(lines, "args = ["+strings.Join(quoted, ", ")+"]")
	}
	if len(server.Env) > 0 {
		lines = append(lines, "", "[mcp_servers."+server.Name+".env]")
		lines = append(lines, quoteTOMLPairs(server.Env)...)
	}
	return lines
}

// quoteTOMLPairs 按键名排序输出 "key" = "value" 行。
func quoteTOMLPairs(values map[string]string) []string {
	keys := sortedKeys(values)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, quoteTOML(key)+" = "+quoteTOML(values[key]))
	}
	return lines
}

// quoteTOML 把字符串转为 TOML 基本字符串。JSON 的转义（\"、\\、\n、\uXXXX 等）与 TOML 基本字符串兼容。
// 对字符串做 json.Marshal 不会失败，因此这里忽略错误。
func quoteTOML(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// parseTOMLHeader 解析表头（[a.b] 或 [[a.b]]）的键路径，返回去引号后的各段。非表头行返回 false。
// 引号内的转义按原样比较：托管名称只含裸键字符，不受影响。
func parseTOMLHeader(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimPrefix(s, "[")

	var keys []string
	var part strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				part.WriteByte(c)
				i++
				part.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
			} else {
				part.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '.':
			keys = append(keys, part.String())
			part.Reset()
		case ']':
			return append(keys, part.String()), true
		case ' ', '\t':
		default:
			part.WriteByte(c)
		}
	}
	return nil, false
}
