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
