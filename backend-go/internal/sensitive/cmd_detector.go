package sensitive

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

const maxCommandWindow = 256 << 10

// MaxCommandBytes 限制规则检测的完整上下文；AI 闸门另设更小的输入上限。
const MaxCommandBytes = maxCommandWindow

// CommandMatch 描述一次可执行上下文中的危险命令命中。
type CommandMatch struct {
	Rule             string `json:"rule"`
	Command          string `json:"command"`
	Context          string `json:"context"`
	ToolName         string `json:"toolName,omitempty"`
	ExecutionContext string `json:"-"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
}

// CommandIdentity 保留完整命令、工具与参数语义；参数、路径、引号或管道变化均不能复用授权。
func CommandIdentity(match CommandMatch) string {
	encoded, _ := json.Marshal([]string{match.Context, match.ToolName, match.Command, match.ExecutionContext})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// CmdDetector 只对代码块或工具参数执行危险命令检测。
type CmdDetector struct {
	enabled bool
	rules   []commandRule
}

// NewCmdDetector 根据设置创建危险命令检测器。
func NewCmdDetector(settings config.DangerousCmdConfig) (*CmdDetector, error) {
	if err := validateCommandRules(settings.EnabledRules); err != nil {
		return nil, err
	}
	detector := &CmdDetector{enabled: settings.Enabled}
	if !settings.Enabled {
		return detector, nil
	}
	selected := make(map[string]struct{}, len(settings.EnabledRules))
	for _, rule := range settings.EnabledRules {
		selected[rule] = struct{}{}
	}
	for _, rule := range builtinCommandRules {
		if _, ok := selected[rule.name]; ok {
			detector.rules = append(detector.rules, rule)
		}
	}
	return detector, nil
}

// Find 检测 Markdown 代码围栏内的危险命令，普通文本不会触发匹配。
func (d *CmdDetector) Find(text string) []CommandMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 || text == "" {
		return nil
	}
	matches := make([]CommandMatch, 0)
	for _, span := range codeBlockSpans(text) {
		matches = append(matches, d.findInSegment(text[span.start:span.end], "code_block", span.start)...)
	}
	return matches
}

// FindInToolArguments 保留无工具名调用入口，仅把命令字段作为执行输入。
func (d *CmdDetector) FindInToolArguments(value any) []CommandMatch {
	return d.FindInNamedToolArguments("", value)
}

// FindInNamedToolArguments 保留完整参数作为执行上下文，编辑/读取工具的文件内容不视为命令。
func (d *CmdDetector) FindInNamedToolArguments(toolName string, value any) []CommandMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 {
		return nil
	}
	if nonExecutingTool(toolName) {
		return nil
	}
	if encoded, ok := value.(string); ok {
		if len(encoded) > MaxCommandBytes {
			return []CommandMatch{{Rule: "command_size_limit", Context: "tool", ToolName: toolName, Command: "工具执行上下文超过检查容量上限"}}
		}
		if strings.TrimSpace(encoded) == "" {
			encoded = "{}"
		}
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return []CommandMatch{{Rule: "invalid_tool_arguments", Context: "tool", ToolName: toolName}}
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return []CommandMatch{{Rule: "invalid_tool_arguments", Context: "tool", ToolName: toolName}}
		}
		value = decoded
	}
	encodedContext, err := json.Marshal(value)
	if err != nil {
		return []CommandMatch{{Rule: "invalid_tool_arguments", Context: "tool", ToolName: toolName}}
	}
	if len(encodedContext) > MaxCommandBytes {
		return []CommandMatch{{Rule: "command_size_limit", Context: "tool", ToolName: toolName, Command: "工具执行上下文超过检查容量上限"}}
	}
	matches := make([]CommandMatch, 0)
	limited := false
	var walk func(any, bool)
	walk = func(current any, commandField bool) {
		if limited {
			return
		}
		switch typed := current.(type) {
		case string:
			if commandField {
				matches = append(matches, d.findInSegment(typed, "tool", 0)...)
				limited = len(matches) > 256
			}
		case []any:
			for _, item := range typed {
				walk(item, commandField)
			}
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				isCommand := false
				switch strings.ToLower(key) {
				case "command", "commands", "cmd", "script", "shell_command", "shellcommand":
					isCommand = true
				}
				walk(typed[key], isCommand)
			}
		}
	}
	walk(value, true)
	if limited {
		return []CommandMatch{{Rule: "command_count_limit", Context: "tool", ToolName: toolName, Command: "工具参数中的危险命令超过 256 条检查上限"}}
	}
	executionContext := string(encodedContext)
	for index := range matches {
		matches[index].ToolName = toolName
		matches[index].ExecutionContext = executionContext
	}
	return matches
}

// FindInRawToolInput 检查自定义工具的原始脚本，不把 shell 的花括号或引号当成 JSON。
func (d *CmdDetector) FindInRawToolInput(toolName, input string) []CommandMatch {
	if d == nil || !d.enabled || len(d.rules) == 0 || nonExecutingTool(toolName) {
		return nil
	}
	if len(input) > MaxCommandBytes {
		return []CommandMatch{{Rule: "command_size_limit", Context: "tool", ToolName: toolName, Command: "工具执行上下文超过检查容量上限"}}
	}
	matches := d.findInSegment(input, "tool", 0)
	executionContext := "raw_input:\n" + input
	for index := range matches {
		matches[index].ToolName = toolName
		// 前缀区分原始输入和函数参数，即使文本碰巧是合法 JSON 也不能共用授权。
		matches[index].ExecutionContext = executionContext
	}
	return matches
}

func nonExecutingTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if index := strings.LastIndex(name, "__"); index >= 0 {
		name = name[index+2:]
	}
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	switch name {
	case "apply_patch", "write", "write_file", "edit", "edit_file", "multiedit", "read", "read_file", "glob", "grep", "search", "web_search", "web_fetch", "view_image":
		return true
	default:
		return false
	}
}

// Contains 返回文本代码块或工具参数中是否命中危险命令。
func (d *CmdDetector) Contains(text string) bool {
	return len(d.Find(text)) > 0
}

func (d *CmdDetector) findInSegment(segment, context string, base int) []CommandMatch {
	if len(segment) > MaxCommandBytes {
		return []CommandMatch{{Rule: "command_size_limit", Context: context, Command: "命令上下文超过检查容量上限"}}
	}
	matches := make([]CommandMatch, 0)
	for _, rule := range d.rules {
		// 完整上下文只需按规则审一次，避免重复命中让同一大脚本反复散列和排队。
		if index := rule.regex.FindStringIndex(segment); index != nil {
			matches = append(matches, CommandMatch{
				Rule:    rule.name,
				Command: segment,
				Context: context,
				Start:   base + index[0],
				End:     base + index[1],
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End < matches[j].End
	})
	return matches
}

type codeSpan struct {
	start int
	end   int
}

func codeBlockSpans(text string) []codeSpan {
	spans := make([]codeSpan, 0)
	searchFrom := 0
	for searchFrom < len(text) {
		opening := strings.Index(text[searchFrom:], "```")
		if opening < 0 {
			break
		}
		opening += searchFrom
		contentStart := opening + len("```")
		closing := strings.Index(text[contentStart:], "```")
		if closing < 0 {
			spans = append(spans, codeSpan{start: contentStart, end: len(text)})
			break
		}
		closing += contentStart
		spans = append(spans, codeSpan{start: contentStart, end: closing})
		searchFrom = closing + len("```")
	}
	return spans
}

func validateCommandRules(rules []string) error {
	allowed := make(map[string]struct{}, len(builtinCommandRules))
	for _, rule := range builtinCommandRules {
		allowed[rule.name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "" {
			return fmt.Errorf("危险命令规则名不能为空")
		}
		if _, ok := allowed[rule]; !ok {
			return fmt.Errorf("不支持的危险命令规则 %q", rule)
		}
		if _, ok := seen[rule]; ok {
			return fmt.Errorf("危险命令规则 %q 重复", rule)
		}
		seen[rule] = struct{}{}
	}
	return nil
}

// StreamCmdScanner 保留完整代码块，闭合围栏或流结束后检查；不截断命令或批准未完成的前缀。
type StreamCmdScanner struct {
	detector *CmdDetector
	window   string
	pending  string
	inCode   bool
	blocked  *CommandMatch
	queued   []CommandMatch
	seen     map[string]struct{}
	buffer   strings.Builder
	err      error
}

// NewStreamCmdScanner 创建流式危险命令扫描器。
func NewStreamCmdScanner(detector *CmdDetector) *StreamCmdScanner {
	return &StreamCmdScanner{detector: detector}
}

// Feed 输入一个响应片段；命中后后续调用仍返回同一命中。
func (s *StreamCmdScanner) Feed(chunk string) (*CommandMatch, bool) {
	if s == nil || s.detector == nil || !s.detector.enabled || len(s.detector.rules) == 0 {
		return nil, false
	}
	if s.blocked != nil {
		return s.blocked, true
	}
	input := s.pending + chunk
	s.pending = ""
	body, suffix := splitFenceSuffix(input)
	s.pending = strings.Clone(suffix)
	for position := 0; position < len(body); {
		fence := strings.Index(body[position:], "```")
		if fence < 0 {
			if s.inCode {
				s.appendCode(body[position:], false)
			}
			break
		}
		fence += position
		if s.inCode {
			s.appendCode(body[position:fence], true)
		}
		s.inCode = !s.inCode
		if !s.inCode {
			s.window = ""
			s.buffer.Reset()
		}
		position = fence + len("```")
	}
	return s.nextMatch()
}

// Allow 在调用方放行当前命中后继续取同一片段中的下一条命令。
// 只有经过闸门放行才能推进，避免第一条放行后整个流都停止检测。
func (s *StreamCmdScanner) Allow() (*CommandMatch, bool) {
	if s == nil {
		return nil, false
	}
	s.blocked = nil
	return s.nextMatch()
}

func (s *StreamCmdScanner) nextMatch() (*CommandMatch, bool) {
	if len(s.queued) == 0 {
		return nil, false
	}
	match := s.queued[0]
	s.queued[0] = CommandMatch{}
	s.queued = s.queued[1:]
	if len(s.queued) == 0 {
		s.queued = nil
	}
	s.blocked = &match
	return s.blocked, true
}

// Flush 处理流末尾未闭合的围栏前缀。
func (s *StreamCmdScanner) Flush() (*CommandMatch, bool) {
	if s == nil {
		return nil, false
	}
	if s.blocked != nil {
		return s.blocked, s.blocked != nil
	}
	if s.inCode {
		s.appendCode(s.pending, true)
	}
	s.inCode = false
	s.window = ""
	s.buffer.Reset()
	s.pending = ""
	return s.nextMatch()
}

// BufferedBytes 返回尚未完成代码块的暂存大小，供请求级总量限制使用。
func (s *StreamCmdScanner) BufferedBytes() int {
	if s == nil {
		return 0
	}
	return s.buffer.Len() + len(s.pending)
}

func (s *StreamCmdScanner) appendCode(fragment string, final bool) {
	if s.err != nil {
		return
	}
	if len(fragment) > maxCommandWindow-s.buffer.Len() {
		s.err = fmt.Errorf("流式代码块超过 %d 字节检查上限", maxCommandWindow)
		return
	}
	s.buffer.WriteString(fragment)
	s.window = s.buffer.String()
	if !final {
		return
	}
	for _, match := range s.detector.findInSegment(s.window, "code_block", 0) {
		key := match.Rule + "\x00" + CommandIdentity(match)
		if _, ok := s.seen[key]; ok {
			continue
		}
		if s.seen == nil {
			s.seen = make(map[string]struct{})
		}
		if len(s.seen) >= 256 {
			s.err = fmt.Errorf("单次响应的危险命令超过 256 条检查上限")
			return
		}
		s.seen[key] = struct{}{}
		s.queued = append(s.queued, match)
	}
}

func (s *StreamCmdScanner) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

func splitFenceSuffix(input string) (string, string) {
	if strings.HasSuffix(input, "```") {
		return input, ""
	}
	backticks := 0
	for backticks < 2 && backticks < len(input) && input[len(input)-1-backticks] == '`' {
		backticks++
	}
	if backticks == 0 {
		return input, ""
	}
	return input[:len(input)-backticks], input[len(input)-backticks:]
}
