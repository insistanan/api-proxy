package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// stubToolVersionProbes 替换本地探测与 npm 查询两个注入点并重置全局缓存；
// 结束后还原。local 返回 (版本, 错误)；distTags 是 npm 桩返回的 dist-tags JSON。
func stubToolVersionProbes(t *testing.T, local func(ctx context.Context, tool string) (string, string), distTags string, status int) {
	t.Helper()
	originalProbe := toolVersionProbeLocal
	originalGet := toolVersionHTTPGet
	toolVersionProbeLocal = local
	toolVersionHTTPGet = func(ctx context.Context, url string) ([]byte, int, error) {
		if status != 0 {
			return []byte(distTags), status, nil
		}
		if strings.Contains(url, "registry.npmjs.org") {
			return []byte(distTags), http.StatusOK, nil
		}
		return nil, 0, fmt.Errorf("测试桩不认识该 URL: %s", url)
	}
	resetToolVersionStatesForTest()
	t.Cleanup(func() {
		toolVersionProbeLocal = originalProbe
		toolVersionHTTPGet = originalGet
		resetToolVersionStatesForTest()
	})
}

func resetToolVersionStatesForTest() {
	toolVersionStatesMu.Lock()
	toolVersionStates = map[string]*toolVersionState{}
	toolVersionStatesMu.Unlock()
}

func performToolVersionRequest(t *testing.T, tool, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/tools/:tool/version", GetToolVersion())
	target := "/tools/" + tool + "/version"
	if query != "" {
		target += "?" + query
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeToolVersionResponse(t *testing.T, recorder *httptest.ResponseRecorder) toolVersionResult {
	t.Helper()
	var result toolVersionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("响应不是合法 JSON: %v %s", err, recorder.Body.String())
	}
	return result
}

func TestGetToolVersionHandler(t *testing.T) {
	t.Run("非法工具名返回 400", func(t *testing.T) {
		stubToolVersionProbes(t, nil, "", 0)
		recorder := performToolVersionRequest(t, "unknown-tool", "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("未安装时 current 为空且不算错误", func(t *testing.T) {
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) { return "", "" },
			`{"latest":"1.2.3"}`, 0)
		recorder := performToolVersionRequest(t, "codex", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d %s", recorder.Code, recorder.Body.String())
		}
		result := decodeToolVersionResponse(t, recorder)
		if result.Current != "" || result.CurrentError != "" {
			t.Fatalf("未安装应返回空 current 且无错误: %+v", result)
		}
		if result.Latest != "1.2.3" {
			t.Fatalf("latest 错误: %+v", result)
		}
		if result.InstallCmd == "" || result.DisplayName == "" || result.Tool != "codex" {
			t.Fatalf("静态字段缺失: %+v", result)
		}
	})

	t.Run("本地探测失败与 npm 失败独立透出", func(t *testing.T) {
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) {
				return "", "执行 codex --version 失败: exit 1"
			},
			`{"latest":"1.2.3"}`, http.StatusForbidden)
		recorder := performToolVersionRequest(t, "codex", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("探测失败不应改变响应状态码: %d %s", recorder.Code, recorder.Body.String())
		}
		result := decodeToolVersionResponse(t, recorder)
		if result.CurrentError == "" {
			t.Fatalf("currentError 未透出: %+v", result)
		}
		if result.Latest != "" || !strings.Contains(result.LatestError, "403") {
			t.Fatalf("latestError 未透出: %+v", result)
		}
	})

	t.Run("dist-tags 缺少 latest 时报错", func(t *testing.T) {
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) { return "1.0.0", "" },
			`{"next":"2.0.0-rc1"}`, 0)
		recorder := performToolVersionRequest(t, "codex", "")
		result := decodeToolVersionResponse(t, recorder)
		if result.Latest != "" || result.LatestError == "" {
			t.Fatalf("缺少 latest 应报错: %+v", result)
		}
	})

	t.Run("TTL 内命中缓存不再探测", func(t *testing.T) {
		var probes int32
		var mu sync.Mutex
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) {
				mu.Lock()
				defer mu.Unlock()
				probes++
				return "1.0.0", ""
			},
			`{"latest":"1.0.0"}`, 0)
		for i := 0; i < 3; i++ {
			recorder := performToolVersionRequest(t, "codex", "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("第 %d 次请求状态码错误: %d", i+1, recorder.Code)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if probes != 1 {
			t.Fatalf("TTL 内应只探测一次, 实际 %d 次", probes)
		}
	})

	t.Run("force=true 绕过缓存", func(t *testing.T) {
		var probes int32
		var mu sync.Mutex
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) {
				mu.Lock()
				defer mu.Unlock()
				probes++
				return "1.0.0", ""
			},
			`{"latest":"1.0.0"}`, 0)
		performToolVersionRequest(t, "codex", "")
		recorder := performToolVersionRequest(t, "codex", "force=true")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码错误: %d", recorder.Code)
		}
		mu.Lock()
		defer mu.Unlock()
		if probes != 2 {
			t.Fatalf("force 应重新探测, 实际探测 %d 次", probes)
		}
	})

	t.Run("单端过期只刷新失效端", func(t *testing.T) {
		var localProbes int32
		var mu sync.Mutex
		stubToolVersionProbes(t,
			func(_ context.Context, tool string) (string, string) {
				mu.Lock()
				defer mu.Unlock()
				localProbes++
				return "1.0.0", ""
			},
			`{"latest":"1.0.0"}`, 0)
		state := getToolVersionState("codex")
		performToolVersionRequest(t, "codex", "")
		// 人为让 latest 端过期（TTL 之外），current 端仍有效。
		state.mu.Lock()
		state.latestAt = time.Now().Add(-2 * toolVersionCacheTTL)
		state.mu.Unlock()
		recorder := performToolVersionRequest(t, "codex", "")
		result := decodeToolVersionResponse(t, recorder)
		mu.Lock()
		defer mu.Unlock()
		if localProbes != 1 {
			t.Fatalf("current 端有效时不应重新本地探测, 实际 %d 次", localProbes)
		}
		if result.Current != "1.0.0" || result.Latest != "1.0.0" {
			t.Fatalf("刷新后应保留有效端结果: %+v", result)
		}
	})
}

func TestToolVersionRetriesOnlyFailedProbeAfterShortTTL(t *testing.T) {
	localCalls := 0
	stubToolVersionProbes(t, func(context.Context, string) (string, string) {
		localCalls++
		return "1.0.0", ""
	}, "", http.StatusNotAcceptable)
	first := getToolVersionResult(context.Background(), "opencode", toolSpecs["opencode"], false)
	if first.LatestError == "" {
		t.Fatal("registry 失败必须显式记录")
	}
	toolVersionHTTPGet = func(context.Context, string) ([]byte, int, error) {
		return []byte(`{"latest":"1.1.0"}`), http.StatusOK, nil
	}
	state := getToolVersionState("opencode")
	state.mu.Lock()
	state.latestAt = time.Now().Add(-2 * toolVersionFailureCacheTTL)
	state.mu.Unlock()
	result := getToolVersionResult(context.Background(), "opencode", toolSpecs["opencode"], false)
	if result.Latest != "1.1.0" || result.LatestError != "" {
		t.Fatalf("网络恢复后普通查询应重新探测: %+v", result)
	}
	if localCalls != 1 || result.Current != "1.0.0" {
		t.Fatalf("成功的本地探测应继续复用缓存: calls=%d result=%+v", localCalls, result)
	}
}
