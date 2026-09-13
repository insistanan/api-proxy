package sensitive

import (
	"regexp"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// CustomMatch 描述一次自定义规则命中。Original 只用于当前请求内完成替换，
// 调用方不得将其写入日志或错误响应。
type CustomMatch struct {
	Rule     string
	Type     string
	Original string
	Start    int
	End      int
}

type customRule struct {
	name    string
	typeTag string
	pattern *regexp.Regexp
}

// CustomDetector 执行用户自定义的正则检测规则。它只负责检测，
// 命中后的可逆脱敏由请求 Hook 统一执行（走 Vault 占位符链路）。
// 仅在 sensitiveData.mode=mask 时被构造参与，block 模式不构造。
type CustomDetector struct {
	enabled bool
	rules   []customRule
}

// NewCustomDetector 根据设置编译自定义规则。配置合法性（类型前缀字符集、
// 正则可编译、去重）由 config.ValidateCustomRulesConfig 把关；此处再防御一次
// 正则编译，保证检测器永不持有半初始化状态。
func NewCustomDetector(settings config.CustomRulesConfig) (*CustomDetector, error) {
	detector := &CustomDetector{enabled: settings.Enabled}
	if !settings.Enabled {
		return detector, nil
	}
	for _, item := range settings.Rules {
		pattern, err := regexp.Compile(item.Pattern)
		if err != nil {
			return nil, err
		}
		detector.rules = append(detector.rules, customRule{
			name:    strings.TrimSpace(item.Name),
			typeTag: strings.TrimSpace(item.Type),
			pattern: pattern,
		})
	}
	return detector, nil
}

// Enabled 返回自定义规则检测是否启用。
func (d *CustomDetector) Enabled() bool {
	return d != nil && d.enabled && len(d.rules) > 0
}

// FindAll 返回文本中全部自定义规则命中，按 (Start, 长度降序) 稳定排序，
// 与 Vault.Mask 期望的区间顺序一致。跨规则重叠区间由 Vault.Mask 的区间
// 合法性校验兜底（重叠即报错，不静默丢命中）；正则规则间通常不重叠。
func (d *CustomDetector) FindAll(text string) []CustomMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 || text == "" {
		return nil
	}
	matches := make([]CustomMatch, 0)
	for _, rule := range d.rules {
		for _, index := range rule.pattern.FindAllStringIndex(text, -1) {
			if index[0] == index[1] {
				continue // 空匹配无掩码意义
			}
			matches = append(matches, CustomMatch{
				Rule:     rule.name,
				Type:     rule.typeTag,
				Original: text[index[0]:index[1]],
				Start:    index[0],
				End:      index[1],
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End > matches[j].End
	})
	return matches
}
