package sensitive

import (
	"context"
	"fmt"
	"time"
)

// 会话授权与加密原文分离。旧映射不会自动取得任意会话的恢复权限。
func (s *MappingStore) initScopeSchema() error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS redaction_scopes (scope BLOB PRIMARY KEY, invalid INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS redaction_scope_mappings (scope BLOB NOT NULL, placeholder TEXT NOT NULL, PRIMARY KEY(scope, placeholder))`,
		`CREATE INDEX IF NOT EXISTS idx_redaction_scope_placeholder ON redaction_scope_mappings(placeholder)`,
		`CREATE TABLE IF NOT EXISTS redaction_scope_revisions (scope BLOB PRIMARY KEY, revision INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO redaction_scope_revisions SELECT scope, 0 FROM redaction_scopes`,
		`CREATE TRIGGER IF NOT EXISTS redaction_scope_revision_insert AFTER INSERT ON redaction_scopes BEGIN
			INSERT INTO redaction_scope_revisions VALUES (NEW.scope, 1) ON CONFLICT(scope) DO UPDATE SET revision = revision + 1; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_scope_revision_delete AFTER DELETE ON redaction_scopes BEGIN
			UPDATE redaction_scope_revisions SET revision = revision + 1 WHERE scope = OLD.scope; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_grant_revision_insert AFTER INSERT ON redaction_scope_mappings BEGIN
			UPDATE redaction_scope_revisions SET revision = revision + 1 WHERE scope = NEW.scope; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_grant_revision_delete AFTER DELETE ON redaction_scope_mappings BEGIN
			UPDATE redaction_scope_revisions SET revision = revision + 1 WHERE scope = OLD.scope; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_grant_revision_update AFTER UPDATE ON redaction_scope_mappings BEGIN
			UPDATE redaction_scope_revisions SET revision = revision + 1 WHERE scope = OLD.scope OR scope = NEW.scope; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_mapping_revision_update AFTER UPDATE ON redaction_mappings
			WHEN NEW.original_ct != OLD.original_ct OR NEW.original_hmac != OLD.original_hmac OR NEW.placeholder != OLD.placeholder OR NEW.last_access < OLD.last_access BEGIN
			UPDATE redaction_scope_revisions SET revision = revision + 1 WHERE scope IN
				(SELECT scope FROM redaction_scope_mappings WHERE placeholder = OLD.placeholder); END`,
		`CREATE TABLE IF NOT EXISTS redaction_scope_counts (id INTEGER PRIMARY KEY CHECK(id = 1), scopes INTEGER NOT NULL, grants INTEGER NOT NULL)`,
		`INSERT OR REPLACE INTO redaction_scope_counts SELECT 1, (SELECT COUNT(*) FROM redaction_scopes), (SELECT COUNT(*) FROM redaction_scope_mappings)`,
		`CREATE TRIGGER IF NOT EXISTS redaction_scope_count_insert AFTER INSERT ON redaction_scopes BEGIN UPDATE redaction_scope_counts SET scopes = scopes + 1 WHERE id = 1; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_scope_count_delete AFTER DELETE ON redaction_scopes BEGIN UPDATE redaction_scope_counts SET scopes = scopes - 1 WHERE id = 1; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_grant_count_insert AFTER INSERT ON redaction_scope_mappings BEGIN UPDATE redaction_scope_counts SET grants = grants + 1 WHERE id = 1; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_grant_count_delete AFTER DELETE ON redaction_scope_mappings BEGIN UPDATE redaction_scope_counts SET grants = grants - 1 WHERE id = 1; END`,
		`CREATE TRIGGER IF NOT EXISTS redaction_scope_delete AFTER DELETE ON redaction_mappings BEGIN
			UPDATE redaction_scopes SET invalid = 1 WHERE scope IN (SELECT scope FROM redaction_scope_mappings WHERE placeholder = OLD.placeholder);
			DELETE FROM redaction_scope_mappings WHERE placeholder = OLD.placeholder; END`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("初始化脱敏会话授权失败: %w", err)
		}
	}
	return nil
}

func (s *MappingStore) loadScope(ctx context.Context, scope string, v *Vault) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, mappingIOTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始脱敏会话读取失败: %w", err)
	}
	defer tx.Rollback()
	scopeID := s.hmacOriginal("scope\x00" + scope)
	var invalid int
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT invalid FROM redaction_scopes WHERE scope = ?), 0),
		COALESCE((SELECT revision FROM redaction_scope_revisions WHERE scope = ?), 0)`, scopeID, scopeID).Scan(&invalid, &revision); err != nil {
		return fmt.Errorf("读取脱敏会话状态失败: %w", err)
	}
	if invalid != 0 {
		return fmt.Errorf("该会话的脱敏映射已被清理，无法保证历史安全；请以完整原始历史建立新会话")
	}
	now := time.Now()
	if cached := s.scopeCache.get(string(scopeID), revision, now); cached != nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("结束脱敏会话快照失败: %w", err)
		}
		if err := s.ensureOpen(); err != nil {
			return err
		}
		cached.install(v)
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.placeholder, m.original_ct, m.last_access FROM redaction_mappings m
		JOIN redaction_scope_mappings s ON s.placeholder = m.placeholder
		WHERE s.scope = ?`, scopeID)
	if err != nil {
		return fmt.Errorf("加载脱敏会话失败: %w", err)
	}
	defer rows.Close()
	expires := now.Add(scopeCacheTTL)
	for rows.Next() {
		var placeholder, accessed string
		var encrypted []byte
		if err := rows.Scan(&placeholder, &encrypted, &accessed); err != nil {
			return fmt.Errorf("读取脱敏会话失败: %w", err)
		}
		accessedAt, err := time.Parse(mappingTimeLayout, accessed)
		if err != nil {
			return fmt.Errorf("读取脱敏映射有效期失败: %w", err)
		}
		mappingExpires := accessedAt.Add(mappingTTL)
		if !now.Before(mappingExpires) {
			return fmt.Errorf("该会话的脱敏映射已过期，拒绝按不完整映射处理历史")
		}
		if mappingExpires.Before(expires) {
			expires = mappingExpires
		}
		original, err := s.decrypt(encrypted)
		if err != nil {
			return fmt.Errorf("解密脱敏会话失败: %w", err)
		}
		if err := v.addMappingLocked(string(original), placeholder); err != nil {
			return err
		}
		v.scopeKnown[placeholder] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历脱敏会话失败: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("结束脱敏会话读取失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("结束脱敏会话快照失败: %w", err)
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	v.scopeSnapshot = s.scopeCache.put(string(scopeID), revision, expires, v)
	return nil
}

// CommitScope 同步提交本次新识别值的授权；失败时请求不得发往上游。
// 已加载的历史映射不重复写库，常规长历史请求只读一次会话映射。
func (v *Vault) CommitScope() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return v.err
	}
	if v.scope == "" || v.state.store == nil {
		return nil
	}
	pending := make([]string, 0)
	for placeholder := range v.byPlaceholder {
		if _, known := v.scopeKnown[placeholder]; !known {
			pending = append(pending, placeholder)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	s := v.state.store
	if err := s.ensureOpen(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(v.ctx, mappingIOTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始脱敏会话事务失败: %w", err)
	}
	defer tx.Rollback()
	scope := s.hmacOriginal("scope\x00" + v.scope)
	var scopeCount, count, invalid, exists int
	if err := tx.QueryRowContext(ctx, `SELECT scopes, grants, COALESCE((SELECT invalid FROM redaction_scopes WHERE scope = ?), 0),
		EXISTS(SELECT 1 FROM redaction_scopes WHERE scope = ?) FROM redaction_scope_counts WHERE id = 1`, scope, scope).Scan(&scopeCount, &count, &invalid, &exists); err != nil {
		return fmt.Errorf("读取脱敏会话状态失败: %w", err)
	}
	if invalid != 0 || exists == 0 && scopeCount >= mappingMaxRows {
		return fmt.Errorf("脱敏会话已失效或会话总数达到容量上限")
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO redaction_scopes(scope) VALUES (?)`, scope); err != nil {
		return fmt.Errorf("登记脱敏会话失败: %w", err)
	}
	// 总授权数设置硬上限；不能静默淘汰仍可能需要再次脱敏的会话知识。
	if count+len(pending) > mappingMaxRows {
		return fmt.Errorf("脱敏会话授权超过 %d 条上限", mappingMaxRows)
	}
	for _, placeholder := range pending {
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO redaction_scope_mappings(scope, placeholder)
			SELECT ?, placeholder FROM redaction_mappings WHERE placeholder = ?`, scope, placeholder)
		if err != nil {
			return fmt.Errorf("保存脱敏会话授权失败: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("确认脱敏会话授权写入失败: %w", err)
		}
		if n == 0 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM redaction_scope_mappings WHERE scope = ? AND placeholder = ?`, scope, placeholder).Scan(&exists); err != nil {
				return fmt.Errorf("确认既有脱敏会话授权失败: %w", err)
			}
			if exists != 1 {
				return fmt.Errorf("保存脱敏会话授权失败：映射已被清理")
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交脱敏会话授权失败: %w", err)
	}
	for _, placeholder := range pending {
		v.scopeKnown[placeholder] = struct{}{}
	}
	return nil
}
