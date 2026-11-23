package clients

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/clients/skills"
	"github.com/stretchr/testify/require"
)

func TestSkillTranslationRetriesAfterFirstPassAndSkipsSavedTranslation(t *testing.T) {
	root := t.TempDir()
	api := NewSkillsAPI(func() (string, error) { return root, nil })
	for _, name := range []string{"alpha", "beta", "gamma", "saved"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(path, 0700))
		content := fmt.Sprintf("---\nname: %s\ndescription: 项目技能说明\n---\n# 工作流程\n\n用于处理日常任务。\n", name)
		require.NoError(t, os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0600))
	}
	saved := skills.Item{Name: "saved", LocationKey: "project", Path: filepath.Join(root, "saved")}
	original, err := skills.ReadContent(saved)
	require.NoError(t, err)
	_, err = skills.SaveTranslationBackup(root, saved, original, skills.BackupInput{Translated: "---\nname: saved\ndisplay_name: Saved Skill\ndescription: Project skill description\n---\n# Workflow\n"})
	require.NoError(t, err)

	jobRoot, targets, skipped, err := api.projectTranslationTargets()
	require.NoError(t, err)
	require.Equal(t, 1, skipped)
	require.Len(t, targets, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.translations.task = &skillTranslationTask{Running: true, Skipped: skipped}
	api.translations.cancel = cancel
	api.translations.done = make(chan struct{})
	var calls []string
	alphaAttempts := 0
	api.runSkillTranslation(ctx, jobRoot, targets, skillTranslationRequest{Model: "test"}, func(_ context.Context, _, _, _, instructions, content string) (string, string, error) {
		name, _, parseErr := skills.ParseFrontmatter([]byte(content))
		require.NoError(t, parseErr)
		require.Contains(t, instructions, "翻译为英文")
		calls = append(calls, name)
		if name == "alpha" {
			alphaAttempts++
			if alphaAttempts == 1 {
				return "", "", errors.New("首次翻译失败")
			}
		}
		if name == "beta" {
			return "", "", errors.New("翻译失败")
		}
		translation := fmt.Sprintf("---\nname: %s\ndisplay_name: Project Skill\ndescription: Project skill description\n---\n# Workflow\n\nHandles daily tasks.\n", name)
		return translation, "测试渠道", nil
	})

	require.Equal(t, []string{"alpha", "beta", "gamma", "alpha", "beta"}, calls)
	result := api.translations.task
	require.False(t, result.Running)
	require.Equal(t, 2, result.Succeeded)
	require.Equal(t, 1, result.Skipped)
	require.Len(t, result.Failures, 1)
	require.Equal(t, "beta", result.Failures[0].Name)
	_, remaining, skipped, err := api.projectTranslationTargets()
	require.NoError(t, err)
	require.Equal(t, 3, skipped)
	require.Len(t, remaining, 1)
	require.Equal(t, "beta", remaining[0].Name)
}
