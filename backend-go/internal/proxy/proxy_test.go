package proxy

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoForbiddenDependencies 静态断言本包不导入 gin / handlers / SQLite 驱动
// （方案 7.3：以导入规则约束拟议 internal/proxy 的依赖方向）。
func TestNoForbiddenDependencies(t *testing.T) {
	forbidden := map[string]string{
		"github.com/gin-gonic/gin": "不得依赖 HTTP 框架",
		"internal/handlers":        "不得反向依赖 HTTP 适配层",
		"modernc.org/sqlite":       "不得直接依赖 SQLite 驱动",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", entry.Name(), err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for prefix, reason := range forbidden {
				if path == prefix || strings.HasSuffix(path, prefix) || strings.Contains(path, prefix) {
					t.Errorf("%s 导入 %s：%s", entry.Name(), path, reason)
				}
			}
		}
	}
}

func TestCandidateRetryStateLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewCandidateRetryState(2)

	if s.MaxRetries() != 4 { // 2 keys × (1 same-candidate retry + 1)
		t.Fatalf("MaxRetries = %d, want 4", s.MaxRetries())
	}

	candidate := "https://a.example.com\x00key-1"
	if !s.CanRetryCandidate(candidate, ctx) {
		t.Fatal("首次应可重试")
	}
	if got := s.RecordCandidateRetry(candidate); got != 1 {
		t.Fatalf("重试计数 = %d, want 1", got)
	}
	if s.CanRetryCandidate(candidate, ctx) {
		t.Fatal("超过同候选重试上限后不应再重试")
	}

	s.MarkKeyFailed("key-1")
	// 已取消的 context 不允许重试。
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s.CanRetryCandidate("other", canceled) {
		t.Fatal("context 已取消不应重试")
	}
}

func TestRetrySameCandidateErrorChain(t *testing.T) {
	inner := errors.New("inner")
	err := NewRetrySameCandidateError(inner)
	if !IsRetrySameCandidateError(err) {
		t.Fatal("应识别为同候选重试错误")
	}
	if !errors.Is(err, inner) {
		t.Fatal("应保留错误链")
	}
	if IsRetrySameCandidateError(errors.New("plain")) {
		t.Fatal("普通错误不得误判")
	}
}

func TestIsClientSideCancel(t *testing.T) {
	if !IsClientSideCancel(context.Canceled) {
		t.Fatal("context.Canceled 应识别为客户端取消")
	}
	if !IsClientSideCancel(errors.New("write tcp [::1]:9996->[::1]:9657: wsasend: An existing connection was forcibly closed by the remote host")) {
		t.Fatal("Windows 客户端断开写回错误应识别为客户端取消")
	}
	if !IsClientSideCancel(errors.New("write tcp [::1]:9996->[::1]:6740: wsasend: An established connection was aborted by the software in your host machine.")) {
		t.Fatal("Windows 本机中止写回错误应识别为客户端取消")
	}
	if IsClientSideCancel(errors.New("broken pipe")) {
		t.Fatal("连接故障不是客户端取消")
	}
	if IsClientSideCancel(nil) {
		t.Fatal("nil 不是取消")
	}
}
