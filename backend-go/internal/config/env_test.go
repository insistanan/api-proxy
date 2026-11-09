package config

import (
	"path/filepath"
	"testing"
)

func TestNewEnvConfigDefaultProduction(t *testing.T) {
	t.Setenv("ENV", "")

	cfg := NewEnvConfig()
	if cfg.Env != "production" {
		t.Fatalf("未设置 ENV 时应默认 production，实际 %q", cfg.Env)
	}
	if cfg.IsDevelopment() {
		t.Fatal("默认不应判定为 development")
	}
	if !cfg.IsProduction() {
		t.Fatal("默认应判定为 production")
	}
}

func TestNewEnvConfigCustomValues(t *testing.T) {
	t.Setenv("ENV", "development")
	t.Setenv("PORT", "8080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_TO_CONSOLE", "true")
	t.Setenv("ENABLE_WEB_UI", "false")
	t.Setenv("PROXY_ACCESS_KEY", "test-key-123")

	cfg := NewEnvConfig()
	if cfg.Env != "development" {
		t.Fatalf("ENV 应为 development，实际 %q", cfg.Env)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port 应为 8080，实际 %d", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel 应为 debug，实际 %q", cfg.LogLevel)
	}
	if !cfg.LogToConsole {
		t.Fatal("LogToConsole 应为 true")
	}
	if cfg.EnableWebUI {
		t.Fatal("EnableWebUI 应为 false")
	}
	if cfg.ProxyAccessKey != "test-key-123" {
		t.Fatalf("ProxyAccessKey 实际 %q", cfg.ProxyAccessKey)
	}
	if cfg.QuietPollingLogs {
		t.Fatal("debug 模式下 QuietPollingLogs 应为 false")
	}
}

func TestCandidateDotEnvPathsDedupAndAbsolute(t *testing.T) {
	paths := candidateDotEnvPaths()
	if len(paths) == 0 {
		t.Fatal("至少应包含工作目录 .env 候选")
	}
	seen := make(map[string]struct{}, len(paths))
	for _, envPath := range paths {
		if !filepath.IsAbs(envPath) {
			t.Fatalf("候选路径必须是绝对路径: %s", envPath)
		}
		if filepath.Base(envPath) != ".env" {
			t.Fatalf("候选文件名必须是 .env: %s", envPath)
		}
		if _, exists := seen[envPath]; exists {
			t.Fatalf("重复路径: %s", envPath)
		}
		seen[envPath] = struct{}{}
	}
}
