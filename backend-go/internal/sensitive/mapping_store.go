package sensitive

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 加密持久化映射库（参考 Veil ADR 0004 方案）：
// 明文列 AES-256-GCM 加密（每行独立随机 nonce），等值查找索引用
// HMAC-SHA256(macKey, original) 而非裸哈希——只偷到数据库文件的人
// 无法对手机号等有限字符集做彩虹表反查。
// 主密钥 32 字节存独立文件（0600），HKDF-SHA256 派生 enc/mac 两把子钥。
//
// 语义：mask 模式启用时 Vault 优先查持久库（同原文跨重启同占位符），
// 读失败降级进程内映射（不中断）；写失败按"掩码失败"处理（不转发）。

const (
	mappingTTL           = 90 * 24 * time.Hour
	mappingMaxRows       = 100_000
	mappingTimeLayout    = "2006-01-02T15:04:05.000000000Z07:00"
	mappingPurgeEvery    = 24 * time.Hour
	masterKeyFilePerm    = 0600
	hkdfSaltMappingStore = "api-proxy-redaction-mappings-v1"
)

var (
	// ErrMappingStoreClosed 表示映射库已关闭。
	ErrMappingStoreClosed = errors.New("映射库已关闭")
)

// MappingStore 是加密持久化的占位符↔原文映射库。
type MappingStore struct {
	db            *sql.DB
	masterKeyPath string
	mu            sync.RWMutex
	closed        bool
	closeOnce     sync.Once
	encKey        [32]byte
	macKey        [32]byte
	purgeStop     chan struct{}
	purgeOnce     sync.Once
}

// NewMappingStore 打开（或创建）映射库并加载/生成主密钥。
// masterKeyPath 指向 32 字节主密钥文件；文件不存在时生成随机密钥并写入（0600）。
// masterKeyPath 为空表示"仅进程内"（不落盘）——返回 nil store，调用方按无持久化处理。
func NewMappingStore(dbPath, masterKeyPath string) (*MappingStore, error) {
	dbPath = strings.TrimSpace(dbPath)
	masterKeyPath = strings.TrimSpace(masterKeyPath)
	if dbPath == "" || masterKeyPath == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("创建映射库目录失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(masterKeyPath), 0755); err != nil {
		return nil, fmt.Errorf("创建主密钥目录失败: %w", err)
	}
	masterKey, err := loadOrCreateMasterKey(masterKeyPath)
	if err != nil {
		return nil, err
	}
	encKey, macKey := deriveMappingKeys(masterKey)

	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开映射库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &MappingStore{
		db:            db,
		masterKeyPath: masterKeyPath,
		encKey:        encKey,
		macKey:        macKey,
		purgeStop:     make(chan struct{}),
	}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// 启动即清理一次过期/超额行，之后每日一次。
	_ = store.Purge()
	go store.purgeLoop()
	return store, nil
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	if key, err := os.ReadFile(path); err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("主密钥文件 %s 长度非法（期望 32 字节，实际 %d）", path, len(key))
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取主密钥文件失败: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成主密钥失败: %w", err)
	}
	if err := os.WriteFile(path, key, masterKeyFilePerm); err != nil {
		return nil, fmt.Errorf("写入主密钥文件失败: %w", err)
	}
	return key, nil
}

// deriveMappingKeys 用 HKDF-SHA256 从主密钥派生 enc/mac 两把子钥（RFC 5869，
// salt 固定、info 区分用途；标准库无直接 HKDF，Expand 语义十行实现，不引依赖）。
func deriveMappingKeys(masterKey []byte) (encKey, macKey [32]byte) {
	// Extract: PRK = HMAC(salt, IKM)
	extractor := hmac.New(sha256.New, []byte(hkdfSaltMappingStore))
	extractor.Write(masterKey)
	prk := extractor.Sum(nil)
	// Expand: T(1) = HMAC(PRK, info || 0x01)，T(2) = HMAC(PRK, T(1) || info || 0x02)
	encKey = hkdfExpandBlock(prk, []byte("enc"))
	macKey = hkdfExpandBlock(prk, []byte("mac"))
	return encKey, macKey
}

func hkdfExpandBlock(prk, info []byte) [32]byte {
	mac := hmac.New(sha256.New, prk)
	mac.Write(info)
	mac.Write([]byte{0x01})
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func (s *MappingStore) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS redaction_mappings (
		placeholder TEXT PRIMARY KEY,
		type TEXT NOT NULL DEFAULT '',
		original_hmac BLOB NOT NULL,
		original_ct BLOB NOT NULL,
		created_at TEXT NOT NULL,
		last_access TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("初始化映射表失败: %w", err)
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_redaction_hmac ON redaction_mappings(original_hmac)`,
		`CREATE INDEX IF NOT EXISTS idx_redaction_lru ON redaction_mappings(last_access)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("初始化映射索引失败: %w", err)
		}
	}
	return nil
}

func (s *MappingStore) purgeLoop() {
	ticker := time.NewTicker(mappingPurgeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.Purge()
		case <-s.purgeStop:
			return
		}
	}
}

// MappingLookup 是一次持久库查询的结果。
type MappingLookup struct {
	// Found 为 true 时 Placeholder / Original 有效。
	Found       bool
	Placeholder string
	Original    string
}

// LookupOriginal 按原文查占位符（掩码方向）。store 为 nil（仅进程内）时恒 miss。
func (s *MappingStore) LookupOriginal(original string) (MappingLookup, error) {
	if s == nil {
		return MappingLookup{}, nil
	}
	if err := s.ensureOpen(); err != nil {
		return MappingLookup{}, err
	}
	row := s.db.QueryRow(`SELECT placeholder FROM redaction_mappings WHERE original_hmac = ?`, s.hmacOriginal(original))
	var placeholder string
	if err := row.Scan(&placeholder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MappingLookup{}, nil
		}
		return MappingLookup{}, fmt.Errorf("查询持久映射失败: %w", err)
	}
	return MappingLookup{Found: true, Placeholder: placeholder, Original: original}, nil
}

// LookupPlaceholder 按占位符查原文（还原方向），并刷新 last_access。
func (s *MappingStore) LookupPlaceholder(placeholder string) (MappingLookup, error) {
	if s == nil {
		return MappingLookup{}, nil
	}
	if err := s.ensureOpen(); err != nil {
		return MappingLookup{}, err
	}
	row := s.db.QueryRow(`SELECT original_ct FROM redaction_mappings WHERE placeholder = ?`, placeholder)
	var ciphertext []byte
	if err := row.Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MappingLookup{}, nil
		}
		return MappingLookup{}, fmt.Errorf("查询持久映射失败: %w", err)
	}
	original, err := s.decrypt(ciphertext)
	if err != nil {
		return MappingLookup{}, fmt.Errorf("解密持久映射失败: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE redaction_mappings SET last_access = ? WHERE placeholder = ?`,
		time.Now().UTC().Format(mappingTimeLayout), placeholder); err != nil {
		// 刷新访问时间失败不影响本次还原结果。
		_ = err
	}
	return MappingLookup{Found: true, Placeholder: placeholder, Original: string(original)}, nil
}

// PutMapping 写入一条映射（原文已存在时复用既有占位符）。
func (s *MappingStore) PutMapping(original, typeTag, placeholder string) error {
	if s == nil {
		return nil
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if strings.TrimSpace(original) == "" || strings.TrimSpace(placeholder) == "" {
		return fmt.Errorf("持久映射的原文与占位符不能为空")
	}
	ciphertext, err := s.encrypt([]byte(original))
	if err != nil {
		return fmt.Errorf("加密持久映射失败: %w", err)
	}
	now := time.Now().UTC().Format(mappingTimeLayout)
	if _, err := s.db.Exec(`INSERT INTO redaction_mappings (placeholder, type, original_hmac, original_ct, created_at, last_access)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(placeholder) DO UPDATE SET last_access = excluded.last_access`,
		placeholder, typeTag, s.hmacOriginal(original), ciphertext, now, now); err != nil {
		return fmt.Errorf("写入持久映射失败: %w", err)
	}
	return nil
}

// Purge 清理过期（TTL）与超额（LRU）行。
func (s *MappingStore) Purge() error {
	if s == nil {
		return nil
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-mappingTTL).Format(mappingTimeLayout)
	if _, err := s.db.Exec(`DELETE FROM redaction_mappings WHERE last_access <= ?`, cutoff); err != nil {
		return fmt.Errorf("清理过期映射失败: %w", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM redaction_mappings`).Scan(&count); err != nil {
		return fmt.Errorf("统计映射行数失败: %w", err)
	}
	if count > mappingMaxRows {
		excess := count - mappingMaxRows
		if _, err := s.db.Exec(`DELETE FROM redaction_mappings WHERE placeholder IN (
			SELECT placeholder FROM redaction_mappings ORDER BY last_access ASC, rowid ASC LIMIT ?)`, excess); err != nil {
			return fmt.Errorf("清理超额映射失败: %w", err)
		}
	}
	return nil
}

// RowCount 返回当前行数（测试与运维观测用）。
func (s *MappingStore) RowCount() (int, error) {
	if s == nil {
		return 0, nil
	}
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM redaction_mappings`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Close 幂等关闭数据库与清理循环。
func (s *MappingStore) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.purgeOnce.Do(func() { close(s.purgeStop) })
		closeErr = s.db.Close()
	})
	return closeErr
}

func (s *MappingStore) ensureOpen() error {
	if s == nil || s.db == nil {
		return ErrMappingStoreClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrMappingStoreClosed
	}
	return nil
}

// PlaceholderKey 返回占位符派生密钥（从主密钥 HKDF 派生，info="placeholder"）。
// Vault 在持久模式下用它替代进程随机密钥，保证同一原文跨重启生成同一占位符。
// 派生与 deriveMappingKeys 同构但 info 不同，与 enc/mac 子钥互不等值。
func (s *MappingStore) PlaceholderKey() ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("映射库未初始化")
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	// 主密钥不长期驻留内存：派生时从密钥文件重读，用后丢弃。
	// 映射库构造时已校验过文件，此处重读失败按错误处理（fail-closed）。
	key, err := os.ReadFile(s.masterKeyPath)
	if err != nil {
		return nil, fmt.Errorf("读取主密钥失败: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("主密钥文件长度非法")
	}
	extractor := hmac.New(sha256.New, []byte(hkdfSaltMappingStore))
	extractor.Write(key)
	prk := extractor.Sum(nil)
	derived := hkdfExpandBlock(prk, []byte("placeholder"))
	return derived[:], nil
}

func (s *MappingStore) hmacOriginal(original string) []byte {
	mac := hmac.New(sha256.New, s.macKey[:])
	mac.Write([]byte(original))
	return mac.Sum(nil)
}

// encrypt 以 AES-256-GCM 加密，输出 nonce || ciphertext||tag。
func (s *MappingStore) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *MappingStore) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("密文长度非法")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}
