package utils

import (
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
)

// MergeResponsesItemsDedup 合并 Responses 会话历史与当前请求输入。
//
// Responses 客户端有两种合法用法：只发送 previous_response_id 后的新后缀，
// 或者每轮重放完整历史。调用方不能把两种输入简单 append，否则完整重放会把
// 同一段上下文再次写入代理 session，并在协议转换时重复发送。
//
// 完整前缀匹配时可以直接去重。部分前缀优先要求匹配项带有稳定的服务端 id
// 或 call_id；无稳定身份时也只有在至少完成 user+assistant（或工具调用）
// 的一轮边界、且后面确实还有新 item 时才去重。否则保留 history + current，
// 避免把“用户恰好重复发送相同内容”误判为历史重放。
// 比较时忽略 Responses 服务端生成的 item id 和可变化的 status，但保留
// encrypted_content/signature 等回传完整性字段。
func MergeResponsesItemsDedup(history, current []types.ResponsesItem) []types.ResponsesItem {
	if len(history) == 0 {
		return append([]types.ResponsesItem(nil), current...)
	}
	if len(current) == 0 {
		return append([]types.ResponsesItem(nil), history...)
	}
	if ResponsesItemsContainCompaction(current) {
		// Responses compaction item 是一个新的不透明历史根。它已经代表压缩
		// 前的服务端上下文；再把旧 session 接在它前面，会让后续协议转换
		// 重新发送完整旧 transcript，压缩马上失效并造成上下文/费用膨胀。
		return append([]types.ResponsesItem(nil), current...)
	}

	if prefixEnd, ok := ResponsesItemsPrefixMatch(history, current); ok {
		// 以 session 为基准保留代理内部的 reasoning item，再接上客户端
		// 尚未持久化的后缀。这样客户端省略 reasoning 时不会导致可见历史
		// 被重复追加，也不会因为去重而丢掉代理内部的 reasoning 缓存。
		merged := make([]types.ResponsesItem, 0, len(history)+len(current)-prefixEnd)
		merged = append(merged, history...)
		merged = append(merged, current[prefixEnd:]...)
		return merged
	}
	merged := make([]types.ResponsesItem, 0, len(history)+len(current))
	merged = append(merged, history...)
	merged = append(merged, current...)
	return merged
}

// ResponsesItemsPrefixMatch 返回 current 中可安全视为 history 前缀的长度。
// 第二个返回值为 false 时，调用方不得裁剪或假定存在重叠。
//
// reasoning 是上游/代理的服务端专有输出：Cursor 等客户端可能不会把它
// 重放到下一轮，但它仍然存在于代理 session。匹配时允许 history 或 current
// 中出现 reasoning，并返回 current 的原始前缀长度，从而让透传调用方能按
// 原始 input 下标裁剪，而不是重新编码 item。
func ResponsesItemsPrefixMatch(history, current []types.ResponsesItem) (int, bool) {
	if len(history) == 0 || len(current) == 0 {
		return 0, false
	}

	currentAt := func(index int) (types.ResponsesItem, bool) {
		if index < 0 || index >= len(current) {
			return types.ResponsesItem{}, false
		}
		return current[index], true
	}

	// 常规情况：当前请求从 session 的第一个可见 item 开始。
	if result := responsesItemsPrefixResult(history, current, currentAt); responsesPrefixMatchCanDedup(result, current, currentAt, true) {
		return result.currentEnd, true
	}

	// Cursor 压缩/滚动窗口可能只保留历史尾部，当前 input 不再从 session[0]
	// 开始。寻找“历史可见尾部 == 当前可见头部”的最长安全重叠，避免把窗口内
	// 已经存在的上下文重新追加。候选必须完整匹配到历史末尾；否则客户端可能
	// 已经改写了旧分支，裁剪会把旧分支和新分支错误拼接。
	for historyStart := 1; historyStart < len(history); historyStart++ {
		if isResponsesReasoningItem(history[historyStart]) {
			continue
		}
		result := responsesItemsPrefixResult(history[historyStart:], current, currentAt)
		if result.complete && responsesPrefixMatchCanDedup(result, current, currentAt, false) {
			return result.currentEnd, true
		}
	}
	return 0, false
}

type responsesPrefixResult struct {
	currentEnd        int
	matched           int
	complete          bool
	hasStableIdentity bool
	lastMatched       types.ResponsesItem
	hasLastMatched    bool
}

// responsesItemsPrefixResult 在遇到第一个不匹配项时停止，保留已经匹配的
// current 下标。reasoning 可能只存在于代理内部或客户端重放中，因此不参与
// 可见前缀匹配，但它的原始下标必须计入 currentEnd，供透传路径精确裁剪。
func responsesItemsPrefixResult(
	history []types.ResponsesItem,
	current []types.ResponsesItem,
	currentAt func(index int) (types.ResponsesItem, bool),
) responsesPrefixResult {
	currentIndex := 0
	matched := 0
	hasStableIdentity := false
	var lastMatched types.ResponsesItem
	hasLastMatched := false
	for _, historyItem := range history {
		if isResponsesReasoningItem(historyItem) {
			continue
		}

		for currentIndex < len(current) {
			currentItem, ok := currentAt(currentIndex)
			if !ok {
				return responsesPrefixResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
			}
			if !isResponsesReasoningItem(currentItem) {
				break
			}
			currentIndex++
		}
		if currentIndex >= len(current) {
			return responsesPrefixResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
		}
		currentItem, ok := currentAt(currentIndex)
		if !ok || !responsesItemEqual(historyItem, currentItem) {
			return responsesPrefixResult{
				currentEnd:        currentIndex,
				matched:           matched,
				hasStableIdentity: hasStableIdentity,
				lastMatched:       lastMatched,
				hasLastMatched:    hasLastMatched,
			}
		}
		if responsesItemHasStableIdentity(historyItem, currentItem) {
			// 只要已匹配前缀中有一个不可复用的稳定身份，就足以证明
			// 这些 item 是客户端重放的历史，而不是新的同文输入。
			hasStableIdentity = true
		}
		lastMatched = historyItem
		hasLastMatched = true
		currentIndex++
		matched++
	}
	return responsesPrefixResult{
		currentEnd:        currentIndex,
		matched:           matched,
		complete:          true,
		hasStableIdentity: hasStableIdentity,
		lastMatched:       lastMatched,
		hasLastMatched:    hasLastMatched,
	}
}

func responsesPrefixCanDedup(
	result responsesPrefixResult,
	current []types.ResponsesItem,
	currentAt func(index int) (types.ResponsesItem, bool),
) bool {
	if !hasVisibleResponsesItemAfterWith(current, result.currentEnd, currentAt) {
		return false
	}
	if result.hasStableIdentity {
		return true
	}
	// 无稳定 id 时仅接受完整的一轮边界：至少已经匹配 user+assistant，且
	// 后继是新 user 或工具结果。这样能覆盖 Cursor 的无 id 历史重放，同时
	// 不会把一条恰好重复的单条用户输入静默吞掉。
	if !result.hasLastMatched || result.matched < 2 {
		return false
	}
	nextIndex := result.currentEnd
	for nextIndex < len(current) {
		next, ok := currentAt(nextIndex)
		if !ok {
			return false
		}
		if isResponsesReasoningItem(next) {
			nextIndex++
			continue
		}
		return isResponsesReplayBoundary(result.lastMatched, next)
	}
	return false
}

func responsesPrefixMatchCanDedup(
	result responsesPrefixResult,
	current []types.ResponsesItem,
	currentAt func(index int) (types.ResponsesItem, bool),
	fullHistory bool,
) bool {
	if result.matched == 0 {
		return false
	}
	if fullHistory && result.complete {
		return true
	}
	return responsesPrefixCanDedup(result, current, currentAt)
}

func hasVisibleResponsesItemAfterWith(
	current []types.ResponsesItem,
	start int,
	currentAt func(index int) (types.ResponsesItem, bool),
) bool {
	for index := start; index < len(current); index++ {
		item, ok := currentAt(index)
		if !ok {
			return true
		}
		if !isResponsesReasoningItem(item) {
			return true
		}
	}
	return false
}

func isResponsesReplayBoundary(previous, next types.ResponsesItem) bool {
	nextType := strings.ToLower(strings.TrimSpace(next.Type))
	switch nextType {
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(next.Role), "user") &&
		(strings.EqualFold(strings.TrimSpace(previous.Role), "assistant") ||
			isResponsesToolCallItem(previous))
}

func isResponsesToolCallItem(item types.ResponsesItem) bool {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "function_call", "custom_tool_call", "tool_search_call", "tool_call":
		return true
	default:
		return false
	}
}

func responsesItemHasStableIdentity(left, right types.ResponsesItem) bool {
	if left.ID != "" && left.ID == right.ID {
		return true
	}
	if left.CallID != "" && left.CallID == right.CallID {
		return true
	}
	return left.ToolUse != nil && right.ToolUse != nil &&
		left.ToolUse.ID != "" && left.ToolUse.ID == right.ToolUse.ID
}

// ResponsesItemsPrefixMatchRaw 是 Responses 透传专用的前缀匹配版本。
//
// currentRaw 必须是客户端 input 的原始 item 数组。内部 ResponsesItem 结构只覆盖
// 代理需要转换的字段，直接用它来推导裁剪下标会丢失 encrypted_content 等扩展字段；
// 这里只把 raw item 用于语义比较，真正裁剪仍由调用方按返回的原始下标完成。
func ResponsesItemsPrefixMatchRaw(history []types.ResponsesItem, currentRaw []json.RawMessage) (int, bool) {
	if len(history) == 0 || len(currentRaw) == 0 {
		return 0, false
	}

	if result := matchResponsesRawPrefix(history, currentRaw); responsesRawPrefixMatchCanDedup(result, currentRaw, true) {
		return result.currentEnd, true
	}

	// 与结构化版本保持相同的滚动窗口处理，但返回值必须是客户端原始
	// input 的下标，不能用解析后的 item 下标替代。
	for historyStart := 1; historyStart < len(history); historyStart++ {
		if isResponsesReasoningItem(history[historyStart]) {
			continue
		}
		result := matchResponsesRawPrefix(history[historyStart:], currentRaw)
		if result.complete && responsesRawPrefixMatchCanDedup(result, currentRaw, false) {
			return result.currentEnd, true
		}
	}
	return 0, false
}

type responsesRawPrefixMatchResult struct {
	currentEnd        int
	matched           int
	complete          bool
	hasStableIdentity bool
	lastMatched       types.ResponsesItem
	hasLastMatched    bool
}

func matchResponsesRawPrefix(history []types.ResponsesItem, currentRaw []json.RawMessage) responsesRawPrefixMatchResult {
	currentIndex := 0
	matched := 0
	hasStableIdentity := false
	var lastMatched types.ResponsesItem
	hasLastMatched := false
	for _, historyItem := range history {
		if isResponsesReasoningItem(historyItem) {
			continue
		}

		for currentIndex < len(currentRaw) {
			var rawItem map[string]interface{}
			if err := json.Unmarshal(currentRaw[currentIndex], &rawItem); err != nil {
				return responsesRawPrefixMatchResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
			}
			if isResponsesReasoningMap(rawItem) {
				currentIndex++
				continue
			}
			break
		}
		if currentIndex >= len(currentRaw) {
			return responsesRawPrefixMatchResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
		}
		var rawItem map[string]interface{}
		if err := json.Unmarshal(currentRaw[currentIndex], &rawItem); err != nil {
			return responsesRawPrefixMatchResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
		}
		if !responsesItemRawEqual(historyItem, currentRaw[currentIndex]) {
			return responsesRawPrefixMatchResult{currentEnd: currentIndex, matched: matched, hasStableIdentity: hasStableIdentity, lastMatched: lastMatched, hasLastMatched: hasLastMatched}
		}
		if responsesItemRawHasStableIdentity(historyItem, rawItem) {
			hasStableIdentity = true
		}
		lastMatched = historyItem
		hasLastMatched = true
		currentIndex++
		matched++
	}
	return responsesRawPrefixMatchResult{
		currentEnd:        currentIndex,
		matched:           matched,
		complete:          true,
		hasStableIdentity: hasStableIdentity,
		lastMatched:       lastMatched,
		hasLastMatched:    hasLastMatched,
	}
}

func responsesRawPrefixCanDedup(result responsesRawPrefixMatchResult, currentRaw []json.RawMessage) bool {
	if !hasVisibleRawItemAfter(currentRaw, result.currentEnd) {
		return false
	}
	if result.hasStableIdentity {
		return true
	}
	if !result.hasLastMatched || result.matched < 2 {
		return false
	}
	for index := result.currentEnd; index < len(currentRaw); index++ {
		var rawItem map[string]interface{}
		if err := json.Unmarshal(currentRaw[index], &rawItem); err != nil {
			return false
		}
		if isResponsesReasoningMap(rawItem) {
			continue
		}
		next := types.ResponsesItem{
			Type: stringFromRawMap(rawItem, "type"),
			Role: stringFromRawMap(rawItem, "role"),
		}
		return isResponsesReplayBoundary(result.lastMatched, next)
	}
	return false
}

func responsesRawPrefixMatchCanDedup(result responsesRawPrefixMatchResult, currentRaw []json.RawMessage, fullHistory bool) bool {
	if result.matched == 0 {
		return false
	}
	if fullHistory && result.complete {
		return true
	}
	return responsesRawPrefixCanDedup(result, currentRaw)
}

func responsesItemRawHasStableIdentity(left types.ResponsesItem, right map[string]interface{}) bool {
	rightID, _ := right["id"].(string)
	if left.ID != "" && left.ID == rightID {
		return true
	}
	rightCallID, _ := right["call_id"].(string)
	if left.CallID != "" && left.CallID == rightCallID {
		return true
	}
	rightToolUse, _ := right["tool_use"].(map[string]interface{})
	rightToolUseID, _ := rightToolUse["id"].(string)
	return left.ToolUse != nil && left.ToolUse.ID != "" && left.ToolUse.ID == rightToolUseID
}

func hasVisibleRawItemAfter(items []json.RawMessage, start int) bool {
	for index := start; index < len(items); index++ {
		var item map[string]interface{}
		if err := json.Unmarshal(items[index], &item); err != nil {
			return true
		}
		if !isResponsesReasoningMap(item) {
			return true
		}
	}
	return false
}

// ResponsesRawItemsReplayHistory 判断客户端 input 是否重放了本地会话里已经存在的可见 item。
//
// 前缀匹配（ResponsesItemsPrefixMatchRaw）对“真实增量”和“历史被客户端重写”都返回
// (0, false)，无法区分二者。重叠可以：合法增量只发服务端链上还不存在的新 item，
// 而客户端侧压缩/分支/编辑会把最近若干轮原样重发到新的历史根之后。
//
// 判据故意保守——误判会删掉服务端链而 input 其实只是后缀，整段上下文都会丢：
//   - 命中两个以上历史 item 才算重放。单个命中可能只是用户又发了一条内容相同的
//     短消息（“继续”“ok”），那是正常增量。
//   - 单个命中里只有 assistant 侧产出（assistant 消息 / 工具调用）算重放：这类 item
//     只可能由服务端生成，客户端把它重新发上来本身就说明在重放历史。
//   - 工具结果（function_call_output 等）不算：上游已成功但客户端没收到响应时的
//     重试会原样重发同一条工具结果，此时必须保留链，否则上下文全丢。
func ResponsesRawItemsReplayHistory(history []types.ResponsesItem, currentRaw []json.RawMessage) bool {
	if len(history) == 0 || len(currentRaw) == 0 {
		return false
	}

	historyFingerprints := make(map[string]struct{}, len(history))
	for _, item := range history {
		if isResponsesReasoningItem(item) {
			continue
		}
		historyFingerprints[CanonicalJSON(canonicalResponsesItem(item))] = struct{}{}
	}
	if len(historyFingerprints) == 0 {
		return false
	}

	replayed := 0
	for _, rawItem := range currentRaw {
		var decoded map[string]interface{}
		if err := json.Unmarshal(rawItem, &decoded); err != nil {
			continue
		}
		if isResponsesReasoningMap(decoded) {
			continue
		}
		if _, ok := historyFingerprints[CanonicalJSON(canonicalResponsesRawItem(decoded))]; !ok {
			continue
		}
		replayed++
		if replayed >= 2 || isResponsesAssistantOutputMap(decoded) {
			return true
		}
	}
	return false
}

func isResponsesAssistantOutputMap(item map[string]interface{}) bool {
	if strings.EqualFold(strings.TrimSpace(stringFromRawMap(item, "role")), "assistant") {
		return true
	}
	return isResponsesToolCallItem(types.ResponsesItem{Type: stringFromRawMap(item, "type")})
}

// ResponsesItemsPrefixOverlap 返回可安全从 current 起点裁掉的历史长度。
//
// current 完整包含 history 的可见前缀，或部分前缀带稳定身份且后面有新增 item 时，
// 返回 current 的原始前缀长度。该函数服务于 Responses 原生透传请求裁剪：调用方
// 需要知道精确的 raw input 下标，但不能把 raw item 重新序列化成内部结构，否则可能
// 丢失 encrypted_content、annotations 等上游扩展字段。
func ResponsesItemsPrefixOverlap(history, current []types.ResponsesItem) int {
	if prefixEnd, ok := ResponsesItemsPrefixMatch(history, current); ok {
		return prefixEnd
	}
	return 0
}

func isResponsesReasoningItem(item types.ResponsesItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.Type), "reasoning")
}

// ResponsesItemsContainCompaction 判断当前 input/output 是否包含 Responses
// compaction 控制项。该 item 的 encrypted_content 通常是不透明的压缩边界，
// 不能按普通消息追加到旧历史之后。
func ResponsesItemsContainCompaction(items []types.ResponsesItem) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "compaction") {
			return true
		}
	}
	return false
}

// ResponsesItemsFromCompactionRoot 用于 /responses 服务端自动压缩的 output，
// 丢弃压缩根之前的旧 transcript。不能用于客户端重放的 /responses/compact
// 完整窗口：该窗口的 compaction 前置消息也属于新的有效上下文。
func ResponsesItemsFromCompactionRoot(items []types.ResponsesItem) []types.ResponsesItem {
	for index := len(items) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(items[index].Type), "compaction") {
			return append([]types.ResponsesItem(nil), items[index:]...)
		}
	}
	return append([]types.ResponsesItem(nil), items...)
}

// ResponsesTurnItems 区分客户端完整压缩窗口与本轮服务端自动压缩。
// 客户端带 compaction 时整体保留 input；output 再次压缩时以最新根替换旧窗口。
func ResponsesTurnItems(input, output []types.ResponsesItem) []types.ResponsesItem {
	if ResponsesItemsContainCompaction(output) {
		return ResponsesItemsFromCompactionRoot(output)
	}
	items := make([]types.ResponsesItem, 0, len(input)+len(output))
	items = append(items, input...)
	return append(items, output...)
}

// ResponsesItemsContainCompactionFromInput 处理 ResponsesRequest.Input 在
// json.Unmarshal 后的两种形态：string 或 []interface{}。它只用于判断是否
// 需要切换会话根，不会修改 input，也不会把不透明字段重新编码。
func ResponsesItemsContainCompactionFromInput(input interface{}) bool {
	switch value := input.(type) {
	case []types.ResponsesItem:
		return ResponsesItemsContainCompaction(value)
	case []interface{}:
		for _, rawItem := range value {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := item["type"].(string)
			if strings.EqualFold(strings.TrimSpace(typ), "compaction") {
				return true
			}
		}
	}
	return false
}

// ResponsesRawItemsContainCompaction 是原生 Responses 透传路径使用的版本。
// 原始 item 必须保留原样，这里只读取 type，不重新编码任何字段。
func ResponsesRawItemsContainCompaction(items []json.RawMessage) bool {
	for _, rawItem := range items {
		var item map[string]interface{}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		typ, _ := item["type"].(string)
		if strings.EqualFold(strings.TrimSpace(typ), "compaction") {
			return true
		}
	}
	return false
}

func isResponsesReasoningMap(item map[string]interface{}) bool {
	typ, _ := item["type"].(string)
	return strings.EqualFold(strings.TrimSpace(typ), "reasoning")
}

func responsesItemRawEqual(left types.ResponsesItem, right json.RawMessage) bool {
	var rawItem map[string]interface{}
	if err := json.Unmarshal(right, &rawItem); err != nil {
		return false
	}
	return CanonicalJSON(canonicalResponsesItem(left)) == CanonicalJSON(canonicalResponsesRawItem(rawItem))
}

func canonicalResponsesRawItem(item map[string]interface{}) interface{} {
	typ, _ := item["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))
	role, _ := item["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))

	switch typ {
	case "", "message", "text":
		if role == "" {
			role = "user"
		}
		content := item["content"]
		if content == nil {
			// 某些兼容网关把 message 的正文放在 output；内部解析器
			// 也会把它归一到 Content，比较时必须使用同一语义。
			content = item["output"]
		}
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": canonicalResponsesMessageContent(content),
		}, item)
	case "tool_call":
		if toolUse, ok := item["tool_use"].(map[string]interface{}); ok {
			return withResponsesRawIntegrityFields(map[string]interface{}{
				"type":      "function_call",
				"call_id":   stringFromRawMap(toolUse, "id"),
				"name":      stringFromRawMap(toolUse, "name"),
				"arguments": canonicalResponsesValue(toolUse["input"]),
			}, item)
		}
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":      typ,
			"call_id":   stringFromRawMap(item, "call_id"),
			"name":      stringFromRawMap(item, "name"),
			"arguments": canonicalResponsesRawArguments(item["arguments"]),
		}, item)
	case "function_call", "custom_tool_call", "tool_search_call":
		content := item["content"]
		if content == nil && typ == "custom_tool_call" {
			content = item["input"]
		}
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":      typ,
			"call_id":   stringFromRawMap(item, "call_id"),
			"name":      stringFromRawMap(item, "name"),
			"arguments": canonicalResponsesRawArguments(item["arguments"]),
			"content":   canonicalResponsesValue(content),
			"namespace": stringFromRawMap(item, "namespace"),
			"execution": stringFromRawMap(item, "execution"),
		}, item)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		content := item["content"]
		if content == nil {
			content = item["output"]
		}
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":    typ,
			"call_id": stringFromRawMap(item, "call_id"),
			"content": canonicalResponsesValue(content),
			"tools":   canonicalResponsesValue(item["tools"]),
		}, item)
	case "web_search_call":
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":   typ,
			"action": canonicalResponsesValue(item["action"]),
		}, item)
	case "reasoning":
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":    typ,
			"summary": canonicalResponsesValue(item["summary"]),
			"content": canonicalResponsesValue(item["content"]),
		}, item)
	default:
		return withResponsesRawIntegrityFields(map[string]interface{}{
			"type":      typ,
			"role":      role,
			"content":   canonicalResponsesValue(item["content"]),
			"summary":   canonicalResponsesValue(item["summary"]),
			"tool_use":  canonicalResponsesValue(item["tool_use"]),
			"call_id":   stringFromRawMap(item, "call_id"),
			"name":      stringFromRawMap(item, "name"),
			"arguments": canonicalResponsesRawArguments(item["arguments"]),
			"tools":     canonicalResponsesValue(item["tools"]),
			"namespace": stringFromRawMap(item, "namespace"),
			"execution": stringFromRawMap(item, "execution"),
		}, item)
	}
}

// withResponsesRawIntegrityFields 把不能被内部 ResponsesItem 丢失的完整性字段
// 纳入比较。若 session 旧记录没有这些字段，比较会失败并放弃裁剪，宁可保留
// 客户端 item，也不能误删 encrypted_content 或签名。
func withResponsesRawIntegrityFields(normalized map[string]interface{}, raw map[string]interface{}) map[string]interface{} {
	for _, key := range []string{"encrypted_content", "signature"} {
		if value, exists := raw[key]; exists {
			normalized[key] = canonicalResponsesValue(value)
		}
	}
	return normalized
}

func canonicalResponsesRawArguments(value interface{}) interface{} {
	if text, ok := value.(string); ok {
		return canonicalResponsesArguments(text)
	}
	return canonicalResponsesValue(value)
}

func stringFromRawMap(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return value
}

func responsesItemsEqual(left, right []types.ResponsesItem) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !responsesItemEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func responsesItemEqual(left, right types.ResponsesItem) bool {
	return CanonicalJSON(canonicalResponsesItem(left)) == CanonicalJSON(canonicalResponsesItem(right))
}

// canonicalResponsesItem 把 Responses 的多个等价线格式归一到同一个语义表示。
//
// session 中可能同时出现三种来源：客户端重放的标准 message/output_text、
// 代理早期版本保存的 text 字段、以及不同上游转换器生成的 function_call。
// id/status 是服务端或流式阶段字段，不能参与重放去重；call_id 则是工具结果
// 关联所必需的真实语义，必须保留。
func canonicalResponsesItem(item types.ResponsesItem) interface{} {
	typ := strings.ToLower(strings.TrimSpace(item.Type))
	role := strings.ToLower(strings.TrimSpace(item.Role))

	switch typ {
	case "", "message", "text":
		if role == "" {
			role = "user"
		}
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": canonicalResponsesMessageContent(item.Content),
		}, item)
	case "tool_call":
		if item.ToolUse == nil {
			return withResponsesIntegrityFields(map[string]interface{}{
				"type":      typ,
				"call_id":   item.CallID,
				"name":      item.Name,
				"arguments": canonicalResponsesArguments(item.Arguments),
			}, item)
		}
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":      "function_call",
			"call_id":   item.ToolUse.ID,
			"name":      item.ToolUse.Name,
			"arguments": canonicalResponsesValue(item.ToolUse.Input),
		}, item)
	case "function_call", "custom_tool_call", "tool_search_call":
		value := map[string]interface{}{
			"type":      typ,
			"call_id":   item.CallID,
			"name":      item.Name,
			"arguments": canonicalResponsesArguments(item.Arguments),
			"content":   canonicalResponsesValue(item.Content),
			"namespace": item.Namespace,
			"execution": item.Execution,
		}
		return withResponsesIntegrityFields(value, item)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":    typ,
			"call_id": item.CallID,
			"content": canonicalResponsesValue(item.Content),
			"tools":   canonicalResponsesValue(item.Tools),
		}, item)
	case "web_search_call":
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":   typ,
			"action": canonicalResponsesValue(item.Action),
		}, item)
	case "reasoning":
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":    typ,
			"summary": canonicalResponsesValue(item.Summary),
			"content": canonicalResponsesValue(item.Content),
		}, item)
	default:
		return withResponsesIntegrityFields(map[string]interface{}{
			"type":      typ,
			"role":      role,
			"content":   canonicalResponsesValue(item.Content),
			"summary":   canonicalResponsesValue(item.Summary),
			"tool_use":  canonicalResponsesValue(item.ToolUse),
			"call_id":   item.CallID,
			"name":      item.Name,
			"arguments": canonicalResponsesArguments(item.Arguments),
			"tools":     canonicalResponsesValue(item.Tools),
			"namespace": item.Namespace,
			"execution": item.Execution,
		}, item)
	}
}

func withResponsesIntegrityFields(value map[string]interface{}, item types.ResponsesItem) map[string]interface{} {
	if item.EncryptedContent != nil {
		value["encrypted_content"] = canonicalResponsesValue(item.EncryptedContent)
	}
	if item.Signature != "" {
		value["signature"] = item.Signature
	}
	return value
}

func canonicalResponsesMessageContent(content interface{}) interface{} {
	if text, ok := content.(string); ok {
		return text
	}

	blocks := canonicalResponsesContentBlocks(content)
	if len(blocks) == 0 {
		return canonicalResponsesValue(content)
	}

	allText := true
	var textBuilder strings.Builder
	for _, block := range blocks {
		if blockType, _ := block["type"].(string); blockType != "text" {
			allText = false
			break
		}
		text, _ := block["text"].(string)
		textBuilder.WriteString(text)
	}
	if allText {
		return textBuilder.String()
	}
	return blocks
}

func canonicalResponsesContentBlocks(content interface{}) []map[string]interface{} {
	var raw []interface{}
	switch value := content.(type) {
	case []interface{}:
		raw = value
	case []map[string]interface{}:
		raw = make([]interface{}, len(value))
		for index := range value {
			raw[index] = value[index]
		}
	case []types.ContentBlock:
		raw = make([]interface{}, len(value))
		for index, block := range value {
			raw[index] = map[string]interface{}{
				"type": block.Type,
				"text": block.Text,
			}
		}
	default:
		return nil
	}

	blocks := make([]map[string]interface{}, 0, len(raw))
	for _, value := range raw {
		block, ok := value.(map[string]interface{})
		if !ok {
			return nil
		}
		blockType, _ := block["type"].(string)
		if blockType == "text" || blockType == "input_text" || blockType == "output_text" {
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": block["text"],
			})
			continue
		}
		normalized, ok := canonicalResponsesValue(block).(map[string]interface{})
		if !ok {
			return nil
		}
		blocks = append(blocks, normalized)
	}
	return blocks
}

func canonicalResponsesValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var normalized interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return string(data)
	}
	return canonicalResponsesValueRecursive(normalized)
}

func canonicalResponsesValueRecursive(value interface{}) interface{} {
	switch typed := value.(type) {
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, canonicalResponsesValueRecursive(item))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			switch key {
			case "annotations", "logprobs", "cache_control":
				continue
			}
			out[key] = canonicalResponsesValueRecursive(item)
		}
		return out
	default:
		return value
	}
}

func canonicalResponsesArguments(raw string) interface{} {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		return value
	}
	return trimmed
}
