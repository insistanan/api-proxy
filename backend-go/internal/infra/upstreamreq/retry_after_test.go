package upstreamreq

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter_UsesOnlyExplicitValidDeadlines(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		wait  time.Duration
		valid bool
	}{
		{"0", 0, true},
		{" 90 ", 90 * time.Second, true},
		{"691200", 8 * 24 * time.Hour, true},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
		{"", 0, false},
		{"invalid", 0, false},
		{"-1", 0, false},
		{"1.5", 0, false},
		{"9223372037", 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			until, valid := ParseRetryAfter(tc.value, now)
			require.Equal(t, tc.valid, valid)
			if valid {
				require.Equal(t, now.Add(tc.wait), until)
			} else {
				require.True(t, until.IsZero(), "缺失或无效值不应制造等待时间")
			}
		})
	}
}
