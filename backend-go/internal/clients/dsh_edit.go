package clients

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// saveDSHConfiguration 在应用服务内准备完整变更，再按凭据、配置、旧凭据清理的顺序发布。
func saveDSHConfiguration(req saveDSHSettingsRequest) (string, string, error) {
	dshSettingsMu.Lock()
	defer dshSettingsMu.Unlock()
	path, credentialsPath := resolveDSHSettingsPath(), resolveDSHCredentialsPath()
	if req.Revision == "" {
		return path, credentialsPath, filetxn.ErrRevisionRequired
	}
	root, previous, _, err := readDSHSettingsFile(path)
	if err != nil {
		return path, credentialsPath, err
	}
	credentialsRaw, err := filetxn.Read(credentialsPath)
	if err != nil {
		return path, credentialsPath, err
	}
	if req.Revision != filetxn.RevisionSet(previous, credentialsRaw) {
		return path, credentialsPath, filetxn.ErrConflict
	}
	prepared, cleaned, err := prepareDSHCredentials(credentialsPath, &req)
	if err != nil {
		return path, credentialsPath, err
	}
	piAI := objectValue(root["llm-pi-ai"])
	piAI["providers"] = mergeDSHProviders(objectValue(piAI["providers"]), req.Providers)
	root["llm-pi-ai"] = piAI
	if req.DefaultModel == nil {
		delete(root, "agent-default-model")
	} else {
		defaultModel := copyObject(objectValue(root["agent-default-model"]))
		defaultModel["provider"], defaultModel["model"] = req.DefaultModel.Provider, req.DefaultModel.Model
		root["agent-default-model"] = defaultModel
	}
	data, err := marshalDSHYAML(root, req.ProviderKeys, req.Providers)
	if err != nil {
		return path, credentialsPath, err
	}
	var changes []filetxn.Change
	if prepared != nil {
		changes = append(changes, filetxn.Change{Path: credentialsPath, Previous: credentialsRaw, Next: prepared, NoBackup: true})
	}
	changes = append(changes, filetxn.Change{Path: path, Previous: previous, Next: data})
	if err := filetxn.Commit(changes...); err != nil {
		return path, credentialsPath, err
	}
	if cleaned != nil {
		beforeCleanup := credentialsRaw
		if prepared != nil {
			beforeCleanup = prepared
		}
		if err := filetxn.Replace(credentialsPath, beforeCleanup, cleaned, "", 0600); err != nil {
			return path, credentialsPath, &filetxn.CommitError{Files: []string{path}, Err: err}
		}
	}
	return path, credentialsPath, nil
}

func validateDSHSettings(req saveDSHSettingsRequest) error {
	if req.Providers == nil {
		return fmt.Errorf("必须提交完整 providers 对象")
	}
	for name, provider := range req.Providers {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("provider 标识不能为空")
		}
		if !slices.Contains([]string{"", "openai-completions", "openai-responses", "anthropic-messages", "google-generative-ai", "gemini-generate-content"}, provider.API) {
			return fmt.Errorf("provider %s 的协议无效", name)
		}
		if provider.BaseURL != "" {
			parsed, err := url.Parse(provider.BaseURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fmt.Errorf("provider %s 的 URL 无效", name)
			}
		}
		models := make(map[string]bool)
		for _, model := range provider.Models {
			if strings.TrimSpace(model.ID) == "" || models[model.ID] {
				return fmt.Errorf("provider %s 的模型 ID 为空或重复", name)
			}
			models[model.ID] = true
			if model.ContextWindow != nil && *model.ContextWindow <= 0 || model.MaxTokens != nil && *model.MaxTokens <= 0 {
				return fmt.Errorf("模型 %s 的容量必须为正数", model.ID)
			}
			for _, input := range model.Input {
				if input != "text" && input != "image" {
					return fmt.Errorf("模型 %s 的输入类型无效", model.ID)
				}
			}
		}
	}
	if req.DefaultModel != nil {
		provider, exists := req.Providers[req.DefaultModel.Provider]
		if !exists || !slices.ContainsFunc(provider.Models, func(model DSHModel) bool { return model.ID == req.DefaultModel.Model }) {
			return fmt.Errorf("默认模型必须引用本次配置中的 provider 和模型")
		}
	}
	return validateDSHCredentialUpdates(req.CredentialUpdates, req.Providers)
}

// mergeDSHProviders 仅修改编辑器拥有的字段，扩展字段由磁盘原对象保留。
func mergeDSHProviders(previous map[string]interface{}, next map[string]DSHProvider) map[string]interface{} {
	result := make(map[string]interface{}, len(next))
	for name, provider := range next {
		old := copyObject(objectValue(previous[name]))
		oldModels := make(map[string]map[string]interface{})
		if rows, ok := old["models"].([]interface{}); ok {
			for _, row := range rows {
				if model, ok := row.(map[string]interface{}); ok {
					if id, ok := model["id"].(string); ok {
						oldModels[id] = model
					}
				}
			}
		}
		for _, field := range []string{"apiKeyEnv", "api", "baseURL", "displayName", "models"} {
			delete(old, field)
		}
		for field, value := range dshProviderToMap(provider) {
			old[field] = value
		}
		models := make([]interface{}, 0, len(provider.Models))
		for _, model := range provider.Models {
			merged := copyObject(oldModels[model.ID])
			for _, field := range []string{"id", "name", "input", "reasoningEfforts", "contextWindow", "maxTokens"} {
				delete(merged, field)
			}
			for field, value := range dshModelToMap(model) {
				merged[field] = value
			}
			models = append(models, merged)
		}
		old["models"] = models
		result[name] = old
	}
	return result
}

// prepareDSHCredentials 使用新引用发布替换值，旧 settings 在提交前仍读到原凭据。
// 删除只清理本次显式解除且不再被任何 provider 引用的值。
func prepareDSHCredentials(path string, req *saveDSHSettingsRequest) (prepared, cleaned []byte, err error) {
	if len(req.CredentialUpdates) == 0 {
		return nil, nil, nil
	}
	document, _, err := readDSHCredentialsDocument(path)
	if err != nil {
		return nil, nil, err
	}
	refs, err := dshCredentialsRefs(document)
	if err != nil {
		return nil, nil, err
	}
	remove := make(map[string]bool)
	changed := false
	for name, update := range req.CredentialUpdates {
		provider := req.Providers[name]
		switch update.Action {
		case dshCredentialActionReplace:
			remove[provider.APIKeyEnv] = true
			provider.APIKeyEnv = "API_PROXY_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			setDSHCredentialValue(refs, provider.APIKeyEnv, strings.TrimSpace(update.APIKey))
			changed = true
		case dshCredentialActionRemove:
			remove[provider.APIKeyEnv] = true
			provider.APIKeyEnv = ""
		}
		req.Providers[name] = provider
	}
	if changed {
		prepared, err = marshalDSHCredentialsDocument(document)
		if err != nil {
			return nil, nil, err
		}
	}
	for _, provider := range req.Providers {
		delete(remove, provider.APIKeyEnv)
	}
	deleted := false
	for reference := range remove {
		deleted = deleteDSHCredentialValue(refs, reference) || deleted
	}
	if deleted {
		cleaned, err = marshalDSHCredentialsDocument(document)
	}
	return prepared, cleaned, err
}
