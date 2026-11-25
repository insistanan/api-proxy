package codec

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

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
