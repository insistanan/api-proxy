package sensitive

import (
	"testing"

	"insistanan/api-proxy/internal/infra/config"
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
		{name: "手机号", rule: config.SensitiveInfoRulePhone, input: "手机号 13800138000", expected: "手机号 [MASKED_PII:phone]"},
		{name: "身份证数字尾", rule: config.SensitiveInfoRuleIDCard, input: "11010519491231002X", expected: "[MASKED_PII:id_card]"},
		{name: "身份证小写x", rule: config.SensitiveInfoRuleIDCard, input: "11010519491231002x", expected: "[MASKED_PII:id_card]"},
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

func TestInfoDetectorIPMaskScopes(t *testing.T) {
	t.Run("public 范围跳过非公网地址", func(t *testing.T) {
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
	})

	t.Run("all 范围掩码所有地址", func(t *testing.T) {
		settings := defaultSensitiveInfoSettings()
		settings.IPMaskScope = config.SensitiveInfoIPMaskScopeAll
		detector, err := NewInfoDetector(settings)
		if err != nil {
			t.Fatalf("创建全量掩码检测器失败: %v", err)
		}
		for _, input := range []string{"回环地址 127.0.0.1", "内网网关 192.168.1.1", "IPv6文档 2001:db8::1"} {
			t.Run(input, func(t *testing.T) {
				masked, matches := detector.Mask(input)
				if len(matches) != 1 || matches[0].Rule != config.SensitiveInfoRuleIPAddress || masked == input {
					t.Fatalf("全量掩码范围下应替换 IP: %q -> %q, matches: %+v", input, masked, matches)
				}
			})
		}
	})
}

func TestInfoDetectorVersionHintBoundary(t *testing.T) {
	detector, err := NewInfoDetector(defaultSensitiveInfoSettings())
	if err != nil {
		t.Fatalf("创建检测器失败: %v", err)
	}

	for _, input := range []string{
		"version 1.2.3.4",
		"appVersion: 52.109.0.7",
		"my-build@18.31.4.10",
		"release = 3.9.0.1",
		"revision 12.0.0.1",
		"Rev 6.1.2.3",
	} {
		t.Run("版本号 "+input, func(t *testing.T) {
			masked, matches := detector.Mask(input)
			if masked != input || len(matches) != 0 {
				t.Fatalf("版本号语境不应被掩码: %q -> %q, matches: %+v", input, masked, matches)
			}
		})
	}

	// 版本关键词只在紧邻语境中豁免，后文中的真实 IP 仍须掩码。
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "普通公网IP", input: "服务器 8.8.4.4 可用", expected: "服务器 [MASKED_PII:ip_address] 可用"},
		{name: "版本关键词后接多段文本", input: "版本回退到 8.8.4.4 之后失败", expected: "版本回退到 [MASKED_PII:ip_address] 之后失败"},
		{name: "数字结尾的普通词", input: "升级 release2 8.8.4.4 立刻生效", expected: "升级 release2 [MASKED_PII:ip_address] 立刻生效"},
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

func TestInfoDetectorRuleSelectionAndValidation(t *testing.T) {
	detector, err := NewInfoDetector(config.SensitiveInfoConfig{
		Enabled:      true,
		EnabledRules: []string{config.SensitiveInfoRulePhone},
	})
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

	// 空 IP 掩码范围等价于 public；未知范围必须报错。
	ipSettings := config.SensitiveInfoConfig{Enabled: true, EnabledRules: []string{config.SensitiveInfoRuleIPAddress}}
	ipDetector, err := NewInfoDetector(ipSettings)
	if err != nil {
		t.Fatalf("空 IP 掩码范围应按默认 public 处理: %v", err)
	}
	if masked, _ := ipDetector.Mask("内网 192.168.1.1"); masked != "内网 192.168.1.1" {
		t.Fatalf("空范围应按 public 跳过内网地址: %q", masked)
	}
	ipSettings.IPMaskScope = "unexpected"
	if _, err := NewInfoDetector(ipSettings); err == nil {
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

// TestInfoDetectorCodeLikeTextNoFalsePositives 确认冒号、十六进制密集的代码与配置文本不会被误判为 IP。
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

// TestInfoDetectorIPv6BoundaryCases 验证 IPv6 命中区间的边界：混在代码文本中仍能识别，且偏移正确。
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
		{name: "行首完整地址", input: "2607:f8b0:4005:808::200e", expected: "[MASKED_PII:ip_address]"},
		{name: "冒号路径中的地址", input: "endpoint=2607:f8b0:4005:808::200e 备用", expected: "endpoint=[MASKED_PII:ip_address] 备用"},
		{name: "地址后接冒号端口分隔符", input: "[2606:4700:4700::1111]:443", expected: "[[MASKED_PII:ip_address]]:443"},
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
