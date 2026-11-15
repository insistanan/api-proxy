package semantic

import (
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func TestBuiltinStarterV2CountsAndSplits(t *testing.T) {
	for _, tc := range []struct {
		id                                      string
		total, index, eval, families, perFamily int
	}{
		{"starter-intents-v2", 192, 144, 48, 96, 2},
		{"starter-tools-v2", 300, 250, 50, 30, 10},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var dataset Dataset
			for _, d := range BuiltinDatasets() {
				if d.ID == tc.id {
					dataset = d
				}
			}
			require.Equal(t, tc.id, dataset.ID)
			require.NoError(t, ValidateDataset(dataset))
			require.Len(t, dataset.Samples, tc.total)
			splits, families, labels, platforms := map[string]int{}, map[string]int{}, map[string]int{}, map[string]bool{}
			negatives := 0
			for _, row := range dataset.Samples {
				splits[row.Split]++
				families[row.Family]++
				labels[row.Label]++
				platforms[row.Platform] = true
				require.Equal(t, "synthetic-starter-v2", row.Source)
				require.False(t, row.Approved)
				if row.Negative {
					negatives++
				}
			}
			require.Equal(t, tc.index, splits["index"])
			require.Equal(t, tc.eval, splits["eval"])
			require.Len(t, families, tc.families)
			for _, count := range families {
				require.Equal(t, tc.perFamily, count)
			}
			if dataset.Kind == "intent" {
				require.Len(t, labels, 12)
				for _, task := range config.IntentTasks() {
					require.Equal(t, 16, labels[task.ID])
				}
			} else {
				require.Len(t, labels, 5)
				require.Equal(t, 150, negatives)
				require.Equal(t, map[string]bool{"windows": true, "posix": true, "any": true}, platforms)
			}
		})
	}
}
