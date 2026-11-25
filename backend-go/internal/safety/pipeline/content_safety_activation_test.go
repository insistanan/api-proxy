package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/safety/sensitive"
)

func TestContentSafetyGroupsDisabled_MaskModeDoesNotInspectOrBuffer(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety = config.DefaultContentSafetyConfig()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	require.NoError(t, manager.UpdateSettings(settings))
	dir := t.TempDir()
	store, err := sensitive.NewMappingStore(filepath.Join(dir, "mappings.db"), filepath.Join(dir, "master-key"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	recorder := &gateTestRecorder{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	AttachHookPipeline(c, NewContentSafetyPipelineWithSemantic(manager, recorder, nil, nil, store), HookContext{
		APIType: "responses", Stream: true, RedactionScope: "disabled-session",
	})
	t.Cleanup(func() { ClearAttachedSensitiveData(c) })
	attached, err := attachedPipelineFromContext(c)
	require.NoError(t, err)
	require.False(t, attached.vault.CanRestore(), "模式本身不能开启持久化映射读取与还原")

	const body = `{"input":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	require.NoError(t, RunAttachedPreRequestHooks(context.Background(), c, req, "primary"))
	requestBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(requestBody))
	require.Nil(t, attached.preRequestCache, "检测全关时不应进入请求检查与改写")

	// 非流式工具响应也不能因选中 mask 而校验模型输出的占位符。
	const response = `{"output":[{"type":"function_call","name":"Bash","arguments":"{\"command\":\"MASKED_SECRET_AAAAAAAAAAAAA_END\"}"}]}`
	restored, err := RunAttachedPostResponseHooks(context.Background(), c, []byte(response), nil)
	require.NoError(t, err)
	require.Equal(t, response, string(restored))

	// 超过原缓冲字节上限的自定义工具输入应直接交付，不能创建审核状态。
	input := strings.Repeat("x", (4<<20)+1)
	require.NoError(t, FeedAttachedStreamToolInput(c, "responses.tool.0", input, false, "apply_patch"))
	encoded, err := json.Marshal(map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": 0, "delta": input})
	require.NoError(t, err)
	event := "event: response.custom_tool_call_input.delta\ndata: " + string(encoded) + "\n\n"
	sent, err := RestoreAttachedSSEEvent(c, event)
	require.NoError(t, err)
	require.Equal(t, event, sent)

	// 大量小分片也不累计为待审核事件；同时覆盖 JSON 工具参数入口。
	for index := 0; index <= 8192; index++ {
		require.NoError(t, FeedAttachedStreamToolArgumentsForKey(c, "responses.tool.1", "x", "Bash"))
		const delta = "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"x\"}\n\n"
		sent, err := RestoreAttachedSSEEvent(c, delta)
		require.NoError(t, err)
		require.Equal(t, delta, sent)
	}
	require.NoError(t, FeedAttachedStreamToolArgumentsSnapshot(c, "responses.tool.1", `{"command":"rm -rf /"}`, "Bash"))
	require.NoError(t, FlushAttachedStreamHooks(c))
	require.Empty(t, attached.toolArgs)
	require.Empty(t, attached.toolDelivery)
	require.Empty(t, recorder.entries, "检测全关时不应产生审核或还原失败记录")
}

func TestContentSafetyMasterSwitch_DisablesAllHooksAndPreservesRules(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	require.False(t, settings.ContentSafety.Enabled, "新安装默认关闭")
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.DangerousCmd.Enabled = true
	settings.ContentSafety.DangerousCmd.AIGate.Enabled = true
	settings.ContentSafety.Credential.Enabled = true
	settings.ContentSafety.CustomRules.Enabled = true
	settings.ContentSafety.CustomRules.Rules = []config.CustomRuleItem{{Name: "尚未完成的草稿", Type: "CUSTOM", Pattern: "["}}
	require.NoError(t, manager.UpdateSettings(settings), "关闭总开关时可以保存未完成的规则草稿")
	pipeline := NewContentSafetyPipeline(manager)
	for _, protocol := range []string{"messages", "responses", "chat", "gemini", "images"} {
		t.Run(protocol, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			AttachHookPipeline(c, pipeline, HookContext{APIType: protocol, Stream: true})
			attached, err := attachedPipelineFromContext(c)
			require.NoError(t, err)
			require.Nil(t, attached, "关闭总开关时不绑定安全状态或读取映射")
			const body = `{"input":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`
			req := httptest.NewRequest("POST", "/", strings.NewReader(body))
			require.NoError(t, RunAttachedPreRequestHooks(context.Background(), c, req, "primary"))
			response, err := RunAttachedPostResponseHooks(context.Background(), c, []byte(body), nil)
			require.NoError(t, err)
			require.Equal(t, body, string(response))
			require.NoError(t, FeedAttachedStreamToolInput(c, "tool-1", "rm -rf /", true, "Bash"))
			const event = "data: {\"delta\":\"MASKED_SECRET_AAAAAAAAAAAAA_END\"}\n\n"
			sent, err := RestoreAttachedSSEEvent(c, event)
			require.NoError(t, err)
			require.Equal(t, event, sent)
			require.NoError(t, FlushAttachedStreamHooks(c))
			// 没有绑定上下文的独立处理器也必须服从总开关。
			_, err = pipeline.RunPreRequest(context.Background(), HookContext{APIType: protocol}, HookResult{RequestBody: []byte(body), UpstreamRequest: req})
			require.NoError(t, err)
			_, err = pipeline.RunPostResponse(context.Background(), HookContext{APIType: protocol}, HookResult{ResponseBody: []byte(body)})
			require.NoError(t, err)
		})
	}
	require.Equal(t, settings.ContentSafety, manager.GetContentSafetyConfig())
	settings.ContentSafety.Enabled = true
	require.Error(t, manager.UpdateSettings(settings), "再次开启前须完整校验草稿")
}

func TestContentSafety_LargeToolInputIsReviewedAndDeliveredWhole(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	require.NoError(t, manager.UpdateSettings(settings))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, NewContentSafetyPipeline(manager), HookContext{APIType: "responses", Stream: true})
	t.Cleanup(func() { ClearAttachedSensitiveData(c) })
	input := strings.Repeat("x", (4<<20)+1)
	require.NoError(t, FeedAttachedStreamToolInput(c, "responses.tool.0", input, false, "apply_patch"))
	encoded, err := json.Marshal(map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": 0, "delta": input})
	require.NoError(t, err)
	event := "data: " + string(encoded) + "\n\n"
	sent, err := RestoreAttachedSSEEvent(c, event)
	require.NoError(t, err)
	require.Empty(t, sent, "未结束的工具不能提前交付")
	require.NoError(t, FeedAttachedStreamToolInput(c, "responses.tool.0", input, true, "apply_patch"))
	delivered, err := DrainAttachedStreamRestoration(c)
	require.NoError(t, err)
	require.Equal(t, event, strings.Join(delivered, ""), "超过旧参数和交付容量限制的完整内容应按原序交付")
}

func TestContentSafetyRedactionActivation_UsesRequestSnapshot(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety = config.DefaultContentSafetyConfig()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	require.NoError(t, manager.UpdateSettings(settings))
	pipeline := NewContentSafetyPipeline(manager)
	newRequest := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Stream: true})
		t.Cleanup(func() { ClearAttachedSensitiveData(c) })
		return c
	}
	disabledRequest := newRequest()
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	require.NoError(t, manager.UpdateSettings(settings))
	enabledRequest := newRequest()
	settings.ContentSafety.Enabled = false
	require.NoError(t, manager.UpdateSettings(settings))
	newDisabledRequest := newRequest()

	const arguments = `{"name":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`
	for _, c := range []*gin.Context{disabledRequest, newDisabledRequest} {
		require.NoError(t, FeedAttachedStreamToolArgumentsForKey(c, "messages.content_block.0", arguments, "lookup"))
	}
	disabledAttached, err := attachedPipelineFromContext(disabledRequest)
	require.NoError(t, err)
	require.NotNil(t, disabledAttached, "总开关开启的请求保留其规则快照")
	require.Empty(t, disabledAttached.toolArgs)
	newDisabledAttached, err := attachedPipelineFromContext(newDisabledRequest)
	require.NoError(t, err)
	require.Nil(t, newDisabledAttached, "总开关关闭后的新请求不绑定安全状态")
	err = FeedAttachedStreamToolArgumentsForKey(enabledRequest, "messages.content_block.0", arguments, "lookup")
	require.ErrorContains(t, err, "未获当前请求或会话授权", "启用脱敏的既有请求仍需完整检查工具参数")
}

func TestContentSafetyCustomRule_StillMasksAndChecksTools(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety = config.DefaultContentSafetyConfig()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.CustomRules = config.CustomRulesConfig{
		Enabled: true,
		Rules:   []config.CustomRuleItem{{Name: "内部代号", Type: "CODENAME", Pattern: "project-falcon"}},
	}
	require.NoError(t, manager.UpdateSettings(settings))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, NewContentSafetyPipeline(manager), HookContext{APIType: "messages", Stream: true})
	t.Cleanup(func() { ClearAttachedSensitiveData(c) })
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"messages":[{"role":"user","content":"查询 project-falcon"}]}`))
	require.NoError(t, RunAttachedPreRequestHooks(context.Background(), c, req, "primary"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), "project-falcon")
	require.Contains(t, string(body), "MASKED_CODENAME_")
	err = FeedAttachedStreamToolArgumentsForKey(c, "messages.content_block.0", `{"query":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`, "lookup")
	require.ErrorContains(t, err, "未获当前请求或会话授权", "仅开启自定义规则时仍需校验工具中的占位符")
}
