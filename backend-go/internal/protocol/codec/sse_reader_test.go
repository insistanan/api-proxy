package codec

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestSSELineReader_LargeLinesAndTrailingData(t *testing.T) {
	large := "data: " + strings.Repeat("x", 2*1024*1024)
	reader := NewSSELineReader(context.Background(), strings.NewReader(large+"\r\n\r\ndata: tail"))
	for _, expected := range []string{large, "", "data: tail"} {
		require.True(t, reader.Scan())
		require.Equal(t, expected, reader.Text())
	}
	require.False(t, reader.Scan())
	require.NoError(t, reader.Err())
}

type failingSSEReader struct{ err error }

func (r failingSSEReader) Read([]byte) (int, error) { return 0, r.err }

func TestSSELineReader_PreservesReadFailureAndCancellation(t *testing.T) {
	readErr := errors.New("读取中断")
	for _, partial := range []string{"data: partial", "data: " + strings.Repeat("x", 2*1024*1024)} {
		reader := NewSSELineReader(context.Background(), io.MultiReader(strings.NewReader(partial), failingSSEReader{readErr}))
		require.True(t, reader.Scan())
		require.Equal(t, partial, reader.Text())
		require.False(t, reader.Scan())
		require.ErrorIs(t, reader.Err(), readErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := NewSSELineReader(ctx, strings.NewReader("data: buffered\n"))
	cancel()
	require.False(t, reader.Scan())
	require.ErrorIs(t, reader.Err(), context.Canceled)
}

func TestSSELineReader_CancelDuringLargeLineStopsRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body, writer := io.Pipe()
		defer writer.Close()
		defer BindStreamBody(ctx, body)()
		reader := NewSSELineReader(ctx, body)
		done := make(chan bool, 1)
		go func() { done <- reader.Scan() }()
		_, err := io.WriteString(writer, "data: "+strings.Repeat("x", 2*1024*1024))
		require.NoError(t, err)
		cancel()
		synctest.Wait()
		require.False(t, <-done)
		require.ErrorIs(t, reader.Err(), context.Canceled)
	})
}
