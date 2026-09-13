package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

// newContentSafetySnapshotForTest 按给定配置组装检测器快照。
// applyContentSafetySegments 会无条件调用三个检测器（words / info / credential），
// 缺一个就是 nil panic，所以这里统一建齐，避免各测试各漏一个。
func newContentSafetySnapshotForTest(t *testing.T, settings config.ContentSafetyConfig) *contentSafetySnapshot {
	t.Helper()
	words, err := sensitive.NewWordFilter(settings.SensitiveWord)
	if err != nil {
		t.Fatalf("创建敏感词检测器失败: %v", err)
	}
	info, err := sensitive.NewInfoDetector(settings.SensitiveInfo)
	if err != nil {
		t.Fatalf("创建个人信息检测器失败: %v", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	return &contentSafetySnapshot{settings: settings, words: words, info: info, credential: credential}
}

// TestSafetySegmentsMaskUserTextAcrossProtocols 锁定跨协议掩码契约：三种上游载荷形状
// （Claude messages / Responses input / Gemini contents）里的用户正文都要被改写，
// 助手历史同样脱敏，且只有改写后的用户文本进入观测 prompts。
func TestSafetySegmentsMaskUserTextAcrossProtocols(t *testing.T) {
	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveWord.Enabled = false
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.SensitiveInfo.Mode = config.ContentSafetyModeMask
	settings.SensitiveInfo.EnabledRules = []string{
		config.SensitiveInfoRulePhone,
		config.SensitiveInfoRuleEmail,
	}
	snapshot := newContentSafetySnapshotForTest(t, settings)

	tests := []struct {
		protocol string
		body     string
		masked   string
		original string
	}{
		{
			protocol: "messages",
			body:     `{"messages":[{"role":"assistant","content":"电话 13800138001"},{"role":"user","content":[{"type":"text","text":"电话 13800138000"}]}]}`,
			masked:   "MASKED_PHONE_",
			original: "13800138000",
		},
		{
			protocol: "responses",
			body:     `{"input":[{"role":"assistant","content":"电话 13800138001"},{"role":"user","content":[{"type":"input_text","text":"邮箱 test@example.com"}]}]}`,
			masked:   "MASKED_EMAIL_",
			original: "test@example.com",
		},
		{
			protocol: "gemini",
			body:     `{"contents":[{"role":"model","parts":[{"text":"电话 13800138001"}]},{"role":"user","parts":[{"text":"备用电话 13800138000"}]}]}`,
			masked:   "MASKED_PHONE_",
			original: "13800138000",
		},
	}

	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(test.body), &payload); err != nil {
				t.Fatalf("解析测试请求失败: %v", err)
			}
			segments, err := extractSafetySegments(test.protocol, payload)
			if err != nil {
				t.Fatalf("提取片段失败: %v", err)
			}
			if len(segments) != 2 || segments[0].Source != safetySourceAssistant || segments[1].Source != safetySourceUser {
				t.Fatalf("用户正文与助手历史都应进入敏感数据检查范围: %+v", segments)
			}

			prompts := make([]string, 0, 1)
			changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: test.protocol}, segments, snapshot, nil, &prompts)
			if err != nil {
				t.Fatalf("应用内容安全策略失败: %v", err)
			}
			if !changed {
				t.Fatal("预期用户正文被掩码改写")
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("序列化测试请求失败: %v", err)
			}
			text := string(raw)
			if !strings.Contains(text, test.masked) || strings.Contains(text, test.original) {
				t.Errorf("用户正文掩码结果不正确: %s", text)
			}
			if strings.Contains(text, "13800138001") {
				t.Errorf("助手历史敏感信息未脱敏: %s", text)
			}
			if len(prompts) != 1 || !strings.Contains(prompts[0], test.masked) {
				t.Errorf("观测 prompts 应为掩码后的用户正文: %#v", prompts)
			}
		})
	}
}

// TestSafetySegmentsBlockOnlyUserSensitiveWord 锁定敏感词只对用户/系统输入生效：
// 助手历史里出现敏感词是既有上下文，拦下来等于让整段会话无法继续。
func TestSafetySegmentsBlockOnlyUserSensitiveWord(t *testing.T) {
	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveWord.Enabled = true
	settings.SensitiveWord.CustomWords = []string{"危险词"}
	settings.SensitiveInfo.Enabled = false
	settings.Credential.Enabled = false
	snapshot := newContentSafetySnapshotForTest(t, settings)

	payload := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "危险词"},
			map[string]interface{}{"role": "user", "content": "普通内容"},
		},
	}
	segments, err := extractSafetySegments("messages", payload)
	if err != nil {
		t.Fatalf("提取片段失败: %v", err)
	}
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, new([]string))
	if err != nil || changed {
		t.Fatalf("assistant 历史命中不应拦截或修改: changed=%v err=%v", changed, err)
	}

	payload["messages"].([]interface{})[1].(map[string]interface{})["content"] = "用户包含危险词"
	segments, err = extractSafetySegments("messages", payload)
	if err != nil {
		t.Fatalf("提取片段失败: %v", err)
	}
	_, err = applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, new([]string))
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.Code() != "SENSITIVE_WORD_BLOCKED" {
		t.Fatalf("用户命中错误 = %#v", err)
	}
}

func TestExtractSafetySegmentsFindsToolResultsInAllProtocols(t *testing.T) {
	tests := []struct {
		apiType string
		body    string
	}{
		{apiType: "messages", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"正常请求"},{"type":"tool_result","tool_use_id":"read_1","content":"API_KEY=sk-1234567890abcdefghijklmnop"}]}]}`},
		{apiType: "chat", body: `{"messages":[{"role":"user","content":"正常请求"},{"role":"tool","tool_call_id":"read_1","content":"API_KEY=sk-1234567890abcdefghijklmnop"}]}`},
		{apiType: "responses", body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"正常请求"}]},{"type":"function_call_output","call_id":"read_1","output":"API_KEY=sk-1234567890abcdefghijklmnop"}]}`},
		{apiType: "gemini", body: `{"contents":[{"role":"user","parts":[{"text":"正常请求"},{"functionResponse":{"name":"read_file","response":{"API_KEY":"sk-1234567890abcdefghijklmnop"}}}]}]}`},
	}

	for _, test := range tests {
		t.Run(test.apiType, func(t *testing.T) {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(test.body), &payload); err != nil {
				t.Fatalf("解析测试请求失败: %v", err)
			}
			segments, err := extractSafetySegments(test.apiType, payload)
			if err != nil {
				t.Fatalf("提取片段失败: %v", err)
			}
			var toolResult *SafetySegment
			for index := range segments {
				if segments[index].Source == safetySourceToolResult {
					toolResult = &segments[index]
					break
				}
			}
			if toolResult == nil || !toolResult.Mutable || !strings.Contains(toolResult.Text, "sk-1234567890abcdefghijklmnop") {
				t.Fatalf("工具结果片段不正确: %+v", segments)
			}
		})
	}
}

func TestToolResultCredentialIsBlockedWithoutMutatingPayload(t *testing.T) {
	settings := config.DefaultContentSafetyConfig()
	settings.Credential.Enabled = true
	settings.Credential.EnabledRules = []string{config.CredentialRuleAPIKey, config.CredentialRuleNamedSecret}
	settings.Credential.UserInputMode = config.ContentSafetyModeMask
	settings.Credential.ToolResultMode = config.ContentSafetyModeBlock
	words, err := sensitive.NewWordFilter(settings.SensitiveWord)
	if err != nil {
		t.Fatalf("创建敏感词检测器失败: %v", err)
	}
	info, err := sensitive.NewInfoDetector(settings.SensitiveInfo)
	if err != nil {
		t.Fatalf("创建个人信息检测器失败: %v", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	snapshot := &contentSafetySnapshot{settings: settings, words: words, info: info, credential: credential}
	payload := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{
				map[string]interface{}{"type": "tool_result", "content": "API_KEY=sk-1234567890abcdefghijklmnop"},
			}},
		},
	}
	segments, err := extractSafetySegments("messages", payload)
	if err != nil {
		t.Fatalf("提取片段失败: %v", err)
	}
	_, err = applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, new([]string))
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.Code() != "CREDENTIAL_BLOCKED" {
		t.Fatalf("工具结果凭据未被阻断: %v", err)
	}
	body, _ := json.Marshal(payload)
	if !strings.Contains(string(body), "sk-1234567890abcdefghijklmnop") || strings.Contains(string(body), "MASKED_CREDENTIAL") {
		t.Fatalf("阻断前不应改写原始工具结果: %s", body)
	}
}

func TestExtractorCoversAssistantHistoryWithoutTouchingUnrelatedFields(t *testing.T) {
	payload := map[string]interface{}{
		"metadata": map[string]interface{}{"note": "user@example.com"},
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "assistant@example.com"},
			map[string]interface{}{"role": "user", "content": "user@example.com"},
		},
	}
	segments, err := extractSafetySegments("chat", payload)
	if err != nil {
		t.Fatalf("提取片段失败: %v", err)
	}
	if len(segments) != 2 || segments[0].Text != "assistant@example.com" || segments[0].Path != "messages[0].content" ||
		segments[1].Text != "user@example.com" || segments[1].Path != "messages[1].content" {
		t.Fatalf("精确提取范围错误: %+v", segments)
	}
}

func TestPayloadProtocolUsesConvertedUpstreamShape(t *testing.T) {
	tests := []struct {
		entry       string
		serviceType string
		want        string
	}{
		{entry: "messages", serviceType: "openai", want: "chat"},
		{entry: "messages", serviceType: "gemini", want: "gemini"},
		{entry: "responses", serviceType: "claude", want: "messages"},
		{entry: "gemini", serviceType: "openai", want: "chat"},
		{entry: "chat", serviceType: "claude", want: "chat"},
		// Images 入口不做协议转换：渠道常配 ServiceType=openai，若被映射成 chat，
		// images 载荷会被当 Chat Completions 解析而检查全部落空。
		{entry: "images", serviceType: "openai", want: "images"},
		{entry: "images", serviceType: "claude", want: "images"},
	}
	for _, test := range tests {
		if got := PayloadProtocolForServiceType(test.serviceType, test.entry); got != test.want {
			t.Errorf("入口 %s / 渠道 %s 的载荷协议 = %s，期望 %s", test.entry, test.serviceType, got, test.want)
		}
	}
}

func TestExtractResponseToolArgumentsUsesProtocolFieldsOnly(t *testing.T) {
	payload := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "PASSWORD=normal-prose-value"},
			map[string]interface{}{"type": "tool_use", "input": map[string]interface{}{"value": "PASSWORD=actual-tool-value"}},
		},
		"metadata": map[string]interface{}{"arguments": "PASSWORD=metadata-value"},
	}
	segments := extractResponseToolArgumentSegments("messages", payload)
	if len(segments) != 1 || !strings.Contains(segments[0].Text, "actual-tool-value") || segments[0].Source != safetySourceToolArgument {
		t.Fatalf("工具参数提取范围错误: %+v", segments)
	}
}

func TestContentSafetyPipelineReplacesRequestBodyAndHonorsSettings(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()

	settings := configManager.GetSettings()
	settings.ContentSafety.SensitiveWord.Enabled = false
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}
	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Model: "test"})

	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(`{"messages":[{"role":"user","content":"联系电话 13800138000"}]}`))
	req.Header.Set("Content-Type", "application/json")
	if err := runAttachedPreRequestHooks(context.Background(), c, req, "channel"); err != nil {
		t.Fatalf("请求前 Hook 执行失败: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取替换后的请求体失败: %v", err)
	}
	if !strings.Contains(string(body), "MASKED_PHONE_") || strings.Contains(string(body), "13800138000") {
		t.Fatalf("上游请求体未掩码: %s", body)
	}
	if req.ContentLength != int64(len(body)) {
		t.Fatalf("ContentLength = %d，实际长度 = %d", req.ContentLength, len(body))
	}

	settings.ContentSafety.SensitiveInfo.Enabled = false
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("关闭敏感信息设置失败: %v", err)
	}
	// 热更新从下一个请求生效；同一请求的多次检查使用已固定的设置快照。
	ClearAttachedSensitiveData(c)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Model: "test"})
	req = httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(`{"messages":[{"role":"user","content":"请联系 18012345523"}]}`))
	req.Header.Set("Content-Type", "application/json")
	if err := runAttachedPreRequestHooks(context.Background(), c, req, "channel"); err != nil {
		t.Fatalf("关闭开关后 Hook 执行失败: %v", err)
	}
	body, _ = io.ReadAll(req.Body)
	if string(body) != `{"messages":[{"role":"user","content":"请联系 18012345523"}]}` {
		t.Fatalf("关闭敏感信息设置后请求体不应发生任何改写: %s", body)
	}
}

func TestDisabledContentSafetyPreservesRequestBytesAndMetadata(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()

	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Model: "test"})

	original := []byte("{\n  \"messages\": [{\"role\": \"user\", \"content\": \"user@example.com\"}]\n}\n")
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(original))
	req.Header.Set("Content-Type", "application/json")
	originalLength := req.ContentLength
	originalGetBody := req.GetBody
	if err := runAttachedPreRequestHooks(context.Background(), c, req, "channel"); err != nil {
		t.Fatalf("默认关闭时请求前 Hook 不应报错: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取请求体失败: %v", err)
	}
	if !bytes.Equal(body, original) {
		t.Fatalf("默认关闭时请求字节被改变:\n%s", body)
	}
	if req.ContentLength != originalLength || (req.GetBody == nil) != (originalGetBody == nil) {
		t.Fatalf("默认关闭时请求元数据被改变: ContentLength=%d GetBodyNil=%v", req.ContentLength, req.GetBody == nil)
	}
}

func TestContentSafetyPostResponseBlocksExecutableContexts(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	enableDangerousCmdForTest(t, configManager)
	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "chat", Model: "test"})

	tests := []struct {
		name      string
		body      string
		wantBlock bool
	}{
		{
			name:      "代码块",
			body:      "{\"choices\":[{\"message\":{\"content\":\"执行：\\n```sh\\nrm -rf /\\n```\"}}]}",
			wantBlock: true,
		},
		{
			name:      "普通解释",
			body:      `{"choices":[{"message":{"content":"rm -rf / 是危险命令，请勿执行"}}]}`,
			wantBlock: false,
		},
		{
			name:      "工具参数",
			body:      `{"choices":[{"message":{"tool_calls":[{"function":{"name":"shell","arguments":"{\"command\":\"rm -rf /\"}"}}]}}]}`,
			wantBlock: true,
		},
		{
			name:      "工具返回内容",
			body:      `{"tool_result":{"content":"rm -rf / 是历史输出，没有执行"}}`,
			wantBlock: false,
		},
		{
			name:      "自定义工具原始安全脚本",
			body:      `{"output":[{"type":"custom_tool_call","name":"shell","input":"{ echo safe; }"}]}`,
			wantBlock: false,
		},
		{
			name:      "自定义工具原始危险脚本",
			body:      `{"output":[{"type":"custom_tool_call","name":"shell","input":"{ rm -rf /; }"}]}`,
			wantBlock: true,
		},
		{
			name:      "畸形函数参数",
			body:      `{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"rm -rf /\""}}]}}]}`,
			wantBlock: true,
		},
		{
			name:      "工具结构标识不是命令",
			body:      `{"choices":[{"message":{"tool_calls":[{"id":"rm -rf /","type":"function","function":{"name":"shell","arguments":"{\"command\":\"echo safe\"}"}}]}}]}`,
			wantBlock: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RunAttachedPostResponseHooks(context.Background(), c, []byte(tt.body), nil)
			var safetyErr *ContentSafetyError
			blocked := errors.As(err, &safetyErr)
			if blocked != tt.wantBlock {
				t.Fatalf("blocked=%v err=%v，期望 %v", blocked, err, tt.wantBlock)
			}
			if blocked && safetyErr.Code() != "DANGEROUS_COMMAND_BLOCKED" {
				t.Fatalf("错误码 = %q", safetyErr.Code())
			}
		})
	}
}

func TestContentSafetyPostResponseHonorsDisabledSetting(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	settings := configManager.GetSettings()
	settings.ContentSafety.DangerousCmd.Enabled = false
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("关闭危险命令检测失败: %v", err)
	}
	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages"})
	body := []byte("{\"content\":[{\"type\":\"text\",\"text\":\"```sh\\nrm -rf /\\n```\"}]}")
	if _, err := RunAttachedPostResponseHooks(context.Background(), c, body, nil); err != nil {
		t.Fatalf("关闭危险命令检测后不应拦截: %v", err)
	}
}

func TestContentSafetyStreamScannerDetectsAcrossChunks(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	enableDangerousCmdForTest(t, configManager)
	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Stream: true})
	if err := FeedAttachedStreamText(c, "```sh\nrm -rf"); err != nil {
		t.Fatalf("前缀不应立即拦截: %v", err)
	}
	err = FeedAttachedStreamText(c, " /\n```")
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.Code() != "DANGEROUS_COMMAND_BLOCKED" {
		t.Fatalf("跨 chunk 检测错误 = %v", err)
	}
}

func TestContentSafetyStreamToolArgumentsAndProtocolError(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	enableDangerousCmdForTest(t, configManager)
	pipeline := NewContentSafetyPipeline(configManager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "chat", Stream: true})
	if err := FeedAttachedStreamToolArgumentsForKey(c, defaultStreamToolArgumentKey, `{"command":"rm -rf `); err != nil {
		t.Fatalf("不完整工具参数不应拦截: %v", err)
	}
	err = FeedAttachedStreamToolArgumentsForKey(c, defaultStreamToolArgumentKey, `/"}`)
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) {
		t.Fatalf("完整工具参数未拦截: %v", err)
	}
	if err := WriteAttachedStreamError(c, safetyErr); err != nil {
		t.Fatalf("写出 Chat 流错误失败: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"content_safety_error"`) || !strings.Contains(body, "DANGEROUS_COMMAND_BLOCKED") {
		t.Fatalf("Chat 流错误格式不正确: %s", body)
	}

	for _, testCase := range []struct {
		name     string
		input    string
		rawInput bool
		wantErr  bool
	}{
		{name: "原始安全脚本", input: "{ echo safe; }", rawInput: true},
		{name: "原始危险脚本", input: "{ rm -rf /; }", rawInput: true, wantErr: true},
		{name: "未完成 JSON", input: `{"command":"echo safe"`, wantErr: true},
		{name: "空函数参数"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			AttachHookPipeline(context, pipeline, HookContext{APIType: "responses", Stream: true})
			t.Cleanup(func() { ClearAttachedSensitiveData(context) })
			var err error
			if testCase.rawInput {
				err = FeedAttachedStreamToolInput(context, "tool", testCase.input, true, "shell")
			} else {
				err = FeedAttachedStreamToolArgumentsSnapshot(context, "tool", testCase.input, "shell")
			}
			if (err != nil) != testCase.wantErr {
				t.Fatalf("工具输入审核结果不符: %v", err)
			}
		})
	}
}

func TestContentSafetyPipelineRecordsPreResponseAndStreamBlocks(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	settings := configManager.GetSettings()
	settings.ContentSafety.SensitiveWord.Enabled = true
	settings.ContentSafety.SensitiveWord.CustomWords = []string{"仅用于记录测试的禁词"}
	settings.ContentSafety.DangerousCmd.Enabled = true
	settings.ContentSafety.DangerousCmd.EnabledRules = allDangerousCmdRulesForTest()
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}

	store, err := sensitive.NewBlockedStore(filepath.Join(t.TempDir(), "blocked.db"))
	if err != nil {
		t.Fatalf("创建拦截记录存储失败: %v", err)
	}
	defer store.Close()
	pipeline := NewContentSafetyPipelineWithRecorder(configManager, store)

	preRecorder := httptest.NewRecorder()
	preContext, _ := gin.CreateTestContext(preRecorder)
	AttachHookPipeline(preContext, pipeline, HookContext{APIType: "messages", Model: "claude-test"})
	preRequest := httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(
		`{"messages":[{"role":"user","content":"包含仅用于记录测试的禁词"}]}`,
	))
	preRequest.Header.Set("Content-Type", "application/json")
	preRequest.Header.Set("X-Request-Id", "req-pre")
	err = runAttachedPreRequestHooks(context.Background(), preContext, preRequest, "messages-primary")
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.BlockType != sensitive.BlockTypeSensitiveWord {
		t.Fatalf("请求前拦截错误 = %v", err)
	}
	if err := AssignAttachedConversation(context.Background(), preContext, "conv-shared"); err != nil {
		t.Fatalf("关联请求前拦截记录失败: %v", err)
	}

	postRecorder := httptest.NewRecorder()
	postContext, _ := gin.CreateTestContext(postRecorder)
	AttachHookPipeline(postContext, pipeline, HookContext{APIType: "chat", Model: "gpt-test"})
	postRequest := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(
		`{"messages":[{"role":"user","content":"普通请求"}]}`,
	))
	postRequest.Header.Set("Content-Type", "application/json")
	postRequest.Header.Set("X-Oai-Request-Id", "req-post")
	if err := runAttachedPreRequestHooks(context.Background(), postContext, postRequest, "chat-primary"); err != nil {
		t.Fatalf("准备响应拦截元数据失败: %v", err)
	}
	if err := AssignAttachedConversation(context.Background(), postContext, "conv-shared"); err != nil {
		t.Fatalf("关联响应拦截记录失败: %v", err)
	}
	_, err = RunAttachedPostResponseHooks(context.Background(), postContext,
		[]byte("{\"choices\":[{\"message\":{\"content\":\"```sh\\nrm -rf /\\n```\"}}]}"), nil)
	if !errors.As(err, &safetyErr) || safetyErr.BlockType != sensitive.BlockTypeDangerousCmd {
		t.Fatalf("非流式响应拦截错误 = %v", err)
	}

	streamRecorder := httptest.NewRecorder()
	streamContext, _ := gin.CreateTestContext(streamRecorder)
	AttachHookPipeline(streamContext, pipeline, HookContext{APIType: "responses", Model: "codex-test", Stream: true})
	streamRequest := httptest.NewRequest("POST", "/v1/responses", bytes.NewBufferString(`{"input":"普通请求"}`))
	streamRequest.Header.Set("Content-Type", "application/json")
	streamRequest.Header.Set("X-Request-Id", "req-stream")
	if err := runAttachedPreRequestHooks(context.Background(), streamContext, streamRequest, "responses-primary"); err != nil {
		t.Fatalf("准备流式拦截元数据失败: %v", err)
	}
	if err := AssignAttachedConversation(context.Background(), streamContext, "conv-shared"); err != nil {
		t.Fatalf("关联流式拦截记录失败: %v", err)
	}
	if err := FeedAttachedStreamText(streamContext, "```sh\nrm -rf"); err != nil {
		t.Fatalf("危险命令前缀不应提前拦截: %v", err)
	}
	err = FeedAttachedStreamText(streamContext, " /\n```")
	if !errors.As(err, &safetyErr) || safetyErr.BlockType != sensitive.BlockTypeDangerousCmd {
		t.Fatalf("流式响应拦截错误 = %v", err)
	}

	page, err := store.List(context.Background(), sensitive.BlockedLogListOptions{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询拦截记录失败: %v", err)
	}
	if page.Total != 3 || len(page.Logs) != 3 {
		t.Fatalf("拦截记录数量 = %d/%d，期望 3: %+v", page.Total, len(page.Logs), page.Logs)
	}
	byRequestID := make(map[string]sensitive.BlockedLog, len(page.Logs))
	for _, entry := range page.Logs {
		if entry.ConversationID != "conv-shared" {
			t.Fatalf("拦截记录未关联同一会话: %+v", entry)
		}
		byRequestID[entry.RequestID] = entry
	}
	assertBlockedLogMetadata(t, byRequestID["req-pre"], "messages", sensitive.BlockTypeSensitiveWord, "messages-primary", "claude-test")
	assertBlockedLogMetadata(t, byRequestID["req-post"], "chat", sensitive.BlockTypeDangerousCmd, "chat-primary", "gpt-test")
	assertBlockedLogMetadata(t, byRequestID["req-stream"], "responses", sensitive.BlockTypeDangerousCmd, "responses-primary", "codex-test")
}

func TestContentSafetyPipelineReturnsRecorderFailure(t *testing.T) {
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	defer configManager.Close()
	enableDangerousCmdForTest(t, configManager)
	store, err := sensitive.NewBlockedStore(filepath.Join(t.TempDir(), "blocked.db"))
	if err != nil {
		t.Fatalf("创建拦截记录存储失败: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭拦截记录存储失败: %v", err)
	}

	pipeline := NewContentSafetyPipelineWithRecorder(configManager, store)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "chat", Model: "test", RequestID: "req-closed"})
	_, err = RunAttachedPostResponseHooks(context.Background(), c,
		[]byte("{\"choices\":[{\"message\":{\"content\":\"```sh\\nrm -rf /\\n```\"}}]}"), nil)
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) {
		t.Fatalf("存储失败后内容安全错误丢失: %v", err)
	}
	if !errors.Is(err, sensitive.ErrBlockedStoreClosed) {
		t.Fatalf("存储失败未显式返回: %v", err)
	}
}

func assertBlockedLogMetadata(t *testing.T, entry sensitive.BlockedLog, apiType, blockType, channel, model string) {
	t.Helper()
	if entry.RequestID == "" || entry.APIType != apiType || entry.BlockType != blockType ||
		entry.ChannelName != channel || entry.Model != model || entry.RuleName == "" || entry.PromptSnippet == "" {
		t.Fatalf("拦截记录元数据不完整: %+v", entry)
	}
}

func TestExtractSafetySegmentsCoversImagesJSONPrompt(t *testing.T) {
	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveWord.Enabled = false
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.SensitiveInfo.Mode = config.ContentSafetyModeMask
	settings.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	words, err := sensitive.NewWordFilter(settings.SensitiveWord)
	if err != nil {
		t.Fatalf("创建敏感词检测器失败: %v", err)
	}
	info, err := sensitive.NewInfoDetector(settings.SensitiveInfo)
	if err != nil {
		t.Fatalf("创建敏感信息检测器失败: %v", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	snapshot := &contentSafetySnapshot{settings: settings, words: words, info: info, credential: credential}

	payload := map[string]interface{}{
		"model":  "gpt-image-1",
		"prompt": "把电话 18012345523 写在海报上",
		"size":   "1024x1024",
		"user":   "tenant-42",
	}
	segments, err := extractSafetySegments("images", payload)
	if err != nil {
		t.Fatalf("提取 images 片段失败: %v", err)
	}
	// size / model / n 是枚举或数值，user 是终端用户标识而非正文，都不该进检查范围。
	if len(segments) != 1 || segments[0].Path != "prompt" || segments[0].Source != safetySourceUser {
		t.Fatalf("images 片段提取范围错误: %+v", segments)
	}

	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "images"}, segments, snapshot, nil, &prompts)
	if err != nil {
		t.Fatalf("应用内容安全策略失败: %v", err)
	}
	if !changed {
		t.Fatal("预期 prompt 被掩码改写")
	}
	prompt, _ := payload["prompt"].(string)
	if !strings.Contains(prompt, "MASKED_PHONE_") || strings.Contains(prompt, "13800138000") {
		t.Fatalf("images prompt 未掩码: %q", prompt)
	}
	if payload["user"] != "tenant-42" || payload["size"] != "1024x1024" {
		t.Fatalf("非正文字段不应被改写: %+v", payload)
	}
}

func enableDangerousCmdForTest(t *testing.T, configManager *config.ConfigManager) {
	t.Helper()
	settings := configManager.GetSettings()
	settings.ContentSafety.DangerousCmd.Enabled = true
	settings.ContentSafety.DangerousCmd.EnabledRules = allDangerousCmdRulesForTest()
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("启用危险命令测试规则失败: %v", err)
	}
}

func allDangerousCmdRulesForTest() []string {
	return []string{
		config.DangerousCmdRuleDestructive,
		config.DangerousCmdRuleDownloadExecute,
		config.DangerousCmdRuleReverseShell,
		config.DangerousCmdRulePrivilegeEscalation,
		config.DangerousCmdRuleEnvironmentTampering,
	}
}

func TestStreamCandidateFinishKeepsOtherArgumentsPending(t *testing.T) {
	for _, protocol := range []struct {
		api    string
		prefix string
		field  string
		finish string
	}{
		{api: "chat", prefix: "chat.choice.", field: "choices", finish: "finish_reason"},
		{api: "gemini", prefix: "gemini.candidate.", field: "candidates", finish: "finishReason"},
	} {
		t.Run(protocol.api, func(t *testing.T) {
			manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			enableDangerousCmdForTest(t, manager)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			AttachHookPipeline(c, NewContentSafetyPipeline(manager), HookContext{APIType: protocol.api, Stream: true})
			defer ClearAttachedSensitiveData(c)
			if err := FeedAttachedStreamToolArgumentsForKey(c, protocol.prefix+"1.tool.0", `{"command":"echo`, "Bash"); err != nil {
				t.Fatal(err)
			}
			finish := func(index int) error {
				body, err := json.Marshal(map[string]any{
					protocol.field: []any{map[string]any{"index": index, protocol.finish: "stop"}},
				})
				if err != nil {
					return err
				}
				_, err = RestoreAttachedSSEEvent(c, "data: "+string(body)+"\n\n")
				return err
			}
			if err := finish(0); err != nil {
				t.Fatalf("候选 0 结束不应检查候选 1 的半段 JSON: %v", err)
			}
			if err := FeedAttachedStreamToolArgumentsForKey(c, protocol.prefix+"1.tool.0", ` ok"}`, "Bash"); err != nil {
				t.Fatal(err)
			}
			if err := finish(1); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamCandidatesKeepIndependentCodeBlocks(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	enableDangerousCmdForTest(t, manager)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, NewContentSafetyPipeline(manager), HookContext{APIType: "chat", Stream: true})
	defer ClearAttachedSensitiveData(c)
	for _, fragment := range []struct{ key, text string }{
		{"chat.choice.0.", "```sh\n"},
		{"chat.choice.1.", "rm -rf /\n"},
		{"chat.choice.0.", "echo ok\n```"},
		{"chat.choice.1.", "```sh\nrm -rf /\n"},
	} {
		if err := FeedAttachedStreamTextForKey(c, fragment.key, fragment.text); err != nil {
			t.Fatalf("不同候选的代码块不能互相拼接或提前结束: %v", err)
		}
	}
	if err := FinishAttachedStreamSafetyEvent(c, `data: {"choices":[{"index":0,"finish_reason":"stop"}]}`); err != nil {
		t.Fatalf("候选 0 结束不应触发候选 1 的未闭合代码块: %v", err)
	}
	err = FinishAttachedStreamSafetyEvent(c, `data: {"choices":[{"index":1,"finish_reason":"stop"}]}`)
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.BlockType != sensitive.BlockTypeDangerousCmd {
		t.Fatalf("候选 1 结束前必须拦截其危险命令: %v", err)
	}
}
