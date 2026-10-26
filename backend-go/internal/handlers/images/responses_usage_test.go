package images

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesImageUsageMapsOpenAICachedTokens(t *testing.T) {
	usage, err := responsesImageUsage(json.RawMessage(`{
		"input_tokens":9233,
		"output_tokens":702,
		"input_tokens_details":{"cached_tokens":4224},
		"cache_creation_input_tokens":5010
	}`))
	require.NoError(t, err)
	require.Equal(t, 9233, usage.InputTokens)
	require.Equal(t, 702, usage.OutputTokens)
	require.Equal(t, 4224, usage.CacheReadInputTokens)
	require.Equal(t, 5010, usage.CacheCreationInputTokens)
}

func TestResponsesImageUsageRejectsInvalidObject(t *testing.T) {
	_, err := responsesImageUsage(json.RawMessage(`[]`))
	require.ErrorContains(t, err, "不是有效对象")
}
