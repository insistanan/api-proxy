package sensitive

import (
	"encoding/base32"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkVaultMaskExistingMappings(b *testing.B) {
	dir := b.TempDir()
	store, err := NewMappingStore(filepath.Join(dir, "mappings.db"), filepath.Join(dir, "master-key"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Error(err)
		}
	})
	var source strings.Builder
	var matches []RedactionMatch
	for index := 0; index < 64; index++ {
		original := fmt.Sprintf("synthetic-secret-%03d", index)
		token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(fmt.Sprintf("%08d", index)))
		placeholder := "MASKED_SECRET_" + token + "_PRESERVE_EXACTLY"
		if err := store.PutMapping(original, "SECRET", placeholder); err != nil {
			b.Fatal(err)
		}
		start := source.Len()
		source.WriteString(original)
		matches = append(matches, RedactionMatch{Type: "SECRET", Rule: "test", Original: original, Start: start, End: source.Len()})
		source.WriteString("; ")
	}
	text := source.String()
	state := &placeholderKeyState{store: store}
	b.ReportAllocs()
	for b.Loop() {
		vault := NewVault()
		vault.state = state
		if _, err := vault.Mask(text, matches); err != nil {
			b.Fatal(err)
		}
	}
}

// 使用合成数据区分本地还原、加密计算和持久映射访问的开销。
func BenchmarkStreamRestorer(b *testing.B) {
	for _, size := range []int{32, 4096} {
		name := "短片段"
		if size == 4096 {
			name = "长片段"
		}
		b.Run(name, func(b *testing.B) {
			fragment := strings.Repeat("ordinary response text; ", size/23+1)[:size]
			restorer := NewStreamRestorer(NewVault())
			b.SetBytes(int64(len(fragment)))
			b.ReportAllocs()
			for b.Loop() {
				if got := restorer.Feed(fragment); got != fragment {
					b.Fatal("普通片段不应被改写")
				}
			}
		})
	}
}

func BenchmarkMappingOperations(b *testing.B) {
	dir := b.TempDir()
	store, err := NewMappingStore(filepath.Join(dir, "mappings.db"), filepath.Join(dir, "master-key"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Error(err)
		}
	})
	original := "synthetic-sensitive-value-for-local-benchmark"
	placeholder := "MASKED_SECRET_ABCDEFGHIJKLM_PRESERVE_EXACTLY"
	if err := store.PutMapping(original, "SECRET", placeholder); err != nil {
		b.Fatal(err)
	}
	b.Run("AES加密", func(b *testing.B) {
		plaintext := []byte(original)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := store.encrypt(plaintext); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("已有原文查询", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			lookup, err := store.LookupOriginal(original)
			if err != nil || !lookup.Found || lookup.Placeholder != placeholder {
				b.Fatalf("查询已有映射失败: %v", err)
			}
		}
	})
	b.Run("历史占位符还原", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			lookup, err := store.LookupPlaceholder(placeholder)
			if err != nil || !lookup.Found || lookup.Original != original {
				b.Fatalf("还原历史映射失败: %v", err)
			}
		}
	})
}
