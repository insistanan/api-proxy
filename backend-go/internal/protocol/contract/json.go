// Package contract 定义各协议转换边界共用的校验，不执行降级或注入默认业务值。
package contract

import (
	"encoding/json"
	"fmt"

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
