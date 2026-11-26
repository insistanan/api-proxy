package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"
)

const maxProfiles = 4096
const profileIdleTTL = 24 * time.Hour

// ProfileIdentity 的数组下标仅供展示，身份由协议和稳定渠道 ID 决定。
type ProfileIdentity struct {
	Kind  string
	ID    string
	Index int
}

func ProfileCredentials(keys []string) string {
	ordered := slices.Clone(keys)
	slices.Sort(ordered)
	data, _ := json.Marshal(ordered)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (pm *ProfileManager) SetIdentityValidator(valid func(ProfileIdentity, string, string) bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.validIdentity = valid
}

// 在途请求持有画像租约；只回收已空闲的画像，旧凭据仅保留摘要。
func (pm *ProfileManager) pruneLocked(now time.Time, makeRoom bool) {
	var oldestKey string
	var oldest time.Time
	for key, profile := range pm.profiles {
		profile.mu.RLock()
		active, last := profile.ActiveRequests, profile.lastUsed
		identity, baseURL, credentials := profile.Identity, profile.BaseURL, profile.credentialDigest
		profile.mu.RUnlock()
		if active != 0 {
			continue
		}
		if now.Sub(last) >= profileIdleTTL || pm.validIdentity != nil && !pm.validIdentity(identity, baseURL, credentials) {
			delete(pm.profiles, key)
			continue
		}
		if oldest.IsZero() || last.Before(oldest) {
			oldestKey, oldest = key, last
		}
	}
	if makeRoom && len(pm.profiles) >= maxProfiles && oldestKey != "" {
		delete(pm.profiles, oldestKey)
	}
}
