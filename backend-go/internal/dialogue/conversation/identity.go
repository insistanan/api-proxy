package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Identity 描述客户端提供的会话身份。ExplicitID 只有在字段语义明确为
// conversation/thread/composer/session 时才填写；ScopeID 仅用于缩小历史匹配范围。
type Identity struct {
	ClientFamily  string
	ExplicitID    string
	ScopeID       string
	AgentID       string
	ParentAgentID string
	Source        string
	LaneHash      string
}

// Transcript 保存协议归一化后的累计消息哈希。PrefixHashes[n-1] 表示前 n 条消息的哈希。
type Transcript struct {
	PrefixHashes []string
}

func (t Transcript) Depth() int { return len(t.PrefixHashes) }

func (t Transcript) FrontierHash() string {
	if len(t.PrefixHashes) == 0 {
		return ""
	}
	return t.PrefixHashes[len(t.PrefixHashes)-1]
}

type lineageState struct {
	ClientFamily         string
	IdentitySource       string
	ScopeHash            string
	LaneHash             string
	FrontierHash         string
	FrontierDepth        int
	ParentConversationID string
	UpdatedAt            time.Time
}

func normalizeClientFamily(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func hashIdentityScope(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func frontierIndexKey(apiKind string, clientFamily string, depth int, hash string) string {
	return strings.ToLower(strings.Join([]string{
		firstNonEmpty(strings.TrimSpace(apiKind), "unknown"),
		normalizeClientFamily(clientFamily),
		fmt.Sprintf("%d", depth),
		strings.TrimSpace(hash),
	}, "|"))
}

func scopeCompatible(stored string, incoming string) bool {
	return stored == "" || incoming == "" || stored == incoming
}

func laneCompatible(stored string, incoming string) bool {
	return stored == "" || incoming == "" || stored == incoming
}

func shortIdentityHash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 16 {
		return value
	}
	return value[:16]
}

func (r *Registry) resolveObservationLocked(obs Observation) (*Record, string, string, string) {
	explicitKey := buildExplicitIdentityKey(obs)
	if explicitKey != "" {
		explicitResolution := firstNonEmpty(obs.Identity.Source, "explicit_id")
		if strings.TrimSpace(obs.Identity.AgentID) != "" {
			explicitResolution = "agent_id"
		}
		parentID := r.resolveExplicitParentLocked(obs)
		if recordID, ok := r.identityIndex[explicitKey]; ok {
			rec := r.records[recordID]
			if rec != nil && shouldSplitCursorLane(rec, obs) {
				laneKey := explicitKey + "|lane:" + shortIdentityHash(obs.Identity.LaneHash)
				if laneRecordID, exists := r.identityIndex[laneKey]; exists {
					if laneRecord := r.records[laneRecordID]; laneRecord != nil {
						return laneRecord, laneKey, rec.ID, "explicit_agent_lane"
					}
				}
				return nil, laneKey, rec.ID, "explicit_agent_lane"
			}
			return rec, explicitKey, parentID, explicitResolution
		}
		// AssociateExternalID 保存的是不带 AgentID 的基础 alias，而主请求
		// 解析时会把 AgentID 纳入显式 key。previous_response 只允许在这里
		// 回退到基础 alias，避免普通 conversation ID 的不同 Cursor lane 被
		// 错误合并。
		if isPreviousResponseIdentity(obs.Identity) {
			baseValue := strings.TrimSpace(obs.Identity.ExplicitID)
			if baseValue == "" {
				baseValue = strings.TrimSpace(obs.ConversationID)
			}
			if recordID, ok := r.identityIndex[explicitIdentityKey(obs.APIKind, baseValue)]; ok {
				if rec := r.records[recordID]; rec != nil {
					return rec, explicitIdentityKey(obs.APIKind, baseValue), parentID, explicitResolution
				}
			}
		}
		return nil, explicitKey, parentID, explicitResolution
	}

	rec, parentID, resolution := r.resolveTranscriptLocked(obs)
	return rec, "", parentID, resolution
}

func shouldSplitCursorLane(rec *Record, obs Observation) bool {
	if rec == nil || strings.TrimSpace(obs.Identity.AgentID) != "" {
		return false
	}
	if normalizeClientFamily(obs.Identity.ClientFamily) != "cursor" {
		return false
	}
	return !laneCompatible(rec.lineage.LaneHash, strings.TrimSpace(obs.Identity.LaneHash))
}

func (r *Registry) resolveExplicitParentLocked(obs Observation) string {
	rootID := strings.TrimSpace(obs.Identity.ExplicitID)
	if rootID == "" {
		rootID = strings.TrimSpace(obs.ConversationID)
	}
	if rootID == "" {
		return ""
	}
	parentAgentID := strings.TrimSpace(obs.Identity.ParentAgentID)
	parentValue := rootID
	if parentAgentID != "" {
		parentValue += "|agent:" + parentAgentID
	}
	if recordID, ok := r.identityIndex[explicitIdentityKey(obs.APIKind, parentValue)]; ok {
		return recordID
	}
	return ""
}

func (r *Registry) resolveTranscriptLocked(obs Observation) (*Record, string, string) {
	incomingDepth := obs.Transcript.Depth()
	clientFamily := normalizeClientFamily(obs.Identity.ClientFamily)
	scopeHash := hashIdentityScope(obs.Identity.ScopeID)
	laneHash := strings.TrimSpace(obs.Identity.LaneHash)
	parentID := ""

	// 只接受严格扩展。相同请求可能来自另一个标签或重试，不能据此合并。
	// depth=1 无前序历史可扩展，跳过循环直接进入重试匹配。
	for depth := incomingDepth - 1; depth >= 1; depth-- {
		prefixHash := obs.Transcript.PrefixHashes[depth-1]
		bucket := r.frontierIndex[frontierIndexKey(obs.APIKind, clientFamily, depth, prefixHash)]
		if len(bucket) == 0 {
			continue
		}

		eligible := make([]*Record, 0, len(bucket))
		parentCandidates := make([]*Record, 0, len(bucket))
		for recordID := range bucket {
			rec := r.records[recordID]
			if rec == nil || rec.lineage.FrontierDepth != depth || rec.lineage.FrontierHash != prefixHash {
				continue
			}
			if !scopeCompatible(rec.lineage.ScopeHash, scopeHash) {
				continue
			}
			if rec.ActiveRequests > 0 || !laneCompatible(rec.lineage.LaneHash, laneHash) {
				parentCandidates = append(parentCandidates, rec)
				continue
			}
			eligible = append(eligible, rec)
		}

		if len(eligible) == 1 {
			return eligible[0], "", "unique_history_extension"
		}
		if len(eligible) > 1 {
			return nil, "", "ambiguous_history"
		}
		if len(parentCandidates) == 1 && parentID == "" {
			parentID = parentCandidates[0].ID
		}
	}
	if parentID != "" {
		return nil, parentID, "history_branch"
	}

	// 同深度重试匹配：严格扩展未命中时，尝试匹配同 frontier 的失败记录。
	// 仅当上次请求失败（LastError != ""）且已结束（ActiveRequests == 0）时才合并。
	// depth=1 要求 scopeHash 非空，否则无法区分不同用户的首条消息。
	if rec := r.matchRetryLocked(obs, incomingDepth, clientFamily, scopeHash, laneHash); rec != nil {
		return rec, "", "retry_match"
	}

	return nil, "", "anonymous"
}

func (r *Registry) matchRetryLocked(obs Observation, incomingDepth int, clientFamily string, scopeHash string, laneHash string) *Record {
	if incomingDepth == 1 && scopeHash == "" {
		return nil
	}
	frontierHash := obs.Transcript.FrontierHash()
	if frontierHash == "" {
		return nil
	}
	bucket := r.frontierIndex[frontierIndexKey(obs.APIKind, clientFamily, incomingDepth, frontierHash)]
	if len(bucket) == 0 {
		return nil
	}
	now := time.Now()
	candidates := make([]*Record, 0, len(bucket))
	for recordID := range bucket {
		rec := r.records[recordID]
		if rec == nil || rec.lineage.FrontierDepth != incomingDepth || rec.lineage.FrontierHash != frontierHash {
			continue
		}
		if rec.LastError == "" {
			continue
		}
		if rec.ActiveRequests > 0 {
			continue
		}
		if !scopeCompatible(rec.lineage.ScopeHash, scopeHash) {
			continue
		}
		if !laneCompatible(rec.lineage.LaneHash, laneHash) {
			continue
		}
		if !rec.LastCompletedAt.IsZero() && now.Sub(rec.LastCompletedAt) > retryMatchWindow {
			continue
		}
		candidates = append(candidates, rec)
	}
	// frontierIndex 是 map，不能依赖遍历顺序决定并发失败记录的归并对象。
	// 优先最近完成的记录；时间相同时用稳定 ID 打破平局，保证重启前后结果可复现。
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].LastCompletedAt.Equal(candidates[j].LastCompletedAt) {
			return candidates[i].LastCompletedAt.After(candidates[j].LastCompletedAt)
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

func (r *Registry) applyObservationLineageLocked(rec *Record, obs Observation, resolution string, parentID string, now time.Time) {
	if rec == nil {
		return
	}
	clientFamily := normalizeClientFamily(obs.Identity.ClientFamily)
	if clientFamily != "unknown" || rec.lineage.ClientFamily == "" {
		rec.lineage.ClientFamily = clientFamily
	}
	if source := firstNonEmpty(resolution, obs.Identity.Source); source != "" {
		rec.lineage.IdentitySource = source
	}
	if scopeHash := hashIdentityScope(obs.Identity.ScopeID); scopeHash != "" {
		rec.lineage.ScopeHash = scopeHash
	}
	if laneHash := strings.TrimSpace(obs.Identity.LaneHash); laneHash != "" {
		rec.lineage.LaneHash = laneHash
	}
	if parentID != "" && parentID != rec.ID && rec.lineage.ParentConversationID == "" {
		rec.lineage.ParentConversationID = parentID
	}
	if obs.Transcript.Depth() > 0 {
		rec.lineage.FrontierDepth = obs.Transcript.Depth()
		rec.lineage.FrontierHash = obs.Transcript.FrontierHash()
	}
	rec.lineage.UpdatedAt = now
	rec.ClientFamily = rec.lineage.ClientFamily
	rec.IdentitySource = rec.lineage.IdentitySource
	rec.ParentConversationID = rec.lineage.ParentConversationID
	r.indexLineageLocked(rec)
}

func (r *Registry) indexLineageLocked(rec *Record) {
	if rec == nil || rec.lineage.FrontierDepth <= 0 || rec.lineage.FrontierHash == "" {
		return
	}
	key := frontierIndexKey(rec.APIKind, rec.lineage.ClientFamily, rec.lineage.FrontierDepth, rec.lineage.FrontierHash)
	bucket := r.frontierIndex[key]
	if bucket == nil {
		bucket = make(map[string]struct{})
		r.frontierIndex[key] = bucket
	}
	bucket[rec.ID] = struct{}{}
}

func (r *Registry) unindexLineageLocked(rec *Record) {
	if rec == nil || rec.lineage.FrontierDepth <= 0 || rec.lineage.FrontierHash == "" {
		return
	}
	key := frontierIndexKey(rec.APIKind, rec.lineage.ClientFamily, rec.lineage.FrontierDepth, rec.lineage.FrontierHash)
	bucket := r.frontierIndex[key]
	delete(bucket, rec.ID)
	if len(bucket) == 0 {
		delete(r.frontierIndex, key)
	}
}
