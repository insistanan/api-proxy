package messages

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/logger"
)

func TestStreamLogBudgetDoesNotLimitDelivery(t *testing.T) {
	for _, mode := range []string{"full", "metadata", "disabled", "paused"} {
		t.Run(mode, func(t *testing.T) {
			policy := logger.CapturePolicy{Response: mode != "disabled", Mode: mode, BodyLimit: 256, Paused: mode == "paused"}
			requestCtx := logger.ContextWithCapturePolicy(context.Background(), policy)
			env := &config.EnvConfig{EnableResponseLogs: true}
			ctx := NewStreamContext(requestCtx, env)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(requestCtx)
			text := strings.Repeat("完整输出", 1024)
			for _, event := range []string{
				`{"type":"message_start","message":{}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text),
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_stop"}`,
			} {
				require.NoError(t, ProcessStreamEvent(c, c.Writer, c.Writer, "data: "+event+"\n\n", ctx, env, nil))
			}
			require.Contains(t, w.Body.String(), text)
			require.Contains(t, w.Body.String(), `"type":"message_stop"`)
			require.LessOrEqual(t, ctx.LogBuffer.Len(), policy.BodyLimit)
			if mode == "full" {
				require.True(t, ctx.LogBuffer.Truncated)
				require.True(t, ctx.Synthesizer.Truncated())
			} else {
				require.Zero(t, ctx.LogBuffer.OriginalBytes)
				require.Nil(t, ctx.Synthesizer)
			}
		})
	}
}
