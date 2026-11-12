// Model list response parsers.
// Supports mainstream upstream response shapes beyond strict OpenAI {data:[{id}]}.
package modelcatalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseModelsFlexible extracts model IDs from a wide range of real-world list-models
// response formats used by OpenAI-compatible gateways, Chinese relays, OneAPI/NewAPI,
// Cohere-like envelopes, and bare arrays.
func parseModelsFlexible(body []byte) ([]string, error) {
	body = trimJSONNoise(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty models response body")
	}

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON models response: %w", err)
	}

	models := extractModelIDs(raw, 0)
	models = dedupeStrings(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("no model ids found in response")
	}
	return models, nil
}

func parseOpenAIModels(body []byte) ([]string, error) {
	return parseModelsFlexible(body)
}

func parseAnthropicModels(body []byte) ([]string, error) {
	// Anthropic uses the same data[].id shape as OpenAI; flexible parser covers it
	// and also tolerates display_name-only entries if id is missing.
	return parseModelsFlexible(body)
}

func parseGeminiModels(body []byte) ([]string, error) {
	var resp struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		// Fall back to flexible extraction for Gemini OpenAI-compat or proxies.
		return parseModelsFlexible(body)
	}
	if len(resp.Models) == 0 {
		return parseModelsFlexible(body)
	}

	models := make([]string, 0, len(resp.Models))
	for _, item := range resp.Models {
		if !supportsGeminiGeneration(item.SupportedGenerationMethods) {
			continue
		}
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		if id != "" {
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("gemini models response contained no generateContent models")
	}
	return dedupeStrings(models), nil
}

func parseOllamaModels(body []byte) ([]string, error) {
	var resp struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return parseModelsFlexible(body)
	}
	models := make([]string, 0, len(resp.Models))
	for _, item := range resp.Models {
		id := strings.TrimSpace(item.Name)
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		if id != "" {
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return parseModelsFlexible(body)
	}
	return dedupeStrings(models), nil
}

func supportsGeminiGeneration(methods []string) bool {
	if len(methods) == 0 {
		// Some proxies omit methods; keep the model rather than drop everything.
		return true
	}
	for _, method := range methods {
		switch method {
		case "generateContent", "streamGenerateContent":
			return true
		}
	}
	return false
}

// anthropicPageMeta captures Anthropic list pagination fields.
type anthropicPageMeta struct {
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func parseAnthropicPage(body []byte) ([]string, anthropicPageMeta, error) {
	var page struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		models, flexErr := parseModelsFlexible(body)
		return models, anthropicPageMeta{}, flexErr
	}
	models := make([]string, 0, len(page.Data))
	for _, item := range page.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.DisplayName)
		}
		if id != "" {
			models = append(models, id)
		}
	}
	return dedupeStrings(models), anthropicPageMeta{HasMore: page.HasMore, LastID: page.LastID}, nil
}

// geminiPageMeta captures Gemini list pagination fields.
type geminiPageMeta struct {
	NextPageToken string `json:"nextPageToken"`
}

func parseGeminiPage(body []byte) ([]string, geminiPageMeta, error) {
	var page struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		models, flexErr := parseModelsFlexible(body)
		return models, geminiPageMeta{}, flexErr
	}
	models := make([]string, 0, len(page.Models))
	for _, item := range page.Models {
		if !supportsGeminiGeneration(item.SupportedGenerationMethods) {
			continue
		}
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		if id != "" {
			models = append(models, id)
		}
	}
	if len(models) == 0 && page.NextPageToken == "" {
		// Non-native shape (e.g. OpenAI-compat under a gemini base).
		flexModels, flexErr := parseModelsFlexible(body)
		return flexModels, geminiPageMeta{}, flexErr
	}
	return dedupeStrings(models), geminiPageMeta{NextPageToken: page.NextPageToken}, nil
}

func trimJSONNoise(body []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	// Some gateways prepend BOM or "callback(...)" — strip UTF-8 BOM only.
	trimmed = strings.TrimPrefix(trimmed, "\ufeff")
	return []byte(trimmed)
}

// extractModelIDs walks common JSON envelopes and collects model identifiers.
// depth is capped to avoid pathological nesting from unexpected payloads.
func extractModelIDs(value any, depth int) []string {
	if value == nil || depth > 6 {
		return nil
	}

	switch typed := value.(type) {
	case string:
		id := normalizeExtractedModelID(typed)
		if id == "" {
			return nil
		}
		return []string{id}

	case []any:
		results := make([]string, 0, len(typed))
		for _, item := range typed {
			results = append(results, extractModelIDs(item, depth+1)...)
		}
		return results

	case map[string]any:
		// Prefer well-known list containers before scanning every value.
		for _, key := range []string{"data", "models", "result", "results", "items", "list", "rows"} {
			if nested, ok := typed[key]; ok {
				if extracted := extractModelIDs(nested, depth+1); len(extracted) > 0 {
					return extracted
				}
			}
		}

		// Single model object: {id|name|model|model_id|modelId}
		if id := modelIDFromObject(typed); id != "" {
			return []string{id}
		}

		// Last resort: scan nested objects (bounded by depth).
		results := make([]string, 0)
		for _, nested := range typed {
			results = append(results, extractModelIDs(nested, depth+1)...)
		}
		return results
	}

	return nil
}

func modelIDFromObject(object map[string]any) string {
	for _, key := range []string{"id", "name", "model", "model_id", "modelId", "model_name", "modelName"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			continue
		}
		if id := normalizeExtractedModelID(text); id != "" {
			return id
		}
	}
	// Anthropic-style fallback.
	if displayName, ok := object["display_name"].(string); ok {
		return normalizeExtractedModelID(displayName)
	}
	return ""
}

func normalizeExtractedModelID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	// Drop obvious non-model noise from overly broad walks.
	lower := strings.ToLower(id)
	switch lower {
	case "list", "model", "models", "object", "error", "success", "ok", "true", "false":
		return ""
	}
	// Ignore ultra-short tokens that are almost never model IDs.
	if len(id) < 2 {
		return ""
	}
	id = strings.TrimPrefix(id, "models/")
	return id
}
