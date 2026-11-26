package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateDotEnvPathsDedupAndAbsolute(t *testing.T) {
	paths, err := candidateDotEnvPaths()
	if err != nil {
		t.Fatal(err)
	}
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

func TestLoadEnvConfig_RejectsExplicitInvalidValues(t *testing.T) {
	for key, value := range map[string]string{
		"PORT": "3000", "ENV": "production", "LOG_LEVEL": "info",
		"ENABLE_WEB_UI": "true", "LOG_TO_CONSOLE": "false", "PROXY_ACCESS_KEY": "fixture-key",
	} {
		t.Setenv(key, value)
	}
	for _, tc := range []struct{ key, value string }{
		{"PORT", "abc"}, {"PORT", "0"}, {"PORT", "65536"}, {"PORT", ""},
		{"ENABLE_WEB_UI", "FALSE"}, {"LOG_TO_CONSOLE", "yes"},
		{"ENV", "unknown"}, {"LOG_LEVEL", "verbose"}, {"PROXY_ACCESS_KEY", ""},
		{"PROXY_ACCESS_KEY", "your-proxy-access-key"}, {"PROXY_ACCESS_KEY", "   "},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := LoadEnvConfig()
			require.ErrorContains(t, err, tc.key)
		})
	}
	current, err := LoadEnvConfig()
	require.NoError(t, err)
	require.Equal(t, 3000, current.Port)
	require.True(t, current.EnableWebUI)
	require.False(t, current.LogToConsole)
}
