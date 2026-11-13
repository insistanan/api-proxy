package clients

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindRemoteSkillFileSupportsRootAndSingleSkillLayouts(t *testing.T) {
	tests := []struct {
		name         string
		tree         []githubTreeEntry
		requested    string
		wantFile     string
		wantRoot     string
		wantRelative []string
	}{
		{
			name: "仓库根目录",
			tree: []githubTreeEntry{
				{Path: "SKILL.md", Type: "blob"},
				{Path: "scripts/delegate.py", Type: "blob"},
				{Path: "references/runners.md", Type: "blob"},
				{Path: "README.md", Type: "blob"},
				{Path: "assets", Type: "tree"},
			},
			requested:    "oil-delegate",
			wantFile:     "SKILL.md",
			wantRoot:     ".",
			wantRelative: []string{"SKILL.md", "scripts/delegate.py", "references/runners.md", "README.md"},
		},
		{
			name: "任意单技能目录",
			tree: []githubTreeEntry{
				{Path: "skill/SKILL.md", Type: "blob"},
				{Path: "skill/scripts/run.sh", Type: "blob"},
				{Path: "README.md", Type: "blob"},
			},
			requested:    "opentui",
			wantFile:     "skill/SKILL.md",
			wantRoot:     "skill",
			wantRelative: []string{"SKILL.md", "scripts/run.sh"},
		},
		{
			name: "skills 目录按 slug 命中",
			tree: []githubTreeEntry{
				{Path: "SKILL.md", Type: "blob"},
				{Path: "skills/foo/SKILL.md", Type: "blob"},
				{Path: "skills/foo/references/a.md", Type: "blob"},
				{Path: "skills/bar/SKILL.md", Type: "blob"},
				{Path: "skills/bar/scripts/b.sh", Type: "blob"},
			},
			requested:    "foo",
			wantFile:     "skills/foo/SKILL.md",
			wantRoot:     "skills/foo",
			wantRelative: []string{"SKILL.md", "references/a.md"},
		},
		{
			name: "嵌套目录按后缀命中",
			tree: []githubTreeEntry{
				{Path: "plugins/foo/skills/bar/SKILL.md", Type: "blob"},
				{Path: "plugins/foo/skills/bar/scripts/x.sh", Type: "blob"},
				{Path: "plugins/foo/skills/other/SKILL.md", Type: "blob"},
			},
			requested:    "bar",
			wantFile:     "plugins/foo/skills/bar/SKILL.md",
			wantRoot:     "plugins/foo/skills/bar",
			wantRelative: []string{"SKILL.md", "scripts/x.sh"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skillFile, skillRoot, err := findRemoteSkillFile(test.tree, test.requested)
			require.NoError(t, err)
			require.Equal(t, test.wantFile, skillFile)
			require.Equal(t, test.wantRoot, skillRoot)

			files, err := collectRemoteSkillFiles(test.tree, skillFile, skillRoot)
			require.NoError(t, err)
			require.ElementsMatch(t, test.wantRelative, remoteSkillRelativePaths(files))
		})
	}
}

func TestFindRemoteSkillFileRejectsAmbiguousSkillLayout(t *testing.T) {
	tree := []githubTreeEntry{
		{Path: "SKILL.md", Type: "blob"},
		{Path: "other/SKILL.md", Type: "blob"},
	}
	_, _, err := findRemoteSkillFile(tree, "missing")
	require.ErrorContains(t, err, "存在多个 SKILL.md")
}

func TestFindRemoteSkillFileKeepsMissingSkillError(t *testing.T) {
	_, _, err := findRemoteSkillFile([]githubTreeEntry{{Path: "README.md", Type: "blob"}}, "foo")
	require.EqualError(t, err, "GitHub 仓库中找不到 foo/SKILL.md")
}

func TestCollectRemoteSkillFilesRejectsInvalidPath(t *testing.T) {
	tree := []githubTreeEntry{
		{Path: "SKILL.md", Type: "blob"},
		{Path: "scripts/../escape.md", Type: "blob"},
	}
	_, err := collectRemoteSkillFiles(tree, "SKILL.md", ".")
	require.ErrorContains(t, err, "无效文件路径")
}

func TestRemoteSkillTreeURL(t *testing.T) {
	const repositoryURL = "https://github.com/oil-oil/oil-delegate"
	require.Equal(t, repositoryURL+"/tree/main", remoteSkillTreeURL(repositoryURL, "main", "."))
	require.Equal(t, repositoryURL+"/tree/main/skill", remoteSkillTreeURL(repositoryURL, "main", "skill"))
	require.Equal(t, repositoryURL+"/tree/main/skills/foo", remoteSkillTreeURL(repositoryURL, "main", "skills/foo"))
}

func remoteSkillRelativePaths(files []remoteSkillFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.relative)
	}
	return paths
}
