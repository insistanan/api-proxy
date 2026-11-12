package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/tidwall/gjson"
)

const maxContextItems = 256

// ContextPart 的 token 仅为本地字符估算，与上游 usage 独立保存。
type ContextPart struct {
	Kind            string `json:"kind"`
	Items           int    `json:"items"`
	Bytes           int    `json:"bytes"`
	EstimatedTokens int    `json:"estimatedTokens"`
}

type ContextItem struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	Hash            string `json:"hash"`
	EstimatedTokens int    `json:"estimatedTokens"`
}

// ContextSnapshot 描述已保存的上下文，不将摘要或服务端引用冒充完整历史。
type ContextSnapshot struct {
	TrafficID          int64          `json:"trafficId,omitempty"`
	Phase              string         `json:"phase,omitempty"`
	AttemptID          string         `json:"attemptId,omitempty"`
	Completeness       string         `json:"completeness"`
	Notice             string         `json:"notice,omitempty"`
	PreviousResponseID string         `json:"previousResponseId,omitempty"`
	MessageCount       int            `json:"messageCount"`
	OriginalMessages   int            `json:"originalMessages"`
	EstimatedTokens    int            `json:"estimatedTokens"`
	Parts              []ContextPart  `json:"parts"`
	Items              []ContextItem  `json:"items,omitempty"`
	ItemsLimited       bool           `json:"itemsLimited,omitempty"`
	InputChange        *ContextChange `json:"inputChange,omitempty"`
}

type ContextChange struct {
	PreviousRequestID string `json:"previousRequestId"`
	Added             int    `json:"added"`
	Removed           int    `json:"removed"`
	EstimatedDelta    int    `json:"estimatedDelta"`
	Comparable        bool   `json:"comparable"`
	OrderChanged      bool   `json:"orderChanged"`
	Notice            string `json:"notice"`
}

func LogPreview(text string, limit int) string {
	text = strings.Join(strings.Fields(utils.RedactSensitivePlaceholdersForLog(text)), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

func contextKind(item gjson.Result) string {
	switch item.Get("type").String() {
	case "function_call", "custom_tool_call", "local_shell_call", "tool_use":
		return "tool_call"
	case "function_call_output", "custom_tool_call_output", "tool_result":
		return "tool_result"
	case "reasoning", "thinking", "redacted_thinking":
		return "reasoning"
	}
	switch item.Get("role").String() {
	case "system", "developer":
		return "system"
	case "assistant", "model":
		return "assistant"
	case "tool", "function":
		return "tool_result"
	default:
		return "user"
	}
}

// AnalyzeContext 只读取已经清洗且受采集上限约束的正文，不触碰转发数据。
func AnalyzeContext(entry TrafficLog) *ContextSnapshot {
	snapshot := &ContextSnapshot{
		TrafficID: entry.ID, Phase: entry.Phase, AttemptID: entry.AttemptID,
		Completeness: "unavailable", Parts: []ContextPart{},
	}
	if entry.Truncated || len(entry.Body) > MaxBodyBytes || !gjson.Valid(entry.Body) {
		snapshot.Notice = "正文缺失、截断或不是完整 JSON，无法统计上下文"
		return snapshot
	}
	root := gjson.Parse(entry.Body)
	field := ""
	for _, key := range []string{"messages", "input", "contents"} {
		value := root.Get(key)
		if value.IsArray() || (key == "input" && value.Type == gjson.String) {
			field = key
			break
		}
	}
	if field == "" {
		snapshot.Notice = "此正文没有可识别的消息结构"
		return snapshot
	}
	snapshot.Completeness = "captured"
	snapshot.Notice = "统计已保存正文；Token 为字符估算，媒体和协议开销未精确计量"
	if meta := root.Get("_log"); entry.Phase == PhaseClientInput && meta.Exists() {
		snapshot.Completeness = "summary"
		snapshot.OriginalMessages = int(meta.Get("originalMessages").Int())
		snapshot.Notice = "仅统计尾部摘要，原始历史、长文本和媒体可能缺失"
	}
	if value := root.Get("previous_response_id"); value.Type == gjson.String && value.Str != "" {
		snapshot.PreviousResponseID = LogPreview(value.Str, 256)
		snapshot.Notice += "；包含服务端历史引用，未将引用历史计入本次正文"
	}
	partIndex := make(map[string]int)
	add := func(path, kind string, value gjson.Result, owners ...gjson.Result) {
		// 对象字段顺序变化不应被误报为内容替换。
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(value.Raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			snapshot.Completeness = "unavailable"
			snapshot.Notice = "无法解析上下文内容块：" + err.Error()
			return
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			snapshot.Completeness = "unavailable"
			snapshot.Notice = "无法编码上下文内容块：" + err.Error()
			return
		}
		canonical := string(encoded)
		fingerprint := canonical
		if len(owners) > 0 {
			// 正文相同但角色或工具结果归属变化，也属于上下文变化。
			owner := owners[0]
			fingerprint = owner.Get("role").String() + "\x00" + owner.Get("tool_call_id").String() + "\x00" + owner.Get("name").String() + "\x00" + canonical
		}
		tokens := utils.EstimateTokens(canonical)
		index, exists := partIndex[kind]
		if !exists {
			index = len(snapshot.Parts)
			partIndex[kind] = index
			snapshot.Parts = append(snapshot.Parts, ContextPart{Kind: kind})
		}
		part := &snapshot.Parts[index]
		part.Items++
		part.Bytes += len(canonical)
		part.EstimatedTokens += tokens
		snapshot.EstimatedTokens += tokens
		if len(snapshot.Items) < maxContextItems {
			snapshot.Items = append(snapshot.Items, ContextItem{Path: path, Kind: kind, Hash: hashLogText(fingerprint), EstimatedTokens: tokens})
		} else {
			snapshot.ItemsLimited = true
		}
	}
	for _, key := range []string{"system", "instructions", "systemInstruction", "system_instruction"} {
		if value := root.Get(key); value.Exists() {
			add(key, "system", value)
		}
	}
	root.Get("tools").ForEach(func(key, value gjson.Result) bool {
		add(fmt.Sprintf("tools.%d", key.Int()), "tools", value)
		return true
	})
	messages := root.Get(field)
	if messages.Type == gjson.String {
		snapshot.MessageCount = 1
		add(field, "user", messages)
	} else {
		messages.ForEach(func(key, item gjson.Result) bool {
			snapshot.MessageCount++
			path := fmt.Sprintf("%s.%d", field, key.Int())
			blocks := item.Get("content")
			if !blocks.IsArray() {
				blocks = item.Get("parts")
			}
			if blocks.IsArray() && blocks.Get("#").Int() > 0 {
				blockField := "content"
				if item.Get("parts").IsArray() {
					blockField = "parts"
				}
				blocks.ForEach(func(blockKey, block gjson.Result) bool {
					kind := contextKind(item)
					switch block.Get("type").String() {
					case "tool_use":
						kind = "tool_call"
					case "tool_result":
						kind = "tool_result"
					case "thinking", "redacted_thinking":
						kind = "reasoning"
					}
					if block.Get("functionCall").Exists() {
						kind = "tool_call"
					}
					if block.Get("functionResponse").Exists() {
						kind = "tool_result"
					}
					add(fmt.Sprintf("%s.%s.%d", path, blockField, blockKey.Int()), kind, block, item)
					return true
				})
			} else {
				if content := item.Get("content"); content.Exists() {
					add(path+".content", contextKind(item), content, item)
				} else if !item.Get("tool_calls").IsArray() {
					add(path, contextKind(item), item)
				}
			}
			if calls := item.Get("tool_calls"); calls.IsArray() {
				add(path+".tool_calls", "tool_call", calls, item)
			}
			if reasoning := item.Get("reasoning_content"); reasoning.Type == gjson.String {
				add(path+".reasoning_content", "reasoning", reasoning, item)
			}
			return true
		})
	}
	if snapshot.Completeness == "captured" {
		snapshot.OriginalMessages = snapshot.MessageCount
	}
	if snapshot.Completeness == "unavailable" {
		snapshot.Parts = []ContextPart{}
		snapshot.Items = nil
		snapshot.EstimatedTokens = 0
	}
	return snapshot
}

func ContextHead(snapshot *ContextSnapshot) *ContextSnapshot {
	if snapshot == nil {
		return nil
	}
	head := *snapshot
	head.Items = nil
	head.InputChange = nil
	return &head
}

func CompareContexts(previousID string, previous, current *ContextSnapshot) *ContextChange {
	change := &ContextChange{PreviousRequestID: previousID, Notice: "记录不完整，无法可靠比较全部上下文"}
	if previous == nil || current == nil || previous.Completeness != "captured" || current.Completeness != "captured" || previous.ItemsLimited || current.ItemsLimited || previous.PreviousResponseID != "" || current.PreviousResponseID != "" {
		return change
	}
	change.Comparable = true
	change.EstimatedDelta = current.EstimatedTokens - previous.EstimatedTokens
	counts := make(map[string]int)
	for _, item := range previous.Items {
		counts[item.Kind+":"+item.Hash]++
	}
	for _, item := range current.Items {
		key := item.Kind + ":" + item.Hash
		if counts[key] > 0 {
			counts[key]--
		} else {
			change.Added++
		}
	}
	for _, count := range counts {
		change.Removed += count
	}
	if change.Added == 0 && change.Removed == 0 {
		for index, item := range current.Items {
			if item.Hash != previous.Items[index].Hash || item.Kind != previous.Items[index].Kind {
				change.OrderChanged = true
				break
			}
		}
	}
	change.Notice = "按记录顺序比较内容块；移除或替换不代表已确认发生压缩"
	return change
}

func inputContext(entries []TrafficLog) *ContextSnapshot {
	var input *TrafficLog
	for index := range entries {
		entry := &entries[index]
		if entry.Phase == PhaseClientInput || (entry.Phase == PhaseClientRequest && input == nil) {
			input = entry
		}
	}
	if input == nil {
		return nil
	}
	return AnalyzeContext(*input)
}

func (s *Store) contextChange(ctx context.Context, summary SystemLogSummary, current *ContextSnapshot) (*ContextChange, error) {
	if !s.requestIndex || current == nil || summary.ConversationID == "" {
		return nil, nil
	}
	var previousID string
	err := s.readDB.QueryRowContext(ctx, `SELECT request_id FROM log_request_index
		WHERE group_key = ? AND (started_at < ? OR (started_at = ? AND request_id < ?))
		ORDER BY started_at DESC, request_id DESC LIMIT 1`,
		logGroupKey(summary), summary.StartedAt, summary.StartedAt, summary.RequestID).Scan(&previousID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询前序上下文失败: %w", err)
	}
	// 仅获取前序输入，不下载它的上游尝试和响应。
	query := s.trafficSelect() + ` WHERE t.request_id = ? AND t.phase IN ('client_request', 'client_input') ORDER BY t.id`
	rows, err := s.readDB.QueryContext(ctx, query, previousID)
	if err != nil {
		return nil, fmt.Errorf("查询前序输入失败: %w", err)
	}
	defer rows.Close()
	entries := make([]TrafficLog, 0, 2)
	for rows.Next() {
		var entry TrafficLog
		var stream, truncated int
		if err := rows.Scan(&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.RequestID, &entry.AttemptID,
			&entry.Phase, &entry.APIType, &entry.Method, &entry.URL, &entry.StatusCode,
			&stream, &entry.HeadersJSON, &entry.Body, &truncated, &entry.OriginalBytes); err != nil {
			return nil, err
		}
		entry.Truncated = truncated != 0
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return CompareContexts(previousID, inputContext(entries), current), nil
}
