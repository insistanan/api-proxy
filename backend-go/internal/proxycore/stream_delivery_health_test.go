package proxycore

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/api/common"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

type failedDeliveryWriter struct {
	gin.ResponseWriter
	err error
}

func (w failedDeliveryWriter) Write([]byte) (int, error) { return 0, w.err }

func TestStreamDeliveryHealth_ErrorSourceControlsRetryAndHealth(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, source := range []string{"下游写入", "上游读取", "上游失败且通知写入失败"} {
			t.Run(fmt.Sprintf("%s/已提交=%v", source, committed), func(t *testing.T) {
				env := newHealthAttemptEnv(t)
				var requests atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusOK)
				}))
				defer server.Close()
				attempt := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"})
				// 相同网络错误来自不同方向，不能依赖文本判断健康结果。
				ioErr := errors.New("write tcp: wsasend: An existing connection was forcibly closed by the remote host.")
				attempt.HandleSuccess = func(c *gin.Context, _ *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
					if committed {
						c.Writer.WriteHeaderNow()
					}
					writer := failedDeliveryWriter{ResponseWriter: c.Writer, err: ioErr}
					switch source {
					case "下游写入":
						return nil, common.WriteStream(writer, nil, []byte("data: {}\n\n"))
					case "上游失败且通知写入失败":
						c.Writer = writer
						return nil, common.ReportStreamFailure(c, "chat", ioErr)
					default:
						return nil, ioErr
					}
				}
				result := attempt.TryWithModelMappingFailover()
				if source == "下游写入" || committed {
					require.ErrorIs(t, result.LastError, ioErr)
				} else {
					require.NotNil(t, result.FailoverError)
					require.Contains(t, string(result.FailoverError.Body), ioErr.Error())
				}
				require.NoError(t, attempt.Context.Request.Context().Err())
				if source == "下游写入" {
					require.True(t, result.Handled)
					require.Equal(t, channelHealthNeutral, result.Health)
					require.EqualValues(t, 1, requests.Load())
				} else {
					require.Equal(t, channelHealthFailure, result.Health)
					require.Equal(t, committed, result.Handled)
					if committed {
						require.EqualValues(t, 1, requests.Load())
					} else {
						require.EqualValues(t, 2, requests.Load())
					}
				}
				for _, key := range attempt.Upstream.APIKeys {
					keyMetrics := env.metricsManager.GetKeyMetrics(server.URL, key, 0)
					if source == "下游写入" {
						if keyMetrics != nil {
							require.Zero(t, keyMetrics.FailureCount)
						}
					} else if key == "key-a" || !committed {
						require.NotNil(t, keyMetrics)
						require.EqualValues(t, 1, keyMetrics.FailureCount)
					}
				}
			})
		}
	}
}
