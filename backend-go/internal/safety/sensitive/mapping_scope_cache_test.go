package sensitive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func seedScopedMapping(t testing.TB, store *MappingStore, scope, original string) string {
	t.Helper()
	vault := store.NewScopedVault(context.Background(), scope, nil)
	require.NoError(t, vault.Err())
	placeholder, err := vault.Mask(original, []RedactionMatch{{Type: "SECRET", Original: original, Start: 0, End: len(original)}})
	require.NoError(t, err)
	require.NoError(t, vault.CommitScope())
	return placeholder
}

func TestScopeCache_ReuseAndRequestIsolation(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	placeholder := seedScopedMapping(t, store, "session", "first-secret")
	first := store.NewScopedVault(context.Background(), "session", nil)
	require.NoError(t, first.Err())
	require.NotNil(t, first.scopeSnapshot)
	require.Equal(t, placeholder, first.MaskKnown("first-secret"))
	second := store.NewScopedVault(context.Background(), "session", nil)
	require.NoError(t, second.Err())
	require.Equal(t, placeholder, second.MaskKnown("first-secret"))
	require.Same(t, first.scopeSnapshot, second.scopeSnapshot)
	require.Same(t, first.maskReplacer, second.maskReplacer)
	first.Clear()
	require.Equal(t, "first-secret", second.Restore(placeholder))
	newToken, err := second.Mask("new-secret", []RedactionMatch{{Type: "SECRET", Original: "new-secret", Start: 0, End: 10}})
	require.NoError(t, err)
	require.Equal(t, newToken, second.MaskKnown("new-secret"))
	third := store.NewScopedVault(context.Background(), "session", nil)
	require.Equal(t, "new-secret", third.MaskKnown("new-secret"))
	require.NoError(t, second.CommitScope())
	fourth := store.NewScopedVault(context.Background(), "session", nil)
	require.NoError(t, fourth.Err())
	require.Equal(t, newToken, fourth.MaskKnown("new-secret"))
	other := store.NewScopedVault(context.Background(), "other", nil)
	require.Equal(t, placeholder, other.Restore(placeholder))
}

func TestScopeCache_DatabaseChangesAndCancellationCannotBeHidden(t *testing.T) {
	for _, mutation := range []string{"delete", "ciphertext", "expire", "cancel", "close"} {
		t.Run(mutation, func(t *testing.T) {
			store, _ := newMappingTestEnv(t)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			seedScopedMapping(t, store, "session", "secret")
			warm := store.NewScopedVault(context.Background(), "session", nil)
			require.NoError(t, warm.Err())
			require.NotNil(t, warm.scopeSnapshot)
			ctx := context.Background()
			switch mutation {
			case "delete":
				_, err := store.db.Exec(`DELETE FROM redaction_mappings`)
				require.NoError(t, err)
			case "ciphertext":
				_, err := store.db.Exec(`UPDATE redaction_mappings SET original_ct = X'00'`)
				require.NoError(t, err)
			case "expire":
				_, err := store.db.Exec(`UPDATE redaction_mappings SET last_access = ?`, time.Now().Add(-mappingTTL-time.Hour).UTC().Format(mappingTimeLayout))
				require.NoError(t, err)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "close":
				require.NoError(t, store.Close())
			}
			require.Error(t, store.NewScopedVault(ctx, "session", nil).Err())
		})
	}
}

func TestScopeCache_CapacityTTLAndClose(t *testing.T) {
	var cache scopeMappingCache
	vault := NewVault()
	require.NoError(t, vault.addMappingLocked("secret", "MASKED_SECRET_AAAAAAAAAAAAA_END"))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= scopeCacheEntries; i++ {
		require.NotNil(t, cache.put(fmt.Sprint(i), 1, now.Add(scopeCacheTTL), vault))
	}
	require.Len(t, cache.entries, scopeCacheEntries)
	require.Nil(t, cache.get("0", 1, now))
	require.NotNil(t, cache.get("1", 1, now))
	require.Nil(t, cache.get("1", 2, now))
	cache.purge(now.Add(scopeCacheTTL))
	require.Empty(t, cache.entries)
	require.Zero(t, cache.bytes)
	large := NewVault()
	require.NoError(t, large.addMappingLocked(strings.Repeat("x", scopeCacheBudget/16), "token"))
	require.Nil(t, cache.put("large", 1, now.Add(scopeCacheTTL), large))
	cache.close()
	require.Nil(t, cache.put("closed", 1, now.Add(scopeCacheTTL), vault))
}

func BenchmarkScopedVaultRepeatedHistory(b *testing.B) {
	dir := b.TempDir()
	store, err := NewMappingStore(filepath.Join(dir, "mapping.db"), filepath.Join(dir, "master-key"))
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, store.Close()) })
	var body strings.Builder
	for i := 0; i < 128; i++ {
		original := fmt.Sprintf("secret-value-%04d", i)
		seedScopedMapping(b, store, "session", original)
		body.WriteString(original + "\n")
	}
	text := body.String()
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cache=%t", cached), func(b *testing.B) {
			warm := store.NewScopedVault(context.Background(), "session", nil)
			require.NoError(b, warm.Err())
			warm.MaskKnown(text)
			warm.Clear()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !cached {
					store.scopeCache.purge(time.Now().Add(scopeCacheTTL))
				}
				vault := store.NewScopedVault(context.Background(), "session", nil)
				if err := vault.Err(); err != nil {
					b.Fatal(err)
				}
				if masked := vault.MaskKnown(text); strings.Contains(masked, "secret-value-") {
					b.Fatal("历史原值未被覆盖")
				}
				vault.Clear()
			}
		})
	}
}
