package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// TestRateLimit_PassesWithToken 验证足够令牌时 handler 正常执行
func TestRateLimit_PassesWithToken(t *testing.T) {
	// 每秒 100 个令牌，突发上限 10
	limiter := rate.NewLimiter(100, 10)

	handler := func(ctx context.Context, input string) (string, error) {
		return "processed-" + input, nil
	}

	rl := RateLimit[string, string](limiter)
	wrapped := rl(handler)

	result, err := wrapped(context.Background(), "data")
	require.NoError(t, err)
	assert.Equal(t, "processed-data", result)
}

// TestRateLimit_BlockedWhenNoToken 验证限流器无法获取令牌时，handler 不会被调用
func TestRateLimit_BlockedWhenNoToken(t *testing.T) {
	// 每秒补充 1 个令牌、突发上限 1。
	// 先消耗唯一可用令牌，再次 Wait 时因速率太低无法在 ctx 超时前获取令牌 → 返回 err
	limiter := rate.NewLimiter(1, 1)
	require.NoError(t, limiter.Wait(context.Background()))

	handler := func(ctx context.Context, input string) (string, error) {
		return "should-not-reach", nil
	}

	rl := RateLimit[string, string](limiter)
	wrapped := rl(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := wrapped(ctx, "data")
	require.Error(t, err, "expected an error when rate limiter cannot acquire token in time")
	assert.Empty(t, result)
	// 确认 handler 未被执行：若被执行则会返回 "should-not-reach"
	assert.NotEqual(t, "should-not-reach", result)
}

// TestRateLimit_Transparent 验证输入输出正确传递
func TestRateLimit_Transparent(t *testing.T) {
	// 充足令牌，确保限流不会阻塞
	limiter := rate.NewLimiter(rate.Inf, 1)

	handler := func(ctx context.Context, input int) (int, error) {
		return input * 3, nil
	}

	rl := RateLimit[int, int](limiter)
	wrapped := rl(handler)

	result, err := wrapped(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, 21, result)
}
