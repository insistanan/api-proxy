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

func TestSaveTranslationBackup_PreservesExistingProjectSkill(t *testing.T) {
	const name = "example-skill"
	projectDir := t.TempDir()
	destination := writeTestSkill(t, projectDir, name, "项目原文")
	script := filepath.Join(destination, "scripts", "run.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("echo project"), 0600); err != nil {
		t.Fatal(err)
	}
	ref := Item{Name: name, LocationKey: "codex", Path: writeTestSkill(t, t.TempDir(), name, "来源原文")}
	original, err := ReadContent(ref)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := SaveTranslationBackup(projectDir, ref, original, BackupInput{Translated: "译文"})
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "SKILL.md")); err != nil || !strings.HasSuffix(string(content), "项目原文") {
		t.Fatalf("项目原文被替换，content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(script); err != nil || string(content) != "echo project" {
		t.Fatalf("项目脚本丢失或被替换，content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(backup, "translated.md")); err != nil || string(content) != "译文" {
		t.Fatalf("译文未保存，content=%q err=%v", content, err)
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
	destination := writeTestSkill(t, t.TempDir(), "fat", "目标原文")
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
	if content, err := os.ReadFile(filepath.Join(destination, "SKILL.md")); err != nil || !strings.HasSuffix(string(content), "目标原文") {
		t.Fatalf("复制失败后未保留目标原文，content=%q err=%v", content, err)
	}
	assertNoSkillTempEntries(t, filepath.Dir(destination))
}

// TestInstallFilesReplacesAllTargetsAndKeepsBackups 验证多目标安装先暂存再替换，
// 清理旧文件、保留项目翻译备份，且不留下临时目录。
func TestInstallFilesReplacesAllTargetsAndKeepsBackups(t *testing.T) {
	root := t.TempDir()
	agents := Location{Key: "agents", Agent: "Agents 目录", Path: filepath.Join(root, "agents")}
	project := Location{Key: "project", Agent: "项目目录", Path: filepath.Join(root, "project")}
	agentsDestination := filepath.Join(agents.Path, "demo")
	projectDestination := filepath.Join(project.Path, "demo")
	backup := filepath.Join(projectDestination, backupsDirName, "20260801-120000.000000000", "translated.zh-CN.md")
	stale := filepath.Join(agentsDestination, "obsolete.md")

	for _, directory := range []string{filepath.Dir(backup), agentsDestination} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(backup, []byte("旧译文"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("旧文件"), 0600); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"SKILL.md":              []byte("---\nname: demo\ndescription: 示例\n---\n"),
		"references/current.md": []byte("当前资源"),
	}
	if err := InstallFiles([]Location{agents, project}, "demo", files); err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("过期文件未清理，err=%v", err)
	}
	if content, err := os.ReadFile(backup); err != nil || string(content) != "旧译文" {
		t.Fatalf("翻译备份未保留，content=%q err=%v", content, err)
	}
	for _, destination := range []string{agentsDestination, projectDestination} {
		if content, err := os.ReadFile(filepath.Join(destination, "references", "current.md")); err != nil || string(content) != "当前资源" {
			t.Fatalf("目标 %s 未写入当前资源，content=%q err=%v", destination, content, err)
		}
	}
	assertNoSkillTempEntries(t, agents.Path, project.Path)
}

// TestInstallFilesLeavesExistingSkillWhenStagingFails 验证暂存阶段失败时不会破坏已有安装。
func TestInstallFilesLeavesExistingSkillWhenStagingFails(t *testing.T) {
	root := t.TempDir()
	location := Location{Key: "agents", Agent: "Agents 目录", Path: root}
	destination := filepath.Join(root, "demo")
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(destination, "SKILL.md")
	if err := os.WriteFile(original, []byte("旧内容"), 0600); err != nil {
		t.Fatal(err)
	}

	err := InstallFiles([]Location{location}, "demo", map[string][]byte{"../escape.md": []byte("越界")})
	if err == nil || !strings.Contains(err.Error(), "Skill 文件路径无效") {
		t.Fatalf("期望路径校验失败，得到: %v", err)
	}
	content, readErr := os.ReadFile(original)
	if readErr != nil || string(content) != "旧内容" {
		t.Fatalf("已有 Skill 被破坏，content=%q err=%v", content, readErr)
	}
	assertNoSkillTempEntries(t, root)
}

func assertNoSkillTempEntries(t *testing.T, roots ...string) {
	t.Helper()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".skill-stage-") || strings.HasPrefix(entry.Name(), ".skill-old-") {
				t.Fatalf("目录 %s 残留临时条目: %s", root, entry.Name())
			}
		}
	}
}
