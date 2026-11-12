package modelapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/admin/modelcatalog"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/middleware"
	"github.com/stretchr/testify/require"
)

func newCatalogRouter(t *testing.T) *gin.Engine {
	t.Helper()
	pools := func(ids ...string) []config.ChannelPool {
		result := make([]config.ChannelPool, 0, len(ids))
		for _, id := range ids {
			result = append(result, config.ChannelPool{ID: id, Name: id, ModelMatcher: id})
		}
		return result
	}
	cfg := config.Config{
		Settings:     config.SettingsConfig{Gateway: config.DefaultGatewaySettings()},
		MessagePools: pools("alpha", "beta", "shared"), ChatPools: pools("chat-only", "shared"),
		ResponsesPools: pools("responses-only"), GeminiPools: pools("gemini-a", "gemini-b", "shared"),
		FreeChannels: &config.FreeChannelsConfig{Kilo: config.KiloFreeConfig{Enabled: true, Models: []string{"vendor/model"}}},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	cm, err := config.NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	env := &config.EnvConfig{ProxyAccessKey: "fixture", EnableCORS: true, CORSOrigin: "*"}
	r := gin.New()
	r.Use(middleware.CORSMiddleware(env))
	RegisterRoutes(r, env, cm)
	r.GET("/api/proxy-models", middleware.AdminAuthMiddleware(env), AdminListHandler(cm))
	return r
}

func catalogRequest(t *testing.T, r *gin.Engine, path string, headers map[string]string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("x-api-key", "fixture")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, status, w.Code, "%s: %s", path, w.Body.String())
	return w
}

func decodeCatalog[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return result
}

func TestCatalogScopeAndFormat(t *testing.T) {
	r := newCatalogRouter(t)
	for _, tc := range []struct {
		path    string
		headers map[string]string
		ids     []string
		owner   string
	}{
		{"/v1/models", nil, []string{"alpha", "beta", "chat-only", "free", "gemini-a", "gemini-b", "responses-only", "shared"}, "pool:chat,gemini,messages"},
		{"/v1/models?type=messages", nil, []string{"alpha", "beta", "free", "shared"}, "pool:messages"},
		{"/v1/models", map[string]string{KindHeader: "chat"}, []string{"chat-only", "free", "shared"}, "pool:chat"},
		{"/v1/models?type=CHAT", map[string]string{KindHeader: "chat"}, []string{"chat-only", "free", "shared"}, "pool:chat"},
		{"/v1/models?format=openai", map[string]string{"anthropic-version": "2023-06-01"}, []string{"alpha", "beta", "free", "shared"}, "pool:messages"},
		{"/api/proxy-models?type=messages", map[string]string{"anthropic-version": "2023-06-01"}, []string{"alpha", "beta", "free", "shared"}, "pool:messages"},
	} {
		t.Run(tc.path+tc.owner, func(t *testing.T) {
			response := decodeCatalog[modelcatalog.ModelsResponse](t, catalogRequest(t, r, tc.path, tc.headers, 200))
			require.Equal(t, "list", response.Object)
			ids := make([]string, 0, len(response.Data))
			for _, model := range response.Data {
				ids = append(ids, model.ID)
				if model.ID == "shared" {
					require.Equal(t, tc.owner, model.OwnedBy)
				}
			}
			require.Equal(t, tc.ids, ids)
		})
	}
	response := decodeCatalog[anthropicList](t, catalogRequest(t, r, "/v1/models", map[string]string{"anthropic-version": "2023-06-01"}, 200))
	require.Len(t, response.Data, 4)
	require.Equal(t, "alpha", *response.FirstID)
	require.Equal(t, "shared", *response.LastID)
	require.Equal(t, anthropicModel{ID: "alpha", Type: "model", DisplayName: "alpha", CreatedAt: "1970-01-01T00:00:00Z"}, response.Data[0])
	require.False(t, response.HasMore)
	explicit := decodeCatalog[anthropicList](t, catalogRequest(t, r, "/v1/models?type=responses&format=anthropic", nil, 200))
	require.Len(t, explicit.Data, 2)
	require.Equal(t, "responses-only", explicit.Data[1].ID)
}

func TestCatalogRejectsInvalidOptions(t *testing.T) {
	r := newCatalogRouter(t)
	for _, path := range []string{
		"/v1/models?type=unknown", "/v1/models?type=", "/v1/models?type=chat&type=messages",
		"/v1/models?format=gemini", "/v1/models?format=", "/v1/models?format=openai&format=anthropic",
		"/v1beta/models?type=chat", "/v1beta/models?format=openai", "/api/proxy-models?type=unknown",
		"/v1/models?format=anthropic&limit=0", "/v1/models?format=anthropic&limit=1001", "/v1/models?format=anthropic&limit=abc",
		"/v1/models?format=anthropic&after_id=missing", "/v1/models?format=anthropic&before_id=chat-only",
		"/v1/models?format=anthropic&after_id=shared&before_id=alpha",
		"/v1/models?format=anthropic&limit=1&limit=2", "/v1/models?format=anthropic&after_id=",
		"/v1beta/models?pageSize=-1", "/v1beta/models?pageSize=1001", "/v1beta/models?pageSize=1.5",
		"/v1beta/models?pageToken=!!!", "/v1beta/models?pageToken=bWlzc2luZw", "/v1beta/models?pageToken=",
	} {
		t.Run(path, func(t *testing.T) {
			body := decodeCatalog[map[string]any](t, catalogRequest(t, r, path, nil, 400))
			require.Contains(t, body, "error")
		})
	}
	catalogRequest(t, r, "/v1/models?type=messages", map[string]string{KindHeader: "chat"}, 400)
	catalogRequest(t, r, "/v1/models", map[string]string{KindHeader: ""}, 400)
	catalogRequest(t, r, "/v1/models", map[string]string{KindHeader: "unknown"}, 400)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "fixture")
	req.Header.Add(KindHeader, "chat")
	req.Header.Add(KindHeader, "messages")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
}

func TestCatalogDetailsPreserveSlashesAndScope(t *testing.T) {
	r := newCatalogRouter(t)
	for _, id := range []string{"free/vendor/model", url.PathEscape("free/vendor/model")} {
		model := decodeCatalog[modelcatalog.ModelEntry](t, catalogRequest(t, r, "/v1/models/"+id+"?type=messages", nil, 200))
		require.Equal(t, "free/vendor/model", model.ID)
		anthropic := decodeCatalog[anthropicModel](t, catalogRequest(t, r, "/v1/models/"+id, map[string]string{"anthropic-version": "2023-06-01"}, 200))
		require.Equal(t, "free/vendor/model", anthropic.ID)
		require.Equal(t, "model", anthropic.Type)
		gemini := decodeCatalog[geminiModel](t, catalogRequest(t, r, "/v1beta/models/"+id, nil, 200))
		require.Equal(t, "models/free/vendor/model", gemini.Name)
		require.Equal(t, []string{"generateContent"}, gemini.SupportedGenerationMethods)
	}
	catalogRequest(t, r, "/v1/models/chat-only?type=messages", nil, 404)
	errorBody := decodeCatalog[map[string]any](t, catalogRequest(t, r, "/v1/models/chat-only?format=anthropic", nil, 404))
	require.Equal(t, "error", errorBody["type"])
	geminiError := decodeCatalog[map[string]map[string]any](t, catalogRequest(t, r, "/v1beta/models/alpha", nil, 404))
	require.Equal(t, "NOT_FOUND", geminiError["error"]["status"])
	catalogRequest(t, r, "/v1/models/free/vendor/model?type=images", nil, 404)
}

func TestAnthropicPagination(t *testing.T) {
	r := newCatalogRouter(t)
	for _, tc := range []struct {
		query string
		ids   []string
		more  bool
	}{
		{"&limit=2", []string{"alpha", "beta"}, true},
		{"&limit=2&after_id=beta", []string{"free", "shared"}, false},
		{"&limit=1&before_id=shared", []string{"free"}, true},
		{"&limit=1&before_id=beta", []string{"alpha"}, false},
		{"&after_id=shared", []string{}, false},
		{"&before_id=alpha", []string{}, false},
		{"&after_id=alpha&before_id=shared", []string{"beta", "free"}, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			page := decodeCatalog[anthropicList](t, catalogRequest(t, r, "/v1/models?format=anthropic"+tc.query, nil, 200))
			ids := make([]string, 0, len(page.Data))
			for _, model := range page.Data {
				ids = append(ids, model.ID)
			}
			require.Equal(t, tc.ids, ids)
			require.Equal(t, tc.more, page.HasMore)
			if len(ids) == 0 {
				require.NotNil(t, page.Data)
				require.Nil(t, page.FirstID)
				require.Nil(t, page.LastID)
			} else {
				require.Equal(t, ids[0], *page.FirstID)
				require.Equal(t, ids[len(ids)-1], *page.LastID)
			}
		})
	}
}

func TestGeminiPagination(t *testing.T) {
	r := newCatalogRouter(t)
	first := decodeCatalog[geminiList](t, catalogRequest(t, r, "/v1beta/models?pageSize=2", nil, 200))
	require.Len(t, first.Models, 2)
	require.Equal(t, "models/free", first.Models[0].Name)
	require.Equal(t, "models/gemini-a", first.Models[1].Name)
	require.NotEmpty(t, first.NextPageToken)
	secondResponse := catalogRequest(t, r, "/v1beta/models?pageSize=2&pageToken="+url.QueryEscape(first.NextPageToken), nil, 200)
	second := decodeCatalog[geminiList](t, secondResponse)
	require.Len(t, second.Models, 2)
	require.Equal(t, "models/gemini-b", second.Models[0].Name)
	require.Equal(t, "models/shared", second.Models[1].Name)
	require.Empty(t, second.NextPageToken)
	require.NotContains(t, decodeCatalog[map[string]any](t, secondResponse), "nextPageToken")
}

func TestEmptyCatalogFormats(t *testing.T) {
	r := gin.New()
	RegisterRoutes(r, &config.EnvConfig{ProxyAccessKey: "fixture"}, nil)
	for path, expected := range map[string]string{
		"/v1/models":                  `{"object":"list","data":[]}`,
		"/v1/models?format=anthropic": `{"data":[],"has_more":false,"first_id":null,"last_id":null}`,
		"/v1beta/models":              `{"models":[]}`,
	} {
		require.JSONEq(t, expected, catalogRequest(t, r, path, nil, 200).Body.String())
	}
}

func TestCatalogAuthAndCORS(t *testing.T) {
	r := newCatalogRouter(t)
	for _, path := range []string{"/v1/models", "/v1/models/alpha", "/v1beta/models", "/v1beta/models/gemini-a", "/api/proxy-models"} {
		for _, query := range []string{"", "?key=fixture", "?api_key=fixture"} {
			req := httptest.NewRequest(http.MethodGet, path+query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusUnauthorized, w.Code, path+query)
		}
	}
	for _, headers := range []map[string]string{
		{"x-api-key": "", "Authorization": "Bearer fixture"},
		{"x-api-key": "", "x-goog-api-key": "fixture"},
	} {
		catalogRequest(t, r, "/v1beta/models", headers, 200)
	}
	req := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	req.Header.Set("Origin", "https://client.example")
	req.Header.Set("Access-Control-Request-Headers", "anthropic-version, x-api-proxy-type, x-api-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "anthropic-version")
	require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), KindHeader)
}
