package providers

import (
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestOpenAIProviderConvertToClaudeResponse_HidesReasoningContent(t *testing.T) {
	providerResp := &types.ProviderResponse{
		Body: []byte(`{
			"id":"chatcmpl_test",
			"choices":[{
				"finish_reason":"stop",
				"message":{
					"role":"assistant",
					"reasoning_content":"need inspect files",
					"content":"I will inspect the files."
				}
			}]
		}`),
	}

	got, err := (&OpenAIProvider{hideReasoning: true}).ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "I will inspect the files." {
		t.Fatalf("content = %#v, want only text", got.Content)
	}
}

func TestOpenAIProviderConvertToClaudeResponse_HidesEmbeddedThinkingTags(t *testing.T) {
	providerResp := &types.ProviderResponse{Body: []byte(`{
		"choices":[{"finish_reason":"stop","message":{
			"role":"assistant","content":"<think>private reasoning</think>Final answer."
		}}]
	}`)}

	got, err := (&OpenAIProvider{hideReasoning: true, parseEmbeddedReasoning: true}).ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "Final answer." {
		t.Fatalf("content = %#v, want only final answer text", got.Content)
	}
}
