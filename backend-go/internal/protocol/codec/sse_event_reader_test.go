package codec

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestReadSSEEvent_PreservesWireAndCombinesMultilineData(t *testing.T) {
	wire := "id: one\r\ndata: {\r\nevent: image_generation.completed\r\ndata: \"value\":1}\r\n\r\n"
	reader := bufio.NewReader(strings.NewReader(wire + "data: tail"))
	event, err := ReadSSEEvent(context.Background(), reader)
	require.NoError(t, err)
	require.Equal(t, wire, event.Raw)
	require.Equal(t, "image_generation.completed", event.Name)
	require.JSONEq(t, `{"value":1}`, event.Data)
	event, err = ReadSSEEvent(context.Background(), reader)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, "tail", event.Data)
	require.Equal(t, "data: tail", event.Raw)
}

func TestSSEEventReadingIsIndependentOfTransportChunks(t *testing.T) {
	wire := ": heartbeat\r\nid: 7\r\ndata: {\r\ndata: \"text\":\"中文\"}\r\n\r\ndata: [DONE]"
	for _, reader := range []io.Reader{strings.NewReader(wire), iotest.OneByteReader(strings.NewReader(wire)), iotest.HalfReader(strings.NewReader(wire))} {
		scanner := NewSSEEventReader(context.Background(), reader)
		require.True(t, scanner.Scan())
		line, err := scanner.DataLine()
		require.NoError(t, err)
		require.Equal(t, `data: {"text":"中文"}`, line)
		normalized, err := scanner.Event().Normalized()
		require.NoError(t, err)
		require.Equal(t, ": heartbeat\nid: 7\n"+line+"\n\n", normalized)
		require.True(t, scanner.Scan())
		line, err = scanner.DataLine()
		require.NoError(t, err)
		require.Equal(t, "data: [DONE]", line)
		require.False(t, scanner.Scan())
		require.NoError(t, scanner.Err())
	}
}

func TestNormalizedEventsRemainSeparate(t *testing.T) {
	reader := NewSSEEventReader(context.Background(), strings.NewReader("event: first\ndata: {\ndata: \"x\":1}\n\nevent: second\ndata: {}\n\n"))
	var wire strings.Builder
	for reader.Scan() {
		event, err := reader.Event().Normalized()
		require.NoError(t, err)
		wire.WriteString(event)
	}
	require.NoError(t, reader.Err())
	parsed := NewSSEEventReader(context.Background(), strings.NewReader(wire.String()))
	require.True(t, parsed.Scan())
	require.Equal(t, "first", parsed.Event().Name)
	require.JSONEq(t, `{"x":1}`, parsed.Event().Data)
	require.True(t, parsed.Scan())
	require.Equal(t, "second", parsed.Event().Name)
	require.False(t, parsed.Scan())
}
