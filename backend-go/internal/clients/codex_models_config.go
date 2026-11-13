// Codex 模型目录与 config.toml 的同步；保持根键修改和目录清理语义。
package clients

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// setCodexDefaultModel 将 config.toml 根级 model / model_catalog_json / model_reasoning_effort
// 指向目标模型。状态机：
//   - 目标在 catalog 中：正常写入；顺带补齐缺失的内置条目（替换语义下不补则内置模型失效）
//   - 目标是内置模型且 catalog 非空：把内置条目并入 catalog 后写入
//   - 目标是内置模型且 catalog 为空：只写 model 键并移除 model_catalog_json（Codex 直接用内置清单）
func setCodexDefaultModel(ctx context.Context, configPath, modelsPath, slug, reasoningEffort string, clearReasoningEffort bool) error {
	catalog, err := readCodexModelCatalog(modelsPath)
	if err != nil {
		return err
	}
	builtin, builtinErr := loadCodexBuiltinCatalog(ctx, true)

	catalogChanged := false
	meta, findErr := catalog.findModel(slug)
	switch {
	case findErr == nil:
		if builtinErr == nil {
			catalogChanged = catalog.ensureBuiltinEntries(builtin)
		}
	case builtinErr != nil:
		return findErr
	case !builtin.has(slug):
		return newCodexInputError("模型 %s 不存在", slug)
	case len(catalog.entries) == 0:
		if meta, findErr = builtin.entryMeta(slug); findErr != nil {
			return findErr
		}
	default:
		catalogChanged = catalog.ensureBuiltinEntries(builtin)
		if meta, findErr = catalog.findModel(slug); findErr != nil {
			return findErr
		}
	}
	if reasoningEffort != "" && !meta.hasEffort(reasoningEffort) {
		return newCodexInputError("模型 %s 不支持推理档位 %q", slug, reasoningEffort)
	}

	if catalogChanged {
		encoded, err := catalog.encode()
		if err != nil {
			return fmt.Errorf("编码 models.json 失败: %w", err)
		}
		// 先落盘 models.json，再写 config.toml，保证 model_catalog_json 指向的文件先就绪。
		if err := writeCodexFile(modelsPath, catalog.raw, encoded, ".codex-models-*.tmp"); err != nil {
			return fmt.Errorf("保存 models.json 失败: %w", err)
		}
	}

	_, configRaw, _, err := readCodexConfig(configPath)
	if err != nil {
		return err
	}
	updatedConfig := configRaw
	model := slug
	if updatedConfig, err = mergeCodexRootKey(updatedConfig, codexRootKeyModel, &model); err != nil {
		return err
	}
	if len(catalog.entries) > 0 {
		catalogPath := modelsPath
		if updatedConfig, err = mergeCodexRootKey(updatedConfig, codexRootKeyModelCatalog, &catalogPath); err != nil {
			return err
		}
	} else if updatedConfig, err = mergeCodexRootKey(updatedConfig, codexRootKeyModelCatalog, nil); err != nil {
		return err
	}
	if reasoningEffort != "" {
		effort := reasoningEffort
		if updatedConfig, err = mergeCodexRootKey(updatedConfig, codexRootKeyReasoningLevel, &effort); err != nil {
			return err
		}
	} else if clearReasoningEffort {
		if updatedConfig, err = mergeCodexRootKey(updatedConfig, codexRootKeyReasoningLevel, nil); err != nil {
			return err
		}
	}
	if !bytes.Equal(configRaw, updatedConfig) {
		if err := writeCodexFile(configPath, configRaw, updatedConfig, ".codex-config-*.tmp"); err != nil {
			return fmt.Errorf("保存 Codex config.toml 失败: %w", err)
		}
	}
	return nil
}

// cleanupCodexConfigAfterModelDelete 在删除条目后回退 config.toml：
//   - 目录删空：移除 model_catalog_json（空目录会让 Codex 拒绝启动，回退后使用内置清单）
//   - 被删模型是当前默认模型：移除 model 与 model_reasoning_effort（指向不存在的模型会让 Codex 启动失败）
func cleanupCodexConfigAfterModelDelete(configPath, slug string, catalogEmpty bool) error {
	config, configRaw, exists, err := readCodexConfig(configPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	updated := configRaw
	if catalogEmpty {
		if updated, err = mergeCodexRootKey(updated, codexRootKeyModelCatalog, nil); err != nil {
			return err
		}
	}
	if strings.TrimSpace(config.Model) == slug {
		if updated, err = mergeCodexRootKey(updated, codexRootKeyModel, nil); err != nil {
			return err
		}
		if updated, err = mergeCodexRootKey(updated, codexRootKeyReasoningLevel, nil); err != nil {
			return err
		}
	}
	if bytes.Equal(configRaw, updated) {
		return nil
	}
	return writeCodexFile(configPath, configRaw, updated, ".codex-config-*.tmp")
}

// mergeCodexRootKey 定点修改 config.toml 根级键（仅限白名单键），保留其余内容与格式。
// value 为 nil 时删除该键；键不存在时插入到首个表头之前（TOML 根级键必须位于任何表头之前）。
func mergeCodexRootKey(raw []byte, key string, value *string) ([]byte, error) {
	if !slices.Contains(codexRootKeys, key) {
		return nil, fmt.Errorf("不支持的 config.toml 根级字段: %s", key)
	}
	lineEnding := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		lineEnding = "\r\n"
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	firstTable := len(lines)
	for index, line := range lines {
		if isTOMLTableHeader(line) {
			firstTable = index
			break
		}
	}

	keyPattern := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(key) + `\s*=`)
	keyIndex := -1
	indent := ""
	for index := 0; index < firstTable; index++ {
		if match := keyPattern.FindStringSubmatch(lines[index]); match != nil {
			keyIndex = index
			indent = match[1]
			break
		}
	}

	switch {
	case keyIndex >= 0 && value == nil:
		lines = append(lines[:keyIndex], lines[keyIndex+1:]...)
	case keyIndex >= 0:
		lines[keyIndex] = indent + key + " = " + quoteTOMLString(*value)
	case value != nil:
		insertAt := firstTable
		for insertAt > 0 {
			previous := strings.TrimSpace(lines[insertAt-1])
			if previous == "" || strings.HasPrefix(previous, "#") {
				insertAt--
				continue
			}
			break
		}
		keyLine := key + " = " + quoteTOMLString(*value)
		lines = append(lines[:insertAt], append([]string{keyLine}, lines[insertAt:]...)...)
	}

	updated := strings.Join(lines, "\n")
	if lineEnding == "\r\n" {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	result := []byte(updated)
	var validation map[string]interface{}
	if err := toml.Unmarshal(result, &validation); err != nil {
		return nil, fmt.Errorf("更新后的 config.toml 无效: %w", err)
	}
	return result, nil
}

// quoteTOMLString 生成 TOML 字符串字面量：优先使用单引号字面量字符串（无需转义），
// 值中含单引号或控制字符时退回到双引号基本字符串并做 TOML 转义。
func quoteTOMLString(value string) string {
	needsEscape := strings.ContainsRune(value, '\'')
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return "'" + value + "'"
	}
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				builder.WriteString(fmt.Sprintf(`\u%04X`, r))
			} else {
				builder.WriteRune(r)
			}
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
