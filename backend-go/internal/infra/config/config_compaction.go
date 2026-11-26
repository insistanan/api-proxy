package config

import (
	"fmt"
	"strings"
)

// CompactionConfig 分开声明摘要能力与委托策略；默认全部关闭。
// 原生压缩仅允许同渠道、单地址、单密钥的精确模型对，兼容性由管理员确认。
type CompactionConfig struct {
	SummaryPool                  bool   `json:"summaryPool"`
	SummaryCapable               bool   `json:"summaryCapable"`
	SummaryModel                 string `json:"summaryModel"`
	DetectClientPrompts          bool   `json:"detectClientPrompts"`
	NativeSourceModel            string `json:"nativeSourceModel"`
	NativeModel                  string `json:"nativeModel"`
	NativeCompatibilityConfirmed bool   `json:"nativeCompatibilityConfirmed"`
}

func validateCompactionConfig(kind string, up *UpstreamConfig) error {
	c := up.Compaction
	if c.NativeModel != strings.TrimSpace(c.NativeModel) || c.NativeSourceModel != strings.TrimSpace(c.NativeSourceModel) || c.SummaryModel != strings.TrimSpace(c.SummaryModel) {
		return fmt.Errorf("压缩模型 ID 不能包含首尾空白")
	}
	if c == (CompactionConfig{}) {
		return nil
	}
	if !IsConversationChannelKind(kind) || up.BuiltinProvider != "" {
		return fmt.Errorf("上下文压缩配置只适用于普通会话渠道")
	}
	if c.SummaryCapable {
		if !up.ExcludeFromConversation || strings.TrimSpace(c.SummaryModel) == "" {
			return fmt.Errorf("摘要池渠道必须关闭常规对话调度并指定摘要模型")
		}
		if c.SummaryPool {
			return fmt.Errorf("摘要池渠道不能再次委托摘要池")
		}
	} else if c.SummaryModel != "" {
		return fmt.Errorf("指定摘要模型前必须加入摘要池")
	}
	if c.NativeModel == "" {
		if c.NativeSourceModel != "" || c.NativeCompatibilityConfirmed {
			return fmt.Errorf("原生压缩配置缺少目标模型")
		}
		return nil
	}
	native := (kind == "messages" && up.ServiceType == "claude") || (kind == "responses" && up.ServiceType == "responses")
	if !native || c.SummaryCapable || strings.TrimSpace(c.NativeSourceModel) == "" || !c.NativeCompatibilityConfirmed {
		return fmt.Errorf("原生压缩替换要求原生 Messages/Responses 渠道、精确源模型及兼容性确认")
	}
	if c.NativeSourceModel == c.NativeModel {
		return fmt.Errorf("原生压缩目标模型必须与源模型不同")
	}
	if len(up.APIKeys) != 1 || len(up.GetAllBaseURLs()) != 1 || up.StripThoughtSignature || up.InjectDummyThoughtSignature {
		return fmt.Errorf("原生压缩替换要求单地址、单密钥，且不能删除或伪造思考签名")
	}
	return nil
}
