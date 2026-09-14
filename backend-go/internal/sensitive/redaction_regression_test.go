package sensitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStreamRestorerPlainFragmentsPreserveIdentifierBoundaries(t *testing.T) {
	vault := NewVault()
	original := "synthetic-original"
	placeholder, err := vault.Mask(original, []RedactionMatch{{
		Type: "SECRET", Rule: "test", Original: original, Start: 0, End: len(original),
	}})
	if err != nil {
		t.Fatal(err)
	}
	oversized := "MASKED_" + strings.Repeat("A", maxPlaceholderBytes+1)
	cases := []struct {
		name      string
		fragments []string
		want      string
	}{
		{"标识符前缀", []string{"prefix", placeholder, ";"}, "prefix" + placeholder + ";"},
		{"下划线前缀", []string{"_", placeholder, ";"}, "_" + placeholder + ";"},
		{"标识符后缀", []string{" ", placeholder, "_suffix", ";"}, " " + placeholder + "_suffix;"},
		{"跨片段标记", []string{"plain ", "M", "", placeholder[1:], ";"}, "plain " + original + ";"},
		{"多字节边界", []string{"中文", placeholder, "。"}, "中文" + original + "。"},
		{"普通大写字母", []string{"Markdown ", "M", "ATH notation"}, "Markdown MATH notation"},
		{"超长候选", []string{"plain ", oversized, ";"}, "plain " + oversized + ";"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			restorer := NewStreamRestorer(vault)
			var result strings.Builder
			for _, fragment := range test.fragments {
				result.WriteString(restorer.Feed(fragment))
			}
			result.WriteString(restorer.Flush())
			if got := result.String(); got != test.want {
				t.Fatalf("流式边界处理错误: got=%q want=%q", got, test.want)
			}
		})
	}
}

func TestMappingStoreBatchLookupPreservesMappingsAndAccessTimes(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	originals := make([]string, mappingLookupBatch+3)
	for index := range originals {
		originals[index] = fmt.Sprintf("synthetic-original-%d", index)
		if err := store.PutMapping(originals[index], "SECRET", fmt.Sprintf("stored-placeholder-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	const oldAccess = "2020-01-01T00:00:00.000000000Z"
	if _, err := store.db.Exec(`UPDATE redaction_mappings SET last_access = ?`, oldAccess); err != nil {
		t.Fatal(err)
	}
	query := append(append([]string(nil), originals...), originals[0], "missing-original")
	stored, err := store.lookupOriginals(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(originals) {
		t.Fatalf("批量查询不应新增缺失值或重复计数: %d", len(stored))
	}
	for index, original := range originals {
		if stored[original] != fmt.Sprintf("stored-placeholder-%d", index) {
			t.Fatalf("第 %d 条映射未复用原占位符", index)
		}
	}
	var stale int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM redaction_mappings WHERE last_access <= ?`, oldAccess).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("批量查询后仍有 %d 条访问时间未刷新", stale)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.lookupOriginals(ctx, originals); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须显式传播: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.lookupOriginals(context.Background(), originals); !errors.Is(err, ErrMappingStoreClosed) {
		t.Fatalf("已关闭的映射库必须返回错误: %v", err)
	}
}

func TestVaultMaskBatchReusesExistingAndPersistsNewMappings(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	const previous = "MASKED_SECRET_ABCDEFGHIJKLM_PRESERVE_EXACTLY"
	if err := store.PutMapping("old-value", "SECRET", previous); err != nil {
		t.Fatal(err)
	}
	vault := NewVault()
	vault.state = &placeholderKeyState{store: store}
	text := "old-value; new-value; old-value"
	var matches []RedactionMatch
	start := 0
	for _, original := range []string{"old-value", "new-value", "old-value"} {
		matches = append(matches, RedactionMatch{
			Type: "SECRET", Rule: "test", Original: original, Start: start, End: start + len(original),
		})
		start += len(original) + 2
	}
	masked, err := vault.Mask(text, matches)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(masked, previous) != 2 || vault.Restore(masked) != text {
		t.Fatal("新旧映射混合时必须复用旧占位符并可逆还原")
	}
	count, err := store.RowCount()
	if err != nil || count != 2 {
		t.Fatalf("重复原文只能登记一次，新原文必须已落库: count=%d err=%v", count, err)
	}
	restarted := NewVault()
	restarted.state = &placeholderKeyState{store: store}
	if got := restarted.Restore(masked); got != text || restarted.Err() != nil {
		t.Fatalf("新请求必须能从持久库完整还原: %v", restarted.Err())
	}
}

func TestVaultInvalidMatchDoesNotWritePartialMappings(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	vault := NewVault()
	vault.state = &placeholderKeyState{store: store}
	_, err := vault.Mask("first; second", []RedactionMatch{
		{Type: "SECRET", Rule: "test", Original: "first", Start: 0, End: 5},
		{Type: "SECRET", Rule: "test", Original: "incorrect", Start: 7, End: 13},
	})
	if err == nil {
		t.Fatal("非法命中区间必须返回错误")
	}
	count, err := store.RowCount()
	if err != nil || count != 0 {
		t.Fatalf("校验失败不应留下部分映射: count=%d err=%v", count, err)
	}
}
