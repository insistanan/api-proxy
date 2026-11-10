package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppPathsKeepsServerPathsAndDesktopIsolation(t *testing.T) {
	relative := filepath.Join(".config", "config.json")
	require.Equal(t, relative, (appPaths{}).resolve(relative))
	root := t.TempDir()
	paths := appPaths{root: root}
	require.Equal(t, filepath.Join(root, relative), paths.resolve(relative))
	absolute := filepath.Join(t.TempDir(), "custom-logs.db")
	require.Equal(t, absolute, paths.resolve(absolute))
}
