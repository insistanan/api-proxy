package sensitive

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// InfoMatch 描述一次敏感信息命中及其掩码结果。
type InfoMatch struct {
	Rule        string `json:"rule"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
}

type infoRule struct {
	name  string
	regex *regexp.Regexp
	find  func(string) []infoCandidate
	mask  func(string) string
	// gate 是规则的必要条件预筛：文本结构特征不满足时该规则必然无命中，
	// 跳过整段正则扫描。只允许"拦截必无命中"的保守条件，gate 放行仅代表
	// 需要跑正则，不改变任何检测结果。
	gate func(textFeatures) bool
}

type infoCandidate struct {
	start       int
	end         int
	replacement string
}

// textFeatures 是一次线性扫描收集的文本结构特征。五类敏感信息规则的命中
// 都依赖特定字符结构（'@' / 点分 / 冒号 / 连续数字），特征缺失时规则
// 必然无命中；在代码类文本上跳过整段正则的收益远大于这一遍字符统计。
type textFeatures struct {
	hasAt       bool // 含 '@'（邮箱）
	hasDot      bool // 含 '.'（IPv4 点分四段）
	hasColon    bool // 含 ':'（IPv6 至少两个冒号）
	maxDigitRun int  // 最长纯数字连续块（手机号 11 位 / 身份证 17 位数字）
	maxCardRun  int  // 最长"数字与单空格/横线分组"块的数字个数（银行卡 13 位）
}

// scanTextFeatures 单遍扫描收集 textFeatures。maxCardRun 与银行卡正则的
// `[0-9](?:[ -]?[0-9]){12,18}` 对齐：分隔符必须夹在两个数字之间；连续
// 超长数字块同样计数，是否真正命中仍由正则判定（gate 只是必要条件）。
func scanTextFeatures(text string) textFeatures {
	var features textFeatures
	digitRun, cardRun := 0, 0
	flush := func() {
		if digitRun > features.maxDigitRun {
			features.maxDigitRun = digitRun
		}
		if cardRun > features.maxCardRun {
			features.maxCardRun = cardRun
		}
		digitRun, cardRun = 0, 0
	}
	for i := 0; i < len(text); i++ {
		value := text[i]
		switch {
		case value >= '0' && value <= '9':
			digitRun++
			cardRun++
		case (value == ' ' || value == '-') && i > 0 && i+1 < len(text) &&
			isASCIIDigit(text[i-1]) && isASCIIDigit(text[i+1]):
			// 数字间的单个空格/横线：银行卡分组分隔，数字块本身中断。
			digitRun = 0
		case value == '@':
			features.hasAt = true
			flush()
		case value == '.':
			features.hasDot = true
			flush()
		case value == ':':
			features.hasColon = true
			flush()
		default:
			flush()
		}
	}
	flush()
	return features
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

// 银行卡号去分隔用的替换表：strings.Replacer 只读可并发复用，
// 避免每个候选都重建替换表。
var bankCardCompactor = strings.NewReplacer(" ", "", "-", "")

var (
	idCardPattern   = regexp.MustCompile(`\b[1-9][0-9]{16}[0-9Xx]\b`)
	ipv4Pattern     = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	bankCardPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9](?:[ -]?[0-9]){12,18})(?:[^0-9]|$)`)
	phonePattern    = regexp.MustCompile(`\b1[3-9][0-9]{9}\b`)

	// 仅凭 11 位数字无法区分手机号和设备号、工单号等业务编号；
	// 这些上下文词用于把手机号规则限制在有明确语义的场景。
	phoneContextPattern    = regexp.MustCompile(`(?i)(?:手机|手机号|电话|电话号码|联系电话|联系方式|(?:mobile|phone|telephone|tel|contact)\b|\+86\b)`)
	bankCardContextPattern = regexp.MustCompile(`(?i)(?:银行卡|信用卡|借记卡|卡号|银行卡号|bank[ _-]?card|credit[ _-]?card|debit[ _-]?card|card[ _-]?(?:no|number)\b)`)

	// versionHintPattern 匹配 IPv4 命中前文末尾的版本号语境词
	//（version/release/build/rev/revision，允许字母数字与 _-. 前缀，
	// 例如 appVersion、my-build），命中则按版本号放行。
	// RE2 不支持 lookbehind，因此对命中位置的前文窗口做锚定结尾匹配。
	versionHintPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z.])[a-z0-9_.-]*(?:version|release|build|rev(?:ision)?)[\s:@=_-]*$`)

	// versionHintWindow 是版本号启发向前回看的字符窗口。
	versionHintWindow = 32
)

// extraNonPublicIPNets 是标准库判定方法覆盖不到、public 掩码范围下同样放行的
// 保留网段：本网段、运营商级 NAT、IETF 协议分配、文档示例、基准测试、6to4
// 中继、保留段与 IPv6 专用段。这些地址无个人定位价值，出现在代码与配置里是常态。
var extraNonPublicIPNets = mustParseCIDRs(
	"0.0.0.0/8",       // 本网段（RFC 791）
	"100.64.0.0/10",   // 运营商级 NAT（RFC 6598）
	"192.0.0.0/24",    // IETF 协议分配（RFC 6890）
	"192.0.2.0/24",    // 文档示例 TEST-NET-1（RFC 5737）
	"192.88.99.0/24",  // 6to4 中继（RFC 7526 已废弃）
	"198.18.0.0/15",   // 基准测试（RFC 2544）
	"198.51.100.0/24", // 文档示例 TEST-NET-2（RFC 5737）
	"203.0.113.0/24",  // 文档示例 TEST-NET-3（RFC 5737）
	"240.0.0.0/4",     // 保留段（RFC 1112；含受限广播地址）
	"2001:db8::/32",   // IPv6 文档示例（RFC 3849）
	"64:ff9b:1::/48",  // 本地 NAT64 翻译示例（RFC 8215）
	"100::/64",        // 丢弃前缀（RFC 6666）
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("解析保留网段 %s 失败: %v", cidr, err))
		}
		nets = append(nets, ipNet)
	}
	return nets
}

// InfoDetector 检测并掩码请求方向的敏感信息。
type InfoDetector struct {
	enabled bool
	rules   []infoRule
}

// NewInfoDetector 根据设置创建敏感信息检测器。
func NewInfoDetector(settings config.SensitiveInfoConfig) (*InfoDetector, error) {
	if err := validateInfoRules(settings.EnabledRules); err != nil {
		return nil, err
	}
	ipMaskScope, err := normalizeIPMaskScope(settings.IPMaskScope)
	if err != nil {
		return nil, err
	}
	detector := &InfoDetector{enabled: settings.Enabled}
	if !settings.Enabled {
		return detector, nil
	}

	selected := make(map[string]struct{}, len(settings.EnabledRules))
	for _, rule := range settings.EnabledRules {
		selected[rule] = struct{}{}
	}
	for _, rule := range allInfoRules(ipMaskScope) {
		if _, ok := selected[rule.name]; ok {
			detector.rules = append(detector.rules, rule)
		}
	}
	return detector, nil
}

// FindAll 返回按文本位置排序且不重叠的敏感信息命中。
func (d *InfoDetector) FindAll(text string) []InfoMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 || text == "" {
		return nil
	}

	features := scanTextFeatures(text)
	candidates := make([]infoCandidateWithRule, 0)
	for ruleIndex, rule := range d.rules {
		if rule.gate != nil && !rule.gate(features) {
			continue
		}
		for _, candidate := range findRuleCandidates(rule, text) {
			candidates = append(candidates, infoCandidateWithRule{
				infoCandidate: candidate,
				ruleIndex:     ruleIndex,
				ruleName:      rule.name,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		if candidates[i].end != candidates[j].end {
			return candidates[i].end > candidates[j].end
		}
		return candidates[i].ruleIndex < candidates[j].ruleIndex
	})

	matches := make([]InfoMatch, 0, len(candidates))
	lastEnd := -1
	for _, candidate := range candidates {
		if candidate.start < lastEnd {
			continue
		}
		matches = append(matches, InfoMatch{
			Rule:        candidate.ruleName,
			Original:    text[candidate.start:candidate.end],
			Replacement: candidate.replacement,
			Start:       candidate.start,
			End:         candidate.end,
		})
		lastEnd = candidate.end
	}
	return matches
}

// Mask 掩码替换请求文本，并返回命中明细。
func (d *InfoDetector) Mask(text string) (string, []InfoMatch) {
	matches := d.FindAll(text)
	if len(matches) == 0 {
		return text, nil
	}

	var builder strings.Builder
	builder.Grow(len(text))
	last := 0
	for _, match := range matches {
		builder.WriteString(text[last:match.Start])
		builder.WriteString(match.Replacement)
		last = match.End
	}
	builder.WriteString(text[last:])
	return builder.String(), matches
}

type infoCandidateWithRule struct {
	infoCandidate
	ruleIndex int
	ruleName  string
}

func findRuleCandidates(rule infoRule, text string) []infoCandidate {
	if rule.find != nil {
		return rule.find(text)
	}
	indices := rule.regex.FindAllStringIndex(text, -1)
	candidates := make([]infoCandidate, 0, len(indices))
	for _, index := range indices {
		original := text[index[0]:index[1]]
		candidates = append(candidates, infoCandidate{
			start:       index[0],
			end:         index[1],
			replacement: rule.mask(original),
		})
	}
	return candidates
}

func validateInfoRules(rules []string) error {
	allowed := map[string]struct{}{
		config.SensitiveInfoRulePhone:     {},
		config.SensitiveInfoRuleIDCard:    {},
		config.SensitiveInfoRuleEmail:     {},
		config.SensitiveInfoRuleIPAddress: {},
		config.SensitiveInfoRuleBankCard:  {},
	}
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			return fmt.Errorf("敏感信息规则名不能为空")
		}
		if _, ok := allowed[rule]; !ok {
			return fmt.Errorf("不支持的敏感信息规则 %q", rule)
		}
		if _, ok := seen[rule]; ok {
			return fmt.Errorf("敏感信息规则 %q 重复", rule)
		}
		seen[rule] = struct{}{}
	}
	return nil
}

// normalizeIPMaskScope 归一化 IP 掩码范围：合法值原样返回，零值继续按 public
// 处理以兼容未填该字段的既有直接调用方，未知取值显式报错。
func normalizeIPMaskScope(scope string) (string, error) {
	switch scope {
	case config.SensitiveInfoIPMaskScopePublic, config.SensitiveInfoIPMaskScopeAll:
		return scope, nil
	case "":
		return config.SensitiveInfoIPMaskScopePublic, nil
	default:
		return "", fmt.Errorf("不支持的 IP 掩码范围 %q", scope)
	}
}

func allInfoRules(ipMaskScope string) []infoRule {
	return []infoRule{
		{
			name: config.SensitiveInfoRulePhone,
			find: findPhoneCandidates,
			gate: func(f textFeatures) bool { return f.maxDigitRun >= 11 },
		},
		{
			name: config.SensitiveInfoRuleIDCard,
			find: findIDCardCandidates,
			// 身份证为 17 位数字 + 校验位（校验位可为 X），纯数字块不足 17 必无命中。
			gate: func(f textFeatures) bool { return f.maxDigitRun >= 17 },
		},
		{
			name:  config.SensitiveInfoRuleEmail,
			regex: regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
			mask:  maskEmail,
			gate:  func(f textFeatures) bool { return f.hasAt },
		},
		{
			name: config.SensitiveInfoRuleIPAddress,
			find: func(text string) []infoCandidate {
				return findIPCandidates(text, ipMaskScope)
			},
			// IPv4 依赖点分四段、IPv6 依赖冒号，两者皆无时必无命中。
			gate: func(f textFeatures) bool { return f.hasDot || f.hasColon },
		},
		{
			name: config.SensitiveInfoRuleBankCard,
			find: findBankCardCandidates,
			gate: func(f textFeatures) bool { return f.maxCardRun >= 13 },
		},
	}
}

func findPhoneCandidates(text string) []infoCandidate {
	indices := phonePattern.FindAllStringIndex(text, -1)
	candidates := make([]infoCandidate, 0, len(indices))
	for _, index := range indices {
		if !hasNearbyContext(text, index[0], index[1], phoneContextPattern, 48, 24) {
			continue
		}
		candidates = append(candidates, infoCandidate{
			start: index[0], end: index[1], replacement: maskPhone(text[index[0]:index[1]]),
		})
	}
	return candidates
}

func findBankCardCandidates(text string) []infoCandidate {
	indices := bankCardPattern.FindAllStringSubmatchIndex(text, -1)
	candidates := make([]infoCandidate, 0, len(indices))
	for _, index := range indices {
		if len(index) < 4 || index[2] < 0 {
			continue
		}
		original := text[index[2]:index[3]]
		digits := bankCardCompactor.Replace(original)
		if !validLuhn(digits) {
			continue
		}
		if !hasNearbyContext(text, index[2], index[3], bankCardContextPattern, 48, 24) {
			continue
		}
		candidates = append(candidates, infoCandidate{
			start: index[2], end: index[3], replacement: "[MASKED_PII:bank_card]",
		})
	}
	return candidates
}

func hasNearbyContext(text string, start, end int, pattern *regexp.Regexp, before, after int) bool {
	windowStart := start - before
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := end + after
	if windowEnd > len(text) {
		windowEnd = len(text)
	}
	return pattern.MatchString(text[windowStart:windowEnd])
}

func validLuhn(value string) bool {
	if len(value) < 13 || len(value) > 19 {
		return false
	}
	sum := 0
	double := false
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
		digit := int(value[index] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

func findIDCardCandidates(text string) []infoCandidate {
	indices := idCardPattern.FindAllStringIndex(text, -1)
	candidates := make([]infoCandidate, 0, len(indices))
	for _, index := range indices {
		original := text[index[0]:index[1]]
		if !validChineseIDCard(original) {
			continue
		}
		candidates = append(candidates, infoCandidate{
			start:       index[0],
			end:         index[1],
			replacement: "[MASKED_PII:id_card]",
		})
	}
	return candidates
}

func validChineseIDCard(value string) bool {
	if len(value) != 18 {
		return false
	}
	for index := 0; index < 17; index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	birthDate, err := time.Parse("20060102", value[6:14])
	if err != nil || birthDate.Year() < 1800 || birthDate.After(time.Now().UTC()) {
		return false
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for index, weight := range weights {
		sum += int(value[index]-'0') * weight
	}
	expected := checks[sum%11]
	actual := value[17]
	if actual == 'x' {
		actual = 'X'
	}
	return actual == expected
}

// findIPCandidates 查找文本中的 IP 地址候选。public 范围下跳过私网、保留、
// 文档与组播等无个人定位价值的地址；版本号语境中的四段数字视为版本号放行。
func findIPCandidates(text string, ipMaskScope string) []infoCandidate {
	maskAll := ipMaskScope == config.SensitiveInfoIPMaskScopeAll
	candidates := make([]infoCandidate, 0)
	for _, index := range ipv4Pattern.FindAllStringIndex(text, -1) {
		original := text[index[0]:index[1]]
		ip := net.ParseIP(original)
		if ip == nil {
			continue
		}
		if !maskAll && isNonPublicIP(ip) {
			continue
		}
		if hasVersionNumberHint(text, index[0]) {
			continue
		}
		candidates = append(candidates, infoCandidate{start: index[0], end: index[1], replacement: "[MASKED_PII:ip_address]"})
	}

	candidates = append(candidates, findIPv6Candidates(text, maskAll)...)
	return candidates
}

// findIPv6Candidates 以冒号为锚点手写扫描 IPv6 候选。
// 旧正则 (?i)[0-9a-f:][0-9a-f:.]*:[0-9a-f:.]* 会把每个"十六进制字符后
// 跟冒号"的位置都当成候选（"e:"、"0:"、"1:" 均命中），代码与英文文本上
// 候选数与文本长度成正比，且每个候选都要付一次 net.ParseIP；合法 IPv6
// 文本必然含至少两个冒号（完整形式 7 个、压缩形式含 "::"），因此这里先
// 按 [0-9A-Fa-f:.] 切出连续段，仅对段内冒号数 >= 2 的段做一次 ParseIP
// 精筛。有效候选集合与旧正则一致（合法 IPv6 必出现在整段内），无效候选
// 的产生量从"每个十六进制字符"降到"每段"。
func findIPv6Candidates(text string, maskAll bool) []infoCandidate {
	candidates := make([]infoCandidate, 0)
	segmentStart, colons := -1, 0
	for position := 0; position <= len(text); position++ {
		// 位置等于文本长度时，用哨兵冲刷最后一个未闭合的段。
		if position < len(text) && isIPv6ClassByte(text[position]) {
			if segmentStart < 0 {
				segmentStart = position
			}
			if text[position] == ':' {
				colons++
			}
			continue
		}
		if segmentStart >= 0 && colons >= 2 {
			original := text[segmentStart:position]
			// 形状快筛：合法 IPv6 要么是完整 8 组（恰好 7 个冒号），
			// 要么含压缩形式 "::"（含嵌入 IPv4 的表示同样满足）。
			// 这是充要的必要条件，只有满足时才付 net.ParseIP 的分配。
			if colons == 7 || strings.Contains(original, "::") {
				if ip := net.ParseIP(original); ip != nil && (maskAll || !isNonPublicIP(ip)) {
					candidates = append(candidates, infoCandidate{
						start: segmentStart, end: position, replacement: "[MASKED_PII:ip_address]",
					})
				}
			}
		}
		segmentStart, colons = -1, 0
	}
	return candidates
}

// isIPv6ClassByte 判定 IPv6 文本表示可能出现的字符（十六进制、冒号、嵌入
// IPv4 的点号）。
func isIPv6ClassByte(value byte) bool {
	return isASCIIDigit(value) ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F' ||
		value == ':' || value == '.'
}

// isNonPublicIP 判断地址是否属于私网、回环、链路本地、组播、未指定地址，
// 或 extraNonPublicIPNets 列出的保留网段。这些地址无法定位到具体个人。
func isNonPublicIP(ip net.IP) bool {
	return ip.IsPrivate() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		containsIPNet(extraNonPublicIPNets, ip)
}

func containsIPNet(nets []*net.IPNet, ip net.IP) bool {
	for _, ipNet := range nets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// hasVersionNumberHint 判断 IPv4 命中位置之前是否紧跟版本号语境词。
// 只认高置信度关键词，不做语义猜测：宁可少放行，也不漏掉真实公网 IP 的掩码。
func hasVersionNumberHint(text string, matchStart int) bool {
	windowStart := matchStart - versionHintWindow
	if windowStart < 0 {
		windowStart = 0
	}
	return versionHintPattern.MatchString(text[windowStart:matchStart])
}

func maskPhone(value string) string {
	return "[MASKED_PII:phone]"
}

func maskEmail(value string) string {
	return "[MASKED_PII:email]"
}
