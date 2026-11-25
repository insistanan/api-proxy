package pipeline

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

// 缓存只保存摘要、规则名和区间，不持有历史正文或原值。
// 生命周期绑定不可变配置快照；两代容量同时受条目数与命中数约束。
type safetyDetectionMemo struct {
	mu       sync.Mutex
	current  map[[32]byte][]sensitive.RedactionMatch
	previous map[[32]byte][]sensitive.RedactionMatch
	hits     int
}

func (m *safetyDetectionMemo) find(kind byte, field, text string, detect func() []sensitive.RedactionMatch) []sensitive.RedactionMatch {
	return m.findVersion(kind, field, text, 0, detect)
}

func (m *safetyDetectionMemo) findVersion(kind byte, field, text string, version int64, detect func() []sensitive.RedactionMatch) []sensitive.RedactionMatch {
	if len(text) < 256 {
		return detect()
	}
	h := sha256.New()
	_, _ = h.Write([]byte{kind})
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(version))
	_, _ = h.Write(length[:])
	binary.LittleEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(field))
	_, _ = h.Write([]byte(text))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	m.mu.Lock()
	stored, ok := m.current[key]
	if !ok {
		stored, ok = m.previous[key]
		if ok {
			m.storeLocked(key, stored)
		}
	}
	m.mu.Unlock()
	if ok {
		matches := append([]sensitive.RedactionMatch(nil), stored...)
		for i := range matches {
			matches[i].Original = text[matches[i].Start:matches[i].End]
		}
		return matches
	}
	matches := detect()
	if len(matches) > 512 {
		return matches
	}
	stored = append([]sensitive.RedactionMatch(nil), matches...)
	for i := range stored {
		stored[i].Original = ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storeLocked(key, stored)
	return matches
}

func (m *safetyDetectionMemo) storeLocked(key [32]byte, stored []sensitive.RedactionMatch) {
	if _, exists := m.current[key]; exists {
		return
	}
	if m.current == nil || len(m.current) >= 2048 || m.hits+len(stored) > 8192 {
		m.previous = m.current
		m.current = make(map[[32]byte][]sensitive.RedactionMatch)
		m.hits = 0
	}
	if _, exists := m.current[key]; !exists {
		m.current[key] = stored
		m.hits += len(stored)
	}
}

func (s *contentSafetySnapshot) credentialMatches(field, text string) []sensitive.CredentialMatch {
	if !s.settings.Credential.Enabled {
		return nil
	}
	matches := s.memo.find('c', field, text, func() []sensitive.RedactionMatch {
		return credentialRedactionMatches(s.credential.FindField(field, text))
	})
	if len(matches) == 0 {
		return nil
	}
	result := make([]sensitive.CredentialMatch, 0, len(matches))
	for _, m := range matches {
		result = append(result, sensitive.CredentialMatch{Rule: m.Rule, Original: m.Original, Start: m.Start, End: m.End})
	}
	return result
}

func (s *contentSafetySnapshot) infoMatches(field, text string) []sensitive.InfoMatch {
	if !s.settings.SensitiveInfo.Enabled {
		return nil
	}
	// 身份证规则拒绝未来出生日期，跨 UTC 日期后必须重新检测。
	matches := s.memo.findVersion('i', field, text, time.Now().UTC().Unix()/86400, func() []sensitive.RedactionMatch {
		return infoRedactionMatches(s.info.FindField(field, text))
	})
	if len(matches) == 0 {
		return nil
	}
	result := make([]sensitive.InfoMatch, 0, len(matches))
	for _, m := range matches {
		result = append(result, sensitive.InfoMatch{Rule: m.Rule, Original: m.Original, Start: m.Start, End: m.End})
	}
	return result
}

func (s *contentSafetySnapshot) customMatches(text string) []sensitive.CustomMatch {
	matches := s.memo.find('r', "", text, func() []sensitive.RedactionMatch {
		return customRedactionMatches(s.custom.FindAll(text))
	})
	if len(matches) == 0 {
		return nil
	}
	result := make([]sensitive.CustomMatch, 0, len(matches))
	for _, m := range matches {
		result = append(result, sensitive.CustomMatch{Rule: m.Rule, Type: m.Type, Original: m.Original, Start: m.Start, End: m.End})
	}
	return result
}
