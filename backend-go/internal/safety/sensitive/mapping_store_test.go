package sensitive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newMappingTestEnv 创建持久映射库测试环境；返回库与主密钥路径。
func newMappingTestEnv(t *testing.T) (*MappingStore, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	keyPath := filepath.Join(dir, "master-key")
	store, err := NewMappingStore(dbPath, keyPath)
	if err != nil {
		t.Fatalf("创建映射库失败: %v", err)
	}
	return store, keyPath
}

// TestMappingStoreEncryptedRoundTrip 验证加密往返：原文不落盘，且能按占位符与原文双向查回。
func TestMappingStoreEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	store, err := NewMappingStore(dbPath, filepath.Join(dir, "master-key"))
	if err != nil {
		t.Fatalf("创建映射库失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	original := "13800138000"
	if err := store.PutMapping(original, "PHONE", "MASKED_PHONE_ABCDEFGHIJKLM_PRESERVE_EXACTLY"); err != nil {
		t.Fatalf("写入映射失败: %v", err)
	}
	lookup, err := store.LookupPlaceholder("MASKED_PHONE_ABCDEFGHIJKLM_PRESERVE_EXACTLY")
	if err != nil || !lookup.Found {
		t.Fatalf("按占位符查原文失败: %v %+v", err, lookup)
	}
	if lookup.Original != original {
		t.Fatalf("解密结果不符: %q", lookup.Original)
	}
	// 按原文查占位符。
	byOriginal, err := store.LookupOriginal(original)
	if err != nil || !byOriginal.Found || byOriginal.Placeholder != "MASKED_PHONE_ABCDEFGHIJKLM_PRESERVE_EXACTLY" {
		t.Fatalf("按原文查占位符失败: %v %+v", err, byOriginal)
	}

	// 数据库文件中不得出现明文原文。
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读取 DB 文件失败: %v", err)
	}
	if strings.Contains(string(raw), original) {
		t.Fatal("明文出现在数据库文件中（加密失效）")
	}
}

// TestMappingStoreRestartRestore 验证同一主密钥下，重新打开映射库后仍能还原且占位符保持稳定。
func TestMappingStoreRestartRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	keyPath := filepath.Join(dir, "master-key")

	// 第一次运行：掩码并落盘。
	storeA, err := NewMappingStore(dbPath, keyPath)
	if err != nil {
		t.Fatalf("创建映射库 A 失败: %v", err)
	}
	vaultA := storeA.NewScopedVault(context.Background(), "test-session", nil)
	masked, err := vaultA.Mask("电话 13800138000", []RedactionMatch{{
		Type: "PHONE", Rule: "phone", Original: "13800138000", Start: 7, End: 18,
	}})
	if err != nil {
		t.Fatalf("掩码失败: %v", err)
	}
	if strings.Contains(masked, "13800138000") {
		t.Fatalf("应已掩码: %q", masked)
	}
	if err := vaultA.CommitScope(); err != nil {
		t.Fatal(err)
	}
	if err := storeA.Close(); err != nil {
		t.Fatalf("关闭映射库 A 失败: %v", err)
	}

	// "重启"：同一主密钥新建 store B + 全新 Vault（模拟新请求）。
	storeB, err := NewMappingStore(dbPath, keyPath)
	if err != nil {
		t.Fatalf("创建映射库 B 失败: %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })
	vaultB := storeB.NewScopedVault(context.Background(), "test-session", nil)
	restored := vaultB.Restore(masked)
	if restored != "电话 13800138000" {
		t.Fatalf("跨重启还原失败: %q", restored)
	}

	// 同一原文在新进程生成同一占位符（提示词缓存稳定）。
	// "另一个请求也提 " 7 个汉字（21 字节）+ 空格 = 22 字节，原文区间 [22, 33)。
	vaultC := storeB.NewVault(context.Background(), nil)
	again, err := vaultC.Mask("另一个请求也提 13800138000", []RedactionMatch{{
		Type: "PHONE", Rule: "phone", Original: "13800138000", Start: 22, End: 33,
	}})
	if err != nil {
		t.Fatalf("二次掩码失败: %v", err)
	}
	if again != "另一个请求也提 "+placeholderTokenOf(masked) {
		t.Fatalf("跨重启占位符应一致: %q vs %q", again, masked)
	}
}

// placeholderTokenOf 从掩码文本中提取完整占位符。
func placeholderTokenOf(masked string) string {
	return reservedPlaceholderPattern.FindString(masked)
}

// TestMappingStoreRotatedKeyCannotRestore 验证主密钥更换后旧占位符无法还原：原样下发并触发 miss 回调。
func TestMappingStoreRotatedKeyCannotRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	keyPathA := filepath.Join(dir, "master-key-a")
	keyPathB := filepath.Join(dir, "master-key-b")

	storeA, err := NewMappingStore(dbPath, keyPathA)
	if err != nil {
		t.Fatalf("创建映射库 A 失败: %v", err)
	}
	vaultA := storeA.NewVault(context.Background(), nil)
	masked, err := vaultA.Mask("邮箱 user@example.com", []RedactionMatch{{
		Type: "EMAIL", Rule: "email", Original: "user@example.com", Start: 7, End: 23,
	}})
	if err != nil {
		t.Fatalf("掩码失败: %v", err)
	}
	if err := storeA.Close(); err != nil {
		t.Fatalf("关闭映射库 A 失败: %v", err)
	}

	// 轮换：新主密钥 + 新库文件（模拟运维清空/换库）。
	storeB, err := NewMappingStore(filepath.Join(dir, "mappings-b.db"), keyPathB)
	if err != nil {
		t.Fatalf("创建映射库 B 失败: %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })
	missCount := 0
	vaultB := storeB.NewVault(context.Background(), func(context.Context, string) error {
		missCount++
		return nil
	})
	restored := vaultB.Restore(masked)
	if restored != masked {
		t.Fatalf("密钥轮换后应原样下发: %q", restored)
	}
	if missCount == 0 {
		t.Fatal("还原 miss 应触发审计回调")
	}
}

// TestMappingStorePurge 验证 TTL 过期行被清理。
func TestMappingStorePurge(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.PutMapping("13800138000", "PHONE", "MASKED_PHONE_AAAA_PRESERVE_EXACTLY"); err != nil {
		t.Fatalf("写入映射失败: %v", err)
	}
	// 手动把 last_access 拨回 TTL 之前再 Purge。
	if _, err := store.db.Exec(`UPDATE redaction_mappings SET last_access = '2020-01-01T00:00:00.000000000Z'`); err != nil {
		t.Fatalf("回拨访问时间失败: %v", err)
	}
	if err := store.Purge(); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	count, err := store.RowCount()
	if err != nil || count != 0 {
		t.Fatalf("TTL 过期行应被清理: %v %d", err, count)
	}
}
