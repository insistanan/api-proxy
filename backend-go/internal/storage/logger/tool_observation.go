package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

const maxToolObservations = 128

type ToolObservation struct {
	CallID    string `json:"callId"`
	Role      string `json:"role"`
	Name      string `json:"name,omitempty"`
	Source    string `json:"source"`
	Path      string `json:"path"`
	RequestID string `json:"requestId"`
	AttemptID string `json:"attemptId,omitempty"`
	IsError   bool   `json:"isError,omitempty"`
}

type ToolLinks struct {
	Observations []ToolObservation `json:"observations"`
	Limited      bool              `json:"limited"`
	Notice       string            `json:"notice"`
}

// 工具关联只接受明确的调用 ID，不用工具名、时间间隔或相同参数猜测执行关系。
func extractToolObservations(entry TrafficLog) []ToolObservation {
	source := ""
	switch entry.Phase {
	case PhaseClientRequest, PhaseClientInput:
		source = "input_history"
	case PhaseStreamSynth, PhaseUpstreamResponse:
		source = "model_output"
	default:
		return nil
	}
	if entry.Truncated || len(entry.Body) > MaxBodyBytes || !gjson.Valid(entry.Body) {
		return nil
	}
	root := gjson.Parse(entry.Body)
	observations := make([]ToolObservation, 0)
	add := func(value gjson.Result, role, path string) {
		id := value.Get("call_id").Str
		if id == "" {
			id = value.Get("tool_use_id").Str
		}
		if id == "" {
			id = value.Get("tool_call_id").Str
		}
		// Gemini functionResponse 的 id 明确对应 functionCall.id；其他结果不借用对象 id。
		// Responses 的对象 id 与 call_id 不是同一个标识，不能混用。
		usesObjectID := value.Get("type").Str == "tool_use" || strings.Contains(path, ".tool_calls.") || strings.HasSuffix(path, ".functionCall") || strings.HasSuffix(path, ".functionResponse")
		if id == "" && usesObjectID {
			id = value.Get("id").Str
		}
		if id == "" || len(id) > 512 || len(observations) >= maxToolObservations {
			return
		}
		name := value.Get("name").String()
		if name == "" {
			name = value.Get("function.name").String()
		}
		observations = append(observations, ToolObservation{
			CallID: strings.Clone(id), Role: role, Name: strings.Clone(LogPreview(name, 100)), Source: source, Path: strings.TrimPrefix(path, "."),
			RequestID: entry.RequestID, AttemptID: entry.AttemptID, IsError: value.Get("is_error").Bool(),
		})
	}
	message := func(value gjson.Result, path string) {
		switch value.Get("type").String() {
		case "function_call", "custom_tool_call", "local_shell_call", "tool_use":
			add(value, "call", path)
		case "function_call_output", "custom_tool_call_output", "tool_result":
			add(value, "result", path)
		}
		if value.Get("role").String() == "tool" {
			add(value, "result", path)
		}
		value.Get("tool_calls").ForEach(func(key, call gjson.Result) bool {
			add(call, "call", fmt.Sprintf("%s.tool_calls.%d", path, key.Int()))
			return len(observations) < maxToolObservations
		})
		for _, field := range []string{"content", "parts"} {
			value.Get(field).ForEach(func(key, block gjson.Result) bool {
				blockPath := fmt.Sprintf("%s.%s.%d", path, field, key.Int())
				switch block.Get("type").String() {
				case "tool_use":
					add(block, "call", blockPath)
				case "tool_result":
					add(block, "result", blockPath)
				}
				if call := block.Get("functionCall"); call.Exists() {
					add(call, "call", blockPath+".functionCall")
				}
				if result := block.Get("functionResponse"); result.Exists() {
					add(result, "result", blockPath+".functionResponse")
				}
				return len(observations) < maxToolObservations
			})
		}
	}
	for _, field := range []string{"messages", "input", "contents", "output"} {
		root.Get(field).ForEach(func(key, value gjson.Result) bool {
			message(value, fmt.Sprintf("%s.%d", field, key.Int()))
			return len(observations) < maxToolObservations
		})
	}
	root.Get("choices").ForEach(func(key, choice gjson.Result) bool {
		message(choice.Get("message"), fmt.Sprintf("choices.%d.message", key.Int()))
		return len(observations) < maxToolObservations
	})
	root.Get("candidates").ForEach(func(key, candidate gjson.Result) bool {
		message(candidate.Get("content"), fmt.Sprintf("candidates.%d.content", key.Int()))
		return len(observations) < maxToolObservations
	})
	message(root, "")
	return observations
}

func (s *Store) toolLinks(ctx context.Context, summary SystemLogSummary, entries []TrafficLog) (*ToolLinks, error) {
	if !s.toolIndex {
		return nil, nil
	}
	ids := make(map[string]bool)
	limited := false
	for _, entry := range entries {
		observations := extractToolObservations(entry)
		if len(observations) >= maxToolObservations {
			limited = true
		}
		for _, observation := range observations {
			if len(ids) < maxToolObservations {
				ids[observation.CallID] = true
			} else {
				limited = true
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{logGroupKey(summary)}
	marks := make([]string, 0, len(ids))
	for id := range ids {
		marks = append(marks, "?")
		args = append(args, id)
	}
	query := `SELECT o.payload_json FROM log_tool_observations o
		JOIN log_request_index i ON i.request_id = o.request_id
		WHERE i.group_key = ? AND o.call_id IN (` + strings.Join(marks, ",") + `)
		ORDER BY i.started_at, o.traffic_id, o.ordinal LIMIT 513`
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询工具关联失败: %w", err)
	}
	defer rows.Close()
	result := &ToolLinks{Observations: []ToolObservation{}, Limited: limited,
		Notice: "按明确调用 ID 关联本版本采集且尚未过期的记录；历史引用不代表再次执行，未观测到结果不代表执行失败；不推算执行耗时"}
	seen := make(map[string]bool)
	count := 0
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		count++
		if count > 512 {
			result.Limited = true
			break
		}
		var observation ToolObservation
		if err := json.Unmarshal([]byte(body), &observation); err != nil {
			return nil, fmt.Errorf("解析工具关联失败: %w", err)
		}
		key := observation.CallID + "\x00" + observation.Role + "\x00" + observation.Source + "\x00" + observation.RequestID + "\x00" + observation.AttemptID
		if !seen[key] {
			result.Observations = append(result.Observations, observation)
			seen[key] = true
		}
	}
	return result, rows.Err()
}
