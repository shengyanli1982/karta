package karta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChain_Empty_NoMiddleware 验证空切片返回 identity handler
func TestChain_Empty_NoMiddleware(t *testing.T) {
	ctx := context.Background()
	handler := func(ctx context.Context, input string) (string, error) {
		return input + "-result", nil
	}

	chained := Chain[string, string]()(handler)
	result, err := chained(ctx, "input")
	require.NoError(t, err)
	assert.Equal(t, "input-result", result)
}

// TestChain_Single_MiddlewareTransparent 验证单个 middleware 透传
func TestChain_Single_MiddlewareTransparent(t *testing.T) {
	ctx := context.Background()
	handler := func(ctx context.Context, input string) (string, error) {
		return input, nil
	}

	// noop middleware
	noop := func(next Handler[string, string]) Handler[string, string] {
		return func(ctx context.Context, input string) (string, error) {
			return next(ctx, input)
		}
	}

	chained := Chain[string, string](noop)(handler)
	result, err := chained(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, "test", result)
}

// TestChain_Multiple_OrderPreserved 验证多 middleware 按包裹顺序执行
func TestChain_Multiple_OrderPreserved(t *testing.T) {
	ctx := context.Background()
	handler := func(ctx context.Context, input string) (string, error) {
		return input, nil
	}

	// mw1: 在结果前后添加 "mw1<" ">"
	mw1 := func(next Handler[string, string]) Handler[string, string] {
		return func(ctx context.Context, input string) (string, error) {
			result, err := next(ctx, input)
			return "mw1<" + result + ">", err
		}
	}

	// mw2: 在结果前后添加 "mw2<" ">"
	mw2 := func(next Handler[string, string]) Handler[string, string] {
		return func(ctx context.Context, input string) (string, error) {
			result, err := next(ctx, input)
			return "mw2<" + result + ">", err
		}
	}

	// Chain(mw1, mw2)(handler) = mw1(mw2(handler))
	// 执行顺序：handler → mw2 → mw1
	// input → input → mw2<input> → mw1<mw2<input>>
	chained := Chain[string, string](mw1, mw2)(handler)
	result, err := chained(ctx, "input")
	require.NoError(t, err)
	assert.Equal(t, "mw1<mw2<input>>", result)
}

// TestChain_Shortcut 验证中间件可短路（不调用 next）
func TestChain_Shortcut(t *testing.T) {
	ctx := context.Background()
	handlerCalled := false

	handler := func(ctx context.Context, input string) (string, error) {
		handlerCalled = true
		return "should-not-reach", nil
	}

	// shortcut middleware 不调用 next
	shortcut := func(next Handler[string, string]) Handler[string, string] {
		return func(ctx context.Context, input string) (string, error) {
			return "short-circuited", nil
		}
	}

	afterShortcut := func(next Handler[string, string]) Handler[string, string] {
		return func(ctx context.Context, input string) (string, error) {
			t.Fatal("middleware after shortcut should not be called")
			return "", nil
		}
	}

	// Chain(shortcut, afterShortcut, handler)
	// shortcut 在最外层，afterShortcut 和 handler 不应该被调用
	chained := Chain[string, string](shortcut, afterShortcut)(handler)
	result, err := chained(ctx, "input")
	require.NoError(t, err)
	assert.Equal(t, "short-circuited", result)
	assert.False(t, handlerCalled, "handler should not be called")
}
