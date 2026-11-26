package session

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestAffinityCapacity_ReplacementAndEvictionKeepBound(t *testing.T) {
	m := NewBaseURLAffinityManager()
	defer m.Stop()
	for index := 0; index < maxAffinityEntries; index++ {
		m.SetPreferredBaseURL(fmt.Sprint(index), "https://example")
	}
	m.mu.Lock()
	m.affinity["0"].LastUsedAt = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	m.SetPreferredBaseURL("1", "https://updated")
	require.Len(t, m.affinity, maxAffinityEntries)
	m.SetPreferredBaseURL("new", "https://new")
	require.Len(t, m.affinity, maxAffinityEntries)
	_, exists := m.GetPreferredBaseURL("0")
	require.False(t, exists)
	m.SetPreferredBaseURL(strings.Repeat("x", maxAffinityFieldBytes+1), "https://example")
	require.Len(t, m.affinity, maxAffinityEntries)
}
