package logbody

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBufferBoundsCollectionAndTracksOriginalSize(t *testing.T) {
	for _, limit := range []int{0, 128} {
		buffer := Buffer{Limit: limit}
		chunk := strings.Repeat("x", 1024)
		for range 1000 {
			n, err := buffer.WriteString(chunk)
			require.NoError(t, err)
			require.Equal(t, len(chunk), n)
		}
		require.Equal(t, limit, buffer.Len())
		require.Equal(t, 1024000, buffer.OriginalBytes)
		require.True(t, buffer.Truncated)
	}
}
