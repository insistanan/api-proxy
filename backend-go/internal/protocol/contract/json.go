// Package contract 定义各协议转换边界共用的校验，不执行降级或注入默认业务值。
package contract

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func Unsupported(format string, args ...any) error {
	return &types.UnsupportedProtocolError{Err: fmt.Errorf(format, args...)}
}

func Invalid(format string, args ...any) error {
	return &types.InvalidRequestError{Err: fmt.Errorf(format, args...)}
}

func Object(data []byte, label string) (map[string]interface{}, error) {
	var value map[string]interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s 不是有效 JSON 对象: %w", label, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%s 不能为 null", label)
	}
	return value, nil
}

// Arguments 区分合法空对象和缺失、损坏、数组或标量参数。
func Arguments(raw, callID string) (map[string]interface{}, error) {
	return Object([]byte(raw), "工具 "+callID+" 的参数")
}

func RequestToolCall(callID, name, raw string) error {
	if strings.TrimSpace(callID) == "" || strings.TrimSpace(name) == "" {
		return Invalid("历史工具调用必须包含调用 ID 和名称")
	}
	if _, err := Arguments(raw, callID); err != nil {
		return Invalid("历史工具调用无效: %w", err)
	}
	return nil
}

// StreamArguments 接受空初始对象加 JSON 分片，或只有完整初始参数。
// 两份非空参数没有确定的合并语义，不能悄悄用分片覆盖初始输入。
func StreamArguments(initial, delta, callID string) (string, error) {
	if delta == "" {
		return initial, nil
	}
	if initial != "" {
		value, err := Arguments(initial, callID)
		if err != nil {
			return "", err
		}
		if len(value) > 0 {
			return "", fmt.Errorf("工具 %s 同时包含非空初始参数和增量参数，无法确定合并语义", callID)
		}
	}
	return delta, nil
}
