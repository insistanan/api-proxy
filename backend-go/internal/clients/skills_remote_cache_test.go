package clients

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillCacheReturnsAndExpiresPackage(t *testing.T) {
	var cache remoteSkillCache
	pkg := &remoteSkillPackage{Files: map[string][]byte{"SKILL.md": []byte("demo")}}
	cache.put("owner/repo/skill", pkg)

	got, ok := cache.get("owner/repo/skill")
	require.True(t, ok)
	require.Same(t, pkg, got)

	cache.mu.Lock()
	entry := cache.entries["owner/repo/skill"]
	entry.expiresAt = time.Now().Add(-time.Second)
	cache.entries["owner/repo/skill"] = entry
	cache.mu.Unlock()

	_, ok = cache.get("owner/repo/skill")
	require.False(t, ok)
	require.Zero(t, cache.totalBytes)
}

func TestRemoteSkillCacheSkipsOversizedPackage(t *testing.T) {
	var cache remoteSkillCache
	cache.put("owner/repo/skill", &remoteSkillPackage{
		Files: map[string][]byte{"blob": make([]byte, remoteSkillCacheMaxBytes+1)},
	})
	_, ok := cache.get("owner/repo/skill")
	require.False(t, ok)
	require.Zero(t, cache.totalBytes)
}
