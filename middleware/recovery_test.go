package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecovery_CatchesPanic 验证 panic 被捕获并返回带 stack trace 的 error
func TestRecovery_CatchesPanic(t *testing.T) {
	ctx := context.Background()

	panicHandler := func(ctx context.Context, input string) (string, error) {
		panic("something went wrong")
	}

	recovery := Recovery[string, string]()
	wrapped := recovery(panicHandler)

	result, err := wrapped(ctx, "input")
	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "karta: recovered panic: something went wrong")
	assert.Contains(t, err.Error(), "goroutine")
	assert.Contains(t, err.Error(), "TestRecovery_CatchesPanic")
}

// TestRecovery_Transparent_NoPanic 验证无 panic 时正常透传
func TestRecovery_Transparent_NoPanic(t *testing.T) {
	ctx := context.Background()

	handler := func(ctx context.Context, input string) (string, error) {
		return input + "-processed", nil
	}

	recovery := Recovery[string, string]()
	wrapped := recovery(handler)

	result, err := wrapped(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, "test-processed", result)
}

// TestRecovery_TransparentWithError 验证非 panic error 正常传递
func TestRecovery_TransparentWithError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("normal error")

	handler := func(ctx context.Context, input string) (string, error) {
		return "", expectedErr
	}

	recovery := Recovery[string, string]()
	wrapped := recovery(handler)

	result, err := wrapped(ctx, "input")
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, result)
}

// TestRecovery_WithChain 测试 Recovery 与 Chain 的完整链路
// 由于 Go test 对同模块根包 import 的限制，这里直接模拟 chain 行为：
// Chain(Recovery(), noopMW)(handler) → 验证 Recovery 包裹后能捕获 panic
func TestRecovery_WithChain(t *testing.T) {
	ctx := context.Background()

	panicHandler := func(ctx context.Context, input string) (string, error) {
		panic("chain panic")
	}

	// noopMW: 透传，模拟链路中额外的 middleware
	noopMW := func(next func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
		return next
	}

	// Chain(Recovery(), noopMW)(handler) = Recovery(noopMW(handler))
	recovery := Recovery[string, string]()
	wrapped := recovery(noopMW(panicHandler))

	result, err := wrapped(ctx, "input")
	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "karta: recovered panic: chain panic")
	assert.Contains(t, err.Error(), "goroutine")
}

// TestRecovery_WithChain_InnerMW 测试 Recovery 包裹另一个会 panic 的中间件
func TestRecovery_WithChain_InnerMW(t *testing.T) {
	ctx := context.Background()

	handler := func(ctx context.Context, input string) (string, error) {
		return "ok", nil
	}

	// 一个在调用 next 前会 panic 的中间件
	panicMW := func(next func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
		return func(ctx context.Context, input string) (string, error) {
			panic("middleware panic")
		}
	}

	// Chain(Recovery(), panicMW)(handler) = Recovery(panicMW(handler))
	// Recovery 在最外层，应该捕获 middleware 的 panic
	recovery := Recovery[string, string]()
	outerWrapped := recovery(panicMW(handler))

	result, err := outerWrapped(ctx, "input")
	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "karta: recovered panic: middleware panic")
	assert.True(t, strings.Contains(err.Error(), "goroutine"))
}
