package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

// setupRouterWithAuth 按生产装配方式搭建路由：
// WebAuthMiddleware 全局（页面与 Web UI 开关策略）+
// AdminAuthMiddleware 显式绑定在 /api 与 /admin 路由组上（管理鉴权）。
// 与 main.go 的 newApp 保持同一结构，避免测试装配与生产脱节。
func setupRouterWithAuth(envCfg *config.EnvConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSMiddleware(envCfg))
	r.Use(WebAuthMiddleware(envCfg, nil))

	// Protected management API（路由组绑定管理鉴权）
	apiGroup := r.Group("/api", AdminAuthMiddleware(envCfg))
	apiGroup.GET("/channels", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	// 动态 ID 路由：provider ID 可合法包含 .js / .css 等后缀
	apiGroup.GET("/settings/pi-agent/providers/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	apiGroup.PUT("/settings/pi-agent/providers/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	apiGroup.DELETE("/settings/pi-agent/providers/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Protected admin endpoint
	adminGroup := r.Group("/admin", AdminAuthMiddleware(envCfg))
	adminGroup.POST("/config/save", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	adminGroup.GET("/dev/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// SPA routes should pass through without access key
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "home")
	})
	r.GET("/dashboard", func(c *gin.Context) {
		c.String(http.StatusOK, "dashboard")
	})

	// 嵌入前端静态资源路径（只放行已注册资源位置）
	r.GET("/assets/app.js", func(c *gin.Context) {
		c.String(http.StatusOK, "asset")
	})

	return r
}

func TestWebAuthMiddleware_APIRequiresKey(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	t.Run("missing key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("wrong key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
		req.Header.Set("x-api-key", "wrong")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("correct key allows access", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestWebAuthMiddleware_SPAPassesThrough(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWebAuthMiddleware_RegisteredAssetsPassThrough(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestWebAuthMiddleware_SuffixDisguiseCannotBypassAdminAuth 是 R1 的核心验收：
// 动态路由参数（如 pi-agent provider ID）可以合法包含 .js / .css 等后缀，
// 这些已注册的管理方法不得因后缀被当成静态资源放行。
func TestWebAuthMiddleware_SuffixDisguiseCannotBypassAdminAuth(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	providerIDs := []string{"demo.js", "demo.css", "demo.png", "a.b.js"}
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}

	for _, id := range providerIDs {
		for _, method := range methods {
			for _, keyHeader := range []string{"", "wrong"} {
				req := httptest.NewRequest(method, "/api/settings/pi-agent/providers/"+id, nil)
				if keyHeader != "" {
					req.Header.Set("x-api-key", keyHeader)
				}
				w := httptest.NewRecorder()

				router.ServeHTTP(w, req)

				if w.Code != http.StatusUnauthorized {
					t.Errorf("provider %s method %s key=%q: status = %d, want %d",
						id, method, keyHeader, w.Code, http.StatusUnauthorized)
				}
			}
		}
	}

	// 正确密钥仍能访问（已有合法后缀 ID 不受影响）
	for _, id := range providerIDs {
		req := httptest.NewRequest(http.MethodGet, "/api/settings/pi-agent/providers/"+id, nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("provider %s with correct key: status = %d, want %d", id, w.Code, http.StatusOK)
		}
	}
}

func TestWebAuthMiddleware_AdminRequiresKey(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	t.Run("missing key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/config/save", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("correct key allows access", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/config/save", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestWebAuthMiddleware_DevInfoRequiresKeyInDevelopment(t *testing.T) {
	envCfg := &config.EnvConfig{
		Env:            "development",
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}
	router := setupRouterWithAuth(envCfg)

	t.Run("missing key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/dev/info", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("correct key allows access", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/dev/info", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestWebAuthMiddleware_AllowsV1BetaRoutesWhenWebUIDisabled(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    false,
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(WebAuthMiddleware(envCfg, nil))

	r.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWebAuthMiddleware_AdminEndpointsUnavailableWhenWebUIDisabled(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    false,
	}
	router := setupRouterWithAuth(envCfg)

	// Web UI 关闭（纯 API 模式）时管理端点不可用：即使携带正确密钥也返回 404
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestWebAuthMiddleware_CORSPreflightDoesNotHitBusinessHandler 验证：
// OPTIONS 预检在 CORS 中间件处终止（204），不会执行路由组的业务 handler
// 与管理鉴权（预检不携带密钥）。
func TestWebAuthMiddleware_CORSPreflightDoesNotHitBusinessHandler(t *testing.T) {
	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
		EnableCORS:     true,
	}
	router := setupRouterWithAuth(envCfg)

	req := httptest.NewRequest(http.MethodOptions, "/api/settings/pi-agent/providers/demo.js", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	// 业务 handler 未执行（响应体应为空）
	if body := w.Body.String(); body != "" {
		t.Fatalf("preflight should not execute business handler, got body: %s", body)
	}
}
