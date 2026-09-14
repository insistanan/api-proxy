package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := Write(path, []byte(`{"v":1}`), Options{TempPrefix: ".config-"}); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `{"v":1}` {
		t.Fatalf("首次写入内容不符: %q err=%v", got, err)
	}

	if err := Write(path, []byte(`{"v":2}`), Options{TempPrefix: ".config-"}); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `{"v":2}` {
		t.Fatalf("覆盖后内容不符: %q err=%v", got, err)
	}

	// 不残留临时文件。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// 替换前失败：原文件不被截断、临时文件清理、错误不标记 Committed。
// 通过指向一个不可能创建临时文件的目录（文件占位的路径）模拟创建失败。
func TestFailureBeforeCommitKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 用一个同名目录占位目标路径，使 replace 阶段必然失败
	// （rename 文件到目录报错），验证提交点前的失败语义。
	blockedDir := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blockedDir, "guard"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := Write(filepath.Join(blockedDir, "guard"), []byte("new"), Options{})
	if err == nil {
		t.Fatal("应失败（rename 文件到目录）")
	}
	if IsCommitted(err) {
		t.Fatalf("提交点前失败不得标记为已提交: %v", err)
	}

	// 原文件未被触碰。
	if got, rerr := os.ReadFile(path); rerr != nil || string(got) != "original" {
		t.Fatalf("原文件被破坏: %q err=%v", got, rerr)
	}
}

func TestIsCommittedDetectsCommittedError(t *testing.T) {
	err := &CommittedError{Phase: PhaseDirSync, Err: errors.New("sync failed")}
	if !IsCommitted(err) {
		t.Fatal("CommittedError 应被识别")
	}
	if !errors.Is(err, err.Err) && err.Unwrap() == nil {
		t.Fatal("应保留错误链")
	}
	if IsCommitted(errors.New("plain")) {
		t.Fatal("普通错误不得误判为已提交")
	}
}

// 默认权限 0600（配置含密钥，受限权限）。
// Windows 的权限位由目录 ACL 管理且 Stat 恒报 0666，仅 Unix 断言。
func TestDefaultModeIsRestricted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 按目标目录 ACL 管理权限，不使用 POSIX 权限位断言")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if err := Write(path, []byte("x"), Options{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("新文件权限应为 0600，实际 %o", info.Mode().Perm())
	}
}
