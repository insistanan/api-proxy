package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func setupSkillsTestEnv(t *testing.T) (projectDir string, homeDir string, opts LocationOptions) {
	t.Helper()
	root := t.TempDir()
	homeDir = filepath.Join(root, "home")
	projectDir = filepath.Join(root, "project-skills")
	require.NoError(t, os.MkdirAll(homeDir, 0700))
	require.NoError(t, os.MkdirAll(projectDir, 0700))

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(homeDir, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(homeDir, ".codex"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(homeDir, ".pi", "agent"))

	opts = LocationOptions{
		OpenCodeSkillsDir: filepath.Join(homeDir, ".config", "opencode", "skills"),
		PiSkillsDir:       filepath.Join(homeDir, ".pi", "agent", "skills"),
		ProjectDir:        projectDir,
	}
	return projectDir, homeDir, opts
}

func writeTestSkill(t *testing.T, baseDir, name, body string) string {
	t.Helper()
	dir := filepath.Join(baseDir, name)
	require.NoError(t, os.MkdirAll(dir, 0700))
	content := "---\nname: " + name + "\ndescription: test " + name + "\n---\n\n" + body
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0600))
	return dir
}

func TestToggleAgentAndSyncManifest(t *testing.T) {
	projectDir, homeDir, opts := setupSkillsTestEnv(t)

	// 创建项目 Skill（含 .backups 目录，不能复制到 Agent）
	skillDir := writeTestSkill(t, projectDir, "demo-skill", "v1")
	backupFile := filepath.Join(skillDir, backupsDirName, "20260801-120000.000000000", "translated.zh-CN.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(backupFile), 0700))
	require.NoError(t, os.WriteFile(backupFile, []byte("译文"), 0600))

	// 1. 启用 claude-code 和 pi
	manifest, err := ToggleAgent(opts, "demo-skill", "claude-code", true)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-code"}, manifest.Skills["demo-skill"])

	manifest, err = ToggleAgent(opts, "demo-skill", "pi", true)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-code", "pi"}, manifest.Skills["demo-skill"])

	claudeSkillFile := filepath.Join(homeDir, ".claude", "skills", "demo-skill", "SKILL.md")
	_, err = os.Stat(claudeSkillFile)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(homeDir, ".claude", "skills", "demo-skill", backupsDirName))
	require.True(t, os.IsNotExist(err), ".backups 不应复制到 Agent 目录")

	// 2. 在 codex 放置未托管的其它 Skill，以及已在 manifest 中停用的 demo-skill
	codexDir := filepath.Join(homeDir, ".codex", "skills")
	writeTestSkill(t, codexDir, "unmanaged-skill", "keep me")
	writeTestSkill(t, codexDir, "demo-skill", "stale disabled copy")

	// 3. 修改 claude-code 的 ModTime，验证内容一致时 SyncManifestToAgents 不会重复拷贝
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(claudeSkillFile, oldTime, oldTime))

	report, err := SyncManifestToAgents(opts)
	require.NoError(t, err)
	require.Equal(t, 0, report.Synced)
	require.Equal(t, 1, report.Removed) // codex 的 demo-skill 应被清理

	info, err := os.Stat(claudeSkillFile)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(oldTime), "内容一致时不应重写文件")

	// 未在 manifest 中登记的 Skill 不得被清理
	_, err = os.Stat(filepath.Join(codexDir, "unmanaged-skill", "SKILL.md"))
	require.NoError(t, err)

	// 4. 更新项目 Skill 内容后再次同步，claude-code 与 pi 均应更新
	writeTestSkill(t, projectDir, "demo-skill", "v2")
	report, err = SyncManifestToAgents(opts)
	require.NoError(t, err)
	require.Equal(t, 2, report.Synced)

	updated, err := os.ReadFile(claudeSkillFile)
	require.NoError(t, err)
	require.Contains(t, string(updated), "v2")

	// 5. 停用 pi，应删除 pi 的副本并保留 manifest 键
	manifest, err = ToggleAgent(opts, "demo-skill", "pi", false)
	require.NoError(t, err)
	require.Equal(t, []string{"claude-code"}, manifest.Skills["demo-skill"])
	_, err = os.Stat(filepath.Join(homeDir, ".pi", "agent", "skills", "demo-skill"))
	require.True(t, os.IsNotExist(err))
}

func TestSyncManifestSkipsMissingProjectSkillWithoutDeletingAgentCopies(t *testing.T) {
	projectDir, homeDir, opts := setupSkillsTestEnv(t)

	require.NoError(t, WriteManifest(projectDir, Manifest{
		Version: 1,
		Skills: map[string][]string{
			"missing-skill": {"claude-code"},
		},
	}))

	claudeCopy := writeTestSkill(t, filepath.Join(homeDir, ".claude", "skills"), "missing-skill", "keep")
	codexCopy := writeTestSkill(t, filepath.Join(homeDir, ".codex", "skills"), "missing-skill", "also keep")

	report, err := SyncManifestToAgents(opts)
	require.NoError(t, err)
	require.Equal(t, 1, report.Skipped)
	require.Equal(t, 0, report.Removed)

	_, err = os.Stat(filepath.Join(claudeCopy, "SKILL.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(codexCopy, "SKILL.md"))
	require.NoError(t, err)
}
