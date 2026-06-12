package karta

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countMiddleware 返回一个计数器 middleware，每次 handler 调用时 +1。
// 返回值为 Middleware[In, Out] 命名类型，确保可被 toMiddlewareSlice 断言成功。
func countMiddleware[In, Out any](counter *atomic.Int64) Middleware[In, Out] {
	return func(next Handler[In, Out]) Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			counter.Add(1)
			return next(ctx, input)
		}
	}
}

// doubleInputMiddleware 将输入翻倍后再传递给下游 handler（int→int）。
// 声明为 Middleware[int, int] 变量，确保 WithGroupMiddleware 存储的动态类型正确。
var doubleInputMiddleware Middleware[int, int] = func(next Handler[int, int]) Handler[int, int] {
	return func(ctx context.Context, input int) (int, error) {
		return next(ctx, input*2)
	}
}

// appendPrefixMiddleware 在输出字符串前加上前缀（string→string）。
// 用于测试 middleware 执行顺序。
var appendPrefixMiddleware Middleware[string, string] = func(next Handler[string, string]) Handler[string, string] {
	return func(ctx context.Context, input string) (string, error) {
		out, err := next(ctx, input)
		if err != nil {
			return "", err
		}
		return "[mw]" + out, nil
	}
}

func TestGroup_MiddlewareChain_Applied(t *testing.T) {
	var counter atomic.Int64
	handler := func(ctx context.Context, v int) (string, error) {
		return fmt.Sprintf("ok-%d", v), nil
	}
	// countMiddleware[int, string] 返回 Middleware[int, string]，类型断言可匹配
	mw := countMiddleware[int, string](&counter)
	g := NewGroup[int, string](handler, WithGroupMiddleware(mw), WithGroupWorkers(2))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3, 4, 5})
	require.Len(t, results, 5)
	for i, r := range results {
		assert.NoError(t, r.Err)
		assert.Equal(t, fmt.Sprintf("ok-%d", i+1), r.Value)
	}
	assert.Equal(t, int64(5), counter.Load(), "middleware should have been called 5 times")
}

func TestGroup_MiddlewareChain_OrderPreserved(t *testing.T) {
	// handler 直接返回收到的 input；doubleInputMiddleware 将 input*2
	// 所以输入 3 → middleware 传入 6 → handler 返回 6
	handler := func(ctx context.Context, v int) (int, error) {
		return v, nil
	}
	g := NewGroup[int, int](handler, WithGroupMiddleware(doubleInputMiddleware), WithGroupWorkers(2))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	require.Len(t, results, 3)
	for _, r := range results {
		assert.NoError(t, r.Err)
	}
	assert.Equal(t, 2, results[0].Value, "input 1 → mw doubles to 2")
	assert.Equal(t, 4, results[1].Value, "input 2 → mw doubles to 4")
	assert.Equal(t, 6, results[2].Value, "input 3 → mw doubles to 6")
}

func TestGroup_MiddlewareChain_TwoMiddlewares(t *testing.T) {
	// Chain(mw1, mw2)(h) = mw1(mw2(h))：mw1 先执行，mw2 后执行
	// appendPrefixMiddleware 在结果前加 "[mw]"
	// 再套一层 appendPrefixMiddleware → "[mw][mw]result"
	handler := func(ctx context.Context, v string) (string, error) {
		return v, nil
	}
	g := NewGroup[string, string](
		handler,
		WithGroupMiddleware(appendPrefixMiddleware, appendPrefixMiddleware),
		WithGroupWorkers(2),
	)
	defer g.Stop()

	results := g.Map(context.Background(), []string{"hello"})
	require.Len(t, results, 1)
	assert.NoError(t, results[0].Err)
	// Chain(mw1, mw2)(h) = mw1(mw2(h))
	// mw2(h)("hello") → "[mw]hello"
	// mw1(...mw2(h)...)("[mw]hello") → "[mw][mw]hello"
	assert.Equal(t, "[mw][mw]hello", results[0].Value)
}

func TestPipeline_MiddlewareChain_Applied(t *testing.T) {
	var counter atomic.Int64
	handler := func(ctx context.Context, v int) (int, error) {
		return v * 10, nil
	}
	// countMiddleware[int, int] 返回 Middleware[int, int]
	mw := countMiddleware[int, int](&counter)
	sched := NewSimpleScheduler(256)
	p := NewPipeline[int, int](handler, sched, WithPipelineMiddleware(mw))
	require.NotNil(t, p)
	defer p.Stop()

	const N = 5
	var futures []*Future[int]
	for i := 0; i < N; i++ {
		f, err := p.Submit(context.Background(), i+1)
		require.NoError(t, err)
		futures = append(futures, f)
	}

	for i, f := range futures {
		r := f.Get(context.Background())
		assert.True(t, r.Ok())
		assert.Equal(t, (i+1)*10, r.Value)
	}
	assert.Equal(t, int64(N), counter.Load(), "middleware should have been called %d times", N)
}

func TestPipeline_MiddlewareChain_EmptySlice(t *testing.T) {
	// 无 middleware → handler 直接调用，无额外开销
	handler := func(ctx context.Context, v int) (int, error) {
		return v + 1, nil
	}
	sched := NewSimpleScheduler(256)
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	f, err := p.Submit(context.Background(), 41)
	require.NoError(t, err)
	r := f.Get(context.Background())
	assert.True(t, r.Ok())
	assert.Equal(t, 42, r.Value)
}

func TestPipeline_MiddlewareChain_WithSubmitHandler(t *testing.T) {
	// SubmitWithHandler 传入的自定义 handler 也应被 middleware 包裹
	var counter atomic.Int64
	defaultHandler := func(ctx context.Context, v int) (int, error) {
		return v, nil
	}
	customHandler := func(ctx context.Context, v int) (int, error) {
		return v * 100, nil
	}
	mw := countMiddleware[int, int](&counter)
	sched := NewSimpleScheduler(256)
	p := NewPipeline[int, int](defaultHandler, sched, WithPipelineMiddleware(mw))
	require.NotNil(t, p)
	defer p.Stop()

	// 使用自定义 handler 提交
	f, err := p.SubmitWithHandler(context.Background(), customHandler, 5)
	require.NoError(t, err)
	r := f.Get(context.Background())
	assert.True(t, r.Ok())
	assert.Equal(t, 500, r.Value, "customHandler(5) should return 500")
	assert.Equal(t, int64(1), counter.Load(), "middleware should wrap even per-task handler")
}

func TestGroup_Middleware_WithRecovery(t *testing.T) {
	// 手写 recovery middleware：若 handler panic，middleware 捕获后返回自定义错误，
	// 而不是让 panic 传播到 safeCall 的 recover。
	recoveryMW := Middleware[int, string](func(next Handler[int, string]) Handler[int, string] {
		return func(ctx context.Context, input int) (out string, err error) {
			defer func() {
				if rec := recover(); rec != nil {
					out = ""
					err = fmt.Errorf("middleware recovered: %v", rec)
				}
			}()
			return next(ctx, input)
		}
	})

	handler := func(ctx context.Context, v int) (string, error) {
		panic("handler exploded")
	}

	g := NewGroup[int, string](handler, WithGroupMiddleware(recoveryMW), WithGroupWorkers(2))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1})
	require.Len(t, results, 1)
	require.Error(t, results[0].Err)
	// middleware recovery 捕获 panic，safeCall 的 recover 不再触发
	assert.Contains(t, results[0].Err.Error(), "middleware recovered")
	assert.Contains(t, results[0].Err.Error(), "handler exploded")
	assert.NotContains(t, results[0].Err.Error(), "karta: handler panic")
}

func TestGroup_NoMiddleware_NoOverhead(t *testing.T) {
	// 不设置 middleware → handler 直接调用，行为与改造前完全一致
	handler := func(ctx context.Context, v int) (int, error) {
		return v * 2, nil
	}
	g := NewGroup[int, int](handler, WithGroupWorkers(2))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	require.Len(t, results, 3)
	for i, r := range results {
		assert.NoError(t, r.Err)
		assert.Equal(t, (i+1)*2, r.Value)
	}
}

func TestToMiddlewareSlice_TypeMismatchSilent(t *testing.T) {
	// 类型不匹配的 middleware 被静默忽略，只保留类型匹配的
	mw1 := countMiddleware[int, int](&atomic.Int64{})    // Middleware[int, int]
	mw2 := countMiddleware[int, string](&atomic.Int64{}) // Middleware[int, string] ← 类型不匹配

	result := toMiddlewareSlice[int, int]([]any{mw1, mw2})
	// mw2 类型为 Middleware[int, string]，断言 Middleware[int, int] 失败，被忽略
	assert.Len(t, result, 1)
}

func TestToMiddlewareSlice_EmptyRaw(t *testing.T) {
	// 空输入返回空切片
	result := toMiddlewareSlice[int, int](nil)
	assert.NotNil(t, result)
	assert.Len(t, result, 0)

	result2 := toMiddlewareSlice[int, int]([]any{})
	assert.NotNil(t, result2)
	assert.Len(t, result2, 0)
}
