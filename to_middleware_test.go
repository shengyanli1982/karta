package karta

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToMiddlewareSlice_TypeMismatch_Panics — P1 #4:
// 类型不匹配的 middleware 视为配置错误，fail-fast panic，message 含期望与实际类型
func TestToMiddlewareSlice_TypeMismatch_Panics(t *testing.T) {
	matched := countMiddleware[int, int](nil)
	mismatched := countMiddleware[int, string](nil) // Middleware[int, string] ← 类型不匹配

	require.Panics(t, func() {
		toMiddlewareSlice[int, int]([]any{matched, mismatched})
	}, "类型不匹配的 middleware 应 panic 而非静默丢弃")

	// panic message 必须包含期望类型与实际类型（%T 输出格式）
	defer func() {
		msg, ok := recover().(string)
		require.True(t, ok)
		assert.Contains(t, msg, "Middleware[int,int]")    // 期望类型
		assert.Contains(t, msg, "Middleware[int,string]") // 实际类型
	}()
	toMiddlewareSlice[int, int]([]any{mismatched})
}

// TestNewGroup_MiddlewareTypeMismatch_Panics — NewGroup 挂类型不匹配的 middleware 应 panic
func TestNewGroup_MiddlewareTypeMismatch_Panics(t *testing.T) {
	handler := func(ctx context.Context, v int) (int, error) { return v, nil }
	mismatched := countMiddleware[string, string](nil)

	require.Panics(t, func() {
		NewGroup[int, int](handler, WithGroupMiddleware(mismatched))
	})
}

// TestNewPipeline_MiddlewareTypeMismatch_Panics — NewPipeline 挂类型不匹配的 middleware
// 应在构造时（executor 启动前）panic，而非在 executor goroutine 内崩溃
func TestNewPipeline_MiddlewareTypeMismatch_Panics(t *testing.T) {
	handler := func(ctx context.Context, v int) (int, error) { return v, nil }
	mismatched := countMiddleware[string, string](nil)

	require.Panics(t, func() {
		NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineMiddleware(mismatched))
	})
}

// TestNewPipeline_MiddlewareMatched_Works — 类型匹配的 middleware 正常使用不受影响
func TestNewPipeline_MiddlewareMatched_Works(t *testing.T) {
	var counter atomic.Int64
	handler := func(ctx context.Context, v int) (int, error) { return v * 2, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(16),
		WithPipelineMiddleware(countMiddleware[int, int](&counter)))
	defer p.Stop()

	f, err := p.Submit(context.Background(), 21)
	require.NoError(t, err)
	res := f.Get(context.Background())
	require.NoError(t, res.Err)
	assert.Equal(t, 42, res.Value)
	assert.Equal(t, int64(1), counter.Load(), "middleware 应恰好执行一次")
}
