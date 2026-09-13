package sensitive

import (
	"bytes"
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

var reservedPlaceholderPattern = regexp.MustCompile(`\bMASKED_[A-Z0-9_]+_[A-Z2-7]{13}_PRESERVE_EXACTLY\b`)

var (
	placeholderKeyOnce sync.Once
	placeholderKey     []byte
	placeholderKeyErr  error
)

// SetPersistentMappingStore 注入进程级持久映射库（mask 模式启用时由 main.go 调用）。
// 注入后新 Vault 的占位符密钥改由主密钥经 HKDF 派生——同一原文跨重启生成同一
// 占位符，历史会话里的旧占位符可还原；还原 miss 记审计（回调由 hooks 层设置）。
// 传 nil 撤销持久化（设置热切换 mask→block 时）；已生成的占位符不受影响。
func SetPersistentMappingStore(store *MappingStore) {
	globalPersistentStoreMu.Lock()
	globalPersistentStore = store
	placeholderKeyOnce = sync.Once{}
	placeholderKey = nil
	placeholderKeyErr = nil
	globalPersistentStoreMu.Unlock()
}

var (
	globalPersistentStoreMu sync.RWMutex
	globalPersistentStore   *MappingStore
	// persistentMissCallback 在还原遇到持久库 miss 时被调用（hooks 层注入审计）。
	persistentMissCallback func(placeholder string)
)

// SetPersistentMissCallback 设置还原 miss 的审计回调（hooks 层注入）。
func SetPersistentMissCallback(callback func(placeholder string)) {
	globalPersistentStoreMu.Lock()
	persistentMissCallback = callback
	globalPersistentStoreMu.Unlock()
}

// PersistentMappingStore 返回当前进程级持久映射库（可能为 nil）。
func PersistentMappingStore() *MappingStore {
	globalPersistentStoreMu.RLock()
	defer globalPersistentStoreMu.RUnlock()
	return globalPersistentStore
}

// RedactionMatch 是可逆脱敏使用的统一命中区间。Original 必须与 Text 的对应
// 字节区间完全一致；调用方不得记录该字段。
type RedactionMatch struct {
	Type     string
	Rule     string
	Original string
	Start    int
	End      int
}

// RestoreJSON 递归还原 JSON 中的全部字符串值。非 JSON 响应按普通文本还原。
func (v *Vault) RestoreJSON(body []byte) ([]byte, error) {
	if v == nil || v.Empty() || len(body) == 0 {
		return body, nil
	}
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return []byte(v.Restore(string(body))), nil
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("解析待还原响应体尾部失败: %w", err)
		}
	} else {
		return nil, fmt.Errorf("解析待还原响应体失败: JSON 包含多个顶层值")
	}
	value = restoreJSONStrings(value, v)
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("序列化已还原响应体失败: %w", err)
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}

func restoreJSONStrings(value interface{}, vault *Vault) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			typed[key] = restoreJSONStrings(item, vault)
		}
	case []interface{}:
		for index, item := range typed {
			typed[index] = restoreJSONStrings(item, vault)
		}
	case string:
		return vault.Restore(typed)
	}
	return value
}

// Vault 保存单次请求的明文与占位符映射。Vault 只能挂在请求上下文中，
// 不得放入全局缓存、持久化存储或日志。
type Vault struct {
	mu            sync.RWMutex
	byOriginal    map[string]string
	byPlaceholder map[string]string
	reserved      map[string]struct{}
}

// ReserveText 登记同一请求中尚未处理的原始文本，用于避免占位符与请求
// 自带文本发生冲突。
func (v *Vault) ReserveText(text string) {
	if v == nil || text == "" {
		return
	}
	placeholders := reservedPlaceholderPattern.FindAllString(text, -1)
	if len(placeholders) == 0 {
		return
	}
	v.mu.Lock()
	for _, placeholder := range placeholders {
		v.reserved[placeholder] = struct{}{}
	}
	v.mu.Unlock()
}

func NewVault() *Vault {
	return &Vault{
		byOriginal:    make(map[string]string),
		byPlaceholder: make(map[string]string),
		reserved:      make(map[string]struct{}),
	}
}

func (v *Vault) Empty() bool {
	if v == nil {
		return true
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.byPlaceholder) == 0
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
	v.mu.Unlock()
}

// Mask 使用语义类型和进程级密钥保护的稳定标识替换命中区间。同一原文在
// 进程内的不同请求复用同一占位符，提升上游前缀缓存命中；明文仍只保留在
// 当前请求 Vault 中，占位符不包含可直接推断明文的哈希、长度或尾号。
func (v *Vault) Mask(text string, matches []RedactionMatch) (string, error) {
	if v == nil {
		return "", fmt.Errorf("敏感信息映射仓未初始化")
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

	var builder strings.Builder
	builder.Grow(len(text))
	last := 0
	for _, match := range matches {
		if match.Start < last || match.Start < 0 || match.End > len(text) || match.Start >= match.End {
			return "", fmt.Errorf("敏感信息规则 %q 返回了无效或重叠区间", match.Rule)
		}
		if text[match.Start:match.End] != match.Original {
			return "", fmt.Errorf("敏感信息规则 %q 返回的原文与命中区间不一致", match.Rule)
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

func (v *Vault) placeholder(kind, original, source string) (string, error) {
	// 持久库优先（读失败降级内存/新生成，不中断——见失败路径决策表）：
	// 命中则同原文跨进程复用同一占位符。
	if store := PersistentMappingStore(); store != nil {
		if lookup, err := store.LookupOriginal(original); err == nil && lookup.Found {
			v.mu.Lock()
			v.byOriginal[original] = lookup.Placeholder
			v.byPlaceholder[lookup.Placeholder] = original
			v.mu.Unlock()
			return lookup.Placeholder, nil
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if placeholder, ok := v.byOriginal[original]; ok {
		return placeholder, nil
	}
	kind = normalizePlaceholderType(kind)
	key, err := stablePlaceholderKey()
	if err != nil {
		return "", err
	}
	for counter := byte(0); counter < 16; counter++ {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte{counter})
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(original))
		token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil)[:8])
		placeholder := "MASKED_" + kind + "_" + token + "_PRESERVE_EXACTLY"
		if strings.Contains(source, placeholder) {
			continue
		}
		if _, exists := v.reserved[placeholder]; exists {
			continue
		}
		if _, exists := v.byPlaceholder[placeholder]; exists {
			continue
		}
		v.byOriginal[original] = placeholder
		v.byPlaceholder[placeholder] = original
		// 持久库写入（写失败返回错误——fail-closed，映射写不进不转发）。
		if store := PersistentMappingStore(); store != nil {
			if err := store.PutMapping(original, kind, placeholder); err != nil {
				return "", err
			}
		}
		return placeholder, nil
	}
	return "", fmt.Errorf("生成无冲突的敏感信息占位符失败")
}

// stablePlaceholderKey 返回占位符派生密钥。持久映射库注入时从主密钥
// HKDF 派生（跨进程稳定）；否则进程内随机（重启即变，零落盘模式）。
// SetPersistentMappingStore 会重置 once，注入/撤销后重新解析。
func stablePlaceholderKey() ([]byte, error) {
	placeholderKeyOnce.Do(func() {
		if store := PersistentMappingStore(); store != nil {
			key, err := store.PlaceholderKey()
			if err != nil {
				placeholderKeyErr = err
				return
			}
			placeholderKey = key
			return
		}
		placeholderKey = make([]byte, 32)
		if _, err := rand.Read(placeholderKey); err != nil {
			placeholderKeyErr = fmt.Errorf("生成敏感信息占位符密钥失败: %w", err)
			placeholderKey = nil
		}
	})
	if placeholderKeyErr != nil {
		return nil, placeholderKeyErr
	}
	return placeholderKey, nil
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
	if kind == "" {
		return "SENSITIVE"
	}
	return kind
}

// Restore 还原当前请求中的完整语义占位符。占位符必须位于独立的标识符边界，
// 未知值和部分匹配均保持原样。持久映射库注入时，先按文本中出现的占位符形态
// 预载持久映射（历史会话的旧占位符跨重启还原）；预载命中的 miss 记审计回调。
func (v *Vault) Restore(text string) string {
	if v == nil || text == "" {
		return text
	}
	v.PreloadPersistent(text)
	restorer := NewStreamRestorer(v)
	return restorer.Feed(text) + restorer.Flush()
}

// PreloadPersistent 把文本中出现的合法占位符形态批量登记进当前 Vault
// （仅持久库命中的条目；miss 调用审计回调后跳过——原样下发契约不变）。
// 读失败静默降级（失败路径决策表：持久库读失败不中断）。
func (v *Vault) PreloadPersistent(text string) {
	store := PersistentMappingStore()
	if store == nil {
		return
	}
	seen := make(map[string]struct{})
	for _, placeholder := range reservedPlaceholderPattern.FindAllString(text, -1) {
		if _, exists := seen[placeholder]; exists {
			continue
		}
		seen[placeholder] = struct{}{}
		v.mu.RLock()
		_, known := v.byPlaceholder[placeholder]
		v.mu.RUnlock()
		if known {
			continue
		}
		lookup, err := store.LookupPlaceholder(placeholder)
		if err != nil {
			continue // 读失败降级：该占位符按未知处理，原样下发
		}
		if !lookup.Found {
			// 合法占位符形态但持久库没有（如密钥已轮换）：原样下发 + 审计留痕。
			globalPersistentStoreMu.RLock()
			callback := persistentMissCallback
			globalPersistentStoreMu.RUnlock()
			if callback != nil {
				callback(placeholder)
			}
			continue
		}
		v.mu.Lock()
		v.byPlaceholder[placeholder] = lookup.Original
		v.byOriginal[lookup.Original] = placeholder
		v.mu.Unlock()
	}
}

// MaskKnown 将文本中已经登记到当前请求 Vault 的敏感原文替换为对应占位符。
// 它用于对协议解析阶段提前提取的观测文本应用与请求体一致的脱敏映射。
func (v *Vault) MaskKnown(text string) string {
	if v == nil || text == "" {
		return text
	}
	v.mu.RLock()
	pairs := make([][2]string, 0, len(v.byOriginal))
	for original, placeholder := range v.byOriginal {
		pairs = append(pairs, [2]string{original, placeholder})
	}
	v.mu.RUnlock()
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i][0]) > len(pairs[j][0])
	})
	for _, pair := range pairs {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}
	return text
}

type restoreMapping struct {
	token    string
	original string
}

func (v *Vault) restoreMappings() []restoreMapping {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make([]restoreMapping, 0, len(v.byPlaceholder))
	for placeholder, original := range v.byPlaceholder {
		result = append(result, restoreMapping{token: placeholder, original: original})
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i].token) != len(result[j].token) {
			return len(result[i].token) > len(result[j].token)
		}
		return result[i].token < result[j].token
	})
	return result
}

// StreamRestorer 对一个逻辑文本通道执行增量精确还原。它只暂存可能成为
// 当前 Vault 占位符的尾部前缀，普通文本立即返回。
type StreamRestorer struct {
	vault              *Vault
	pending            string
	previousSourceByte byte
	hasPreviousSource  bool
}

func NewStreamRestorer(vault *Vault) *StreamRestorer {
	return &StreamRestorer{vault: vault}
}

func (r *StreamRestorer) Feed(fragment string) string {
	if r == nil || r.vault == nil || r.vault.Empty() {
		return fragment
	}
	input := r.pending + fragment
	r.pending = ""
	mappings := r.vault.restoreMappings()
	var output strings.Builder
	for len(input) > 0 {
		matched := false
		for _, mapping := range mappings {
			if !strings.HasPrefix(input, mapping.token) || !r.validLeftBoundary(mapping) {
				continue
			}
			if len(input) == len(mapping.token) {
				r.pending = input
				return output.String()
			}
			if isPlaceholderIdentifierByte(input[len(mapping.token)]) {
				continue
			}
			output.WriteString(mapping.original)
			r.consumeSource(mapping.token)
			input = input[len(mapping.token):]
			matched = true
			break
		}
		if matched {
			continue
		}
		possiblePrefix := false
		for _, mapping := range mappings {
			if r.validLeftBoundary(mapping) && strings.HasPrefix(mapping.token, input) {
				possiblePrefix = true
				break
			}
		}
		if possiblePrefix {
			r.pending = input
			break
		}
		output.WriteByte(input[0])
		r.previousSourceByte = input[0]
		r.hasPreviousSource = true
		input = input[1:]
	}
	return output.String()
}

func (r *StreamRestorer) validLeftBoundary(mapping restoreMapping) bool {
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
	for _, mapping := range r.vault.restoreMappings() {
		if pending == mapping.token && r.validLeftBoundary(mapping) {
			r.consumeSource(mapping.token)
			return mapping.original
		}
	}
	r.consumeSource(pending)
	return pending
}
