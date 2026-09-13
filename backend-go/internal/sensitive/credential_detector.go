package sensitive

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// CredentialMatch 描述一次凭据命中。Original 只用于当前请求内完成替换，
// 调用方不得将其写入日志或错误响应。
type CredentialMatch struct {
	Rule        string
	Original    string
	Replacement string
	Start       int
	End         int
}

type credentialRule struct {
	name string
	find func(string) []credentialCandidate
}

type credentialCandidate struct {
	start int
	end   int
}

type credentialCandidateWithRule struct {
	credentialCandidate
	ruleIndex int
	ruleName  string
}

var (
	knownAPIKeyPattern           = regexp.MustCompile(`(?i)(?:sk-ant-[A-Za-z0-9_-]{16,}|sk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{24,}|xai-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{30,}|A(?:KI|SI)A[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,})`)
	bearerTokenPattern           = regexp.MustCompile(`(?i)\bBearer[ \t]+([A-Za-z0-9._~+/=-]{16,})`)
	namedSecretPattern           = regexp.MustCompile(`(?im)(?:^|[\s,{;])["']?((?:password|passwd|secret|token|credential|private[_-]?key|api[_-]?key|access[_-]?key|access[_-]?token|client[_-]?secret|aws_secret_access_key))["']?\s*[:=]\s*["']?([^\s"',;}]{6,})`)
	privateKeyPattern            = regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)
	connectionStringPattern      = regexp.MustCompile(`(?i)\b(?:https?|postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|rediss|amqp|amqps|mssql)://[^/\s:@]*:([^@\s/]+)@[^\s"']+`)
	connectionAssignmentPattern  = regexp.MustCompile(`(?im)(?:^|[\s,{;])["']?(?:database_url|dsn|connection_string)["']?\s*[:=]\s*["']?([^\r\n"']{8,})`)
	connectionPasswordPattern    = regexp.MustCompile(`(?i)(?:password|pwd)\s*=\s*([^;\s"']{3,})`)
	highEntropyAssignmentPattern = regexp.MustCompile(`(?m)(?:^|[\s,{;])["']?([A-Za-z_][A-Za-z0-9_.-]{1,63})["']?\s*[:=]\s*["']?([A-Za-z0-9+/=_-]{24,})`)
	qualifiedReferencePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(?:\.[A-Za-z_][A-Za-z0-9_-]*)+$`)
	simpleIdentifierPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	environmentReferencePattern  = regexp.MustCompile(`^(?:\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|%[A-Za-z_][A-Za-z0-9_]*%)$`)
	windowsPathPattern           = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	uuidLikePattern              = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ulidLikePattern              = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	numericIDPattern             = regexp.MustCompile(`^[0-9]{15,20}$`)
	hexIDPattern                 = regexp.MustCompile(`(?i)^[0-9a-f]{16,64}$`)
	businessIDPrefixPattern      = regexp.MustCompile(`(?i)^(?:conv(?:ersation)?|session|message|msg|request|req|order|transaction|trace|user|task|run|job|event|correlation)[_-][a-z0-9_-]{4,}$`)
)

// CredentialDetector 检测文本中的凭据。它不自行决定审计、阻断或掩码，
// 来源策略由请求 Hook 统一执行。
type CredentialDetector struct {
	enabled bool
	rules   []credentialRule
}

var auditCredentialDetector = &CredentialDetector{enabled: true, rules: allCredentialRules()}

// 审计始终隐藏凭据，不受检测开关和业务白名单影响。
var auditCommandSecretPattern = regexp.MustCompile(`(?i)--(?:password|passwd|token|api[_-]?key|secret|access[_-]?token)(?:=|[ \t]+)["']?([^\s"';]+)`)

// RedactAuditText 用于日志、待确认展示和审计落库，不能用于计算命令授权身份。
func RedactAuditText(text string) string {
	return redactAuditText(text, false)
}

// RedactAuditContext 保留字节偏移，供命中窗口在隐藏邻近凭据后继续按原区间截取。
func RedactAuditContext(text string) string {
	return redactAuditText(text, true)
}

func redactAuditText(text string, preserveOffsets bool) string {
	if text == "" {
		return text
	}
	ranges := make([]credentialCandidate, 0)
	for _, match := range auditCredentialDetector.FindAll(text) {
		ranges = append(ranges, credentialCandidate{start: match.Start, end: match.End})
	}
	for _, index := range auditCommandSecretPattern.FindAllStringSubmatchIndex(text, -1) {
		ranges = append(ranges, credentialCandidate{start: index[2], end: index[3]})
	}
	if len(ranges) == 0 {
		return text
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	var builder strings.Builder
	last := 0
	for index := 0; index < len(ranges); index++ {
		start, end := ranges[index].start, ranges[index].end
		for index+1 < len(ranges) && ranges[index+1].start <= end {
			index++
			if ranges[index].end > end {
				end = ranges[index].end
			}
		}
		builder.WriteString(text[last:start])
		if preserveOffsets {
			builder.WriteString(strings.Repeat("*", end-start))
		} else {
			builder.WriteString("[凭据已隐藏]")
		}
		last = end
	}
	builder.WriteString(text[last:])
	return builder.String()
}

func NewCredentialDetector(settings config.CredentialConfig) (*CredentialDetector, error) {
	if err := validateCredentialRules(settings.EnabledRules); err != nil {
		return nil, err
	}
	detector := &CredentialDetector{enabled: settings.Enabled}
	if !settings.Enabled {
		return detector, nil
	}
	selected := make(map[string]struct{}, len(settings.EnabledRules))
	for _, rule := range settings.EnabledRules {
		selected[rule] = struct{}{}
	}
	for _, rule := range allCredentialRules() {
		if _, ok := selected[rule.name]; ok {
			detector.rules = append(detector.rules, rule)
		}
	}
	return detector, nil
}

func (d *CredentialDetector) FindAll(text string) []CredentialMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 || text == "" {
		return nil
	}
	candidates := make([]credentialCandidateWithRule, 0)
	for ruleIndex, rule := range d.rules {
		for _, candidate := range rule.find(text) {
			if candidate.start < 0 || candidate.end > len(text) || candidate.start >= candidate.end {
				continue
			}
			if isCredentialPlaceholder(text[candidate.start:candidate.end]) {
				continue
			}
			candidates = append(candidates, credentialCandidateWithRule{
				credentialCandidate: candidate,
				ruleIndex:           ruleIndex,
				ruleName:            rule.name,
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

	matches := make([]CredentialMatch, 0, len(candidates))
	lastEnd := -1
	for _, candidate := range candidates {
		if candidate.start < lastEnd {
			continue
		}
		matches = append(matches, CredentialMatch{
			Rule:        candidate.ruleName,
			Original:    text[candidate.start:candidate.end],
			Replacement: "[MASKED_CREDENTIAL:" + candidate.ruleName + "]",
			Start:       candidate.start,
			End:         candidate.end,
		})
		lastEnd = candidate.end
	}
	return matches
}

func (d *CredentialDetector) Mask(text string) (string, []CredentialMatch) {
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

func validateCredentialRules(rules []string) error {
	allowed := map[string]struct{}{
		config.CredentialRuleAPIKey:           {},
		config.CredentialRuleNamedSecret:      {},
		config.CredentialRulePrivateKey:       {},
		config.CredentialRuleConnectionString: {},
		config.CredentialRuleHighEntropy:      {},
	}
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			return fmt.Errorf("凭据规则名不能为空")
		}
		if _, ok := allowed[rule]; !ok {
			return fmt.Errorf("不支持的凭据规则 %q", rule)
		}
		if _, ok := seen[rule]; ok {
			return fmt.Errorf("凭据规则 %q 重复", rule)
		}
		seen[rule] = struct{}{}
	}
	return nil
}

func allCredentialRules() []credentialRule {
	return []credentialRule{
		{name: config.CredentialRulePrivateKey, find: regexpCredentialCandidates(privateKeyPattern)},
		{name: config.CredentialRuleConnectionString, find: findConnectionStringCandidates},
		{name: config.CredentialRuleAPIKey, find: findAPIKeyCandidates},
		{name: config.CredentialRuleNamedSecret, find: findNamedSecretCandidates},
		{name: config.CredentialRuleHighEntropy, find: findHighEntropyCandidates},
	}
}

func findAPIKeyCandidates(text string) []credentialCandidate {
	result := regexpCredentialCandidates(knownAPIKeyPattern)(text)
	for _, index := range bearerTokenPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(index) < 4 || index[2] < 0 {
			continue
		}
		result = append(result, credentialCandidate{start: index[2], end: index[3]})
	}
	return result
}

func findConnectionStringCandidates(text string) []credentialCandidate {
	result := findURIConnectionStringCandidates(text)
	indices := connectionAssignmentPattern.FindAllStringSubmatchIndex(text, -1)
	for _, index := range indices {
		if len(index) < 4 || index[2] < 0 {
			continue
		}
		value := strings.TrimSpace(text[index[2]:index[3]])
		if !isLikelyConnectionSecret(value) {
			continue
		}
		start := index[2]
		end := index[3]
		for start < end && (text[start] == ' ' || text[start] == '\t') {
			start++
		}
		for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
			end--
		}
		result = append(result, credentialCandidate{start: start, end: end})
	}
	return result
}

func findURIConnectionStringCandidates(text string) []credentialCandidate {
	indices := connectionStringPattern.FindAllStringSubmatchIndex(text, -1)
	result := make([]credentialCandidate, 0, len(indices))
	for _, index := range indices {
		if len(index) < 4 || index[0] < 0 || index[2] < 0 {
			continue
		}
		password := text[index[2]:index[3]]
		if isCredentialPlaceholder(password) {
			continue
		}
		result = append(result, credentialCandidate{start: index[0], end: index[1]})
	}
	return result
}

func findNamedSecretCandidates(text string) []credentialCandidate {
	indices := namedSecretPattern.FindAllStringSubmatchIndex(text, -1)
	result := make([]credentialCandidate, 0, len(indices))
	for _, index := range indices {
		if len(index) < 6 || index[2] < 0 || index[4] < 0 {
			continue
		}
		name := text[index[2]:index[3]]
		value := text[index[4]:index[5]]
		quoted := index[4] > 0 && (text[index[4]-1] == '"' || text[index[4]-1] == '\'')
		if isLikelyNamedSecretValue(name, value, quoted) {
			result = append(result, credentialCandidate{start: index[4], end: index[5]})
		}
	}
	return result
}

func regexpCredentialCandidates(pattern *regexp.Regexp) func(string) []credentialCandidate {
	return func(text string) []credentialCandidate {
		indices := pattern.FindAllStringIndex(text, -1)
		result := make([]credentialCandidate, 0, len(indices))
		for _, index := range indices {
			result = append(result, credentialCandidate{start: index[0], end: index[1]})
		}
		return result
	}
}

func findHighEntropyCandidates(text string) []credentialCandidate {
	indices := highEntropyAssignmentPattern.FindAllStringSubmatchIndex(text, -1)
	result := make([]credentialCandidate, 0, len(indices))
	for _, index := range indices {
		if len(index) < 6 || index[2] < 0 || index[4] < 0 {
			continue
		}
		name := text[index[2]:index[3]]
		value := text[index[4]:index[5]]
		if isLikelySecretAssignmentName(name) && isLikelySecretValue(value) {
			result = append(result, credentialCandidate{start: index[4], end: index[5]})
		}
	}
	return result
}

func isLikelyNamedSecretValue(name, value string, quoted bool) bool {
	trimmed := strings.Trim(strings.TrimSpace(value), "\"'")
	if len(trimmed) < 6 || isCredentialPlaceholder(trimmed) {
		return false
	}
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	compactName := strings.NewReplacer("_", "", "-", "", ".", "").Replace(normalizedName)
	weakName := compactName == "token" || compactName == "secret" || compactName == "credential"
	if weakName && isLikelyIdentifierValue(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(trimmed, "://") || strings.ContainsAny(trimmed, "()[]{}") {
		return false
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, "../") || strings.HasPrefix(trimmed, "~/") ||
		windowsPathPattern.MatchString(trimmed) {
		return false
	}
	if qualifiedReferencePattern.MatchString(trimmed) {
		return false
	}
	if !quoted && simpleIdentifierPattern.MatchString(trimmed) && looksLikeCodeIdentifier(trimmed) {
		return false
	}
	switch lower {
	case "config", "settings", "environment", "undefined", "default", "true", "false":
		return false
	default:
		return true
	}
}

func isLikelyIdentifierValue(value string) bool {
	if uuidLikePattern.MatchString(value) || ulidLikePattern.MatchString(value) {
		return true
	}
	if numericIDPattern.MatchString(value) || hexIDPattern.MatchString(value) {
		return true
	}
	return businessIDPrefixPattern.MatchString(value)
}

func looksLikeCodeIdentifier(value string) bool {
	if strings.Contains(value, "_") {
		return true
	}
	hasLower := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current >= 'a' && current <= 'z' {
			hasLower = true
			continue
		}
		if hasLower && current >= 'A' && current <= 'Z' {
			return true
		}
	}
	return false
}

func isLikelyConnectionSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || isCredentialPlaceholder(trimmed) {
		return false
	}
	if len(findURIConnectionStringCandidates(trimmed)) > 0 {
		return true
	}
	match := connectionPasswordPattern.FindStringSubmatch(trimmed)
	return len(match) == 2 && !isCredentialPlaceholder(match[1])
}

func isClearlyNonSecretAssignmentName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{
		"commit", "checksum", "digest", "fingerprint", "etag", "build_id",
		"request_id", "trace_id", "span_id", "version", "revision",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "sha" || strings.HasPrefix(normalized, "sha_") || strings.HasSuffix(normalized, "_sha")
}

// isLikelySecretAssignmentName 将高熵检测限制在有明确秘密语义的字段。
// 任意长随机字符串都可能是会话、消息或业务 ID，不能仅凭熵值判定为凭据。
func isLikelySecretAssignmentName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" || isClearlyNonSecretAssignmentName(normalized) {
		return false
	}
	// 统一处理 snake_case、kebab-case、点号和驼峰字段名。
	compact := strings.NewReplacer("_", "", "-", "", ".", "").Replace(normalized)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential",
		"privatekey", "apikey", "accesskey", "accesstoken",
		"clientsecret", "awssecretaccesskey", "encryptionkey", "signingkey",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func isLikelySecretValue(value string) bool {
	if len(value) < 24 || isCredentialPlaceholder(value) {
		return false
	}
	allHex := true
	classes := 0
	var lower, upper, digit, symbol bool
	counts := make(map[byte]int)
	for i := 0; i < len(value); i++ {
		ch := value[i]
		counts[ch]++
		switch {
		case ch >= 'a' && ch <= 'z':
			lower = true
		case ch >= 'A' && ch <= 'Z':
			upper = true
		case ch >= '0' && ch <= '9':
			digit = true
		default:
			symbol = true
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			allHex = false
		}
	}
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			classes++
		}
	}
	if allHex || classes < 3 {
		return false
	}
	entropy := 0.0
	length := float64(len(value))
	for _, count := range counts {
		probability := float64(count) / length
		entropy -= probability * math.Log2(probability)
	}
	return entropy >= 4.0
}

func isCredentialPlaceholder(value string) bool {
	trimmed := strings.Trim(strings.TrimSpace(value), "\"'")
	lower := strings.ToLower(trimmed)
	if trimmed == "" || strings.Contains(lower, "masked_") || strings.Contains(lower, "redacted") {
		return true
	}
	if strings.Contains(lower, "placeholder") || strings.Contains(lower, "example") || strings.Contains(lower, "changeme") {
		return true
	}
	if environmentReferencePattern.MatchString(trimmed) || strings.HasPrefix(trimmed, "{{") || strings.HasPrefix(trimmed, "<") {
		return true
	}
	switch lower {
	case "null", "none", "redacted", "changeme", "change-me", "example", "password", "secret":
		return true
	}
	return strings.Trim(trimmed, "*xX-_") == "" || strings.HasPrefix(lower, "your-") || strings.HasPrefix(lower, "your_")
}
