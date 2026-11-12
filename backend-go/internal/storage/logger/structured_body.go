// 超大请求只保存独立的尾部消息快照，不依赖其他轮次，也不物化完整媒体对象树。
package logger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/tidwall/gjson"
)

const (
	MaxStructuredBodyBytes = 256 << 10
	maxSnapshotMessages    = 16
	snapshotMessageBudget  = 10 << 10
	snapshotConfigBudget   = 32 << 10
)

type structuredBodyMeta struct {
	MessageOffset    int      `json:"messageOffset"`
	OriginalMessages int      `json:"originalMessages"`
	MessageHashes    []string `json:"messageHashes"`
	TopHash          string   `json:"topHash"`
	RequestHash      string   `json:"requestHash"`
	Notice           string   `json:"notice"`
}

func hashLogText(raw string) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, raw)
	return hex.EncodeToString(hash.Sum(nil))
}

// PrepareStructuredRequest 不放宽原始正文上限，只增加明确标记为摘要的有界记录。
func PrepareStructuredRequest(raw []byte) (string, error) {
	if !gjson.ValidBytes(raw) {
		return "", fmt.Errorf("超大请求正文不是完整 JSON，无法提取结构化摘要")
	}
	root := gjson.ParseBytes(raw)
	if !root.IsObject() {
		return "", fmt.Errorf("超大请求正文必须为 JSON 对象")
	}
	field := ""
	var messages gjson.Result
	for _, candidate := range []string{"messages", "input", "contents"} {
		value := root.Get(candidate)
		if value.IsArray() || (candidate == "input" && value.Type == gjson.String) {
			field, messages = candidate, value
			break
		}
	}
	if field == "" {
		return "", fmt.Errorf("超大请求没有可提取的 messages、input 或 contents")
	}
	// 环形缓冲仅保留最后十六条消息，原始切片仍由请求处理器管理。
	tail := make([]gjson.Result, maxSnapshotMessages)
	count := 0
	if messages.IsArray() {
		messages.ForEach(func(_, value gjson.Result) bool {
			tail[count%maxSnapshotMessages] = value
			count++
			return true
		})
	} else {
		tail[0], count = messages, 1
	}
	offset := max(0, count-maxSnapshotMessages)
	items := make([]any, 0, min(count, maxSnapshotMessages))
	hashes := make([]string, 0, cap(items))
	for index := offset; index < count; index++ {
		value := tail[index%maxSnapshotMessages]
		budget := snapshotMessageBudget
		compact := compactLogValue(value, &budget, 0)
		if messages.Type == gjson.String {
			compact = map[string]any{"role": "user", "content": compact}
		}
		items = append(items, compact)
		hashes = append(hashes, hashLogText(value.Raw))
	}
	snapshot := map[string]any{field: items}
	configBudget := snapshotConfigBudget
	// 配置变化用全量字段哈希识别，展示只保留常用字段和有界摘要。
	topHashes := make([]string, 0, 32)
	tooManyFields := false
	visibleKeys := map[string]bool{
		"model": true, "stream": true, "system": true, "instructions": true,
		"tools": true, "tool_choice": true, "temperature": true, "top_p": true,
		"max_tokens": true, "max_output_tokens": true, "reasoning": true,
		"previous_response_id": true, "parallel_tool_calls": true,
	}
	root.ForEach(func(key, value gjson.Result) bool {
		if key.Str == field {
			return true
		}
		if len(topHashes) >= 256 {
			tooManyFields = true
			return false
		}
		topHashes = append(topHashes, hashLogText(key.Raw)+hashLogText(value.Raw))
		if visibleKeys[key.Str] {
			snapshot[key.Str] = compactLogValue(value, &configBudget, 0)
		}
		return true
	})
	if tooManyFields {
		return "", fmt.Errorf("请求配置字段超过结构化摘要的 256 项预算")
	}
	slices.Sort(topHashes)
	meta := structuredBodyMeta{
		MessageOffset: offset, OriginalMessages: count, MessageHashes: hashes,
		TopHash: hashLogText(strings.Join(topHashes, "")), RequestHash: hashLogText(root.Raw),
		Notice: "原始正文已省略；这里只保存最近消息和常用配置的有界摘要，长文本与媒体可能已省略",
	}
	snapshot["_log"] = meta
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("编码结构化请求摘要失败: %w", err)
	}
	if len(body) > MaxStructuredBodyBytes {
		return "", fmt.Errorf("结构化请求摘要超过 %d 字节上限", MaxStructuredBodyBytes)
	}
	return string(body), nil
}

func compactLogValue(value gjson.Result, budget *int, depth int) any {
	if *budget < 128 || depth >= 12 {
		return "[内容已省略：结构化摘要预算不足]"
	}
	*budget -= 64
	switch {
	case value.IsArray():
		out := make([]any, 0)
		value.ForEach(func(_, child gjson.Result) bool {
			if *budget < 128 || len(out) >= 64 {
				out = append(out, "[后续内容已省略]")
				return false
			}
			out = append(out, compactLogValue(child, budget, depth+1))
			return true
		})
		return out
	case value.IsObject():
		out := make(map[string]any)
		// 内容很长或字段顺序不同，都不能先耗尽角色、工具关联等结构字段的预算。
		for _, field := range []string{"role", "type", "name", "id", "call_id", "tool_use_id", "tool_call_id"} {
			child := value.Get(field)
			if child.Type != gjson.String {
				continue
			}
			fieldBudget := min(*budget, 1024)
			before := fieldBudget
			out[field] = compactLogValue(child, &fieldBudget, depth+1)
			*budget -= before - fieldBudget
		}
		value.ForEach(func(key, child gjson.Result) bool {
			if _, exists := out[key.Str]; exists {
				return true
			}
			if *budget < 128 || len(out) >= 64 || len(key.Str) > 128 {
				out["_log_omitted"] = "后续字段已省略"
				return false
			}
			*budget -= len(key.Str)
			switch key.Str {
			case "b64_json", "b64Json":
				out[key.Str] = fmt.Sprintf("[媒体内容已省略：%d 字节]", len(child.Raw))
			case "data":
				if value.Get("type").Str == "base64" || value.Get("mimeType").Exists() || value.Get("mime_type").Exists() {
					out[key.Str] = fmt.Sprintf("[媒体内容已省略：%d 字节]", len(child.Raw))
				} else {
					out[key.Str] = compactLogValue(child, budget, depth+1)
				}
			default:
				out[key.Str] = compactLogValue(child, budget, depth+1)
			}
			return true
		})
		return out
	case value.Type == gjson.String:
		text := utils.RedactSensitivePlaceholdersForLog(value.Str)
		text = stripImagesInText(text)
		// JSON 转义最多占六倍字节，给最终编码预留预算。
		limit := min(*budget/6, 8192)
		if len(text) > limit {
			end := limit
			for end > 0 && !utf8.RuneStart(text[end]) {
				end--
			}
			text = text[:end] + "\n[后续文本已省略]"
		}
		*budget -= 6 * len(text)
		return text
	case value.Type == gjson.True:
		return true
	case value.Type == gjson.False:
		return false
	case value.Type == gjson.Number:
		return json.Number(value.Raw)
	default:
		return nil
	}
}
