package modelcatalog

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/infra/utils"
)

const (
	// azureModelsAPIVersion is a widely accepted Azure OpenAI query version for list models.
	azureModelsAPIVersion = "2024-06-01"
	// maxModelListPages caps pagination loops against misbehaving upstreams.
	maxModelListPages = 20
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
	openaiFamily := []discoveryMethod{
		{Name: "openai-v1", Kind: "openai-v1"},
		{Name: "openai-root", Kind: "openai-root"},
		{Name: "openai-openai", Kind: "openai-openai"},
		{Name: "openai-api", Kind: "openai-api"},
		{Name: "openai-compat", Kind: "openai-compat"},
		{Name: "gemini-openai", Kind: "gemini-openai"},
		{Name: "azure", Kind: "azure"},
		{Name: "anthropic", Kind: "anthropic"},
		{Name: "gemini", Kind: "gemini"},
		{Name: "ollama", Kind: "ollama"},
	}

	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "anthropic":
		return []discoveryMethod{
			{Name: "anthropic", Kind: "anthropic"},
			{Name: "openai-v1", Kind: "openai-v1"},
			{Name: "openai-root", Kind: "openai-root"},
			{Name: "openai-openai", Kind: "openai-openai"},
			{Name: "openai-api", Kind: "openai-api"},
			{Name: "openai-compat", Kind: "openai-compat"},
			{Name: "azure", Kind: "azure"},
			{Name: "gemini", Kind: "gemini"},
			{Name: "gemini-openai", Kind: "gemini-openai"},
			{Name: "ollama", Kind: "ollama"},
		}
	case "gemini":
		return []discoveryMethod{
			{Name: "gemini", Kind: "gemini"},
			{Name: "gemini-openai", Kind: "gemini-openai"},
			{Name: "openai-v1", Kind: "openai-v1"},
			{Name: "openai-root", Kind: "openai-root"},
			{Name: "openai-openai", Kind: "openai-openai"},
			{Name: "openai-api", Kind: "openai-api"},
			{Name: "openai-compat", Kind: "openai-compat"},
			{Name: "azure", Kind: "azure"},
			{Name: "anthropic", Kind: "anthropic"},
			{Name: "ollama", Kind: "ollama"},
		}
	case "ollama":
		return []discoveryMethod{
			{Name: "ollama", Kind: "ollama"},
			{Name: "openai-v1", Kind: "openai-v1"},
			{Name: "openai-root", Kind: "openai-root"},
			{Name: "openai-api", Kind: "openai-api"},
		}
	case "azure":
		return []discoveryMethod{
			{Name: "azure", Kind: "azure"},
			{Name: "openai-openai", Kind: "openai-openai"},
			{Name: "openai-v1", Kind: "openai-v1"},
			{Name: "openai-root", Kind: "openai-root"},
			{Name: "openai-api", Kind: "openai-api"},
			{Name: "openai-compat", Kind: "openai-compat"},
		}
	default:
		// openai / responses / chat / empty / unknown
		return openaiFamily
	}
}

func discoverModelsForKey(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURLs []string,
) ([]string, string, string, error) {
	methods := orderedDiscoveryMethods(upstream.ServiceType)
	attemptErrors := make([]string, 0, len(baseURLs)*len(methods))

	for _, baseURL := range baseURLs {
		for _, method := range methods {
			models, err := fetchModelsByMethod(ctx, cfgManager, upstream, apiKey, baseURL, method)
			if err != nil {
				attemptErrors = append(attemptErrors, fmt.Sprintf("%s@%s: %v", method.Name, shortenURLForLog(baseURL), err))
				continue
			}
			models = dedupeStrings(models)
			if len(models) > 0 {
				log.Printf("[Models-Discover] 渠道 %s key=%s baseURL=%s method=%s 获取模型 %d 个",
					upstream.Name, keyFingerprint(apiKey), baseURL, method.Name, len(models))
				return models, baseURL, method.Name, nil
			}
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s@%s: empty model list", method.Name, shortenURLForLog(baseURL)))
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
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildOllamaTagsURL(baseURL), []string{"none", "bearer"})
	case "azure":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildAzureModelsURL(baseURL), []string{"azure", "bearer"})
	case "openai-v1":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildVersionedModelsURL(baseURL), openaiAuthOrder(apiKey))
	case "openai-root":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildRootModelsURL(baseURL), openaiAuthOrder(apiKey))
	case "openai-openai":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildOpenAIPathModelsURL(baseURL), openaiAuthOrder(apiKey))
	case "openai-api":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildAPIVersionedModelsURL(baseURL), openaiAuthOrder(apiKey))
	case "openai-compat":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildCompatibleModeModelsURL(baseURL), openaiAuthOrder(apiKey))
	case "gemini-openai":
		return fetchSingleModelsPage(ctx, cfgManager, upstream, apiKey, baseURL, method.Kind, buildGeminiOpenAIModelsURL(baseURL), []string{"gemini", "bearer", "x-api-key"})
	default:
		return nil, fmt.Errorf("unknown discovery method: %s", method.Kind)
	}
}

func openaiAuthOrder(apiKey string) []string {
	// sk-ant-* keys prefer x-api-key; everything else prefers Bearer, then alternates on 401/403.
	if strings.HasPrefix(strings.TrimSpace(apiKey), "sk-ant-") {
		return []string{"x-api-key", "bearer", "azure"}
	}
	return []string{"bearer", "x-api-key", "azure"}
}

func fetchSingleModelsPage(
	ctx context.Context,
	cfgManager *config.ConfigManager,
	upstream *config.UpstreamConfig,
	apiKey string,
	baseURL string,
	kind string,
	reqURL string,
	authStyles []string,
) ([]string, error) {
	var lastErr error
	for _, authStyle := range authStyles {
		body, statusCode, err := doModelsRequest(ctx, cfgManager, upstream, apiKey, reqURL, authStyle)
		if err != nil {
			lastErr = err
			continue
		}
		if statusCode < 200 || statusCode >= 300 {
			lastErr = fmt.Errorf("%s returned %d", kind, statusCode)
			// Only rotate auth on auth failures; other codes mean wrong path/protocol.
			if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
				continue
			}
			return nil, lastErr
		}

		models, parseErr := parseBodyByKind(kind, body)
		if parseErr != nil {
			return nil, fmt.Errorf("%s parse: %w", kind, parseErr)
		}
		return models, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s request failed", kind)
	}
	return nil, lastErr
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
			// Some Claude-compatible relays reject anthropic headers on /v1/models;
			// fall back to bearer on same URL once.
			if page == 0 && (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) {
				body, statusCode, err = doModelsRequest(ctx, cfgManager, upstream, apiKey, reqURL, "bearer")
				if err != nil {
					return nil, err
				}
			}
			if statusCode < 200 || statusCode >= 300 {
				return nil, fmt.Errorf("anthropic returned %d", statusCode)
			}
		}

		models, meta, parseErr := parseAnthropicPage(body)
		if parseErr != nil {
			return nil, fmt.Errorf("anthropic parse: %w", parseErr)
		}
		allModels = append(allModels, models...)
		if !meta.HasMore || meta.LastID == "" || meta.LastID == afterID {
			break
		}
		afterID = meta.LastID
	}

	return dedupeStrings(allModels), nil
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
			return nil, fmt.Errorf("gemini returned %d", statusCode)
		}

		models, meta, parseErr := parseGeminiPage(body)
		if parseErr != nil {
			return nil, fmt.Errorf("gemini parse: %w", parseErr)
		}
		allModels = append(allModels, models...)
		if meta.NextPageToken == "" || meta.NextPageToken == pageToken {
			break
		}
		pageToken = meta.NextPageToken
	}

	return dedupeStrings(allModels), nil
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	body = utils.DecompressGzipIfNeeded(resp, body)
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
		// Query key is already attached by buildGeminiModelsURL when present.
	case "azure":
		req.Header.Set("api-key", apiKey)
	case "x-api-key":
		req.Header.Set("x-api-key", apiKey)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	default:
		utils.SetAuthenticationHeader(req.Header, apiKey)
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

func buildOpenAIPathModelsURL(baseURL string) string {
	baseURL, skipVersionPrefix := normalizeBaseURL(baseURL)
	if skipVersionPrefix {
		return baseURL + "/models"
	}
	// Avoid double /openai/openai when user already pointed at an Azure-style base.
	if strings.HasSuffix(baseURL, "/openai") {
		if versionSuffixPattern.MatchString(baseURL) {
			return baseURL + "/models"
		}
		return baseURL + "/v1/models"
	}
	lower := strings.ToLower(baseURL)
	if strings.Contains(lower, "/openai/") {
		return buildVersionedModelsURL(baseURL)
	}
	return baseURL + "/openai/v1/models"
}

func buildAPIVersionedModelsURL(baseURL string) string {
	baseURL, skipVersionPrefix := normalizeBaseURL(baseURL)
	if skipVersionPrefix {
		return baseURL + "/models"
	}
	lower := strings.ToLower(baseURL)
	if strings.HasSuffix(lower, "/api") {
		if versionSuffixPattern.MatchString(baseURL) {
			return baseURL + "/models"
		}
		return baseURL + "/v1/models"
	}
	if strings.Contains(lower, "/api/") {
		return buildVersionedModelsURL(baseURL)
	}
	return baseURL + "/api/v1/models"
}

func buildCompatibleModeModelsURL(baseURL string) string {
	baseURL, skipVersionPrefix := normalizeBaseURL(baseURL)
	if skipVersionPrefix {
		return baseURL + "/models"
	}
	lower := strings.ToLower(baseURL)
	if strings.Contains(lower, "compatible-mode") {
		return buildVersionedModelsURL(baseURL)
	}
	return baseURL + "/compatible-mode/v1/models"
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

func buildGeminiModelsURL(baseURL, apiKey string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	baseURL = stripGeminiVersionSuffix(baseURL)
	reqURL := baseURL + "/v1beta/models"
	if strings.TrimSpace(apiKey) == "" {
		return reqURL
	}
	query := url.Values{}
	query.Set("key", apiKey)
	return appendQuery(reqURL, query)
}

func buildGeminiOpenAIModelsURL(baseURL string) string {
	baseURL, _ = normalizeBaseURL(baseURL)
	baseURL = stripGeminiVersionSuffix(baseURL)
	// Google OpenAI compatibility: /v1beta/openai/models
	return baseURL + "/v1beta/openai/models"
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

func shortenURLForLog(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if len(rawURL) <= 64 {
		return rawURL
	}
	return rawURL[:61] + "..."
}
