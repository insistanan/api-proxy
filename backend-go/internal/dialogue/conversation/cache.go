package conversation

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultCachedRecords = 256
	defaultCacheBytes    = 8 << 20
)

const storedColumns = `c.id, c.identity_key, c.api_kind, c.name, c.last_model, c.last_resolved_model,
 c.first_prompt, c.prompts_json, c.stream, c.first_seen_at, c.last_seen_at, c.last_request_at,
 c.last_completed_at, c.request_count, c.error_count, c.last_error, c.route_override_json,
 c.last_resolved_json, c.image_fingerprints_json`

// lineageScanner 复用记录解码；同一条查询读取关联信息，避免单连接内嵌套查询。
type lineageScanner struct {
	row     scanner
	lineage *lineageState
	updated int64
}

func (s *lineageScanner) Scan(dest ...any) error {
	l := s.lineage
	return s.row.Scan(append(dest, &l.ClientFamily, &l.IdentitySource, &l.ScopeHash,
		&l.LaneHash, &l.FrontierHash, &l.FrontierDepth, &l.ParentConversationID, &s.updated)...)
}

func (s *SQLiteStore) loadRecords(where string, args ...any) ([]*Record, error) {
	rows, err := s.db.Query(`SELECT `+storedColumns+`,
 COALESCE(l.client_family, ''), COALESCE(l.identity_source, ''), COALESCE(l.scope_hash, ''),
 COALESCE(l.lane_hash, ''), COALESCE(l.frontier_hash, ''), COALESCE(l.frontier_depth, 0),
 COALESCE(l.parent_conversation_id, ''), COALESCE(l.updated_at, 0)
 FROM conversations c LEFT JOIN conversation_lineages l ON l.conversation_id = c.id `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("读取持久化对话失败: %w", err)
	}
	defer rows.Close()
	records := make([]*Record, 0)
	for rows.Next() {
		var lineage lineageState
		scan := &lineageScanner{row: rows, lineage: &lineage}
		rec, err := scanRecord(scan)
		if err != nil {
			return nil, fmt.Errorf("解析持久化对话失败: %w", err)
		}
		lineage.UpdatedAt = unixTime(scan.updated)
		rec.lineage = lineage
		rec.ClientFamily, rec.IdentitySource, rec.ParentConversationID = lineage.ClientFamily, lineage.IdentitySource, lineage.ParentConversationID
		normalizeRecordPrompts(rec)
		records = append(records, rec)
	}
	return records, rows.Err()
}

func observationKeys(obs Observation) []string {
	key := buildExplicitIdentityKey(obs)
	if key == "" {
		return nil
	}
	root := firstNonEmpty(obs.Identity.ExplicitID, obs.ConversationID)
	keys := []string{key, key + "|lane:" + shortIdentityHash(obs.Identity.LaneHash), explicitIdentityKey(obs.APIKind, root)}
	if obs.Identity.ParentAgentID != "" {
		keys = append(keys, explicitIdentityKey(obs.APIKind, root+"|agent:"+obs.Identity.ParentAgentID))
	}
	return keys
}

func (r *Registry) prepareObservationLocked(obs Observation) error {
	if r.store == nil {
		return nil
	}
	key := buildExplicitIdentityKey(obs)
	if rec := r.records[r.identityIndex[key]]; key != "" && rec != nil && !shouldSplitCursorLane(rec, obs) {
		return nil
	}
	keys, err := json.Marshal(observationKeys(obs))
	if err != nil {
		return err
	}
	hashes, err := json.Marshal(obs.Transcript.PrefixHashes)
	if err != nil {
		return err
	}
	where := `WHERE c.id IN (
 SELECT id FROM conversations WHERE identity_key IN (SELECT value FROM json_each(?))
 UNION SELECT conversation_id FROM conversation_aliases WHERE alias_key IN (SELECT value FROM json_each(?))
 UNION SELECT cl.conversation_id FROM conversation_lineages cl JOIN json_each(?) p
 ON cl.frontier_depth = CAST(p.key AS INTEGER) + 1 AND cl.frontier_hash = p.value
 WHERE cl.client_family = ?
 ) AND c.api_kind = ?`
	records, err := r.store.loadRecords(where, string(keys), string(keys), string(hashes), normalizeClientFamily(obs.Identity.ClientFamily), firstNonEmpty(obs.APIKind, "unknown"))
	if err != nil {
		return err
	}
	for _, rec := range records {
		r.cacheRecordLocked(rec)
	}
	rows, err := r.store.db.Query(`SELECT alias_key, conversation_id FROM conversation_aliases WHERE alias_key IN (SELECT value FROM json_each(?))`, string(keys))
	if err != nil {
		return fmt.Errorf("读取对话别名失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var alias, id string
		if err := rows.Scan(&alias, &id); err != nil {
			return err
		}
		if r.records[id] != nil {
			r.identityIndex[alias] = id
		}
	}
	return rows.Err()
}

func (r *Registry) cacheRecordLocked(rec *Record) {
	if r.records[rec.ID] != nil {
		return
	}
	r.records[rec.ID] = rec
	r.identityIndex[rec.identityKey] = rec.ID
	r.indexLineageLocked(rec)
}

func (r *Registry) loadRecordLocked(id string) (*Record, error) {
	if rec := r.records[id]; rec != nil {
		return rec, nil
	}
	if r.store == nil {
		return nil, nil
	}
	records, err := r.store.loadRecords("WHERE c.id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	r.cacheRecordLocked(records[0])
	return records[0], nil
}

func recordCacheBytes(rec *Record) int {
	n := 1024 + len(rec.ID) + len(rec.identityKey) + len(rec.Name) + len(rec.FirstPrompt) + len(rec.LastError)
	n += len(rec.APIKind) + len(rec.LastModel) + len(rec.LastResolvedModel) + len(rec.ClientFamily) + len(rec.IdentitySource) + len(rec.ParentConversationID)
	n += len(rec.lineage.ClientFamily) + len(rec.lineage.IdentitySource) + len(rec.lineage.ScopeHash) + len(rec.lineage.LaneHash) + len(rec.lineage.FrontierHash) + len(rec.lineage.ParentConversationID)
	if rec.RouteOverride != nil {
		n += len(rec.RouteOverride.Kind) + len(rec.RouteOverride.ChannelName)
	}
	if rec.LastResolved != nil {
		n += len(rec.LastResolved.Kind) + len(rec.LastResolved.ChannelName)
	}
	for _, value := range rec.Prompts {
		n += len(value) + 16
	}
	for _, value := range rec.ImageFingerprints {
		n += len(value) + 16
	}
	return n
}

// trimCacheLocked 只淘汰已落库且不在发送的记录；磁盘历史、路由和别名保持完整。
// 活跃请求与落库失败的记录必须保留，不能为了内存预算丢失状态。
func (r *Registry) trimCacheLocked() {
	if r.store == nil {
		return
	}
	for key, id := range r.identityIndex {
		if rec := r.records[id]; rec == nil || key != rec.identityKey {
			delete(r.identityIndex, key)
		}
	}
	total := 0
	for _, rec := range r.records {
		total += recordCacheBytes(rec)
	}
	if len(r.records) <= r.maxCachedRecords && total <= r.maxCacheBytes {
		return
	}
	candidates := make([]*Record, 0, len(r.records))
	for _, rec := range r.records {
		if rec.ActiveRequests == 0 && !rec.dirty {
			candidates = append(candidates, rec)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].LastSeenAt.Before(candidates[j].LastSeenAt) })
	for _, rec := range candidates {
		if len(r.records) <= r.maxCachedRecords && total <= r.maxCacheBytes {
			break
		}
		delete(r.records, rec.ID)
		delete(r.identityIndex, rec.identityKey)
		r.unindexLineageLocked(rec)
		total -= recordCacheBytes(rec)
	}
}

// purgeExpired 删除过期正文和附属索引，不把历史载入 Go 内存；活跃记录由调用方排除。
func (s *SQLiteStore) purgeExpired(cutoff time.Time, activeIDs []string) error {
	if activeIDs == nil {
		activeIDs = []string{}
	}
	active, err := json.Marshal(activeIDs)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS expired_conversation_ids (id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM expired_conversation_ids`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO expired_conversation_ids SELECT id FROM conversations WHERE last_seen_at < ? AND id NOT IN (SELECT value FROM json_each(?))`, cutoff.Unix(), string(active)); err != nil {
		return err
	}
	for _, table := range []string{"conversation_aliases", "conversation_image_understandings", "conversation_lineages"} {
		if _, err := tx.Exec(`DELETE FROM ` + table + ` WHERE conversation_id IN (SELECT id FROM expired_conversation_ids)`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE conversation_lineages SET parent_conversation_id = '' WHERE parent_conversation_id IN (SELECT id FROM expired_conversation_ids)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conversations WHERE id IN (SELECT id FROM expired_conversation_ids)`); err != nil {
		return err
	}
	return tx.Commit()
}

// 保持独立构造入口与共享连接入口的生命周期一致。
func NewPersistentRegistryWithDB(db *sql.DB) (*Registry, error) {
	store, err := newSQLiteStore(db)
	if err != nil {
		return nil, err
	}
	return newPersistentRegistry(store)
}

func newPersistentRegistry(store *SQLiteStore) (*Registry, error) {
	if err := store.purgeExpired(time.Now().Add(-defaultMaxIdle), nil); err != nil {
		return nil, fmt.Errorf("清理过期对话失败: %w", err)
	}
	return newRegistry(store), nil
}

func matchRecordQuery(rec *Record, query string) bool {
	values := []string{rec.ID, rec.Name, rec.APIKind, rec.LastModel, rec.LastResolvedModel, rec.FirstPrompt, rec.LastError, rec.RouteOverrideString()}
	values = append(values, rec.Prompts...)
	if rec.LastResolved != nil {
		values = append(values, rec.LastResolved.ChannelName)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
