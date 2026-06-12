package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimeout_CompletesBeforeDeadline 验证 handler 50ms、timeout 200ms 时成功完成
func TestTimeout_CompletesBeforeDeadline(t *testing.T) {
	handler := func(ctx context.Context, input string) (string, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return "done-" + input, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	timeout := Timeout[string, string](200 * time.Millisecond)
	wrapped := timeout(handler)

	result, err := wrapped(context.Background(), "test")
	require.NoError(t, err)
	assert.Equal(t, "done-test", result)
}

// TestTimeout_ExceedsDeadline 验证 handler 2s、timeout 50ms 时返回 DeadlineExceeded
func TestTimeout_ExceedsDeadline(t *testing.T) {
	handler := func(ctx context.Context, input string) (string, error) {
		select {
		case <-time.After(2 * time.Second):
			return "should-not-reach", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	timeout := Timeout[string, string](50 * time.Millisecond)
	wrapped := timeout(handler)

	start := time.Now()
	result, err := wrapped(context.Background(), "test")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, result)
	// 应在 ~50ms 左右结束，不应等到 2s
	assert.Less(t, elapsed, 500*time.Millisecond, "handler should be cancelled promptly")
}

// TestTimeout_ContextCancelPropagation 验证外层 ctx cancel → handler 感知
func TestTimeout_ContextCancelPropagation(t *testing.T) {
	handler := func(ctx context.Context, input string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	timeout := Timeout[string, string](5 * time.Second) // 大超时，不触发
	wrapped := timeout(handler)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result, err := wrapped(ctx, "test")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, result)
	assert.Less(t, elapsed, 500*time.Millisecond, "handler should respond to cancel promptly")
}
