// Package common 放置 handlers 下多个业务子包共用的小型契约与工具。
// 只收录被两个及以上子包使用的符号，避免演变成新的杂物间。
package common

import (
	"errors"
	"fmt"
	"io"
	"time"
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
