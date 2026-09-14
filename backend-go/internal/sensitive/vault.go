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

var reservedPlaceholderPattern = regexp.MustCompile(`\bMASKED_[A-Z0-9_]+_[A-Z2-7]{13}_PRESERVE_EXACTLY\b`)

const (
	maxVaultMappings    = 8192
	maxVaultBytes       = 8 << 20
	maxPlaceholderBytes = 256
)

// 每次配置切换创建独立状态，已开始的请求继续持有原状态；不重置正在使用的 Once。
type placeholderKeyState struct {
	store *MappingStore
	once  sync.Once
	key   []byte
	err   error
}

// SetPersistentMappingStore 注入进程级持久映射库（mask 模式启用时由 main.go 调用）。
// 注入后新 Vault 的占位符密钥改由主密钥经 HKDF 派生——同一原文跨重启生成同一
// 占位符，历史会话里的旧占位符可还原；还原 miss 记审计（回调由 hooks 层设置）。
// 传 nil 撤销持久化（设置热切换 mask→block 时）；已生成的占位符不受影响。
func SetPersistentMappingStore(store *MappingStore) {
	globalPersistentStoreMu.Lock()
	if globalPlaceholderState.store != store {
		globalPlaceholderState = &placeholderKeyState{store: store}
	}
	globalPersistentStoreMu.Unlock()
}

var (
	globalPersistentStoreMu sync.RWMutex
	globalPlaceholderState  = &placeholderKeyState{}
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
	return globalPlaceholderState.store
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
	if v == nil || len(body) == 0 {
		return body, nil
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if !v.CanRestore() {
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
	value = restoreJSONStrings(value, v)
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
	missing       map[string]struct{}
	state         *placeholderKeyState
	ctx           context.Context
	bytes         int
	err           error
	maskReplacer  *strings.Replacer
}

// ReserveText 登记同一请求中尚未处理的原始文本，用于避免占位符与请求
// 自带文本发生冲突。
func (v *Vault) ReserveText(text string) {
	if v == nil || text == "" {
		return
	}
	placeholders := reservedPlaceholderPattern.FindAllString(text, maxVaultMappings+1)
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
		if len(v.reserved) >= maxVaultMappings {
			v.err = fmt.Errorf("请求中的保留占位符超过 %d 个上限", maxVaultMappings)
			return
		}
		v.reserved[strings.Clone(placeholder)] = struct{}{}
	}
}

func NewVault() *Vault {
	return NewVaultWithContext(context.Background())
}

func NewVaultWithContext(ctx context.Context) *Vault {
	globalPersistentStoreMu.RLock()
	state := globalPlaceholderState
	globalPersistentStoreMu.RUnlock()
	return &Vault{
		byOriginal:    make(map[string]string),
		byPlaceholder: make(map[string]string),
		reserved:      make(map[string]struct{}),
		missing:       make(map[string]struct{}),
		state:         state,
		ctx:           ctx,
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
	return v != nil && (!v.Empty() || v.state.store != nil)
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
	v.maskReplacer = nil
	v.bytes = 0
	v.err = nil
	v.mu.Unlock()
}

// Mask 使用语义类型和进程级密钥保护的稳定标识替换命中区间。同一原文在
// 进程内的不同请求复用同一占位符，提升上游前缀缓存命中；明文仍只保留在
// 当前请求 Vault 中，占位符不包含可直接推断明文的哈希、长度或尾号。
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

	var builder strings.Builder
	builder.Grow(len(text))
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
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return "", v.err
	}
	if placeholder, ok := v.byOriginal[original]; ok {
		return placeholder, nil
	}
	// 同请求先查内存；持久库读取失败必须显式报错，避免生成不一致的新映射。
	store := v.state.store
	if store != nil {
		lookup, err := store.lookupOriginal(v.ctx, original)
		if err != nil {
			return "", fmt.Errorf("读取持久脱敏映射失败: %w", err)
		}
		if lookup.Found {
			if err := v.addMappingLocked(original, lookup.Placeholder); err != nil {
				return "", err
			}
			return lookup.Placeholder, nil
		}
	}
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
		if err := v.checkMappingCapacityLocked(original, placeholder); err != nil {
			return "", err
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

func (v *Vault) checkMappingCapacityLocked(original, placeholder string) error {
	if len(v.byPlaceholder) >= maxVaultMappings || v.bytes+len(original)+len(placeholder) > maxVaultBytes {
		v.err = fmt.Errorf("请求脱敏映射超过容量上限（%d 条或 %d 字节）", maxVaultMappings, maxVaultBytes)
		return v.err
	}
	return nil
}

func (v *Vault) addMappingLocked(original, placeholder string) error {
	if existing, ok := v.byPlaceholder[placeholder]; ok {
		if existing != original {
			v.err = fmt.Errorf("敏感信息占位符映射冲突")
			return v.err
		}
		return nil
	}
	if err := v.checkMappingCapacityLocked(original, placeholder); err != nil {
		return err
	}
	original, placeholder = strings.Clone(original), strings.Clone(placeholder)
	v.byOriginal[original] = placeholder
	v.byPlaceholder[placeholder] = original
	v.bytes += len(original) + len(placeholder)
	v.maskReplacer = nil
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
// 未知值和部分匹配均保持原样。持久映射库按完整候选值查询历史映射，miss 记审计回调。
func (v *Vault) Restore(text string) string {
	if v == nil || text == "" {
		return text
	}
	restorer := NewStreamRestorer(v)
	return restorer.Feed(text) + restorer.Flush()
}

// PreloadPersistent 预载完整占位符；跨片段占位符由 StreamRestorer 在拼全时读取。
func (v *Vault) PreloadPersistent(text string) error {
	if v == nil {
		return nil
	}
	for _, placeholder := range reservedPlaceholderPattern.FindAllString(text, maxVaultMappings+1) {
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
	if _, missed := v.missing[placeholder]; missed || v.state.store == nil || v.err != nil {
		v.mu.Unlock()
		return "", false
	}
	lookup, err := v.state.store.lookupPlaceholder(v.ctx, placeholder)
	if err != nil {
		v.err = fmt.Errorf("读取持久脱敏占位符失败: %w", err)
		v.mu.Unlock()
		return "", false
	}
	if lookup.Found {
		err := v.addMappingLocked(lookup.Original, placeholder)
		v.mu.Unlock()
		return lookup.Original, err == nil
	}
	if len(v.missing) >= maxVaultMappings {
		v.err = fmt.Errorf("请求中的未知占位符超过 %d 个上限", maxVaultMappings)
		v.mu.Unlock()
		return "", false
	}
	v.missing[strings.Clone(placeholder)] = struct{}{}
	v.mu.Unlock()
	globalPersistentStoreMu.RLock()
	callback := persistentMissCallback
	globalPersistentStoreMu.RUnlock()
	if callback != nil {
		callback(placeholder)
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
	if v.maskReplacer == nil {
		originals := make([]string, 0, len(v.byOriginal))
		for original := range v.byOriginal {
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
			pairs = append(pairs, original, v.byOriginal[original])
		}
		v.maskReplacer = strings.NewReplacer(pairs...)
	}
	replacer := v.maskReplacer
	v.mu.Unlock()
	return replacer.Replace(text)
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
	if r == nil || r.vault == nil {
		return fragment
	}
	input := r.pending + fragment
	r.pending = ""
	var output strings.Builder
	for len(input) > 0 {
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
							output.WriteString(original)
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
			return original
		}
	}
	r.consumeSource(pending)
	return pending
}
