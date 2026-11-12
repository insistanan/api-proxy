package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestConfig 写入临时配置文件并初始化 ConfigManager
func writeTestConfig(t *testing.T, raw string) *ConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
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
	cm := writeTestConfig(t, `{
		"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
		"upstream": [
			{"name": "inherit-default", "baseUrl": "https://api.example.com", "apiKeys": ["k"], "serviceType": "claude"},
			{"name": "direct", "baseUrl": "https://direct.example.com", "apiKeys": ["k"], "serviceType": "claude", "proxyMode": "direct"},
			{"name": "custom", "baseUrl": "https://custom.example.com", "apiKeys": ["k"], "serviceType": "claude", "proxyMode": "custom", "proxyUrl": "http://127.0.0.1:8888"}
		]
	}`)
	upstreams := cm.GetConfig().Upstream

	t.Run("inherit + 开关开启 + URL 非空 -> 使用全局代理", func(t *testing.T) {
		got, err := cm.ResolveUpstreamProxyURL(&upstreams[0])
		if err != nil {
			t.Fatalf("解析代理失败: %v", err)
		}
		if got != "http://127.0.0.1:7897" {
			t.Fatalf("代理地址不匹配: got=%q", got)
		}
	})
	t.Run("direct 模式忽略全局代理", func(t *testing.T) {
		got, err := cm.ResolveUpstreamProxyURL(&upstreams[1])
		if err != nil {
			t.Fatalf("解析代理失败: %v", err)
		}
		if got != "" {
			t.Fatalf("direct 模式应直连: got=%q", got)
		}
	})
	t.Run("custom 模式使用独立代理", func(t *testing.T) {
		got, err := cm.ResolveUpstreamProxyURL(&upstreams[2])
		if err != nil {
			t.Fatalf("解析代理失败: %v", err)
		}
		if got != "http://127.0.0.1:8888" {
			t.Fatalf("代理地址不匹配: got=%q", got)
		}
	})
	// 防御路径：配置加载已拦截空 URL，这里验证函数自身的校验逻辑
	t.Run("custom 模式缺少代理地址 -> 报错", func(t *testing.T) {
		if _, err := cm.ResolveUpstreamProxyURL(&UpstreamConfig{ProxyMode: ProxyModeCustom, ProxyURL: ""}); err == nil {
			t.Fatalf("期望返回错误，实际无错误")
		}
	})
}

// TestResolveUpstreamProxyURL_InheritDisabled 覆盖本次修复的核心：
// 全局代理开关关闭时，即使配置残留代理地址也必须直连。
func TestResolveUpstreamProxyURL_InheritDisabled(t *testing.T) {
	cm := writeTestConfig(t, `{
		"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": false}},
		"upstream": [{
			"name": "inherit-disabled",
			"baseUrl": "https://api.example.com",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`)
	upstream := &cm.GetConfig().Upstream[0]

	got, err := cm.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		t.Fatalf("解析代理失败: %v", err)
	}
	if got != "" {
		t.Fatalf("开关关闭时应直连，实际仍走代理: got=%q", got)
	}
}

// TestNetworkSettingsProxyMigration 旧配置只有代理地址没有启用开关时，
// 应迁移为已启用，保持旧行为（URL 非空即走代理）不中断。
func TestNetworkSettingsProxyMigration(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
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

	// 迁移应持久化到配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取迁移后配置失败: %v", err)
	}
	if !strings.Contains(string(data), `"upstreamProxyEnabled": true`) {
		t.Fatalf("迁移结果未持久化: %s", data)
	}
}

// TestUpdateNetworkSettings 全量更新语义：空地址表示清空代理，不应被旧值回填。
func TestUpdateNetworkSettings(t *testing.T) {
	cm := writeTestConfig(t, `{
		"settings": {"network": {"upstreamProxyUrl": "http://127.0.0.1:7897", "upstreamProxyEnabled": true}},
		"upstream": []
	}`)

	if err := cm.UpdateNetworkSettings(NetworkSettings{
		UpstreamProxyURL:     "",
		UpstreamProxyEnabled: false,
	}); err != nil {
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
	t.Run("空版本库合法", func(t *testing.T) {
		if err := ValidatePromptSettings(PromptSettings{}); err != nil {
			t.Fatalf("空版本库应合法: %v", err)
		}
	})

	t.Run("正常版本库合法", func(t *testing.T) {
		settings := PromptSettings{
			Entries: []PromptEntry{
				{Version: "v1", Content: "第一版"},
				{Version: "v2", Content: "第二版"},
			},
			ActiveVersion: "v2",
		}
		if err := ValidatePromptSettings(settings); err != nil {
			t.Fatalf("正常版本库应合法: %v", err)
		}
	})

	t.Run("版本号重复或为空被拒绝", func(t *testing.T) {
		duplicated := PromptSettings{Entries: []PromptEntry{{Version: "v1"}, {Version: "v1"}}}
		if err := ValidatePromptSettings(duplicated); err == nil {
			t.Fatal("重复版本号应被拒绝")
		}
		blank := PromptSettings{Entries: []PromptEntry{{Version: "  "}}}
		if err := ValidatePromptSettings(blank); err == nil {
			t.Fatal("空白版本号应被拒绝")
		}
	})

	t.Run("生效版本不存在被拒绝", func(t *testing.T) {
		settings := PromptSettings{
			Entries:       []PromptEntry{{Version: "v1"}},
			ActiveVersion: "v9",
		}
		err := ValidatePromptSettings(settings)
		if err == nil {
			t.Fatal("生效版本不存在应被拒绝")
		}
		if !strings.Contains(err.Error(), "v9") {
			t.Fatalf("错误信息应包含不存在的版本号: %v", err)
		}
	})

	t.Run("条目数超限被拒绝", func(t *testing.T) {
		entries := make([]PromptEntry, maxPromptEntries+1)
		for i := range entries {
			entries[i] = PromptEntry{Version: "v" + strings.Repeat("1", i+1)}
		}
		if err := ValidatePromptSettings(PromptSettings{Entries: entries}); err == nil {
			t.Fatal("条目数超限应被拒绝")
		}
	})
}

func TestNormalizePromptSettings(t *testing.T) {
	t.Run("trim 版本与备注并刷新时间戳", func(t *testing.T) {
		source := PromptSettings{
			Entries:       []PromptEntry{{Version: " v1 ", Note: " 备注 ", Content: " 内容 ", UpdatedAt: 1}},
			ActiveVersion: " v1 ",
		}
		normalized := NormalizePromptSettings(source, PromptSettings{})
		if len(normalized.Entries) != 1 {
			t.Fatalf("归一化后条目数错误: %d", len(normalized.Entries))
		}
		entry := normalized.Entries[0]
		if entry.Version != "v1" || entry.Note != "备注" {
			t.Fatalf("版本/备注未 trim: %+v", entry)
		}
		if entry.Content != " 内容 " {
			t.Fatalf("内容不应被修改: %q", entry.Content)
		}
		if entry.UpdatedAt == 1 {
			t.Fatal("时间戳应被刷新")
		}
		if normalized.ActiveVersion != "v1" {
			t.Fatalf("生效版本未 trim: %q", normalized.ActiveVersion)
		}
		// 入参不被修改（返回新切片）
		if source.Entries[0].Version != " v1 " {
			t.Fatal("归一化不应修改入参")
		}
	})

	t.Run("未变化的条目保留旧时间戳", func(t *testing.T) {
		next := PromptSettings{
			Entries: []PromptEntry{
				{Version: "v1", Note: "备注", Content: "不变内容"},
				{Version: "v2", Note: "备注", Content: "新内容"},
			},
		}
		previous := PromptSettings{
			Entries: []PromptEntry{
				{Version: "v1", Note: "备注", Content: "不变内容", UpdatedAt: 111},
				{Version: "v2", Note: "旧备注", Content: "旧内容", UpdatedAt: 222},
			},
		}
		normalized := NormalizePromptSettings(next, previous)
		if normalized.Entries[0].UpdatedAt != 111 {
			t.Fatalf("未变化条目应保留旧时间戳: %d", normalized.Entries[0].UpdatedAt)
		}
		if normalized.Entries[1].UpdatedAt == 222 {
			t.Fatal("已变化条目应刷新时间戳")
		}
	})
}
