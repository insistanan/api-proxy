package piagent

import (
	"encoding/json"
	"path/filepath"
)

// CredentialView 是 auth.json 中单个凭据的脱敏视图。
type CredentialView struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"` // api_key / oauth / unknown
	KeyMasked    string            `json:"keyMasked"`
	KeyPresent   bool              `json:"keyPresent"`
	HasOAuth     bool              `json:"hasOAuth"`
	EnvMasked    map[string]string `json:"envMasked"`
	HasEnvValues bool              `json:"hasEnvValues"`
}

// credentialFieldOrder 决定读取凭据时检查的字段顺序。
var credentialFieldOrder = []string{"key", "accessToken", "refreshToken", "clientSecret", "token"}

// ReadCredentials 读取 auth.json 并返回脱敏后的凭据视图。
func (s *Store) ReadCredentials() ([]CredentialView, map[string]json.RawMessage, []byte, bool, error) {
	path, err := s.authPath()
	if err != nil {
		return nil, nil, nil, false, err
	}
	root, raw, exists, err := ReadJSON(path)
	if err != nil {
		return nil, nil, raw, exists, err
	}
	views := make([]CredentialView, 0, len(root))
	for id, rawValue := range root {
		cred := make(map[string]interface{})
		if err := json.Unmarshal(rawValue, &cred); err != nil {
			views = append(views, CredentialView{ID: id, Type: "unknown"})
			continue
		}
		view := credentialView(id, cred)
		views = append(views, view)
	}
	return views, root, raw, exists, nil
}

// ReadRawAuth 读取 auth.json 原始根对象，供探测请求获取真实密钥（不落盘）。
func (s *Store) ReadRawAuth() (map[string]json.RawMessage, []byte, bool, error) {
	path, err := s.authPath()
	if err != nil {
		return nil, nil, false, err
	}
	return ReadJSON(path)
}

// credentialView 构造单个凭据的脱敏视图。
func credentialView(id string, cred map[string]interface{}) CredentialView {
	view := CredentialView{
		ID:        id,
		Type:      credentialType(cred),
		EnvMasked: make(map[string]string),
	}
	apiKey := ""
	for _, field := range credentialFieldOrder {
		if value, ok := cred[field].(string); ok && value != "" {
			if field == "key" {
				apiKey = value
			}
			if field == "accessToken" || field == "refreshToken" {
				view.HasOAuth = true
			}
		}
	}
	if apiKey != "" {
		view.KeyMasked = MaskSecret(apiKey)
		view.KeyPresent = true
	}
	if envValues, ok := cred["env"].(map[string]interface{}); ok {
		for key, value := range envValues {
			if str, ok := value.(string); ok {
				view.EnvMasked[key] = MaskSecret(str)
				view.HasEnvValues = true
			}
		}
	}
	if len(view.EnvMasked) == 0 {
		view.EnvMasked = nil
	}
	return view
}

func credentialType(cred map[string]interface{}) string {
	if hasOAuthFields(cred) {
		return "oauth"
	}
	if value, ok := cred["key"].(string); ok && value != "" {
		return "api_key"
	}
	return "unknown"
}

func hasOAuthFields(cred map[string]interface{}) bool {
	for _, field := range []string{"accessToken", "refreshToken", "oauth", "clientSecret"} {
		if value, ok := cred[field]; ok {
			switch value.(type) {
			case string:
				if value != "" {
					return true
				}
			default:
				return true
			}
		}
	}
	return false
}

func (s *Store) authPath() (string, error) {
	dir, err := ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, string(FileAuth)), nil
}
