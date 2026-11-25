package sensitive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// IsProtocolStructuralField 仅用于协议外壳，工具业务对象中的同名键不适用。
func IsProtocolStructuralField(key string) bool {
	switch strings.ToLower(key) {
	case "type", "object", "id", "item_id", "call_id", "model", "role", "name", "status", "finish_reason", "stop_reason",
		"signature", "thoughtsignature", "thought_signature", "encrypted_content", "data", "image_url", "url":
		return true
	default:
		return false
	}
}

// IsSignedProtocolValue 保护需要原样回传的思考块及签名绑定内容。
func IsSignedProtocolValue(value map[string]interface{}) bool {
	typeName, _ := value["type"].(string)
	return typeName == "thinking" || typeName == "thinking_delta" || typeName == "redacted_thinking" ||
		value["thought"] == true || value["thoughtSignature"] != nil || value["thought_signature"] != nil
}

// ValidateToolText 在工具交付前拒绝未知或破损占位符，不输出敏感原文。
func (v *Vault) ValidateToolText(text string) error {
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], "MASKED_")
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len("MASKED_")
		for end < len(text) && isPlaceholderIdentifierByte(text[end]) {
			end++
		}
		token := text[start:end]
		if len(token) > maxPlaceholderBytes || reservedPlaceholderPattern.FindString(token) != token {
			return fmt.Errorf("工具参数包含不完整或已被改写的脱敏占位符")
		}
		if _, ok := v.lookupPlaceholder(token); !ok {
			if err := v.Err(); err != nil {
				return err
			}
			return fmt.Errorf("工具参数的脱敏占位符未获当前请求或会话授权，拒绝交付")
		}
		if start > 0 && isPlaceholderIdentifierByte(text[start-1]) {
			return fmt.Errorf("工具参数的脱敏占位符边界已被改写")
		}
		offset = end
	}
	return v.Err()
}

// ValidateRequestPlaceholders 防止伪造占位符绕过检测；必须由当前映射解释其来源。
func (v *Vault) ValidateRequestPlaceholders(text string) error {
	if !strings.Contains(text, "MASKED_") {
		return v.Err()
	}
	spans := placeholderTokenPattern.FindAllStringIndex(text, -1)
	for _, span := range spans {
		start, end := span[0], span[1]
		token := text[start:end]
		if len(token) > maxPlaceholderBytes || start > 0 && isPlaceholderIdentifierByte(text[start-1]) || end < len(text) && isPlaceholderIdentifierByte(text[end]) {
			return fmt.Errorf("请求包含破损或与其他标识符粘连的脱敏占位符，无法保证可逆还原")
		}
		v.mu.RLock()
		_, known := v.byPlaceholder[token]
		v.mu.RUnlock()
		if !known {
			return fmt.Errorf("请求包含未获会话授权的脱敏占位符，请使用稳定会话标识并提供完整原始历史")
		}
	}
	return v.Err()
}

func (v *Vault) ValidateToolJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("待还原工具参数不是有效 JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("待还原工具参数包含多余 JSON 内容")
	}
	return v.validateToolValue(value)
}

func (v *Vault) validateToolValue(value any) error {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
			return v.ValidateToolJSON([]byte(trimmed))
		}
		return v.ValidateToolText(typed)
	case map[string]interface{}:
		for key, child := range typed {
			if strings.Contains(key, "MASKED_") {
				return fmt.Errorf("工具参数键包含脱敏占位符，无法保持 schema")
			}
			if err := v.validateToolValue(child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range typed {
			if err := v.validateToolValue(child); err != nil {
				return err
			}
		}
	}
	return nil
}
