package config

import (
	"encoding/json"
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

// 去重只合并语义相同的地址；完整地址模式下末尾路径差异必须保留。
func TestBaseURLDeduplication(t *testing.T) {
	t.Run("full url keeps trailing slash difference", func(t *testing.T) {
		urls := []string{"https://images.example/render/", "https://images.example/render", "https://images.example/render/"}
		got := deduplicateBaseURLs(urls, true)
		if len(got) != 2 || got[0] != urls[0] || got[1] != urls[1] {
			t.Fatalf("完整地址的末尾路径差异被丢弃: %v", got)
		}
	})

	t.Run("add upstream dedupes extra base urls", func(t *testing.T) {
		cm := writeTestConfig(t, `{"upstream": [], "loadBalance": "failover"}`)
		err := cm.AddUpstream(UpstreamConfig{
			Name:        "dedup-test",
			BaseURL:     "https://main.example.com",
			BaseURLs:    []string{"https://a.com", "https://b.com", "https://a.com/", "https://c.com"},
			APIKeys:     []string{"key1"},
			ServiceType: "claude",
		})
		if err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		got := cm.GetConfig().Upstream[0].BaseURLs
		want := []string{"https://a.com", "https://b.com", "https://c.com"}
		if len(got) != len(want) {
			t.Fatalf("BaseURLs length = %d, want %d: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("BaseURLs[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// 更新 baseUrl 与 baseUrls 时两者独立：只改 baseUrl 会清空 baseUrls，其余字段保持原值。
func TestUpdateUpstreamBaseURLConsistency(t *testing.T) {
	cm := writeTestConfig(t, `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://old.example.com",
			"baseUrls": ["https://old-1.example.com", "https://old-2.example.com"],
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}],
		"loadBalance": "failover"
	}`)

	tests := []struct {
		name            string
		updates         UpstreamUpdate
		wantBaseURL     string
		wantBaseURLs    []string
		wantBaseURLsNil bool
	}{
		{
			name:            "只更新 baseUrl 时 baseUrls 应被清空",
			updates:         UpstreamUpdate{BaseURL: strPtr("https://new.example.com")},
			wantBaseURL:     "https://new.example.com",
			wantBaseURLsNil: true,
		},
		{
			name:         "只更新 baseUrls 时 baseUrl 应保持不变",
			updates:      UpstreamUpdate{BaseURLs: []string{"https://urls-1.example.com", "https://urls-2.example.com"}},
			wantBaseURL:  "https://new.example.com",
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
			if _, err := cm.UpdateUpstream(0, tt.updates); err != nil {
				t.Fatalf("UpdateUpstream 失败: %v", err)
			}
			upstream := cm.GetConfig().Upstream[0]
			if upstream.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", upstream.BaseURL, tt.wantBaseURL)
			}
			if tt.wantBaseURLsNil {
				if upstream.BaseURLs != nil {
					t.Errorf("BaseURLs = %v, want nil", upstream.BaseURLs)
				}
				return
			}
			if len(upstream.BaseURLs) != len(tt.wantBaseURLs) {
				t.Fatalf("BaseURLs length = %d, want %d", len(upstream.BaseURLs), len(tt.wantBaseURLs))
			}
			for i, url := range upstream.BaseURLs {
				if url != tt.wantBaseURLs[i] {
					t.Errorf("BaseURLs[%d] = %q, want %q", i, url, tt.wantBaseURLs[i])
				}
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
