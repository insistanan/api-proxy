package main

import (
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestShutdownDeadline_BoundsBlockedCleanupAndPropagatesFailure(t *testing.T) {
	require.ErrorContains(t, awaitShutdown(make(chan error), time.Millisecond), "完整停机超过")
	failure := errors.New("flush failed")
	result := make(chan error, 1)
	result <- failure
	require.ErrorIs(t, awaitShutdown(result, time.Minute), failure)
}
