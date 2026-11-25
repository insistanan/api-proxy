package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestConfig 写入临时配置文件并初始化 ConfigManager。
func writeTestConfig(t *testing.T, raw string) *ConfigManager {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(raw), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	t.Cleanup(func() { cm.Close() })
	return cm
}

func TestResolveUpstreamProxyURL(t *testing.T) {
	t.Run("inherit, global enabled, url set uses global proxy", func(t *testing.T) {
		cm := writeTestConfig(t, `{
			"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
			"upstream": [{"name": "inherit-default", "baseUrl": "https://api.example.com", "apiKeys": ["k"], "serviceType": "claude"}]
		}`)
		got, err := cm.ResolveUpstreamProxyURL(&cm.GetConfig().Upstream[0])
		if err != nil || got != "http://127.0.0.1:7897" {
			t.Fatalf("代理地址不匹配: got=%q err=%v", got, err)
		}
	})

	t.Run("inherit, global disabled, stale url goes direct", func(t *testing.T) {
		cm := writeTestConfig(t, `{
			"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": false}},
			"upstream": [{"name": "inherit-disabled", "baseUrl": "https://api.example.com", "apiKeys": ["test-key"], "serviceType": "claude"}]
		}`)
		got, err := cm.ResolveUpstreamProxyURL(&cm.GetConfig().Upstream[0])
		if err != nil || got != "" {
			t.Fatalf("开关关闭时应直连: got=%q err=%v", got, err)
		}
	})

	t.Run("direct mode ignores global proxy", func(t *testing.T) {
		cm := writeTestConfig(t, `{
			"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
			"upstream": [{"name": "direct", "baseUrl": "https://direct.example.com", "apiKeys": ["k"], "serviceType": "claude", "proxyMode": "direct"}]
		}`)
		got, err := cm.ResolveUpstreamProxyURL(&cm.GetConfig().Upstream[0])
		if err != nil || got != "" {
			t.Fatalf("direct 模式应直连: got=%q err=%v", got, err)
		}
	})

	t.Run("custom mode uses channel proxy", func(t *testing.T) {
		cm := writeTestConfig(t, `{
			"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
			"upstream": [{"name": "custom", "baseUrl": "https://custom.example.com", "apiKeys": ["k"], "serviceType": "claude", "proxyMode": "custom", "proxyUrl": "http://127.0.0.1:8888"}]
		}`)
		got, err := cm.ResolveUpstreamProxyURL(&cm.GetConfig().Upstream[0])
		if err != nil || got != "http://127.0.0.1:8888" {
			t.Fatalf("代理地址不匹配: got=%q err=%v", got, err)
		}
	})

	t.Run("custom mode without url is rejected", func(t *testing.T) {
		cm := writeTestConfig(t, `{"upstream": []}`)
		if _, err := cm.ResolveUpstreamProxyURL(&UpstreamConfig{ProxyMode: ProxyModeCustom, ProxyURL: ""}); err == nil {
			t.Fatalf("期望返回错误，实际无错误")
		}
	})
}

// 旧配置只有代理地址没有启用开关时，应迁移为已启用，保持旧行为（URL 非空即走代理）不中断，并持久化。
func TestNetworkSettingsProxyMigration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897"}},
		"upstream": []
	}`
	if err := os.WriteFile(configPath, []byte(raw), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer cm.Close()

	if !cm.GetSettings().Network.UpstreamProxyEnabled {
		t.Fatalf("旧配置含非空代理地址但缺开关字段，应迁移为已启用")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取迁移后配置失败: %v", err)
	}
	if !strings.Contains(string(data), `"upstreamProxyEnabled": true`) {
		t.Fatalf("迁移结果未持久化: %s", data)
	}
}

// 全量更新语义：空地址表示清空代理，不应被旧值回填。
func TestUpdateNetworkSettingsClearsProxy(t *testing.T) {
	cm := writeTestConfig(t, `{
		"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
		"upstream": []
	}`)
	if err := cm.UpdateNetworkSettings(NetworkSettings{UpstreamProxyURL: "", UpstreamProxyEnabled: false}); err != nil {
		t.Fatalf("清空代理设置失败: %v", err)
	}
	settings := cm.GetSettings()
	if settings.Network.UpstreamProxyURL != "" {
		t.Fatalf("空地址应清空代理，实际保留为 %q", settings.Network.UpstreamProxyURL)
	}
	if settings.Network.UpstreamProxyEnabled {
		t.Fatalf("关闭开关未生效")
	}
}

func TestValidatePromptSettings(t *testing.T) {
	t.Run("empty and normal libraries are valid", func(t *testing.T) {
		if err := ValidatePromptSettings(PromptSettings{}); err != nil {
			t.Fatalf("空版本库应合法: %v", err)
		}
		settings := PromptSettings{
			Entries:       []PromptEntry{{Version: "v1", Content: "第一版"}, {Version: "v2", Content: "第二版"}},
			ActiveVersion: "v2",
		}
		if err := ValidatePromptSettings(settings); err != nil {
			t.Fatalf("正常版本库应合法: %v", err)
		}
	})

	t.Run("duplicate or blank version is rejected", func(t *testing.T) {
		duplicated := PromptSettings{Entries: []PromptEntry{{Version: "v1"}, {Version: "v1"}}}
		if err := ValidatePromptSettings(duplicated); err == nil {
			t.Fatal("重复版本号应被拒绝")
		}
		blank := PromptSettings{Entries: []PromptEntry{{Version: "  "}}}
		if err := ValidatePromptSettings(blank); err == nil {
			t.Fatal("空白版本号应被拒绝")
		}
	})

	t.Run("missing active version is rejected", func(t *testing.T) {
		settings := PromptSettings{Entries: []PromptEntry{{Version: "v1"}}, ActiveVersion: "v9"}
		err := ValidatePromptSettings(settings)
		if err == nil {
			t.Fatal("生效版本不存在应被拒绝")
		}
		if !strings.Contains(err.Error(), "v9") {
			t.Fatalf("错误信息应包含不存在的版本号: %v", err)
		}
	})

	t.Run("entry count limit is enforced", func(t *testing.T) {
		entries := make([]PromptEntry, maxPromptEntries+1)
		for i := range entries {
			entries[i] = PromptEntry{Version: "v" + strings.Repeat("1", i+1)}
		}
		if err := ValidatePromptSettings(PromptSettings{Entries: entries}); err == nil {
			t.Fatal("条目数超限应被拒绝")
		}
	})
}
