package hooks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/sensitive"
	"github.com/insistanan/api-proxy/internal/utils"
)

// Replacer 可并发复用，避免每个 SSE 字段都重建替换表。
var streamIdentityFieldNormalizer = strings.NewReplacer("_", "", "-", "")

type streamPathStep struct {
	key   string
	index int
	array bool
}

type streamRestoreTemplate struct {
	value     interface{}
	path      []streamPathStep
	eventName string
	bytes     int
}

// RestoreAttachedSSEEvent 对已完成协议转换、即将写给客户端的 SSE 事件执行
// 请求级精确还原。待审核工具及必要后续事件按原顺序暂存，审核完成后才交付。
// 不同 JSON 字段路径使用独立增量状态，避免正文与并行工具参数串接。
func RestoreAttachedSSEEvent(c requestContextGetter, event string) (string, error) {
	boundary := streamSafetyBoundaryForEvent(event)
	if err := finishAttachedStreamSafetyEvent(c, boundary); err != nil {
		return "", err
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || event == "" {
		return event, wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	if len(attached.toolArgs) == 0 && len(attached.toolDelivery) == 0 {
		attached.streamMu.Unlock()
		return restoreAttachedSSEEvent(c, event, boundary)
	}
	events, err := attached.prepareToolDelivery(event)
	attached.streamMu.Unlock()
	if err != nil {
		return "", wrapContentSafetyHookError(err)
	}
	if len(events) == 0 {
		return "", nil
	}
	if len(events) == 1 {
		return restoreAttachedSSEEvent(c, events[0], boundary)
	}
	var output strings.Builder
	for _, pending := range events {
		restored, err := restoreAttachedSSEEvent(c, pending, streamSafetyBoundaryForEvent(pending))
		if err != nil {
			return "", err
		}
		output.WriteString(restored)
	}
	return output.String(), nil
}

// prepareToolDelivery 由 streamMu 保护。保持事件顺序，避免工具结束事件先于其参数交付。
func (a *attachedHookPipeline) prepareToolDelivery(event string) ([]string, error) {
	pending := false
	for _, state := range a.toolArgs {
		needsReview := a.streamSettings.DangerousCmd.Enabled && sensitive.IsExecutionTool(state.name) ||
			a.streamSettings.Credential.Enabled && a.streamMaskMode == config.ContentSafetyModeBlock
		if needsReview && !state.finished {
			pending = true
			break
		}
	}
	if !pending && len(a.toolDelivery) == 0 {
		return []string{event}, nil
	}
	if len(event) > maxStreamToolArgumentBytes-a.toolDeliveryBytes || len(a.toolDelivery) >= 8192 {
		return nil, fmt.Errorf("待审核工具的流式交付缓冲超过容量上限")
	}
	a.toolDelivery = append(a.toolDelivery, event)
	a.toolDeliveryBytes += len(event)
	if pending {
		return nil, nil
	}
	events := a.toolDelivery
	a.toolDelivery = nil
	a.toolDeliveryBytes = 0
	return events, nil
}

func restoreAttachedSSEEvent(c requestContextGetter, event string, boundary streamSafetyBoundary) (string, error) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || attached.vault == nil || !attached.vault.CanRestore() || event == "" {
		return event, wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()

	attached.restoreEventBytes = len(event)

	prefix := ""
	terminal := boundary.terminal
	branches := boundary.branches
	if terminal || len(branches) > 0 {
		if terminal {
			branches = nil
		}
		drained, drainErr := drainStreamRestorationLocked(attached, branches)
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
		valueChanged := restoreStreamValue(attached, value, value, fmt.Sprintf("line[%d]", currentLine), nil, nil, eventName, false)
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
	if attached.restoreErr != nil {
		return "", wrapContentSafetyHookError(attached.restoreErr)
	}
	if err := attached.vault.Err(); err != nil {
		return "", wrapContentSafetyHookError(err)
	}
	return prefix + rewritten, nil
}

func restoreStreamValue(attached *attachedHookPipeline, root, value interface{}, key string, path []streamPathStep, identities []string, eventName string, complete bool) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]interface{}:
		fields := sortedMapKeys(typed)
		identities = appendStreamIdentities(identities, path, typed, fields)
		for _, field := range fields {
			if isStreamStructuralField(field) {
				continue
			}
			childPath := appendPath(path, streamPathStep{key: field})
			// 工具完整快照独立还原；末尾占位符不能留到客户端已开始执行之后才释放。
			childComplete := complete || field == "input" && (typed["type"] == "tool_use" || typed["type"] == "custom_tool_call" ||
				equalString(rootEventType(root), "response.custom_tool_call_input.done")) ||
				field == "arguments" && (typed["type"] == "function_call" || equalString(rootEventType(root), "response.function_call_arguments.done")) ||
				field == "args" && typed["name"] != nil && streamPathHasFunction(path)
			if text, ok := typed[field].(string); ok {
				stateKey := streamStateKey(key, eventName, childPath, identities)
				jsonEncoded := field == "partial_json" || field == "arguments" && (typed["name"] != nil || typed["type"] == "function_call" ||
					equalString(rootEventType(root), "response.function_call_arguments.done") || streamPathHasFunction(path)) ||
					field == "delta" && equalString(rootEventType(root), "response.function_call_arguments.delta")
				var restorer *sensitive.StreamRestorer
				if childComplete {
					restorer = sensitive.NewStreamRestorer(attached.vault)
					if jsonEncoded {
						restorer = sensitive.NewJSONStreamRestorer(attached.vault)
					}
				} else {
					restorer = attached.restorer(stateKey, jsonEncoded)
				}
				restored := restorer.Feed(text)
				if childComplete {
					restored += restorer.Flush()
				}
				if restored != text {
					typed[field] = restored
					changed = true
				}
				if restorer.Pending() {
					attached.setRestorePending(stateKey, streamRestoreTemplate{value: root, path: childPath, eventName: eventName, bytes: attached.restoreEventBytes})
				} else {
					attached.deleteRestorePending(stateKey)
				}
				continue
			}
			if restoreStreamValue(attached, root, typed[field], key, childPath, identities, eventName, childComplete) {
				changed = true
			}
		}
	case []interface{}:
		for index := range typed {
			childPath := appendPath(path, streamPathStep{index: index, array: true})
			if text, ok := typed[index].(string); ok {
				stateKey := streamStateKey(key, eventName, childPath, identities)
				var restorer *sensitive.StreamRestorer
				if complete {
					restorer = sensitive.NewStreamRestorer(attached.vault)
				} else {
					restorer = attached.restorer(stateKey)
				}
				restored := restorer.Feed(text)
				if complete {
					restored += restorer.Flush()
				}
				if restored != text {
					typed[index] = restored
					changed = true
				}
				if restorer.Pending() {
					attached.setRestorePending(stateKey, streamRestoreTemplate{value: root, path: childPath, eventName: eventName, bytes: attached.restoreEventBytes})
				} else {
					attached.deleteRestorePending(stateKey)
				}
				continue
			}
			if restoreStreamValue(attached, root, typed[index], key, childPath, identities, eventName, complete) {
				changed = true
			}
		}
	}
	return changed
}

func rootEventType(root interface{}) interface{} {
	if data, ok := root.(map[string]interface{}); ok {
		return data["type"]
	}
	return nil
}

func streamPathHasFunction(path []streamPathStep) bool {
	for _, step := range path {
		if step.key == "function" || step.key == "function_call" || step.key == "functionCall" {
			return true
		}
	}
	return false
}

func (a *attachedHookPipeline) restorer(key string, jsonEncoded ...bool) *sensitive.StreamRestorer {
	if a.restorers == nil {
		a.restorers = make(map[string]*sensitive.StreamRestorer)
	}
	if a.restorePending == nil {
		a.restorePending = make(map[string]streamRestoreTemplate)
	}
	if current := a.restorers[key]; current != nil {
		return current
	}
	if len(a.restorers) >= 2048 {
		a.restoreErr = fmt.Errorf("流式还原字段超过 2048 个上限")
		return nil
	}
	current := sensitive.NewStreamRestorer(a.vault)
	if len(jsonEncoded) > 0 && jsonEncoded[0] {
		current = sensitive.NewJSONStreamRestorer(a.vault)
	}
	a.restorers[key] = current
	return current
}

func (a *attachedHookPipeline) setRestorePending(key string, template streamRestoreTemplate) {
	previous := a.restorePending[key]
	if a.restorePendingBytes-previous.bytes+template.bytes > 4<<20 {
		a.restoreErr = fmt.Errorf("流式还原暂存模板超过 4 MiB 上限")
		return
	}
	a.restorePendingBytes += template.bytes - previous.bytes
	a.restorePending[key] = template
}

func (a *attachedHookPipeline) deleteRestorePending(key string) {
	a.restorePendingBytes -= a.restorePending[key].bytes
	delete(a.restorePending, key)
}

// DrainAttachedStreamRestoration 将流结束时尚未组成完整占位符的前缀原样释放。
func DrainAttachedStreamRestoration(c requestContextGetter) ([]string, error) {
	if err := FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return nil, wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	pending := attached.toolDelivery
	attached.toolDelivery = nil
	attached.toolDeliveryBytes = 0
	attached.streamMu.Unlock()
	result := make([]string, 0, len(pending)+1)
	for _, event := range pending {
		restored, err := restoreAttachedSSEEvent(c, event, streamSafetyBoundaryForEvent(event))
		if err != nil {
			return nil, err
		}
		result = append(result, restored)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	drained, err := drainStreamRestorationLocked(attached, nil)
	if err != nil {
		return nil, err
	}
	return append(result, drained...), nil
}

func drainStreamRestorationLocked(attached *attachedHookPipeline, branches []string) ([]string, error) {
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
		if len(branches) > 0 && !restoreTemplateMatchesBranch(template, branches) {
			continue
		}
		restorer := attached.restorers[key]
		if restorer == nil {
			return nil, fmt.Errorf("流式还原状态 %q 缺少还原器", key)
		}
		pending := restorer.Flush()
		if err := attached.vault.Err(); err != nil {
			return nil, wrapContentSafetyHookError(err)
		}
		if pending == "" {
			attached.deleteRestorePending(key)
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
		attached.deleteRestorePending(key)
	}
	return result, nil
}

func restoreTemplateMatchesBranch(template streamRestoreTemplate, branches []string) bool {
	if len(template.path) < 2 || !template.path[1].array {
		return false
	}
	root, ok := template.value.(map[string]interface{})
	if !ok {
		return false
	}
	field := template.path[0].key
	items, ok := root[field].([]interface{})
	index := template.path[1].index
	if !ok || index < 0 || index >= len(items) {
		return false
	}
	item, ok := items[index].(map[string]interface{})
	if !ok {
		return false
	}
	key := streamBranchKey(field, item["index"], index)
	for _, branch := range branches {
		if branch == key {
			return true
		}
	}
	return false
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
	logicalPath := streamPathKey(path)
	logicalIdentities := append([]string(nil), identities...)
	// 数组位置可能在下一块从 1 变为 0，带逻辑 index 的对象必须沿用同一状态。
	// 先替换内层，再替换外层，保持嵌套工具调用的身份一致。
	for i := len(path) - 1; i >= 0; i-- {
		if !path[i].array {
			continue
		}
		prefix := streamPathKey(path[:i+1])
		for _, identity := range identities {
			fieldPath, value, ok := strings.Cut(identity, "=")
			field := strings.TrimPrefix(fieldPath, prefix+".")
			if !ok || field == fieldPath || !isStreamIdentityField(field) {
				continue
			}
			replacement := streamPathKey(path[:i]) + "[" + field + "=" + value + "]"
			logicalPath = strings.ReplaceAll(logicalPath, prefix, replacement)
			for j := range logicalIdentities {
				logicalIdentities[j] = strings.ReplaceAll(logicalIdentities[j], prefix, replacement)
			}
			break
		}
	}
	return lineKey + "|" + eventName + "|" + logicalPath + "|" + strings.Join(logicalIdentities, "|")
}

// appendStreamIdentities 将协议事件里的稳定逻辑索引加入状态键。流式协议常把
// 并行工具调用放在数组位置 0，但通过 index/output_index 等字段区分逻辑流。
func appendStreamIdentities(identities []string, path []streamPathStep, value map[string]interface{}, fields []string) []string {
	result := append([]string(nil), identities...)
	for _, key := range fields {
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
	normalized := streamIdentityFieldNormalizer.Replace(strings.ToLower(key))
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

type streamSafetyBoundary struct {
	terminal     bool
	branches     []string
	stoppedTools []string
}

// 每个 SSE 事件只解析一次结束边界，避免扫描与还原重复解析整段 JSON。
func streamSafetyBoundaryForEvent(event string) streamSafetyBoundary {
	var boundary streamSafetyBoundary
	for _, line := range strings.Split(event, "\n") {
		payload, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}
		if payload == utils.SSEDoneMarker {
			boundary.terminal = true
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}
		switch data["type"] {
		case "message_stop", "response.completed", "response.incomplete":
			boundary.terminal = true
		case "content_block_stop":
			boundary.stoppedTools = append(boundary.stoppedTools, "messages.content_block."+strings.TrimSpace(fmt.Sprint(data["index"])))
		case "response.output_item.done":
			item, _ := data["item"].(map[string]interface{})
			if item["type"] == "function_call" || item["type"] == "custom_tool_call" {
				for _, field := range []string{"output_index", "item_id", "call_id"} {
					if value, exists := data[field]; exists {
						boundary.stoppedTools = append(boundary.stoppedTools, "responses.tool."+strings.TrimSpace(fmt.Sprint(value)))
						break
					}
				}
			}
		}
		// 只结束声明 finish 的候选，其他候选可能仍在接收半段参数。
		for _, field := range []string{"choices", "candidates"} {
			items, _ := data[field].([]any)
			finishField := "finish_reason"
			if field == "candidates" {
				finishField = "finishReason"
			}
			for index, value := range items {
				item, _ := value.(map[string]any)
				if reason, ok := item[finishField].(string); ok && reason != "" {
					boundary.branches = append(boundary.branches, streamBranchKey(field, item["index"], index))
				}
			}
		}
	}
	return boundary
}

func streamBranchKey(field string, index any, fallback int) string {
	part := fmt.Sprint(fallback)
	if index != nil {
		if explicit := strings.TrimSpace(fmt.Sprint(index)); explicit != "" {
			part = explicit
		}
	}
	if field == "choices" {
		return "chat.choice." + part + "."
	}
	if field == "candidates" {
		return "gemini.candidate." + part + "."
	}
	return ""
}
