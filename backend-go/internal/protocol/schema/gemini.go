// Package schema 只转换 schema 方言，不清洗参数名或业务常量。
package schema

import "strings"

// FromGemini 将 Gemini Schema 的 type/nullable 转成 JSON Schema。
// 只递归 schema 位置；enum、const、default 和 properties 的键原样保留。
func FromGemini(value any) any {
	object, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	result := make(map[string]interface{}, len(object))
	for key, child := range object {
		switch key {
		case "nullable":
			continue
		case "type":
			if name, ok := child.(string); ok {
				result[key] = strings.ToLower(name)
			} else {
				result[key] = child
			}
		case "properties", "$defs", "definitions", "patternProperties":
			if entries, ok := child.(map[string]interface{}); ok {
				mapped := make(map[string]interface{}, len(entries))
				for name, definition := range entries {
					mapped[name] = FromGemini(definition)
				}
				result[key] = mapped
			} else {
				result[key] = child
			}
		case "items", "additionalProperties", "not", "if", "then", "else":
			result[key] = FromGemini(child)
		case "anyOf", "oneOf", "allOf", "prefixItems":
			if entries, ok := child.([]interface{}); ok {
				mapped := make([]interface{}, len(entries))
				for i, definition := range entries {
					mapped[i] = FromGemini(definition)
				}
				result[key] = mapped
			} else {
				result[key] = child
			}
		default:
			result[key] = child
		}
	}
	if nullable, _ := object["nullable"].(bool); nullable {
		return map[string]interface{}{"anyOf": []interface{}{result, map[string]interface{}{"type": "null"}}}
	}
	return result
}
