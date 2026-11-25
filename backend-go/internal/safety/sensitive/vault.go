package sensitive

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var reservedPlaceholderPattern = regexp.MustCompile(`\bMASKED_[A-Z0-9_]+_[A-Z2-7]{13}_(?:END|PRESERVE_EXACTLY)\b`)
var placeholderTokenPattern = regexp.MustCompile(`MASKED_[A-Z0-9_]+_[A-Z2-7]{13}_(?:END|PRESERVE_EXACTLY)`)

const maxPlaceholderBytes = 256

// 密钥状态随映射库实例存活；请求持有固定状态，不受配置热切换影响。
type placeholderKeyState struct {
	store *MappingStore
	once  sync.Once
	key   []byte
	err   error
}

// 显式使用内存模式的 Vault 共用进程密钥，保持跨请求占位符稳定。
var memoryPlaceholderState placeholderKeyState

// RedactionMatch 是可逆脱敏使用的统一命中区间。Original 必须与 Text 的对应
// 字节区间完全一致；调用方不得记录该字段。
type RedactionMatch struct {
	Type     string
	Rule     string
	Original string
	Start    int
	End      int
}

// RestoreJSON 还原协议正文，保留协议身份、签名和不透明载荷。
func (v *Vault) RestoreJSON(body []byte) ([]byte, error) {
	return v.restoreJSONBody(body, false)
}

// RestoreToolJSON 还原工具业务值，name/id/type 等业务键的值也必须恢复。
func (v *Vault) RestoreToolJSON(body []byte) ([]byte, error) {
	if err := v.ValidateToolJSON(body); err != nil {
		return nil, err
	}
	return v.restoreJSONBody(body, true)
}

func (v *Vault) restoreJSONBody(body []byte, business bool) ([]byte, error) {
	if v == nil || len(body) == 0 {
		return body, nil
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if !bytes.Contains(body, []byte("MASKED_")) && !bytes.ContainsRune(body, '\\') {
		return body, nil
	}
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		restored := v.Restore(string(body))
		return []byte(restored), v.Err()
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("解析待还原响应体尾部失败: %w", err)
		}
	} else {
		return nil, fmt.Errorf("解析待还原响应体失败: JSON 包含多个顶层值")
	}
	value = restoreJSONValue(value, v, business)
	if err := v.Err(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("序列化已还原响应体失败: %w", err)
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}

func restoreJSONStrings(value interface{}, vault *Vault) interface{} {
	return restoreJSONValue(value, vault, false)
}

func restoreJSONValue(value interface{}, vault *Vault, business bool) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		if !business && IsSignedProtocolValue(typed) {
			// 签名工具调用不能同时提供改写参数与有效签名，明确拒绝这种交付。
			if call, ok := typed["functionCall"].(map[string]interface{}); ok {
				encoded, err := json.Marshal(call["args"])
				if err != nil || bytes.Contains(encoded, []byte("MASKED_")) {
					vault.setError(fmt.Errorf("签名工具参数包含脱敏占位符，无法安全还原并保持签名"))
				}
			}
			return value
		}
		for key, item := range typed {
			if !business && IsProtocolStructuralField(key) {
				continue
			}
			toolValue := !business && (key == "args" && typed["name"] != nil ||
				key == "input" && (typed["type"] == "tool_use" || typed["type"] == "custom_tool_call") ||
				key == "arguments" && (typed["type"] == "function_call" || typed["name"] != nil))
			// Chat/Responses 的工具参数是 JSON 内嵌字符串；先还原其 JSON 值，
			// 避免明文中的引号、反斜杠或换行破坏客户端将要解析的参数。
			if toolValue && key == "arguments" {
				if encoded, ok := item.(string); ok {
					restored, err := vault.RestoreToolJSON([]byte(encoded))
					if err != nil {
						vault.setError(err)
						return value
					}
					typed[key] = string(restored)
					continue
				}
			}
			if toolValue {
				if err := vault.validateToolValue(item); err != nil {
					vault.setError(err)
					return value
				}
			}
			typed[key] = restoreJSONValue(item, vault, business || toolValue)
		}
	case []interface{}:
		for index, item := range typed {
			typed[index] = restoreJSONValue(item, vault, business)
		}
	case string:
		if business && (strings.Contains(typed, "MASKED_") || strings.Contains(typed, "\\")) {
			trimmed := strings.TrimSpace(typed)
			if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
				restored, err := vault.RestoreToolJSON([]byte(typed))
				if err != nil {
					vault.setError(err)
					return value
				}
				return string(restored)
			}
		}
		return vault.Restore(typed)
	}
	return value
}

func (v *Vault) setError(err error) {
	v.mu.Lock()
	if v.err == nil {
		v.err = err
	}
	v.mu.Unlock()
}

// Vault 保存单次请求的明文与占位符映射。Vault 只能挂在请求上下文中，
// 不得放入全局缓存、持久化存储或日志。
type Vault struct {
	mu            sync.RWMutex
	byOriginal    map[string]string
	byPlaceholder map[string]string
	reserved      map[string]struct{}
	missing       map[string]struct{}
	state         *placeholderKeyState
	ctx           context.Context
	restoreMiss   func(context.Context, string) error
	bytes         int
	err           error
	maskReplacer  *strings.Replacer
	scope         string
	scopeKnown    map[string]struct{}
	scopeSnapshot *scopeMappingSnapshot
}

// ReserveText 登记同一请求中尚未处理的原始文本，用于避免占位符与请求
// 自带文本发生冲突。
func (v *Vault) ReserveText(text string) {
	if v == nil || !strings.Contains(text, "MASKED_") {
		return
	}
	placeholders := reservedPlaceholderPattern.FindAllString(text, -1)
	if len(placeholders) == 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, placeholder := range placeholders {
		if len(placeholder) > maxPlaceholderBytes {
			v.err = fmt.Errorf("请求中的占位符超过 %d 字节上限", maxPlaceholderBytes)
			return
		}
		if _, exists := v.reserved[placeholder]; exists {
			continue
		}
		v.reserved[strings.Clone(placeholder)] = struct{}{}
	}
}

func NewVault() *Vault {
	return NewVaultWithContext(context.Background())
}

func NewVaultWithContext(ctx context.Context) *Vault {
	return newVault(ctx, &memoryPlaceholderState, nil)
}

// NewVault 创建绑定当前映射库的请求级 Vault；审计回调由请求所属管线提供。
func (s *MappingStore) NewVault(ctx context.Context, restoreMiss func(context.Context, string) error) *Vault {
	return newVault(ctx, &s.placeholderState, restoreMiss)
}

// NewScopedVault 仅加载该调用方会话已授权的映射。空范围退为请求内恢复。
func (s *MappingStore) NewScopedVault(ctx context.Context, scope string, restoreMiss func(context.Context, string) error) *Vault {
	v := s.NewVault(ctx, restoreMiss)
	v.scope = scope
	if scope != "" {
		v.err = s.loadScope(ctx, scope, v)
	}
	return v
}

func newVault(ctx context.Context, state *placeholderKeyState, restoreMiss func(context.Context, string) error) *Vault {
	return &Vault{
		byOriginal:    make(map[string]string),
		byPlaceholder: make(map[string]string),
		reserved:      make(map[string]struct{}),
		missing:       make(map[string]struct{}),
		scopeKnown:    make(map[string]struct{}),
		state:         state,
		ctx:           ctx,
		restoreMiss:   restoreMiss,
	}
}

// Err 返回请求级映射的错误，响应出口必须在写出前检查。
func (v *Vault) Err() error {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.err
}

func (v *Vault) Empty() bool {
	if v == nil {
		return true
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.byPlaceholder) == 0
}

func (v *Vault) CanRestore() bool {
	return v != nil && (!v.Empty() || v.state.store != nil || v.Err() != nil)
}

// Generation 随本次请求新增映射递增，用于使已处理正文缓存失效。
func (v *Vault) Generation() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.byPlaceholder)
}

// Clear 释放当前请求持有的全部明文、占位符和冲突检查文本。
func (v *Vault) Clear() {
	if v == nil {
		return
	}
	v.mu.Lock()
	clear(v.byOriginal)
	clear(v.byPlaceholder)
	clear(v.reserved)
	clear(v.missing)
	clear(v.scopeKnown)
	v.maskReplacer = nil
	v.scopeSnapshot = nil
	v.bytes = 0
	v.err = nil
	v.mu.Unlock()
}

// Mask 使用语义类型和进程级密钥保护的稳定标识替换命中区间。同一原文在
// 进程内的不同请求复用同一占位符，提升上游前缀缓存命中。明文只用于请求
// 与有界会话缓存；占位符不包含可直接推断明文的哈希、长度或尾号。
func (v *Vault) Mask(text string, matches []RedactionMatch) (string, error) {
	if v == nil {
		return "", fmt.Errorf("敏感信息映射仓未初始化")
	}
	if err := v.Err(); err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return text, nil
	}
	matches = append([]RedactionMatch(nil), matches...)
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End > matches[j].End
	})

	// 先校验完整区间，再批量加载本段需要的历史映射；非法输入不能触发持久写入。
	last := 0
	for _, match := range matches {
		if err := v.ctx.Err(); err != nil {
			return "", err
		}
		if match.Start < last || match.Start < 0 || match.End > len(text) || match.Start >= match.End {
			return "", fmt.Errorf("敏感信息规则 %q 返回了无效或重叠区间", match.Rule)
		}
		if text[match.Start:match.End] != match.Original {
			return "", fmt.Errorf("敏感信息规则 %q 返回的原文与命中区间不一致", match.Rule)
		}
		last = match.End
	}
	if err := v.preloadOriginals(matches); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.Grow(len(text))
	last = 0
	for _, match := range matches {
		if err := v.ctx.Err(); err != nil {
			return "", err
		}
		placeholder, err := v.placeholder(match.Type, match.Original, text)
		if err != nil {
			return "", err
		}
		builder.WriteString(text[last:match.Start])
		builder.WriteString(placeholder)
		last = match.End
	}
	builder.WriteString(text[last:])
	return builder.String(), nil
}

func (v *Vault) preloadOriginals(matches []RedactionMatch) error {
	if v.state.store == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return v.err
	}
	originals := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, cap(originals))
	for _, match := range matches {
		if _, known := v.byOriginal[match.Original]; known {
			continue
		}
		if _, duplicate := seen[match.Original]; duplicate {
			continue
		}
		seen[match.Original] = struct{}{}
		originals = append(originals, match.Original)
	}
	if len(originals) == 0 {
		return nil
	}
	stored, err := v.state.store.lookupOriginals(v.ctx, originals)
	if err != nil {
		return fmt.Errorf("读取持久脱敏映射失败: %w", err)
	}
	for _, original := range originals {
		if placeholder, found := stored[original]; found {
			if err := v.addMappingLocked(original, placeholder); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *Vault) placeholder(kind, original, source string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return "", v.err
	}
	if placeholder, ok := v.byOriginal[original]; ok {
		return placeholder, nil
	}
	// Mask 已批量预载本段的历史映射；走到这里表示需要生成并同步持久化新映射。
	store := v.state.store
	kind = normalizePlaceholderType(kind)
	key, err := v.state.getKey()
	if err != nil {
		return "", err
	}
	for counter := byte(0); counter < 16; counter++ {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte{counter})
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(original))
		token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil)[:8])
		placeholder := "MASKED_" + kind + "_" + token + "_END"
		if strings.Contains(source, placeholder) {
			continue
		}
		if _, exists := v.reserved[placeholder]; exists {
			continue
		}
		if _, exists := v.byPlaceholder[placeholder]; exists {
			continue
		}
		// 持久库写入（写失败返回错误——fail-closed，映射写不进不转发）。
		if store != nil {
			stored, err := store.putMapping(v.ctx, original, kind, placeholder)
			if err != nil {
				return "", err
			}
			placeholder = stored
		}
		if err := v.addMappingLocked(original, placeholder); err != nil {
			return "", err
		}
		return placeholder, nil
	}
	return "", fmt.Errorf("生成无冲突的敏感信息占位符失败")
}

func (s *placeholderKeyState) getKey() ([]byte, error) {
	s.once.Do(func() {
		if s.store != nil {
			s.key, s.err = s.store.PlaceholderKey()
			return
		}
		s.key = make([]byte, 32)
		if _, err := rand.Read(s.key); err != nil {
			s.err = fmt.Errorf("生成敏感信息占位符密钥失败: %w", err)
			s.key = nil
		}
	})
	return s.key, s.err
}

func (v *Vault) addMappingLocked(original, placeholder string) error {
	if existing, ok := v.byPlaceholder[placeholder]; ok {
		if existing != original {
			v.err = fmt.Errorf("敏感信息占位符映射冲突")
			return v.err
		}
		return nil
	}
	original, placeholder = strings.Clone(original), strings.Clone(placeholder)
	v.byOriginal[original] = placeholder
	v.byPlaceholder[placeholder] = original
	v.bytes += len(original) + len(placeholder)
	v.maskReplacer = nil
	v.scopeSnapshot = nil
	return nil
}

func normalizePlaceholderType(kind string) string {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	var builder strings.Builder
	for _, current := range kind {
		if current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == '_' {
			builder.WriteRune(current)
		}
	}
	kind = strings.Trim(builder.String(), "_")
	if len(kind) > 64 {
		kind = kind[:64]
	}
	if kind == "" {
		return "SENSITIVE"
	}
	return kind
}

// Restore 还原当前请求中的完整语义占位符。占位符必须位于独立的标识符边界，
// 未知值和部分匹配均保持原样；仅使用本次识别及会话授权映射，miss 记审计回调。
func (v *Vault) Restore(text string) string {
	if v == nil || text == "" {
		return text
	}
	restorer := NewStreamRestorer(v)
	return restorer.Feed(text) + restorer.Flush()
}

// PreloadPersistent 检查完整占位符的会话授权；历史映射由 NewScopedVault 一次加载。
func (v *Vault) PreloadPersistent(text string) error {
	if v == nil {
		return nil
	}
	for _, placeholder := range reservedPlaceholderPattern.FindAllString(text, -1) {
		if len(placeholder) > maxPlaceholderBytes {
			v.mu.Lock()
			v.err = fmt.Errorf("请求中的占位符超过 %d 字节上限", maxPlaceholderBytes)
			v.mu.Unlock()
			return v.Err()
		}
		v.lookupPlaceholder(placeholder)
		if err := v.Err(); err != nil {
			return err
		}
	}
	return v.Err()
}

func (v *Vault) lookupPlaceholder(placeholder string) (string, bool) {
	v.mu.Lock()
	if original, known := v.byPlaceholder[placeholder]; known {
		v.mu.Unlock()
		return original, true
	}
	if _, missed := v.missing[placeholder]; missed || v.err != nil {
		v.mu.Unlock()
		return "", false
	}
	// 不能凭模型返回的占位符解密全库；只有本次识别或已加载的会话映射可恢复。
	v.missing[strings.Clone(placeholder)] = struct{}{}
	v.mu.Unlock()
	if v.restoreMiss != nil {
		if err := v.restoreMiss(v.ctx, placeholder); err != nil {
			v.mu.Lock()
			v.err = fmt.Errorf("记录脱敏还原未命中失败: %w", err)
			v.mu.Unlock()
		}
	}
	return "", false
}

// MaskKnown 将文本中已经登记到当前请求 Vault 的敏感原文替换为对应占位符。
// 它用于对协议解析阶段提前提取的观测文本应用与请求体一致的脱敏映射。
func (v *Vault) MaskKnown(text string) string {
	if v == nil || text == "" {
		return text
	}
	v.mu.Lock()
	if len(v.byOriginal) == 0 {
		v.mu.Unlock()
		return text
	}
	if v.maskReplacer == nil {
		if v.scopeSnapshot != nil {
			v.maskReplacer = v.scopeSnapshot.maskReplacer()
		} else {
			v.maskReplacer = newMaskReplacer(v.byOriginal)
		}
	}
	replacer := v.maskReplacer
	v.mu.Unlock()
	// 占位符本身不可再掩码，否则短原值会破坏类型名或标识。
	if !strings.Contains(text, "MASKED_") {
		return replacer.Replace(text)
	}
	spans := reservedPlaceholderPattern.FindAllStringIndex(text, -1)
	if len(spans) == 0 {
		return replacer.Replace(text)
	}
	var out strings.Builder
	last := 0
	for _, span := range spans {
		out.WriteString(replacer.Replace(text[last:span[0]]))
		out.WriteString(text[span[0]:span[1]])
		last = span[1]
	}
	out.WriteString(replacer.Replace(text[last:]))
	return out.String()
}

func newMaskReplacer(byOriginal map[string]string) *strings.Replacer {
	originals := make([]string, 0, len(byOriginal))
	for original := range byOriginal {
		originals = append(originals, original)
	}
	sort.Slice(originals, func(i, j int) bool {
		if len(originals[i]) != len(originals[j]) {
			return len(originals[i]) > len(originals[j])
		}
		return originals[i] < originals[j]
	})
	pairs := make([]string, 0, len(originals)*2)
	for _, original := range originals {
		pairs = append(pairs, original, byOriginal[original])
	}
	return strings.NewReplacer(pairs...)
}

// StreamRestorer 对一个逻辑文本通道执行增量精确还原。它只暂存可能成为
// 当前 Vault 占位符的尾部前缀，普通文本立即返回。
type StreamRestorer struct {
	vault              *Vault
	pending            string
	previousSourceByte byte
	hasPreviousSource  bool
	jsonEncoded        bool
}

func NewStreamRestorer(vault *Vault) *StreamRestorer {
	return &StreamRestorer{vault: vault}
}

// NewJSONStreamRestorer 用于工具参数的 JSON 文本增量，只对还原的明文做 JSON 字符串转义。
func NewJSONStreamRestorer(vault *Vault) *StreamRestorer {
	return &StreamRestorer{vault: vault, jsonEncoded: true}
}

func (r *StreamRestorer) encodeOriginal(original string) string {
	if !r.jsonEncoded {
		return original
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		r.vault.mu.Lock()
		r.vault.err = fmt.Errorf("编码已还原工具参数失败: %w", err)
		r.vault.mu.Unlock()
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

func (r *StreamRestorer) Feed(fragment string) string {
	if r == nil || r.vault == nil {
		return fragment
	}
	// 普通片段直接返回，仍更新源文本边界，避免把下一片段的标识符后缀误还原。
	if r.pending == "" && !strings.Contains(fragment, "M") {
		r.consumeSource(fragment)
		return fragment
	}
	input := r.pending + fragment
	r.pending = ""
	var output strings.Builder
	for len(input) > 0 {
		if input[0] != 'M' {
			end := strings.IndexByte(input, 'M')
			if end < 0 {
				end = len(input)
			}
			output.WriteString(input[:end])
			r.consumeSource(input[:end])
			input = input[end:]
			continue
		}
		if input[0] == 'M' && r.validLeftBoundary() {
			const prefix = "MASKED_"
			if len(input) < len(prefix) && strings.HasPrefix(prefix, input) {
				r.pending = strings.Clone(input)
				break
			}
			if strings.HasPrefix(input, prefix) {
				end := len(prefix)
				for end < len(input) && end <= maxPlaceholderBytes && isPlaceholderIdentifierByte(input[end]) {
					end++
				}
				if end == len(input) && end <= maxPlaceholderBytes {
					r.pending = strings.Clone(input)
					break
				}
				if end <= maxPlaceholderBytes {
					token := input[:end]
					if reservedPlaceholderPattern.FindString(token) == token {
						if original, ok := r.vault.lookupPlaceholder(token); ok {
							output.WriteString(r.encodeOriginal(original))
							r.consumeSource(token)
							input = input[end:]
							continue
						}
					}
				}
			}
		}
		output.WriteByte(input[0])
		r.previousSourceByte = input[0]
		r.hasPreviousSource = true
		input = input[1:]
	}
	return output.String()
}

func (r *StreamRestorer) validLeftBoundary() bool {
	return !r.hasPreviousSource || !isPlaceholderIdentifierByte(r.previousSourceByte)
}

func (r *StreamRestorer) consumeSource(value string) {
	if value == "" {
		return
	}
	r.previousSourceByte = value[len(value)-1]
	r.hasPreviousSource = true
}

func isPlaceholderIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_'
}

func (r *StreamRestorer) Pending() bool {
	return r != nil && r.pending != ""
}

func (r *StreamRestorer) Flush() string {
	if r == nil {
		return ""
	}
	pending := r.pending
	r.pending = ""
	if pending != "" && r.vault != nil && r.validLeftBoundary() && reservedPlaceholderPattern.FindString(pending) == pending {
		if original, ok := r.vault.lookupPlaceholder(pending); ok {
			r.consumeSource(pending)
			return r.encodeOriginal(original)
		}
	}
	r.consumeSource(pending)
	return pending
}
