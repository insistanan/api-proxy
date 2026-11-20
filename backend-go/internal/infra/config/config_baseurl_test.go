package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 完整地址开关支持部分更新、管理 API 回显和 JSON 配置往返。
func TestUpstreamUseFullURLUpdatesAndSerialization(t *testing.T) {
	var upstream UpstreamConfig
	if err := json.Unmarshal([]byte(`{"baseUrl":"https://images.example/render/","serviceType":"openai","apiKeys":["fixture-key"]}`), &upstream); err != nil {
		t.Fatal(err)
	}
	if upstream.UseFullURL {
		t.Fatal("旧配置应默认自动补全")
	}
	for _, rawUpdate := range []string{`{"useFullUrl":true}`, `{"name":"改名"}`, `{"useFullUrl":false}`} {
		var update UpstreamUpdate
		if err := json.Unmarshal([]byte(rawUpdate), &update); err != nil {
			t.Fatal(err)
		}
		if _, err := applyCommonUpdates(&upstream, 0, update, "test"); err != nil {
			t.Fatal(err)
		}
		want := rawUpdate != `{"useFullUrl":false}`
		if upstream.UseFullURL != want || ChannelToDTO(&upstream, 0)["useFullUrl"] != want {
			t.Fatalf("完整地址设置未保留: 更新=%s，配置=%v", rawUpdate, upstream.UseFullURL)
		}
		encoded, err := json.Marshal(upstream)
		if err != nil {
			t.Fatal(err)
		}
		var restored UpstreamConfig
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.UseFullURL != want || restored.BaseURL != upstream.BaseURL {
			t.Fatal("JSON 配置往返改变了完整地址设置")
		}
		if upstream.Clone().UseFullURL != want {
			t.Fatal("故障转移使用的配置副本丢失了完整地址设置")
		}
	}
}

func TestFullURLDeduplicationPreservesTrailingSlash(t *testing.T) {
	urls := []string{"https://images.example/render/", "https://images.example/render", "https://images.example/render/"}
	got := deduplicateBaseURLs(urls, true)
	if len(got) != 2 || got[0] != urls[0] || got[1] != urls[1] {
		t.Fatalf("完整地址的末尾路径差异被丢弃: %v", got)
	}
}

// TestUpdateUpstream_BaseURLConsistency 测试更新 baseUrl 时的一致性
// 覆盖场景：
// 1. 只更新 baseUrl 时，baseUrls 应被清空
// 2. 只更新 baseUrls 时，baseUrl 应保持不变
// 3. 同时更新 baseUrl 和 baseUrls 时，两者应独立更新
// 4. 都不更新时，两者应保持原值
func TestUpdateUpstream_BaseURLConsistency(t *testing.T) {
	// 创建临时配置文件
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://old.example.com",
			"baseUrls": ["https://old-1.example.com", "https://old-2.example.com"],
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}],
		"loadBalance": "failover"
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	// 初始化配置管理器
	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer cm.Close()

	tests := []struct {
		name            string
		updates         UpstreamUpdate
		wantBaseURL     string
		wantBaseURLs    []string
		wantBaseURLsNil bool // 期望 baseUrls 为 nil（而非空切片）
	}{
		{
			name: "只更新 baseUrl 时 baseUrls 应被清空",
			updates: UpstreamUpdate{
				BaseURL: strPtr("https://new.example.com"),
			},
			wantBaseURL:     "https://new.example.com",
			wantBaseURLsNil: true,
		},
		{
			name: "只更新 baseUrls 时 baseUrl 应保持不变",
			updates: UpstreamUpdate{
				BaseURLs: []string{"https://urls-1.example.com", "https://urls-2.example.com"},
			},
			wantBaseURL:  "https://new.example.com", // 保持上个测试的值
			wantBaseURLs: []string{"https://urls-1.example.com", "https://urls-2.example.com"},
		},
		{
			name: "同时更新 baseUrl 和 baseUrls 时两者独立更新",
			updates: UpstreamUpdate{
				BaseURL:  strPtr("https://both-base.example.com"),
				BaseURLs: []string{"https://both-urls-1.example.com", "https://both-urls-2.example.com"},
			},
			wantBaseURL:  "https://both-base.example.com",
			wantBaseURLs: []string{"https://both-urls-1.example.com", "https://both-urls-2.example.com"},
		},
		{
			name:         "都不更新时保持原值",
			updates:      UpstreamUpdate{Name: strPtr("renamed-channel")},
			wantBaseURL:  "https://both-base.example.com",
			wantBaseURLs: []string{"https://both-urls-1.example.com", "https://both-urls-2.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cm.UpdateUpstream(0, tt.updates)
			if err != nil {
				t.Fatalf("UpdateUpstream 失败: %v", err)
			}

			cfg := cm.GetConfig()
			upstream := cfg.Upstream[0]

			if upstream.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", upstream.BaseURL, tt.wantBaseURL)
			}

			if tt.wantBaseURLsNil {
				if upstream.BaseURLs != nil {
					t.Errorf("BaseURLs = %v, want nil", upstream.BaseURLs)
				}
			} else {
				if len(upstream.BaseURLs) != len(tt.wantBaseURLs) {
					t.Errorf("BaseURLs length = %d, want %d", len(upstream.BaseURLs), len(tt.wantBaseURLs))
				} else {
					for i, url := range upstream.BaseURLs {
						if url != tt.wantBaseURLs[i] {
							t.Errorf("BaseURLs[%d] = %q, want %q", i, url, tt.wantBaseURLs[i])
						}
					}
				}
			}
		})
	}
}

// TestAddUpstream_BaseURLDeduplication 测试添加渠道时的 BaseURLs 去重
func TestAddUpstream_BaseURLDeduplication(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{"upstream": [], "loadBalance": "failover"}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer cm.Close()

	// 添加包含重复 URL 的渠道
	err = cm.AddUpstream(UpstreamConfig{
		Name:        "dedup-test",
		BaseURL:     "https://main.example.com",
		BaseURLs:    []string{"https://a.com", "https://b.com", "https://a.com/", "https://c.com"},
		APIKeys:     []string{"key1"},
		ServiceType: "claude",
	})
	if err != nil {
		t.Fatalf("AddUpstream 失败: %v", err)
	}

	cfg := cm.GetConfig()
	upstream := cfg.Upstream[0]

	// 期望去重后只有 3 个 URL
	expectedURLs := []string{"https://a.com", "https://b.com", "https://c.com"}
	if len(upstream.BaseURLs) != len(expectedURLs) {
		t.Errorf("BaseURLs length = %d, want %d", len(upstream.BaseURLs), len(expectedURLs))
	}
	for i, url := range upstream.BaseURLs {
		if url != expectedURLs[i] {
			t.Errorf("BaseURLs[%d] = %q, want %q", i, url, expectedURLs[i])
		}
	}
}

// strPtr 辅助函数：返回字符串指针
func strPtr(s string) *string {
	return &s
}
