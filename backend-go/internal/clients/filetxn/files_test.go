package filetxn

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReplaceRejectsStaleAndMissingSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, nil, []byte("new"), "", 0600); !errors.Is(err, ErrConflict) {
		t.Fatalf("空文件不能当成不存在: %v", err)
	}
	if err := Replace(path, []byte{}, []byte("new"), "", 0600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte{}, []byte("stale"), "", 0600); !errors.Is(err, ErrConflict) {
		t.Fatalf("旧快照应冲突: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "new" {
		t.Fatalf("失败写入改变了文件: %q, %v", raw, err)
	}
}

func TestConcurrentReplaceHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	initial := []byte("initial")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, value := range []string{"first", "second"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- Replace(path, initial, []byte(value), "", 0600)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("同一快照应只有一次提交: success=%d conflict=%d", successes, conflicts)
	}
}
