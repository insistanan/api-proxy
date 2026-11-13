package proxycore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateRequestTPS(t *testing.T) {
	// 0 output tokens -> 0 TPS
	assert.Equal(t, float64(0), calculateRequestTPS(0, 1000, 200, true))

	// 非流式：100 tokens, 2000ms -> 50.0 TPS
	assert.Equal(t, float64(50), calculateRequestTPS(100, 2000, 0, false))

	// 流式：100 tokens, 2500ms 总耗时, 500ms TTFT -> 生成耗时 2000ms -> 50.0 TPS
	assert.Equal(t, float64(50), calculateRequestTPS(100, 2500, 500, true))

	// 流式 TTFT 异常 >= 总耗时：fallback 到总耗时
	assert.Equal(t, float64(50), calculateRequestTPS(100, 2000, 2500, true))
}
