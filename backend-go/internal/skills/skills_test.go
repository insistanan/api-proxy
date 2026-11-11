package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverClaudePluginSkillLocationsOnlyUsesInstalledPlugins(t *testing.T) {
	claudeHome := t.TempDir()
	installedSkillDir := filepath.Join(claudeHome, "plugins", "cache", "official", "installed-plugin", "1.0.0", "skills")
	marketplaceSkillDir := filepath.Join(claudeHome, "plugins", "marketplaces", "official", "plugins", "available-plugin", "skills")
	if err := os.MkdirAll(installedSkillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(marketplaceSkillDir, 0700); err != nil {
		t.Fatal(err)
	}
	installedJSON := `{"plugins":{"installed-plugin@official":[{"installPath":"` + filepath.ToSlash(filepath.Dir(installedSkillDir)) + `"}]}}`
	if err := os.WriteFile(filepath.Join(claudeHome, "plugins", "installed_plugins.json"), []byte(installedJSON), 0600); err != nil {
		t.Fatal(err)
	}

	locations, err := discoverClaudePluginLocations(claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 {
		t.Fatalf("识别到 %d 个目录，期望仅识别已安装插件的 1 个目录: %#v", len(locations), locations)
	}
	if locations[0].Path != installedSkillDir || locations[0].Agent != "Claude 插件 · installed-plugin" {
		t.Fatalf("识别结果错误: %#v", locations[0])
	}
}

func TestWriteSkillFilesPreservingReplacesPackageAndKeepsBackups(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "example-skill")
	backup := filepath.Join(destination, backupsDirName, "20260801-120000.000000000", "translated.zh-CN.md")
	stale := filepath.Join(destination, "scripts", "obsolete.sh")
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("旧译文"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("旧脚本"), 0600); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"SKILL.md":              []byte("---\nname: example-skill\ndescription: 示例\n---\n"),
		"references/current.md": []byte("当前资源"),
	}
	if err := WriteFilesPreserving(destination, files); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("过期资源未清理，err=%v", err)
	}
	if content, err := os.ReadFile(backup); err != nil || string(content) != "旧译文" {
		t.Fatalf("翻译备份未保留，content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "references", "current.md")); err != nil || string(content) != "当前资源" {
		t.Fatalf("当前资源未写入，content=%q err=%v", content, err)
	}
}

// TestCopySkillDirCopiesFilesAndPreservesBackups 验证流式拷贝把源目录资源完整写入目标，
// 同时保留翻译备份目录、清理旧文件，且不把内容收进内存（用大文件验证流式行为）。
func TestCopySkillDirCopiesFilesAndPreservesBackups(t *testing.T) {
	source := filepath.Join(t.TempDir(), "archify")
	destination := filepath.Join(t.TempDir(), "archify")
	backup := filepath.Join(destination, backupsDirName, "20260801-120000.000000000", "translated.zh-CN.md")
	stale := filepath.Join(destination, "scripts", "obsolete.sh")

	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("旧译文"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("旧脚本"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: archify\ndescription: 架构图\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// 单个 8MB 资源文件，旧实现会因总大小 20MB 上限或文件数 128 上限拦截，新流式实现不受影响。
	bigAsset := strings.Repeat("A", 8<<20)
	if err := os.WriteFile(filepath.Join(source, "references", "big.svg"), []byte(bigAsset), 0600); err != nil {
		t.Fatal(err)
	}

	if err := CopyDir(source, destination, true); err != nil {
		t.Fatalf("流式拷贝失败: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("过期文件未清理，err=%v", err)
	}
	if content, err := os.ReadFile(backup); err != nil || string(content) != "旧译文" {
		t.Fatalf("翻译备份未保留，content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "SKILL.md")); err != nil || !strings.HasPrefix(string(content), "---\n") {
		t.Fatalf("SKILL.md 未写入，content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "references", "big.svg")); err != nil || len(content) != len(bigAsset) {
		t.Fatalf("大资源文件未完整写入，len=%d err=%v", len(content), err)
	}
}

// TestCopySkillDirRejectsMissingSkillMD 验证源目录缺少 SKILL.md 时拒绝拷贝。
func TestCopySkillDirRejectsMissingSkillMD(t *testing.T) {
	source := filepath.Join(t.TempDir(), "broken")
	destination := filepath.Join(t.TempDir(), "broken")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("没有 SKILL.md"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyDir(source, destination, false); err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("期望缺少 SKILL.md 报错，得到: %v", err)
	}
}

// TestCopySkillDirRejectsOversizedFile 验证单文件超过上限时拒绝拷贝。
func TestCopySkillDirRejectsOversizedFile(t *testing.T) {
	source := filepath.Join(t.TempDir(), "fat")
	destination := filepath.Join(t.TempDir(), "fat")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: fat\ndescription: 超限\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("B", maxCopyFileBytes+1)
	if err := os.WriteFile(filepath.Join(source, "blob.bin"), []byte(oversized), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyDir(source, destination, false); err == nil || !strings.Contains(err.Error(), "Skill 文件过大") {
		t.Fatalf("期望超大文件报错，得到: %v", err)
	}
}
