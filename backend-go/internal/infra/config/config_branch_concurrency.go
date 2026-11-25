package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const DefaultBranchConcurrency = 3

// BranchConcurrencyLimit 兼容内部构造和旧配置的未设置值；显式非法输入在解码及更新时拒绝。
func (u *UpstreamConfig) BranchConcurrencyLimit() int {
	if u.MaxBranchConcurrency == 0 {
		return DefaultBranchConcurrency
	}
	return u.MaxBranchConcurrency
}

func validateBranchConcurrency(value int) error {
	if value < 1 || int64(value) > 9007199254740991 {
		return fmt.Errorf("maxBranchConcurrency 必须是大于零的安全整数")
	}
	return nil
}

// 指针字段不能区分未传和 null；字段名与 encoding/json 一样忽略大小写。
func validateExplicitBranchConcurrency(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		if !strings.EqualFold(key, "maxBranchConcurrency") {
			continue
		}
		var value int
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("maxBranchConcurrency 必须是大于零的安全整数")
		}
		if err := validateBranchConcurrency(value); err != nil {
			return err
		}
	}
	return nil
}
