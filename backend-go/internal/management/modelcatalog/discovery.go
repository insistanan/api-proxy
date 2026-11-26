package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/httpclient"
	"insistanan/api-proxy/internal/infra/utils"
)

const (
	// azureModelsAPIVersion is a widely accepted Azure OpenAI query version for list models.
	azureModelsAPIVersion = "2024-06-01"
	// maxModelListPages caps pagination loops against misbehaving upstreams.
	maxModelListPages     = 20
	maxModelListBodyBytes = 8 << 20
	// maxModelListPageSize asks Anthropic/Gemini for larger pages when supported.
	maxModelListPageSize = 1000
)

// discoveryMethod describes one upstream models-list strategy.
type discoveryMethod struct {
	Name string
	// Kind selects URL builder, auth and parser family.
	Kind string
}

func orderedDiscoveryMethods(serviceType string) []discoveryMethod {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "anthropic":
		return []discoveryMethod{{Name: "anthropic", Kind: "anthropic"}}
	case "gemini":
		return []discoveryMethod{{Name: "gemini", Kind: "gemini"}}
	case "ollama":
		return []discoveryMethod{{Name: "ollama", Kind: "ollama"}}
	case "azure":
		return []discoveryMethod{{Name: "azure", Kind: "azure"}}
	case "openai", "responses", "chat", "images", "embeddings", "decisions":
		return []discoveryMethod{{Name: "openai-v1", Kind: "openai-v1"}, {Name: "openai-root", Kind: "openai-root"}}
	default:
		return nil
	}
}

type modelsHTTPError struct {
	status int
}

func (e *modelsHTTPError) Error() string { return fmt.Sprintf("模型目录返回 HTTP %d", e.status) }

func discoverModelsForKey(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURLs []string,
) ([]string, string, string, error) {
	methods := orderedDiscoveryMethods(upstream.ServiceType)
	if len(methods) == 0 {
		return nil, "", "", fmt.Errorf("不支持的模型发现协议 %q", upstream.ServiceType)
	}
	attemptErrors := make([]string, 0, len(baseURLs)*len(methods))

	for _, baseURL := range baseURLs {
		for _, method := range methods {
			models, err := fetchModelsByMethod(ctx, cfgManager, upstream, apiKey, baseURL, method)
			if err != nil {
				var statusErr *modelsHTTPError
				if !errors.As(err, &statusErr) || statusErr.status != http.StatusNotFound {
					return nil, "", "", upstreamreq.RedactError(err, []string{baseURL}, apiKey)
				}
				attemptErrors = append(attemptErrors, fmt.Sprintf("%s@%s: %v", method.Name, upstreamreq.RedactURL(baseURL), err))
				continue
			}
			models = utils.DedupeSortedStrings(models)
			log.Printf("[Models-Discover] 渠道 %s key=%s baseURL=%s method=%s 获取模型 %d 个",
				upstream.Name, keyFingerprint(apiKey), upstreamreq.RedactURL(baseURL), method.Name, len(models))
			return models, baseURL, method.Name, nil
		}
	}

	if len(attemptErrors) == 0 {
		return nil, "", "", fmt.Errorf("models endpoint returned no models")
	}
	// Keep the error message useful but bounded.
	const maxErrorParts = 8
	if len(attemptErrors) > maxErrorParts {
		attemptErrors = append(attemptErrors[:maxErrorParts], fmt.Sprintf("...(%d more)", len(attemptErrors)-maxErrorParts))
	}
	return nil, "", "", fmt.Errorf("all discovery methods failed: %s", strings.Join(attemptErrors, "; "))
}

func fetchModelsByMethod(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURL string,
	method discoveryMethod,
) ([]string, error) {
	switch method.Kind {
	case "anthropic":
		return fetchAnthropicModelsPaginated(ctx, cfgManager, upstream, apiKey, baseURL)
	case "gemini":
		return fetchGeminiModelsPaginated(ctx, cfgManager, upstream, apiKey, baseURL)
	case "ollama":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, method.Kind, buildOllamaTagsURL(baseURL), "bearer")
	case "azure":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, method.Kind, buildAzureModelsURL(baseURL), "azure")
	case "openai-v1":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, method.Kind, buildVersionedModelsURL(baseURL), "bearer")
	case "openai-root":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, method.Kind, buildRootModelsURL(baseURL), "bearer")
	default:
		return nil, fmt.Errorf("unknown discovery method: %s", method.Kind)
	}
}

func fetchSingleModelsPage(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	kind string,
	reqURL string,
	authStyle string,
) ([]string, error) {
	body, statusCode, err := doModelsRequest(ctx, cfgManager, upstream, apiKey, reqURL, authStyle)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, &modelsHTTPError{status: statusCode}
	}
	models, err := parseBodyByKind(kind, body)
	if err != nil {
		return nil, fmt.Errorf("%s 目录解析失败: %w", kind, err)
	}
	return models, nil
}

func fetchAnthropicModelsPaginated(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURL string,
) ([]string, error) {
	allModels := make([]string, 0, 64)
	afterID := ""
	seen := make(map[string]bool)

	for page := 0; page < maxModelListPages; page++ {
		reqURL := buildAnthropicModelsURL(baseURL)
		query := url.Values{}
		query.Set("limit", fmt.Sprintf("%d", maxModelListPageSize))
		if afterID != "" {
			query.Set("after_id", afterID)
		}
		reqURL = appendQuery(reqURL, query)

		body, statusCode, err := doModelsRequest(ctx, cfgManager, upstream, apiKey, reqURL, "anthropic")
		if err != nil {
			return nil, err
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, &modelsHTTPError{status: statusCode}
		}

		models, meta, parseErr := parseAnthropicPage(body)
		if parseErr != nil {
			return nil, fmt.Errorf("anthropic parse: %w", parseErr)
		}
		allModels = append(allModels, models...)
		if !meta.HasMore {
			return utils.DedupeSortedStrings(allModels), nil
		}
		if meta.LastID == "" || seen[meta.LastID] {
			return nil, fmt.Errorf("Anthropic 模型目录分页未完成：游标缺失或重复")
		}
		seen[meta.LastID] = true
		afterID = meta.LastID
	}

	return nil, fmt.Errorf("模型目录超过 %d 页，结果未完整取得", maxModelListPages)
}

func fetchGeminiModelsPaginated(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURL string,
) ([]string, error) {
	allModels := make([]string, 0, 64)
	pageToken := ""
	seen := make(map[string]bool)

	for page := 0; page < maxModelListPages; page++ {
		reqURL := buildGeminiModelsURL(baseURL, apiKey)
		query := url.Values{}
		query.Set("pageSize", "100")
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		reqURL = appendQuery(reqURL, query)

		body, statusCode, err := doModelsRequest(ctx, cfgManager, upstream, apiKey, reqURL, "gemini")
		if err != nil {
			return nil, err
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, &modelsHTTPError{status: statusCode}
		}

		models, meta, parseErr := parseGeminiPage(body)
		if parseErr != nil {
			return nil, fmt.Errorf("gemini parse: %w", parseErr)
		}
		allModels = append(allModels, models...)
		if meta.NextPageToken == "" {
			return utils.DedupeSortedStrings(allModels), nil
		}
		if seen[meta.NextPageToken] {
			return nil, fmt.Errorf("Gemini 模型目录分页未完成：游标重复")
		}
		seen[meta.NextPageToken] = true
		pageToken = meta.NextPageToken
	}

	return nil, fmt.Errorf("模型目录超过 %d 页，结果未完整取得", maxModelListPages)
}

func parseBodyByKind(kind string, body []byte) ([]string, error) {
	switch kind {
	case "gemini":
		return parseGeminiModels(body)
	case "ollama":
		return parseOllamaModels(body)
	default:
		// openai-*, azure, gemini-openai, anthropic single-page
		return parseModelsFlexible(body)
	}
}

func doModelsRequest(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	reqURL string,
	authStyle string,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	applyModelsAuth(req, apiKey, authStyle)

	client, err := httpclient.GetManager().GetStandardClientForUpstream(modelsRequestTimeout, cfgManager, upstream)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelListBodyBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > maxModelListBodyBytes {
		return nil, resp.StatusCode, fmt.Errorf("模型目录单页超过 8 MiB")
	}
	body, _, err = codec.DecompressBodyWithLimit(resp.Header.Get("Content-Encoding"), body, maxModelListBodyBytes)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("模型目录解压失败: %w", err)
	}
	return body, resp.StatusCode, nil
}

func applyModelsAuth(req *http.Request, apiKey string, authStyle string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || authStyle == "none" {
		return
	}

	switch authStyle {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		req.Header.Set("x-goog-api-key", apiKey)
	case "azure":
		req.Header.Set("api-key", apiKey)
	case "x-api-key":
		req.Header.Set("x-api-key", apiKey)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	default:
		upstreamreq.SetAuthenticationHeader(req.Header, apiKey)
	}
}

func buildVersionedModelsURL(baseURL string) string {
	baseURL, skipVersionPrefix := normalizeBaseURL(baseURL)
	endpoint := "/models"
	if !skipVersionPrefix && !versionSuffixPattern.MatchString(baseURL) {
		endpoint = "/v1" + endpoint
	}
	return baseURL + endpoint
}

func buildRootModelsURL(baseURL string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	return baseURL + "/models"
}

func buildAnthropicModelsURL(baseURL string) string {
	return buildVersionedModelsURL(baseURL)
}

func buildAzureModelsURL(baseURL string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	// Azure OpenAI: {endpoint}/openai/models?api-version=...
	reqURL := baseURL
	lower := strings.ToLower(baseURL)
	switch {
	case strings.HasSuffix(lower, "/openai/models"), strings.HasSuffix(lower, "/models"):
		// already a models path
	case strings.HasSuffix(lower, "/openai"):
		reqURL = baseURL + "/models"
	case strings.Contains(lower, "/openai/"):
		reqURL = baseURL + "/models"
	default:
		reqURL = baseURL + "/openai/models"
	}
	query := url.Values{}
	query.Set("api-version", azureModelsAPIVersion)
	return appendQuery(reqURL, query)
}

func buildGeminiModelsURL(baseURL, _ string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	baseURL = stripGeminiVersionSuffix(baseURL)
	return baseURL + "/v1beta/models"
}

func buildOllamaTagsURL(baseURL string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	return baseURL + "/api/tags"
}

func stripGeminiVersionSuffix(baseURL string) string {
	for {
		lower := strings.ToLower(baseURL)
		switch {
		case strings.HasSuffix(lower, "/v1beta"):
			baseURL = baseURL[:len(baseURL)-len("/v1beta")]
		case strings.HasSuffix(lower, "/v1"):
			baseURL = baseURL[:len(baseURL)-len("/v1")]
		default:
			return strings.TrimRight(baseURL, "/")
		}
		baseURL = strings.TrimRight(baseURL, "/")
	}
}

func normalizeBaseURL(baseURL string) (string, bool) {
	baseURL = strings.TrimSpace(baseURL)
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Users often paste a full chat/completions (or similar) endpoint as base URL.
	// Strip known accidental API path suffixes so /models discovery can work.
	baseURL = stripAccidentalAPIPath(baseURL)
	return baseURL, skipVersionPrefix
}

func stripAccidentalAPIPath(baseURL string) string {
	lower := strings.ToLower(baseURL)
	suffixes := []string{
		"/chat/completions",
		"/completions",
		"/messages",
		"/responses",
		"/embeddings",
		"/images/generations",
		"/audio/speech",
		"/audio/transcriptions",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return baseURL[:len(baseURL)-len(suffix)]
		}
	}
	return baseURL
}

func appendQuery(rawURL string, values url.Values) string {
	if len(values) == 0 {
		return rawURL
	}
	encoded := values.Encode()
	if encoded == "" {
		return rawURL
	}
	if strings.Contains(rawURL, "?") {
		return rawURL + "&" + encoded
	}
	return rawURL + "?" + encoded
}
