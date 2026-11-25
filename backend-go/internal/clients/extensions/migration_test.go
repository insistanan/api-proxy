package extensions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

	require.NoError(t, rollback())
	_, err = os.Stat(newDir)
	require.True(t, os.IsNotExist(err))
	restored, err := os.ReadFile(filepath.Join(oldDir, "demo-skill", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "demo", string(restored))
}

func TestMigrateSkillsDirRejectsUnsafeTarget(t *testing.T) {
	t.Run("拒绝嵌套目标", func(t *testing.T) {
		root := t.TempDir()
		oldDir := filepath.Join(root, "skills")
		require.NoError(t, os.MkdirAll(oldDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(oldDir, "skills-manifest.json"), []byte("{}"), 0600))
		_, _, err := MigrateSkillsDir(oldDir, filepath.Join(oldDir, "sub"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "包含")
	})

	t.Run("拒绝非空目标", func(t *testing.T) {
		root := t.TempDir()
		oldDir := filepath.Join(root, "skills")
		require.NoError(t, os.MkdirAll(oldDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(oldDir, "skills-manifest.json"), []byte("{}"), 0600))
		nonEmpty := filepath.Join(root, "occupied")
		require.NoError(t, os.MkdirAll(nonEmpty, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep"), 0600))
		_, _, err := MigrateSkillsDir(oldDir, nonEmpty)
		require.Error(t, err)
		require.Contains(t, err.Error(), "非空")
	})
}

func TestCopyThenRemoveKeepsCompleteCopy(t *testing.T) {
	t.Run("只读 pack 文件可被复制并强制删除", func(t *testing.T) {
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
	})

	t.Run("源目录部分删除后目标仍是完整副本", func(t *testing.T) {
		root := t.TempDir()
		src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
		require.NoError(t, os.MkdirAll(src, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(src, "first"), []byte("one"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(src, "second"), []byte("two"), 0600))
		cause := errors.New("源文件被占用")
		err := copyThenRemove(src, dst, func(path string) error {
			require.NoError(t, os.Remove(filepath.Join(path, "first")))
			return cause
		})
		require.ErrorIs(t, err, cause)
		require.ErrorContains(t, err, dst)
		for name, expected := range map[string]string{"first": "one", "second": "two"} {
			data, err := os.ReadFile(filepath.Join(dst, name))
			require.NoError(t, err)
			require.Equal(t, expected, string(data))
		}
	})
}
