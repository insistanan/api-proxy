package channels

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseKiloFreeModelsRequiresExplicitFreeFlag(t *testing.T) {
	models, err := parseKiloFreeModels([]byte(`{"data":[{"id":"paid/free","isFree":false},{"id":"no-price/free"},{"id":"b","isFree":true},{"id":"a","isFree":true},{"id":"b","isFree":true}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, models)
	for _, body := range []string{`{`, `{"data":[]}`, `{"data":[{"id":"free"}]}`, `{"data":[{"id":" ","isFree":true}]}`, `{"data":[{"id":"x\ny","isFree":true}]}`} {
		_, err := parseKiloFreeModels([]byte(body))
		require.Error(t, err, body)
	}
}
