package chat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/stretchr/testify/require"
)

type chatResetBody struct {
	*strings.Reader
	err error
}

func (b *chatResetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}
func (b *chatResetBody) Close() error { return nil }

func TestChatStreamLifecycle(t *testing.T) {
	resetErr := errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")
	prefix := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
	for _, tc := range []struct {
		name, tail string
		complete   bool
	}{
		{"普通EOF截断", "", false},
		{"结束原因后usage尾包", "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":8}}", true},
		{"DONE后内容忽略", "data: [DONE]\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"}}]}\n\n", true},
		{"缺少换行的DONE", "data: [DONE]", true},
		{"上游错误", "data: {\"error\":{\"message\":\"boom\"}}\n\n", false},
		{"非法JSON", "data: {broken\n\n", false},
	} {
		for _, readErr := range []error{io.EOF, resetErr} {
			t.Run(tc.name+"/"+readErr.Error(), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: &chatResetBody{Reader: strings.NewReader(prefix + tc.tail), err: readErr}}
				usage, err := handleStreamSuccess(c, resp, &config.EnvConfig{}, time.Now())
				if tc.complete {
					require.NoError(t, err)
					if strings.Contains(tc.tail, "usage") {
						require.NotNil(t, usage)
						require.Equal(t, 8, usage.OutputTokens)
					}
				} else {
					require.Error(t, err)
				}
				require.NotContains(t, recorder.Body.String(), "late")
				if strings.Contains(tc.tail, "[DONE]") {
					require.Contains(t, recorder.Body.String(), "data: [DONE]\n\n")
				}
				if tc.complete {
					require.True(t, strings.HasSuffix(recorder.Body.String(), "\n\n"))
				}
			})
		}
	}
}

func TestChatStreamRequiresEveryChoiceToFinish(t *testing.T) {
	state := chatStreamCompletion{choices: make(map[int]bool)}
	require.NoError(t, state.consume(`data: {"choices":[{"index":0},{"index":1}]}`))
	require.NoError(t, state.consume(`data: {"choices":[{"index":0,"finish_reason":"stop"}]}`))
	require.False(t, state.completed())
	require.NoError(t, state.consume(`data: {"choices":[{"index":1,"finish_reason":"length"}]}`))
	require.True(t, state.completed())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, &config.EnvConfig{}, time.Now())
	require.ErrorIs(t, err, context.Canceled)
}
