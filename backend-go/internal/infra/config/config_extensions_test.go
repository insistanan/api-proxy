package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSkillsDirCompatibility(t *testing.T) {
	root, err := WorkspaceRoot()
	require.NoError(t, err)
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	customAbs := filepath.Join(t.TempDir(), "custom-skills")

	for _, tt := range []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty", input: "", expected: filepath.Join(root, "skills")},
		{name: "legacy default", input: "skills", expected: filepath.Join(root, "skills")},
		{name: "relative", input: "legacy/skills", expected: filepath.Join(root, "legacy", "skills")},
		{name: "parent relative", input: "../skills", expected: filepath.Join(root, "..", "skills")},
		{name: "home", input: "~", expected: home},
		{name: "home slash", input: "~/skills", expected: filepath.Join(home, "skills")},
		{name: "home backslash", input: `~\skills`, expected: filepath.Join(home, "skills")},
		{name: "absolute", input: "  " + customAbs + "  ", expected: customAbs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := ResolveSkillsDir(tt.input)
			require.NoError(t, err)
			require.Equal(t, filepath.Clean(tt.expected), resolved)
		})
	}
}

func TestValidateExtensionsSettings(t *testing.T) {
	require.NoError(t, ValidateExtensionsSettings(ExtensionsSettings{}))
	require.NoError(t, ValidateExtensionsSettings(ExtensionsSettings{SkillsDir: "  "}))
	customAbs := filepath.Join(t.TempDir(), "custom-skills")
	require.NoError(t, ValidateExtensionsSettings(ExtensionsSettings{
		SkillsDir:     "  " + customAbs + "  ",
		SkillsRepoURL: "https://github.com/example/skills.git",
	}))
	require.NoError(t, ValidateExtensionsSettings(ExtensionsSettings{
		SkillsRepoURL: "git@github.com:example/skills.git",
	}))

	for _, skillsDir := range []string{"skills", "  skills  ", "relative/skills", "../skills", ".", "~", "~/skills", `~\skills`} {
		t.Run(skillsDir, func(t *testing.T) {
			err := ValidateExtensionsSettings(ExtensionsSettings{SkillsDir: skillsDir})
			var configErr *ConfigError
			require.ErrorAs(t, err, &configErr)
			require.EqualError(t, err, "Skills 目录必须填写绝对路径；留空使用工作区下的 skills/")
		})
	}

	err := ValidateExtensionsSettings(ExtensionsSettings{SkillsDir: filepath.VolumeName(customAbs) + string(os.PathSeparator)})
	require.EqualError(t, err, "Skills 目录不能设为文件系统根目录")

	err = ValidateExtensionsSettings(ExtensionsSettings{SkillsRepoURL: "--upload-pack=evil"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "'-'")

	err = ValidateExtensionsSettings(ExtensionsSettings{SkillsRepoURL: "ftp://example.com/repo.git"})
	require.Error(t, err)
}

func TestMigrateSkillsDirMovesAllFilesAndSupportsRollback(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old-skills")
	newDir := filepath.Join(root, "new-skills")

	packFile := filepath.Join(oldDir, ".git", "objects", "pack", "pack-abc.idx")
	require.NoError(t, os.MkdirAll(filepath.Dir(packFile), 0700))
	require.NoError(t, os.WriteFile(packFile, []byte("pack"), 0444)) // 只读文件模拟 .git pack
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "mcp.json"), []byte(`{"version":1}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(oldDir, "demo-skill", ".backups"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "demo-skill", "SKILL.md"), []byte("demo"), 0600))

	// 测试空目标目录场景
	require.NoError(t, os.MkdirAll(newDir, 0700))

	migrated, rollback, err := MigrateSkillsDir(oldDir, newDir)
	require.NoError(t, err)
	require.True(t, migrated)
	require.NotNil(t, rollback)

	_, err = os.Stat(oldDir)
	require.True(t, os.IsNotExist(err))

	content, err := os.ReadFile(filepath.Join(newDir, "mcp.json"))
	require.NoError(t, err)
	require.Equal(t, `{"version":1}`, string(content))

	packContent, err := os.ReadFile(filepath.Join(newDir, ".git", "objects", "pack", "pack-abc.idx"))
	require.NoError(t, err)
	require.Equal(t, "pack", string(packContent))

	// 验证回滚
	require.NoError(t, rollback())
	_, err = os.Stat(newDir)
	require.True(t, os.IsNotExist(err))
	restored, err := os.ReadFile(filepath.Join(oldDir, "demo-skill", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "demo", string(restored))
}

func TestMigrateSkillsDirRejectsNestedAndNonEmptyTarget(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "skills")
	require.NoError(t, os.MkdirAll(oldDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "skills-manifest.json"), []byte("{}"), 0600))

	// 嵌套目录拒绝
	_, _, err := MigrateSkillsDir(oldDir, filepath.Join(oldDir, "sub"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "包含")

	// 非空目标拒绝
	nonEmpty := filepath.Join(root, "occupied")
	require.NoError(t, os.MkdirAll(nonEmpty, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep"), 0600))
	_, _, err = MigrateSkillsDir(oldDir, nonEmpty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "非空")
}

func TestCopyAndRemoveAllForceHandlesReadOnlyPackFiles(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	roFile := filepath.Join(src, ".git", "objects", "pack", "pack-123.pack")
	require.NoError(t, os.MkdirAll(filepath.Dir(roFile), 0700))
	require.NoError(t, os.WriteFile(roFile, []byte("readonly"), 0444))

	require.NoError(t, copyDirRecursive(src, dst))
	require.NoError(t, removeAllForce(src))

	_, err := os.Stat(src)
	require.True(t, os.IsNotExist(err))
	got, err := os.ReadFile(filepath.Join(dst, ".git", "objects", "pack", "pack-123.pack"))
	require.NoError(t, err)
	require.Equal(t, "readonly", string(got))
	require.NoError(t, removeAllForce(dst))
}
