package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPendingFuture_GetAfterResolve(t *testing.T) {
	f := NewPendingFuture[int]()
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.Resolve(Result[int]{Value: 42})
	}()

	r := f.Get(context.Background())
	require.True(t, r.Ok())
	assert.Equal(t, 42, r.Value)
}

func TestNewResolvedFuture_GetImmediate(t *testing.T) {
	f := NewResolvedFuture[string](Result[string]{Value: "done"})
	r := f.Get(context.Background())
	require.True(t, r.Ok())
	assert.Equal(t, "done", r.Value)
}

func TestFuture_Get_ContextCancelled(t *testing.T) {
	f := NewPendingFuture[int]()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	r := f.Get(ctx)
	assert.False(t, r.Ok())
	assert.ErrorIs(t, r.Err, context.DeadlineExceeded)
}

func TestFuture_Then_CalledAfterResolve(t *testing.T) {
	f := NewPendingFuture[int]()
	var called atomic.Bool

	f.Then(func(r Result[int]) {
		called.Store(true)
		assert.Equal(t, 99, r.Value)
	})

	f.Resolve(Result[int]{Value: 99})
	time.Sleep(100 * time.Millisecond)
	assert.True(t, called.Load(), "Then callback was not called")
}

func TestFuture_Then_AlreadyResolved(t *testing.T) {
	f := NewResolvedFuture[int](Result[int]{Value: 1})
	var called atomic.Bool

	f.Then(func(r Result[int]) {
		called.Store(true)
	})

	time.Sleep(100 * time.Millisecond)
	assert.True(t, called.Load(), "Then callback should be called immediately when future already resolved")
}

func TestFuture_Then_ChainReturnsFuture(t *testing.T) {
	f := NewPendingFuture[int]()
	f2 := f.Then(func(r Result[int]) {})
	require.NotNil(t, f2, "Then should return non-nil *Future for chaining")
	f.Resolve(Result[int]{Value: 0})
}

func TestFuture_Resolve_OnlyOnce(t *testing.T) {
	f := NewPendingFuture[int]()
	f.Resolve(Result[int]{Value: 1})
	f.Resolve(Result[int]{Value: 2})

	r := f.Get(context.Background())
	assert.Equal(t, 1, r.Value)
}

func TestFuture_ConcurrentGet(t *testing.T) {
	f := NewPendingFuture[int]()
	var wg sync.WaitGroup
	results := make([]Result[int], 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = f.Get(context.Background())
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	f.Resolve(Result[int]{Value: 42})
	wg.Wait()

	for i, r := range results {
		require.True(t, r.Ok(), "goroutine %d", i)
		assert.Equal(t, 42, r.Value, "goroutine %d", i)
	}
}

// TestNewResolvedFuture_ResolveIgnored — P2 #14: NewResolvedFuture 构造时
// claimed=true，后续 Resolve 不得覆写已读结果
func TestNewResolvedFuture_ResolveIgnored(t *testing.T) {
	f := NewResolvedFuture[int](Result[int]{Value: 1})
	f.Resolve(Result[int]{Value: 2})
	f.Resolve(Result[int]{Err: errors.New("overwrite")})

	r := f.Get(context.Background())
	assert.Equal(t, 1, r.Value, "结果应保持构造值")
	assert.NoError(t, r.Err)
}

// TestNewResolvedFuture_ConcurrentGetResolve — -race 下并发 Get + Resolve 干净，
// 且结果始终为构造值
func TestNewResolvedFuture_ConcurrentGetResolve(t *testing.T) {
	for round := range 100 {
		f := NewResolvedFuture[int](Result[int]{Value: 42})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			f.Resolve(Result[int]{Value: 99}) // 应被 claimed 守卫忽略
		}()
		var r Result[int]
		go func() {
			defer wg.Done()
			r = f.Get(context.Background())
		}()
		wg.Wait()
		assert.Equal(t, 42, r.Value, "round %d", round)
	}
}

// TestFuture_Then_CallbackPanic_DoesNotCrash — C2：Then 回调 panic 必须被
// recover 捕获（修复前 Then/Resolve 以裸 goroutine 直接调用用户回调，
// panic 会击穿整个进程）。覆盖两条派发路径：
//  1. Future 已 resolved：Then 内立即 go 派发
//  2. Pending Future：Resolve 批量派发已注册回调
//
// 子 goroutine 中的 panic 无法被测试框架捕获——若 recover 缺失，测试进程
// 会直接崩溃；本测试能运行到断言即证明进程存活。
func TestFuture_Then_CallbackPanic_DoesNotCrash(t *testing.T) {
	// 路径 1：已 resolved Future 上的 Then（立即派发）
	f1 := NewResolvedFuture[int](Result[int]{Value: 1})
	f1.Then(func(Result[int]) { panic("then callback boom") })

	// 路径 2：先注册回调再 Resolve（Resolve 批量派发）；
	// 一个回调 panic 不得影响同批其他回调的执行
	f2 := NewPendingFuture[int]()
	var secondCalled atomic.Bool
	f2.Then(func(Result[int]) { panic("first callback boom") })
	f2.Then(func(Result[int]) { secondCalled.Store(true) })
	f2.Resolve(Result[int]{Value: 2})

	// 等待回调 goroutine 执行完毕
	time.Sleep(200 * time.Millisecond)
	assert.True(t, secondCalled.Load(),
		"panic 回调不得影响同批其他回调的执行")
}
