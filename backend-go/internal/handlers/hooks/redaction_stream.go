package hooks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

type streamPathStep struct {
	key   string
	index int
	array bool
}

type streamRestoreTemplate struct {
	value     interface{}
	path      []streamPathStep
	eventName string
}

// RestoreAttachedSSEEvent 对已完成协议转换、即将写给客户端的 SSE 事件执行
// 请求级精确还原。不同 JSON 字段路径使用独立增量状态，避免正文与并行工具参数串接。
func RestoreAttachedSSEEvent(c requestContextGetter, event string) (string, error) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || attached.vault == nil || attached.vault.Empty() && sensitive.PersistentMappingStore() == nil || event == "" {
		return event, wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()

	// 持久映射库预载：事件文本中完整出现的占位符形态批量登记进当前 Vault，
	// 让历史会话的旧占位符（跨重启）在本事件还原。读失败/miss 均不影响
	// 既有还原契约（miss 原样下发 + 审计由 Vault 内部处理）。
	if sensitive.PersistentMappingStore() != nil {
		attached.vault.PreloadPersistent(event)
	}

	prefix := ""
	if isStreamTerminalEvent(event) {
		drained, drainErr := drainStreamRestorationLocked(attached)
		if drainErr != nil {
			return event, wrapContentSafetyHookError(drainErr)
		}
		prefix = strings.Join(drained, "")
	}
	eventName := sseEventName(event)
	lineIndex := 0
	rewritten, changed := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		currentLine := lineIndex
		lineIndex++
		if payload == "" || payload == utils.SSEDoneMarker {
			return payload, false
		}
		var value interface{}
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return payload, false
		}
		valueChanged := restoreStreamValue(attached, value, value, fmt.Sprintf("line[%d]", currentLine), nil, nil, eventName)
		if !valueChanged {
			return payload, false
		}
		body, err := utils.MarshalJSONNoEscape(value)
		if err != nil {
			return payload, false
		}
		return string(body), true
	})
	if !changed {
		rewritten = event
	}
	return prefix + rewritten, nil
}

func restoreStreamValue(attached *attachedHookPipeline, root, value interface{}, key string, path []streamPathStep, identities []string, eventName string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]interface{}:
		identities = appendStreamIdentities(identities, path, typed)
		for _, field := range sortedMapKeys(typed) {
			childPath := appendPath(path, streamPathStep{key: field})
			if text, ok := typed[field].(string); ok {
				stateKey := streamStateKey(key, eventName, childPath, identities)
				restorer := attached.restorer(stateKey)
				restored := restorer.Feed(text)
				if restored != text {
					typed[field] = restored
					changed = true
				}
				if restorer.Pending() {
					attached.restorePending[stateKey] = streamRestoreTemplate{value: root, path: childPath, eventName: eventName}
				} else {
					delete(attached.restorePending, stateKey)
				}
				continue
			}
			if restoreStreamValue(attached, root, typed[field], key, childPath, identities, eventName) {
				changed = true
			}
		}
	case []interface{}:
		for index := range typed {
			childPath := appendPath(path, streamPathStep{index: index, array: true})
			if text, ok := typed[index].(string); ok {
				stateKey := streamStateKey(key, eventName, childPath, identities)
				restorer := attached.restorer(stateKey)
				restored := restorer.Feed(text)
				if restored != text {
					typed[index] = restored
					changed = true
				}
				if restorer.Pending() {
					attached.restorePending[stateKey] = streamRestoreTemplate{value: root, path: childPath, eventName: eventName}
				} else {
					delete(attached.restorePending, stateKey)
				}
				continue
			}
			if restoreStreamValue(attached, root, typed[index], key, childPath, identities, eventName) {
				changed = true
			}
		}
	}
	return changed
}

func (a *attachedHookPipeline) restorer(key string) *sensitive.StreamRestorer {
	if a.restorers == nil {
		a.restorers = make(map[string]*sensitive.StreamRestorer)
	}
	if a.restorePending == nil {
		a.restorePending = make(map[string]streamRestoreTemplate)
	}
	if current := a.restorers[key]; current != nil {
		return current
	}
	current := sensitive.NewStreamRestorer(a.vault)
	a.restorers[key] = current
	return current
}

// DrainAttachedStreamRestoration 将流结束时尚未组成完整占位符的前缀原样释放。
func DrainAttachedStreamRestoration(c requestContextGetter) ([]string, error) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return nil, wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	return drainStreamRestorationLocked(attached)
}

func drainStreamRestorationLocked(attached *attachedHookPipeline) ([]string, error) {
	if attached == nil || len(attached.restorePending) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(attached.restorePending))
	for key := range attached.restorePending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		template := attached.restorePending[key]
		restorer := attached.restorers[key]
		if restorer == nil {
			return nil, fmt.Errorf("流式还原状态 %q 缺少还原器", key)
		}
		pending := restorer.Flush()
		if pending == "" {
			continue
		}
		cloneBody, err := utils.MarshalJSONNoEscape(template.value)
		if err != nil {
			return nil, fmt.Errorf("序列化流式还原模板失败: %w", err)
		}
		var clone interface{}
		decoder := json.NewDecoder(strings.NewReader(string(cloneBody)))
		decoder.UseNumber()
		if err := decoder.Decode(&clone); err != nil {
			return nil, fmt.Errorf("复制流式还原模板失败: %w", err)
		}
		blankStreamStrings(clone)
		if !setStreamPath(clone, template.path, pending) {
			return nil, fmt.Errorf("恢复流式还原路径 %q 失败", streamPathKey(template.path))
		}
		payload, err := utils.MarshalJSONNoEscape(clone)
		if err != nil {
			return nil, fmt.Errorf("序列化流式还原残片失败: %w", err)
		}
		if template.eventName != "" {
			result = append(result, "event: "+template.eventName+"\n")
		}
		result = append(result, "data: "+string(payload)+"\n\n")
		delete(attached.restorePending, key)
	}
	return result, nil
}

func appendPath(path []streamPathStep, step streamPathStep) []streamPathStep {
	result := make([]streamPathStep, len(path)+1)
	copy(result, path)
	result[len(path)] = step
	return result
}

func streamPathKey(path []streamPathStep) string {
	var builder strings.Builder
	for _, step := range path {
		if step.array {
			fmt.Fprintf(&builder, "[%d]", step.index)
		} else {
			builder.WriteByte('.')
			builder.WriteString(step.key)
		}
	}
	return builder.String()
}

func streamStateKey(lineKey, eventName string, path []streamPathStep, identities []string) string {
	return lineKey + "|" + eventName + "|" + streamPathKey(path) + "|" + strings.Join(identities, "|")
}

// appendStreamIdentities 将协议事件里的稳定逻辑索引加入状态键。流式协议常把
// 并行工具调用放在数组位置 0，但通过 index/output_index 等字段区分逻辑流。
func appendStreamIdentities(identities []string, path []streamPathStep, value map[string]interface{}) []string {
	result := append([]string(nil), identities...)
	for _, key := range sortedMapKeys(value) {
		if !isStreamIdentityField(key) {
			continue
		}
		identity, ok := streamIdentityNumber(value[key])
		if !ok {
			continue
		}
		fieldPath := appendPath(path, streamPathStep{key: key})
		result = append(result, streamPathKey(fieldPath)+"="+identity)
	}
	return result
}

func isStreamIdentityField(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	switch normalized {
	case "index", "outputindex", "contentindex", "candidateindex", "toolcallindex", "partindex":
		return true
	default:
		return false
	}
}

func streamIdentityNumber(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case json.Number:
		return typed.String(), true
	case float64:
		return fmt.Sprintf("%g", typed), true
	case int:
		return fmt.Sprintf("%d", typed), true
	case int64:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}

func setStreamPath(root interface{}, path []streamPathStep, value string) bool {
	current := root
	for index, step := range path {
		last := index == len(path)-1
		if step.array {
			array, ok := current.([]interface{})
			if !ok || step.index < 0 || step.index >= len(array) {
				return false
			}
			if last {
				array[step.index] = value
				return true
			}
			current = array[step.index]
			continue
		}
		object, ok := current.(map[string]interface{})
		if !ok {
			return false
		}
		if last {
			object[step.key] = value
			return true
		}
		current = object[step.key]
	}
	return false
}

func blankStreamStrings(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			if _, ok := item.(string); ok {
				if !isStreamStructuralField(key) {
					typed[key] = ""
				}
				continue
			}
			blankStreamStrings(item)
		}
	case []interface{}:
		for index, item := range typed {
			if _, ok := item.(string); ok {
				typed[index] = ""
				continue
			}
			blankStreamStrings(item)
		}
	}
}

func isStreamStructuralField(key string) bool {
	switch strings.ToLower(key) {
	case "type", "object", "id", "item_id", "call_id", "model", "role", "name", "status", "finish_reason", "stop_reason":
		return true
	default:
		return false
	}
}

func sseEventName(event string) string {
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "event:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	return ""
}

func isStreamTerminalEvent(event string) bool {
	if strings.Contains(event, utils.SSEDoneMarker) {
		return true
	}
	return strings.Contains(event, `"type":"message_stop"`) ||
		strings.Contains(event, `"type": "message_stop"`) ||
		strings.Contains(event, `"type":"response.completed"`) ||
		strings.Contains(event, `"type": "response.completed"`) ||
		strings.Contains(event, `"finish_reason":"`) ||
		strings.Contains(event, `"finish_reason": "`) ||
		strings.Contains(event, `"finishReason":"`) ||
		strings.Contains(event, `"finishReason": "`)
}
