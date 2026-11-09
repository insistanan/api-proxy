package bodyfilter

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/utils"
)

// FilterPrivateParams 递归过滤 JSON 请求体中所有以下划线 `_` 开头的内部私有字段，
// 同时保护 JSON Schema 中的定义字段名（properties / patternProperties / definitions / $defs）。
func FilterPrivateParams(requestBodyBytes []byte) ([]byte, bool, []string) {
	return FilterPrivateParamsWithWhitelist(requestBodyBytes, nil)
}

// FilterPrivateParamsWithWhitelist 过滤私有参数（支持白名单）。
func FilterPrivateParamsWithWhitelist(requestBodyBytes []byte, whitelist []string) ([]byte, bool, []string) {
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false, nil
	}

	var payload interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return requestBodyBytes, false, nil
	}

	whitelistSet := make(map[string]bool, len(whitelist))
	for _, key := range whitelist {
		whitelistSet[key] = true
	}

	var removedKeys []string
	var path []string

	filteredPayload, modified := filterRecursive(payload, path, &removedKeys, whitelistSet)
	if !modified {
		return requestBodyBytes, false, nil
	}

	filteredBytes, err := utils.MarshalJSONNoEscape(filteredPayload)
	if err != nil {
		return requestBodyBytes, false, nil
	}

	return filteredBytes, true, removedKeys
}

func isSchemaNameMapKey(key string) bool {
	return key == "properties" || key == "patternProperties" || key == "definitions" || key == "$defs"
}

func filterRecursive(
	value interface{},
	path []string,
	removedKeys *[]string,
	whitelist map[string]bool,
) (interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		isSchemaNameMap := false
		if len(path) > 0 {
			isSchemaNameMap = isSchemaNameMapKey(path[len(path)-1])
		}

		filteredMap := make(map[string]interface{}, len(typed))
		anyModified := false

		for key, val := range typed {
			// 字段以 _ 开头，不在白名单且当前不是 JSON Schema 属性名称字典
			if strings.HasPrefix(key, "_") && !whitelist[key] && !isSchemaNameMap {
				*removedKeys = append(*removedKeys, key)
				anyModified = true
				continue
			}

			subPath := append(path, key)
			filteredVal, subModified := filterRecursive(val, subPath, removedKeys, whitelist)
			if subModified {
				anyModified = true
			}
			filteredMap[key] = filteredVal
		}

		return filteredMap, anyModified

	case []interface{}:
		filteredSlice := make([]interface{}, len(typed))
		anyModified := false

		for i, item := range typed {
			filteredItem, subModified := filterRecursive(item, path, removedKeys, whitelist)
			if subModified {
				anyModified = true
			}
			filteredSlice[i] = filteredItem
		}

		return filteredSlice, anyModified

	default:
		return value, false
	}
}
