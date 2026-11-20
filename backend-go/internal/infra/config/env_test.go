package config

import (
	"path/filepath"
	"testing"
)

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
