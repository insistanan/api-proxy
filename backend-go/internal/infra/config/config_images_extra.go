package config

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// imagesExtraJSONNumber 与前端校验使用同一套 JSON 数字文法。
// 前导零、正号和残缺小数不当作数字，避免把普通字符串误判成非法 JSON。
var imagesExtraJSONNumber = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)

// normalizeImagesExtraParams 归一化自定义参数。
// nil 与空 map 都表示没有参数。键会去掉首尾空白；裁剪后冲突、空键、
// 保留字段和非法值都显式报错，不静默丢弃。
func normalizeImagesExtraParams(params map[string]string) (map[string]string, error) {
	if len(params) == 0 {
		return nil, nil
	}
	rawKeys := make([]string, 0, len(params))
	for key := range params {
		rawKeys = append(rawKeys, key)
	}
	sort.Strings(rawKeys)

	normalized := make(map[string]string, len(rawKeys))
	for _, rawKey := range rawKeys {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return nil, &ConfigError{Message: "Images 自定义参数名不能为空"}
		}
		if strings.ContainsAny(key, "\r\n") {
			return nil, &ConfigError{Message: "Images 自定义参数名不能包含换行"}
		}
		if key == "model" || key == "stream" {
			return nil, &ConfigError{Message: fmt.Sprintf("Images 自定义参数不能使用保留字段 %q", key)}
		}
		if _, exists := normalized[key]; exists {
			return nil, &ConfigError{Message: fmt.Sprintf("Images 自定义参数名重复: %s", key)}
		}
		value := strings.TrimSpace(params[rawKey])
		if _, err := parseImagesExtraParamValue(value); err != nil {
			return nil, &ConfigError{Message: fmt.Sprintf("Images 自定义参数 %q 的值无效: %s", key, err.Error())}
		}
		normalized[key] = value
	}
	return normalized, nil
}

// ParseImagesExtraParams 把已保存的文本参数解析成可写入 JSON 的值。
// true/false/null、JSON 数字、对象和数组按对应类型；其余是字符串。
// 强制字符串时保存带引号的 JSON 字符串，例如 "false"。
func ParseImagesExtraParams(params map[string]string) (map[string]interface{}, error) {
	normalized, err := normalizeImagesExtraParams(params)
	if err != nil {
		return nil, err
	}
	parsed := make(map[string]interface{}, len(normalized))
	for key, value := range normalized {
		typed, err := parseImagesExtraParamValue(value)
		if err != nil {
			return nil, &ConfigError{Message: fmt.Sprintf("Images 自定义参数 %q 的值无效: %s", key, err.Error())}
		}
		parsed[key] = typed
	}
	return parsed, nil
}

func parseImagesExtraParamValue(raw string) (interface{}, error) {
	if raw == "" || !imagesExtraParamLooksStructured(raw) {
		return raw, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("不是合法 JSON")
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return nil, fmt.Errorf("不是合法 JSON")
	}
	return value, nil
}

func imagesExtraParamLooksStructured(value string) bool {
	switch value {
	case "true", "false", "null":
		return true
	}
	if imagesExtraJSONNumber.MatchString(value) {
		return true
	}
	switch value[0] {
	case '{', '[', '"':
		return true
	default:
		return false
	}
}
