package middleware

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errRetryTest = errors.New("test retry error")

// TestRetry_SuccessFirstAttempt 验证 handler 成功时只调用 1 次
func TestRetry_SuccessFirstAttempt(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, input string) (string, error) {
		calls.Add(1)
		return "ok:" + input, nil
	}

	mw := Retry[string, string](WithAttempts(3), WithDelay(time.Millisecond))
	h := mw(handler)

	out, err := h(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "ok:hello", out)
	assert.Equal(t, int32(1), calls.Load())
}

// TestRetry_RetriesOnError 验证 handler 前 2 次失败、第 3 次成功，共调用 3 次
func TestRetry_RetriesOnError(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		n := calls.Add(1)
		if n < 3 {
			return 0, errRetryTest
		}
		return input * 2, nil
	}

	mw := Retry[int, int](WithAttempts(3), WithDelay(time.Millisecond))
	h := mw(handler)

	out, err := h(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, 10, out)
	assert.Equal(t, int32(3), calls.Load())
}

// TestRetry_ExhaustsAttempts 验证 attempts 耗尽后返回最后一次 handler 错误
func TestRetry_ExhaustsAttempts(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		calls.Add(1)
		return 0, errRetryTest
	}

	mw := Retry[int, int](WithAttempts(3), WithDelay(time.Millisecond))
	h := mw(handler)

	_, err := h(context.Background(), 1)
	require.Error(t, err)
	assert.Equal(t, errRetryTest, err)
	assert.Equal(t, int32(3), calls.Load())
}

// TestRetry_OnRetryCallback 验证 onRetry 回调在每次失败后被调用
func TestRetry_OnRetryCallback(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		n := calls.Add(1)
		if n < 3 {
			return 0, errRetryTest
		}
		return 42, nil
	}

	var callbackCount atomic.Int32
	var lastAttempt atomic.Int32
	onRetry := func(attempt int, err error) {
		callbackCount.Add(1)
		lastAttempt.Store(int32(attempt))
	}

	mw := Retry[int, int](
		WithAttempts(5),
		WithDelay(time.Millisecond),
		WithOnRetry(onRetry),
	)
	h := mw(handler)

	out, err := h(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, 42, out)
	assert.Equal(t, int32(2), callbackCount.Load()) // 前 2 次失败各触发一次回调
	assert.Equal(t, int32(2), lastAttempt.Load())   // 最后一次回调的 attempt 编号
}

// TestRetry_RetryIfCondition 验证 retryIf 返回 false 时立即停止重试
func TestRetry_RetryIfCondition(t *testing.T) {
	retryableErr := errors.New("retryable")
	nonRetryableErr := errors.New("non-retryable")

	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		n := calls.Add(1)
		if n == 1 {
			return 0, retryableErr // 第一次失败，可重试
		}
		return 0, nonRetryableErr // 第二次失败，不可重试
	}

	mw := Retry[int, int](
		WithAttempts(5),
		WithDelay(time.Millisecond),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, retryableErr)
		}),
	)
	h := mw(handler)

	_, err := h(context.Background(), 0)
	require.Error(t, err)
	assert.Equal(t, nonRetryableErr, err)
	assert.Equal(t, int32(2), calls.Load()) // 第 1 次 retryable，第 2 次 non-retryable 停止
}

// TestRetry_ContextCancel 验证 context 取消后不再重试
func TestRetry_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		calls.Add(1)
		return 0, errRetryTest
	}

	// 使用较大 delay，确保 ctx.Done() 在 timer 之前就绪
	mw := Retry[int, int](WithAttempts(5), WithDelay(200*time.Millisecond))
	h := mw(handler)

	_, err := h(ctx, 0)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, int32(0), calls.Load()) // handler 不应被调用
}

// TestRetry_WithAttempts_NonPositiveIgnored — C3：WithAttempts(n<=0) 必须被
// 忽略（保持现值/默认值）。修复前 uint64(n) 将负数下溢为 2^64-1，
// 造成事实上的无限重试。
func TestRetry_WithAttempts_NonPositiveIgnored(t *testing.T) {
	// 配置层：非正数被忽略，正数正常生效（包内直接断言默认值）
	cfg := &retryConfig{attempts: 3, delay: 100 * time.Millisecond}
	WithAttempts(-1)(cfg)
	assert.Equal(t, uint64(3), cfg.attempts, "WithAttempts(-1) 应被忽略，保持默认值")
	WithAttempts(0)(cfg)
	assert.Equal(t, uint64(3), cfg.attempts, "WithAttempts(0) 应被忽略，保持默认值")
	WithAttempts(5)(cfg)
	assert.Equal(t, uint64(5), cfg.attempts, "正数应正常生效")

	// 行为层：端到端仍按默认 3 次尝试收敛
	//（修复前 -1 下溢为天文数字，本测试将因无限重试而超时）
	var calls atomic.Int32
	handler := func(ctx context.Context, input int) (int, error) {
		calls.Add(1)
		return 0, errRetryTest
	}
	mw := Retry[int, int](WithAttempts(-1), WithDelay(time.Millisecond))
	_, err := mw(handler)(context.Background(), 1)
	require.Error(t, err)
	assert.Equal(t, int32(3), calls.Load(), "应使用默认 attempts=3")
}
