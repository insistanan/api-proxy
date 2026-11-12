package hooks

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

func TestHookPipelineEmptyStagesPreserveResult(t *testing.T) {
	pipeline := NewPipeline()
	initial := HookResult{RequestBody: []byte("request"), ResponseBody: []byte("response")}
	for _, stage := range []HookStage{HookStagePreRequest, HookStagePostResponse} {
		result, err := pipeline.Run(context.Background(), stage, HookContext{}, initial)
		if err != nil || string(result.RequestBody) != "request" || string(result.ResponseBody) != "response" {
			t.Fatalf("空阶段应保留输入: stage=%s result=%+v err=%v", stage, result, err)
		}
	}
}

func TestHookPipelineRejectsInvalidExecution(t *testing.T) {
	pipeline := NewPipeline()
	if _, err := pipeline.Run(context.Background(), HookStage("invalid"), HookContext{}, HookResult{}); err == nil {
		t.Fatal("无效阶段应返回错误")
	}
	if _, err := pipeline.Run(nil, HookStagePreRequest, HookContext{}, HookResult{}); err == nil {
		t.Fatal("空执行上下文应返回错误")
	}
	var missing *Pipeline
	if _, err := missing.RunPreRequest(context.Background(), HookContext{}, HookResult{}); err == nil {
		t.Fatal("空管道应返回错误")
	}
}

func TestHookPipelineCancellationPreservesInputAndErrorCause(t *testing.T) {
	pipeline := NewContentSafetyPipeline(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		stage HookStage
		name  string
	}{
		{HookStagePreRequest, "content-safety"},
		{HookStagePostResponse, "response-content-safety"},
	} {
		initial := HookResult{RequestBody: []byte("request"), ResponseBody: []byte("response")}
		result, err := pipeline.Run(ctx, test.stage, HookContext{}, initial)
		var executionErr *HookExecutionError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &executionErr) ||
			executionErr.Stage != test.stage || executionErr.Hook != test.name {
			t.Fatalf("取消原因及处理阶段应保留: stage=%s err=%v", test.stage, err)
		}
		if string(result.RequestBody) != "request" || string(result.ResponseBody) != "response" {
			t.Fatalf("失败不应替换输入: %+v", result)
		}
	}
}

func TestHookPipelineKeepsRequestAndResponsePoliciesSeparate(t *testing.T) {
	safety := config.DefaultContentSafetyConfig()
	safety.SensitiveWord.Enabled = true
	safety.SensitiveWord.CustomWords = []string{"测试禁词"}
	safety.SensitiveInfo.Enabled = false
	safety.Credential.Enabled = false
	safety.DangerousCmd.Enabled = false
	hook, _ := newPostResponseHookForTest(t, safety)
	pipeline := NewContentSafetyPipeline(hook.cfgManager)
	metadata := HookContext{APIType: "messages"}
	body := []byte(`{"messages":[{"role":"user","content":"测试禁词"}]}`)
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set("Content-Type", "application/json")
	_, err := pipeline.RunPreRequest(context.Background(), metadata, HookResult{RequestBody: body, UpstreamRequest: request})
	if ContentSafetyErrorFrom(err) == nil {
		t.Fatalf("请求方向敏感词应被拦截: %v", err)
	}
	response := []byte(`{"content":[{"type":"text","text":"测试禁词"}]}`)
	result, err := pipeline.RunPostResponse(context.Background(), metadata, HookResult{ResponseBody: response})
	if err != nil || string(result.ResponseBody) != string(response) {
		t.Fatalf("请求方向敏感词策略不应拦截响应: body=%s err=%v", result.ResponseBody, err)
	}
}
