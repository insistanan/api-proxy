package clients

import (
	"strings"

	"insistanan/api-proxy/internal/protocol/codec"
)

// 以下访问器仅用于通过结构校验后的受管理字段。
func objectValue(value interface{}) map[string]interface{} {
	if object, ok := value.(map[string]interface{}); ok {
		return object
	}
	return make(map[string]interface{})
}

func stringMapValue(value interface{}) map[string]string {
	object := objectValue(value)
	result := make(map[string]string, len(object))
	for key, raw := range object {
		if codec.StringValue(raw) != "" {
			result[key] = codec.StringValue(raw)
		}
	}
	return result
}

func copyObject(source map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func intValue(value interface{}) int {
	number, ok := value.(float64)
	if !ok {
		return 0
	}
	return int(number)
}

func setOptionalString(object map[string]interface{}, key, value string) {
	if value = strings.TrimSpace(value); value == "" {
		delete(object, key)
		return
	}
	object[key] = value
}
