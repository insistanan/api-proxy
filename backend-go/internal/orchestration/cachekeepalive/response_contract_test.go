package cachekeepalive

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestKeepaliveCompletion_RejectsMissingOrInvalidReasons(t *testing.T) {
	usage := `"usage":{"prompt_tokens":1010,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":1000}}`
	for _, choices := range []string{`[]`, `[{}]`, `[{"finish_reason":"content_filter"}]`} {
		_, err := parseProtocolResponse("openai", []byte(`{"choices":`+choices+`,`+usage+`}`), 200)
		require.Error(t, err)
	}
	_, err := parseProtocolResponse("claude", []byte(`{"type":"message","usage":{"input_tokens":0,"output_tokens":1,"cache_read_input_tokens":1000}}`), 200)
	require.ErrorContains(t, err, "停止原因")
	_, err = parseStreamResponse("openai", []byte("data: {\"choices\":[],"+usage+"}\n\ndata: [DONE]\n\n"), 200)
	require.Error(t, err)
	stream := "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"length\"}],\n" + "data: \"usage\":null}\n\n" + "data: {\"choices\":[]," + usage + "}\n\ndata: [DONE]\n\n"
	parsed, err := parseStreamResponse("openai", []byte(stream), 200)
	require.NoError(t, err, "多 data 行与 usage-only 结尾应保留合法终态")
	require.Equal(t, 1000, parsed.CacheReadInputTokens)
}
