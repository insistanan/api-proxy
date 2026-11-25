package images

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestResponsesImageFinalWrite_CancellationFollowsDeliveryResult(t *testing.T) {
	for _, writeErr := range []error{nil, errors.New("末尾写入失败")} {
		synctest.Test(t, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			lifecycle := newResponsesImageLifecycle(parent)
			defer lifecycle.close()
			release := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				result <- lifecycle.finishDelivery(func() error {
					<-release
					return writeErr
				})
			}()
			synctest.Wait()
			cancel()
			synctest.Wait()
			require.NoError(t, lifecycle.ctx.Err(), "最终写入尚未返回，取消不能抢先覆盖交付结果")
			close(release)
			synctest.Wait()
			require.ErrorIs(t, <-result, writeErr)
			if writeErr == nil {
				require.NoError(t, lifecycle.ctx.Err(), "成功交付后继续补收用量")
			} else {
				require.ErrorIs(t, context.Cause(lifecycle.ctx), context.Canceled)
			}
		})
	}
}

func TestResponsesImageFinalWrite_CloseDoesNotWaitForWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newResponsesImageLifecycle(context.Background())
		defer lifecycle.close()
		result := make(chan error, 1)
		go func() {
			result <- lifecycle.finishDelivery(func() error {
				<-lifecycle.ctx.Done()
				return context.Cause(lifecycle.ctx)
			})
		}()
		synctest.Wait()
		lifecycle.close()
		require.ErrorIs(t, <-result, context.Canceled)
	})
}
