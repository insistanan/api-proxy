package providers

import (
	"github.com/insistanan/api-proxy/internal/protocol/codec"
)

// restoreGeminiShadowMetadata 仅在当前可见内容和调用 ID 完整匹配时恢复原始
// part 边界与签名；名称、位置或相似文本均不能作为历史替换依据。
func restoreGeminiShadowMetadata(current map[string]interface{}, source interface{}, snapshot GeminiShadowSnapshot, strip, inject bool) {
	currentParts, ok := current["parts"].([]interface{})
	if !ok {
		return
	}
	identity := geminiVisiblePartsIdentity(currentParts)
	ids, _ := extractAssistantToolUseKeys(source)
	match := -1
	for i, turn := range snapshot.Turns {
		if len(ids) != len(turn.ToolCalls) {
			continue
		}
		valid := true
		for _, call := range turn.ToolCalls {
			if !ids[call.ID] {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		parts, _ := turn.AssistantContent["parts"].([]interface{})
		if identity != geminiVisiblePartsIdentity(parts) {
			continue
		}
		if match >= 0 {
			return
		} // 多个相同回合无法确定签名归属。
		match = i
	}
	if match >= 0 {
		current["parts"] = geminiShadowReplayParts(snapshot.Turns[match].AssistantContent, strip, inject)
	}
}

func geminiVisiblePartsIdentity(parts []interface{}) string {
	result := make([]interface{}, 0, len(parts))
	text := ""
	flush := func() {
		if text != "" {
			result = append(result, map[string]interface{}{"text": text})
			text = ""
		}
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]interface{})
		if !ok || isGeminiThoughtPart(part) {
			continue
		}
		copy := cloneMapStringInterface(part)
		delete(copy, "thoughtSignature")
		delete(copy, "thought_signature")
		if value, ok := copy["text"].(string); ok && len(copy) == 1 {
			text += value
			continue
		}
		flush()
		if call, ok := copy["functionCall"].(map[string]interface{}); ok {
			delete(call, "id")
			delete(call, "thoughtSignature")
			delete(call, "thought_signature")
		}
		result = append(result, copy)
	}
	flush()
	return codec.CanonicalJSON(result)
}
