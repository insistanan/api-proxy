//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopOptionsKeepsDataAndProjectDirectoriesIndependent(t *testing.T) {
	data, project := t.TempDir(), t.TempDir()
	options, err := parseDesktopOptions([]string{"--data-dir", data, "--project-dir", project})
	require.NoError(t, err)
	require.Equal(t, data, options.paths.root)
	require.Equal(t, project, options.projectDir)

	options, err = parseDesktopOptions([]string{"--data-dir", data})
	require.NoError(t, err)
	require.Equal(t, data, options.projectDir)
	options, err = parseDesktopOptions(nil)
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	expectedRoot := desktopDataRoot(executable, runtime.GOOS)
	require.Equal(t, expectedRoot, options.paths.root)
	require.Equal(t, expectedRoot, options.projectDir)

	// 从不同工作目录启动仍读取程序旁的数据，不因双击或快捷方式改变。
	t.Chdir(t.TempDir())
	options, err = parseDesktopOptions(nil)
	require.NoError(t, err)
	require.Equal(t, expectedRoot, options.paths.root)

	// macOS 应用包之外保存数据，裸二进制仍使用所在目录。
	root := t.TempDir()
	executable = filepath.Join(root, "API Proxy.app", "Contents", "MacOS", "api-proxy-desktop")
	require.Equal(t, root, desktopDataRoot(executable, "darwin"))
	for _, goos := range []string{"windows", "linux", "darwin"} {
		require.Equal(t, root, desktopDataRoot(filepath.Join(root, "api-proxy-desktop"), goos))
	}
	executable = filepath.Join(root, "Contents", "MacOS", "api-proxy-desktop")
	require.Equal(t, filepath.Dir(executable), desktopDataRoot(executable, "darwin"))

	_, err = parseDesktopOptions([]string{"unexpected"})
	require.Error(t, err)
	_, err = parseDesktopOptions([]string{"--unknown"})
	require.Error(t, err)
}

func TestDesktopOriginAcceptsPageURLsAndRejectsOtherOrigins(t *testing.T) {
	const address = "http://127.0.0.1:3000"
	for _, source := range []string{address, address + "/channels/messages?type=messages", address + "#settings"} {
		require.True(t, desktopSameOrigin(source, address), source)
	}
	for _, source := range []string{"", "null", "about:blank", "http://localhost:3000", "http://127.0.0.1:3001", "https://127.0.0.1:3000", "http://127.0.0.1:3000.evil.test", "http://user@127.0.0.1:3000"} {
		require.False(t, desktopSameOrigin(source, address), source)
	}
	require.False(t, desktopSameOrigin("", ""))
}
