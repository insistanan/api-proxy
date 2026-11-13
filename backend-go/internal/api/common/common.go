// Package common 放置 HTTP 接入层共用的小型契约与工具。
// 只收录被两个及以上子包使用的符号，避免演变成新的杂物间。
package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ConfigManager 促销期配置管理接口，渠道路由与设置类 handler 共用。
type ConfigManager interface {
	SetChannelPromotion(index int, duration time.Duration, count int) error
}

// ReadLimitedBody 读取至多 maxBytes 字节的响应体，超限时返回错误而不是截断。
func ReadLimitedBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("响应大小限制无效")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("响应超过 %d MB", maxBytes/(1<<20))
	}
	return body, nil
}

// ExtractUsage 从原样透传响应体中提取 usage 字段，并统一 Prompt/Completion 与 Input/Output 命名。
// 解析失败或缺少 usage 时返回 nil，由调用方决定兜底策略。
func ExtractUsage(bodyBytes []byte) *types.Usage {
	var envelope struct {
		Usage *types.Usage `json:"usage"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil || envelope.Usage == nil {
		return nil
	}
	usage := envelope.Usage
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	return usage
}
