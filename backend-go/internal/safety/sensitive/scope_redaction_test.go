package sensitive

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScopedVaultRejectsCrossSessionRestoreAndRemasksKnownValues(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	first := store.NewScopedVault(context.Background(), "caller-a/session-a", nil)
	const original = "K8p!rQ7vZ2"
	placeholder, err := first.Mask(original, []RedactionMatch{{Type: "SECRET", Rule: "named_secret", Original: original, Start: 0, End: len(original)}})
	require.NoError(t, err)
	require.NoError(t, first.CommitScope())
	for _, scope := range []string{"", "caller-a/session-b", "caller-b/session-a"} {
		other := store.NewScopedVault(context.Background(), scope, nil)
		require.NoError(t, other.Err())
		require.Equal(t, placeholder, other.Restore(placeholder))
		require.Error(t, other.ValidateToolText(placeholder))
	}
	next := store.NewScopedVault(context.Background(), "caller-a/session-a", nil)
	require.NoError(t, next.Err())
	require.Equal(t, original, next.Restore(placeholder))
	require.Equal(t, "再次使用 "+placeholder, next.MaskKnown("再次使用 "+original))
	require.Equal(t, placeholder, next.MaskKnown(placeholder))
}

func TestScopedVaultInvalidationFailsClosed(t *testing.T) {
	store, _ := newMappingTestEnv(t)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	vault := store.NewScopedVault(context.Background(), "session", nil)
	_, err := vault.Mask("sensitive", []RedactionMatch{{Type: "SECRET", Rule: "test", Original: "sensitive", Start: 0, End: 9}})
	require.NoError(t, err)
	require.NoError(t, vault.CommitScope())
	_, err = store.db.Exec(`DELETE FROM redaction_mappings`)
	require.NoError(t, err)
	next := store.NewScopedVault(context.Background(), "session", nil)
	require.ErrorContains(t, next.Err(), "已被清理")
}

func TestRestoreToolJSONPreservesBusinessFieldsAndNestedEncoding(t *testing.T) {
	vault := NewVault()
	const secret = "line\n\"quoted\"\\path"
	placeholder, err := vault.Mask(secret, []RedactionMatch{{Type: "SECRET", Rule: "test", Original: secret, Start: 0, End: len(secret)}})
	require.NoError(t, err)
	body := `{"type":"function_call","name":"tool","arguments":"{\"name\":\"` + placeholder + `\",\"id\":\"` + placeholder + `\",\"type\":\"` + placeholder + `\"}"}`
	restored, err := vault.RestoreJSON([]byte(body))
	require.NoError(t, err)
	require.NotContains(t, string(restored), placeholder)
	require.Contains(t, string(restored), `"name":"tool"`)
	signed := `{"type":"thinking","thinking":"` + placeholder + `","signature":"opaque"}`
	restored, err = vault.RestoreJSON([]byte(signed))
	require.NoError(t, err)
	require.JSONEq(t, signed, string(restored))
}

func TestToolTokenValidationRejectsCorruption(t *testing.T) {
	vault := NewVault()
	placeholder, err := vault.Mask("value", []RedactionMatch{{Type: "SECRET", Rule: "test", Original: "value", Start: 0, End: 5}})
	require.NoError(t, err)
	for _, damaged := range []string{placeholder[:len(placeholder)-2], "prefix" + placeholder, placeholder + "suffix", "MASKED_UNKNOWN_AAAAAAAAAAAAA_END"} {
		require.Error(t, vault.ValidateToolText(damaged))
	}
	require.NoError(t, vault.ValidateToolText("("+placeholder+")"))
	require.False(t, strings.Contains(placeholder, "PRESERVE"))
}
