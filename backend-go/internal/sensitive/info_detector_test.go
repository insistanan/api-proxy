package sensitive

import (
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
)

func TestInfoDetectorMasksEveryRule(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}

	tests := []struct {
		name     string
		rule     string
		input    string
		expected string
	}{
		{name: "手机号一", rule: config.SensitiveInfoRulePhone, input: "手机号 13800138000", expected: "手机号 [MASKED_PII:phone]"},
		{name: "手机号二", rule: config.SensitiveInfoRulePhone, input: "手机 13912345678", expected: "手机 [MASKED_PII:phone]"},
		{name: "手机号三", rule: config.SensitiveInfoRulePhone, input: "电话 18612345678", expected: "电话 [MASKED_PII:phone]"},
		{name: "身份证数字尾", rule: config.SensitiveInfoRuleIDCard, input: "11010519491231002X", expected: "[MASKED_PII:id_card]"},
		{name: "身份证大写X", rule: config.SensitiveInfoRuleIDCard, input: "11010519491231002X", expected: "[MASKED_PII:id_card]"},
		{name: "身份证小写x", rule: config.SensitiveInfoRuleIDCard, input: "11010519491231002x", expected: "[MASKED_PII:id_card]"},
		{name: "普通邮箱", rule: config.SensitiveInfoRuleEmail, input: "user@example.com", expected: "[MASKED_PII:email]"},
		{name: "单字邮箱", rule: config.SensitiveInfoRuleEmail, input: "a@example.com", expected: "[MASKED_PII:email]"},
		{name: "带标签邮箱", rule: config.SensitiveInfoRuleEmail, input: "user+tag@example.com", expected: "[MASKED_PII:email]"},
		{name: "公网IPv4", rule: config.SensitiveInfoRuleIPAddress, input: "8.8.8.8", expected: "[MASKED_PII:ip_address]"},
		{name: "公网IPv6", rule: config.SensitiveInfoRuleIPAddress, input: "2607:f8b0:4005:808::200e", expected: "[MASKED_PII:ip_address]"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			masked, matches := detector.Mask(test.input)
			if masked != test.expected {
				t.Fatalf("掩码结果错误: got %q, want %q", masked, test.expected)
			}
			if len(matches) != 1 || matches[0].Rule != test.rule {
				t.Fatalf("命中明细错误: %+v", matches)
			}
			if matches[0].Start < 0 || matches[0].End > len(test.input) ||
				matches[0].Original != test.input[matches[0].Start:matches[0].End] {
				t.Fatalf("命中原文或偏移错误: %+v", matches[0])
			}
		})
	}
}

func TestInfoDetectorPublicScopeSkipsNonPublicIPs(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}

	inputs := []string{
		"监听地址 0.0.0.0:8080",
		"回环地址 127.0.0.1",
		"内网网关 192.168.1.1",
		"内网段 10.0.0.5",
		"容器网络 172.16.0.9",
		"链路本地 169.254.1.1",
		"组播 224.0.0.1",
		"广播 255.255.255.255",
		"文档示例 192.0.2.10",
		"文档示例 198.51.100.7",
		"文档示例 203.0.113.99",
		"基准测试 198.18.0.2",
		"运营商NAT 100.64.0.1",
		"保留段 240.1.2.3",
		"IPv6回环 ::1",
		"IPv6链路本地 fe80::1",
		"IPv6唯一本地 fd00::1",
		"IPv6文档 2001:db8::1",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			masked, matches := detector.Mask(input)
			if masked != input || len(matches) != 0 {
				t.Fatalf("非公网地址不应被掩码: %q -> %q, matches: %+v", input, masked, matches)
			}
		})
	}
}

func TestInfoDetectorAllScopeMasksEveryIP(t *testing.T) {
	settings := defaultSensitiveInfoSettings()
	settings.IPMaskScope = config.SensitiveInfoIPMaskScopeAll
	detector, err := NewInfoDetector(settings)
	if err != nil {
		t.Fatalf("创建全量掩码检测器失败: %v", err)
	}

	inputs := []string{
		"回环地址 127.0.0.1",
		"内网网关 192.168.1.1",
		"IPv6文档 2001:db8::1",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			masked, matches := detector.Mask(input)
			if len(matches) != 1 || matches[0].Rule != config.SensitiveInfoRuleIPAddress {
				t.Fatalf("全量掩码范围下应命中 IP 规则: %q -> %q, matches: %+v", input, masked, matches)
			}
			if masked == input {
				t.Fatalf("全量掩码范围下地址未被替换: %q", masked)
			}
		})
	}
}

func TestInfoDetectorVersionHintSkipsVersionNumbers(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}

	inputs := []string{
		"version 1.2.3.4",
		"appVersion: 52.109.0.7",
		"my-build@18.31.4.10",
		"release = 3.9.0.1",
		"revision 12.0.0.1",
		"Rev 6.1.2.3",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			masked, matches := detector.Mask(input)
			if masked != input || len(matches) != 0 {
				t.Fatalf("版本号语境不应被掩码: %q -> %q, matches: %+v", input, masked, matches)
			}
		})
	}
}

func TestInfoDetectorVersionHintDoesNotMaskRealIPs(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "普通公网IP仍掩码",
			input:    "服务器 8.8.4.4 可用",
			expected: "服务器 [MASKED_PII:ip_address] 可用",
		},
		{
			name:     "版本号关键词后接多段文本仍掩码",
			input:    "版本回退到 8.8.4.4 之后失败",
			expected: "版本回退到 [MASKED_PII:ip_address] 之后失败",
		},
		{
			name:     "数字结尾的普通词不构成版本语境",
			input:    "升级 release2 8.8.4.4 立刻生效",
			expected: "升级 release2 [MASKED_PII:ip_address] 立刻生效",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			masked, matches := detector.Mask(test.input)
			if masked != test.expected {
				t.Fatalf("掩码结果错误: got %q, want %q", masked, test.expected)
			}
			if len(matches) != 1 || matches[0].Rule != config.SensitiveInfoRuleIPAddress {
				t.Fatalf("命中明细错误: %+v", matches)
			}
		})
	}
}

func TestInfoDetectorIgnoresInvalidIDLikeNumbers(t *testing.T) {
	detector, err := NewInfoDetector(config.SensitiveInfoConfig{
		Enabled:      true,
		Mode:         config.ContentSafetyModeMask,
		EnabledRules: []string{config.SensitiveInfoRuleIDCard},
	})
	if err != nil {
		t.Fatalf("创建身份证检测器失败: %v", err)
	}
	input := "订单号 123456789012345678，日期错误 11010520261301002X"
	masked, matches := detector.Mask(input)
	if masked != input || len(matches) != 0 {
		t.Fatalf("普通十八位数字不应被当成身份证: %q %+v", masked, matches)
	}
}

func TestInfoDetectorRuleSelectionAndInvalidValues(t *testing.T) {
	settings := config.SensitiveInfoConfig{
		Enabled:      true,
		EnabledRules: []string{config.SensitiveInfoRulePhone},
	}
	detector, err := NewInfoDetector(settings)
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}
	input := "key=sk-1234567890abcdef phone=13800138000 ip=999.1.1.1"
	masked, matches := detector.Mask(input)
	if masked != "key=sk-1234567890abcdef phone=[MASKED_PII:phone] ip=999.1.1.1" {
		t.Fatalf("规则选择错误: %q", masked)
	}
	if len(matches) != 1 || matches[0].Rule != config.SensitiveInfoRulePhone {
		t.Fatalf("规则选择命中错误: %+v", matches)
	}

	if _, err := NewInfoDetector(config.SensitiveInfoConfig{Enabled: true, EnabledRules: []string{"unknown"}}); err == nil {
		t.Fatal("未知规则应返回错误")
	}
	if _, err := NewInfoDetector(config.SensitiveInfoConfig{Enabled: true, EnabledRules: []string{config.SensitiveInfoRulePhone, config.SensitiveInfoRulePhone}}); err == nil {
		t.Fatal("重复规则应返回错误")
	}
}

func TestInfoDetectorIPMaskScopeValidation(t *testing.T) {
	settings := config.SensitiveInfoConfig{
		Enabled:      true,
		EnabledRules: []string{config.SensitiveInfoRuleIPAddress},
	}

	// 零值等价于 public：既有调用方不填该字段时保持默认行为。
	settings.IPMaskScope = ""
	detector, err := NewInfoDetector(settings)
	if err != nil {
		t.Fatalf("空 IP 掩码范围应按默认 public 处理: %v", err)
	}
	if masked, _ := detector.Mask("内网 192.168.1.1"); masked != "内网 192.168.1.1" {
		t.Fatalf("空范围应按 public 跳过内网地址: %q", masked)
	}

	settings.IPMaskScope = "unexpected"
	if _, err := NewInfoDetector(settings); err == nil {
		t.Fatal("未知 IP 掩码范围应返回错误")
	}
}

func TestInfoDetectorMasksMultipleValuesDeterministically(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}
	input := "联系 alice@example.com，电话 13800138000，服务地址 2607:f8b0:4005:808::200e。"
	masked, matches := detector.Mask(input)
	expected := "联系 [MASKED_PII:email]，电话 [MASKED_PII:phone]，服务地址 [MASKED_PII:ip_address]。"
	if masked != expected {
		t.Fatalf("多值掩码错误: got %q, want %q", masked, expected)
	}
	if len(matches) != 3 {
		t.Fatalf("多值命中数量错误: %+v", matches)
	}
	for index := 1; index < len(matches); index++ {
		if matches[index-1].End > matches[index].Start {
			t.Fatalf("命中发生重叠或乱序: %+v", matches)
		}
	}
}

func TestInfoDetectorDisabled(t *testing.T) {
	settings := defaultSensitiveInfoSettings()
	settings.Enabled = false
	detector, err := NewInfoDetector(settings)
	if err != nil {
		t.Fatalf("创建关闭的检测器失败: %v", err)
	}
	input := "alice@example.com 电话 13800138000"
	masked, matches := detector.Mask(input)
	if masked != input || len(matches) != 0 {
		t.Fatalf("关闭后仍发生掩码: %q %+v", masked, matches)
	}
}

// TestInfoDetectorCodeLikeTextNoFalsePositives 用冒号/十六进制密集的代码与
// 配置文本（旧 IPv6 宽正则的高频误判来源）验证 IP 规则不会产生误掩码。
// 时间字面量、bash 命令、URL、UUID、CSS 颜色、路径等都不是 IP，必须原样放行。
func TestInfoDetectorCodeLikeTextNoFalsePositives(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}
	inputs := []string{
		"time:12:30:45",
		"12:34",
		"deadline:2026-09-17T08:00:00Z",
		"export PATH=/usr/local/bin:/usr/bin:/bin:$PATH",
		"https://example.com:8443/path?query=abc",
		"https://example.com",
		"uuid=550e8400-e29b-41d4-a716-446655440000",
		"color: #ff00aa; border-color: #abcdef",
		"case ${var:-default} in",
		"triple:::colon 不是合法 IPv6",
		"gggg:hhhh:iiii 组超出十六进制字符范围",
		"ratio 2:1:3 与 10:20:30 不是地址",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			masked, matches := detector.Mask(input)
			if masked != input || len(matches) != 0 {
				t.Fatalf("代码类文本不应被掩码: %q -> %q, matches: %+v", input, masked, matches)
			}
		})
	}
}

// TestInfoDetectorIPv6BoundaryCases 验证 IPv6 扫描的边界行为：公网地址
// 混在代码文本中仍应被识别，且识别的区间与原正则候选一致。
func TestInfoDetectorIPv6BoundaryCases(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "行首完整地址",
			input:    "2607:f8b0:4005:808::200e",
			expected: "[MASKED_PII:ip_address]",
		},
		{
			name:     "冒号路径中的地址",
			input:    "endpoint=2607:f8b0:4005:808::200e 备用",
			expected: "endpoint=[MASKED_PII:ip_address] 备用",
		},
		{
			name:     "地址后接冒号端口分隔符",
			input:    "[2606:4700:4700::1111]:443",
			expected: "[[MASKED_PII:ip_address]]:443",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			masked, matches := detector.Mask(test.input)
			if masked != test.expected {
				t.Fatalf("掩码结果错误: got %q, want %q", masked, test.expected)
			}
			if len(matches) != 1 {
				t.Fatalf("命中数量错误: %+v", matches)
			}
			if matches[0].Start < 0 || matches[0].End > len(test.input) ||
				matches[0].Original != test.input[matches[0].Start:matches[0].End] {
				t.Fatalf("命中原文或偏移错误: %+v", matches[0])
			}
		})
	}
}

// TestInfoDetectorFeatureGateSkipsImpossibleRules 验证必要条件预筛：
// 缺少结构特征的文本跳过对应规则，不产生任何命中；特征齐全时规则正常执行。
func TestInfoDetectorFeatureGateSkipsImpossibleRules(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}
	if len(detector.rules) == 0 {
		t.Fatal("默认配置应启用至少一条规则")
	}
	for _, rule := range detector.rules {
		if rule.gate == nil {
			t.Fatalf("规则 %s 缺少必要条件预筛", rule.name)
		}
	}

	// 纯中文散文：无 '@'、无 '.'、无 ':'、无长数字块，所有规则 gate 都应拒绝。
	plainText := "这是一段普通的项目说明，请继续整理函数的输入输出并补充文档。"
	features := scanTextFeatures(plainText)
	if features.hasAt || features.hasDot || features.hasColon {
		t.Fatalf("纯中文散文不应含有 '@' '.' ':' 特征: %+v", features)
	}
	if features.maxDigitRun != 0 || features.maxCardRun != 0 {
		t.Fatalf("纯中文散文不应有数字块: %+v", features)
	}
	for _, rule := range detector.rules {
		if rule.gate(features) {
			t.Fatalf("规则 %s 的 gate 对无特征文本应拒绝", rule.name)
		}
	}

	// 关键特征触发 gate 放行：真实检测交给正则。
	if !(scanTextFeatures("a@b.c").hasAt) {
		t.Fatal("邮箱文本应具备 '@' 特征")
	}
	if !(scanTextFeatures("1.2.3.4").hasDot) {
		t.Fatal("点分文本应具备 '.' 特征")
	}
	if !(scanTextFeatures("aa:bb:cc").hasColon) {
		t.Fatal("冒号文本应具备 ':' 特征")
	}
	if got := scanTextFeatures("13800138000").maxDigitRun; got != 11 {
		t.Fatalf("11 位手机号数字块 = %d", got)
	}
	if got := scanTextFeatures("4111 1111 1111 1111").maxCardRun; got != 16 {
		t.Fatalf("空格分组卡号数字块 = %d", got)
	}
}

func defaultSensitiveInfoSettings() config.SensitiveInfoConfig {
	return config.SensitiveInfoConfig{
		Enabled: true,
		Mode:    config.ContentSafetyModeMask,
		EnabledRules: []string{
			config.SensitiveInfoRulePhone,
			config.SensitiveInfoRuleIDCard,
			config.SensitiveInfoRuleEmail,
			config.SensitiveInfoRuleIPAddress,
		},
		IPMaskScope: config.SensitiveInfoIPMaskScopePublic,
	}
}

func TestInfoDetectorBankCardUsesLuhnValidation(t *testing.T) {
	detector, err := NewInfoDetector(config.SensitiveInfoConfig{
		Enabled: true, EnabledRules: []string{config.SensitiveInfoRuleBankCard},
	})
	if err != nil {
		t.Fatalf("创建银行卡检测器失败: %v", err)
	}
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "连续数字", text: "卡号 4111111111111111", want: true},
		{name: "空格分组", text: "卡号 4111 1111 1111 1111", want: true},
		{name: "短横线分组", text: "卡号 4111-1111-1111-1111", want: true},
		{name: "校验失败", text: "普通编号 4111111111111112", want: false},
		{name: "长度不足", text: "手机号 18012345523", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches := detector.FindAll(test.text)
			if got := len(matches) == 1; got != test.want {
				t.Fatalf("命中状态 = %v，期望 %v，matches=%+v", got, test.want, matches)
			}
		})
	}
}
