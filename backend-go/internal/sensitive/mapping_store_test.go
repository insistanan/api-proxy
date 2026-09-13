package sensitive

import (
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

// TestMappingStoreEncryptedRoundTrip 加密往返：写入后能在 DB 文件二进制里
// 找到占位符（明文键）但找不到原文（密文），且 LookupPlaceholder 能解回原文。
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

	// DB 文件二进制：手机号明文绝不出现在磁盘上。
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读取 DB 文件失败: %v", err)
	}
	if strings.Contains(string(raw), original) {
		t.Fatal("明文出现在数据库文件中（加密失效）")
	}
	// HMAC 索引也不是裸 SHA-256：手工算裸哈希比对。
	// （弱验证：裸哈希前 8 字节不应出现在文件里。）
	if strings.Contains(string(raw), "31d5e0f8a4b3") { // sha256("13800138000") 前 12 hex
		t.Fatal("疑似裸哈希索引出现在数据库文件中")
	}
}

// TestMappingStoreRestartRestore 跨"重启"还原：store A 写入 → close →
// 用同一主密钥新建 store B → Vault B 还原出原文。
func TestMappingStoreRestartRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	keyPath := filepath.Join(dir, "master-key")

	// 第一次运行：掩码并落盘。
	storeA, err := NewMappingStore(dbPath, keyPath)
	if err != nil {
		t.Fatalf("创建映射库 A 失败: %v", err)
	}
	SetPersistentMappingStore(storeA)
	t.Cleanup(func() { SetPersistentMappingStore(nil) })
	vaultA := NewVault()
	masked, err := vaultA.Mask("电话 13800138000", []RedactionMatch{{
		Type: "PHONE", Rule: "phone", Original: "13800138000", Start: 7, End: 18,
	}})
	if err != nil {
		t.Fatalf("掩码失败: %v", err)
	}
	if strings.Contains(masked, "13800138000") {
		t.Fatalf("应已掩码: %q", masked)
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
	SetPersistentMappingStore(storeB)
	vaultB := NewVault()
	restored := vaultB.Restore(masked)
	if restored != "电话 13800138000" {
		t.Fatalf("跨重启还原失败: %q", restored)
	}

	// 同一原文在新进程生成同一占位符（提示词缓存稳定）。
	// "另一个请求也提 " 7 个汉字（21 字节）+ 空格 = 22 字节，原文区间 [22, 33)。
	vaultC := NewVault()
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
	start := strings.Index(masked, "MASKED_")
	if start < 0 {
		return ""
	}
	end := strings.Index(masked[start:], "_PRESERVE_EXACTLY")
	if end < 0 {
		return ""
	}
	return masked[start : start+end+len("_PRESERVE_EXACTLY")]
}

// TestMappingStoreRotatedKeyCannotRestore 主密钥轮换后旧占位符还原 miss：
// 原样下发 + miss 审计回调触发。
func TestMappingStoreRotatedKeyCannotRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mappings.db")
	keyPathA := filepath.Join(dir, "master-key-a")
	keyPathB := filepath.Join(dir, "master-key-b")

	storeA, err := NewMappingStore(dbPath, keyPathA)
	if err != nil {
		t.Fatalf("创建映射库 A 失败: %v", err)
	}
	SetPersistentMappingStore(storeA)
	t.Cleanup(func() { SetPersistentMappingStore(nil) })
	vaultA := NewVault()
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
	SetPersistentMappingStore(storeB)

	missCount := 0
	SetPersistentMissCallback(func(string) { missCount++ })
	t.Cleanup(func() { SetPersistentMissCallback(nil) })

	vaultB := NewVault()
	restored := vaultB.Restore(masked)
	if restored != masked {
		t.Fatalf("密钥轮换后应原样下发: %q", restored)
	}
	if missCount == 0 {
		t.Fatal("还原 miss 应触发审计回调")
	}
}

// TestMappingStorePurge TTL 过期清理。
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

// TestMappingStoreNilIsNoOp 零值/nil store 全部 no-op（block 模式退化路径）。
func TestMappingStoreNilIsNoOp(t *testing.T) {
	store, err := NewMappingStore("", "")
	if err != nil {
		t.Fatalf("空路径应返回 nil store: %v", err)
	}
	if store != nil {
		t.Fatal("空路径应返回 nil store")
	}
	var nilStore *MappingStore
	if _, err := nilStore.LookupOriginal("x"); err != nil {
		t.Fatalf("nil store LookupOriginal 应 no-op: %v", err)
	}
	if _, err := nilStore.LookupPlaceholder("y"); err != nil {
		t.Fatalf("nil store LookupPlaceholder 应 no-op: %v", err)
	}
	if err := nilStore.PutMapping("a", "T", "b"); err != nil {
		t.Fatalf("nil store PutMapping 应 no-op: %v", err)
	}
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil store Close 应 no-op: %v", err)
	}
}
