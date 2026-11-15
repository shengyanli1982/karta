package karta

import (
	"context"
	"errors"
	"runtime"
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

// TestFuture_GetBackground_NoChanAllocated — T3 白盒：Get(context.Background())
// （不可取消 ctx）走内嵌 Cond 慢路径，全程不得分配 done channel（f.done 保持
// nil）；Resolve 唤醒后结果正确、等待者计数归零。
// 改前（lazy done-chan 全量慢路径）该测试必然失败：done 会被分配。
func TestFuture_GetBackground_NoChanAllocated(t *testing.T) {
	f := NewPendingFuture[int]()
	got := make(chan Result[int], 1)
	go func() { got <- f.Get(context.Background()) }()

	// 等待 waiter 登记进 Cond 等待计数（上限 2s，防实现走样时死等）
	deadline := time.Now().Add(2 * time.Second)
	for f.condWaiters.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	require.Equal(t, int32(1), f.condWaiters.Load(), "waiter 应已登记 Cond 等待计数")

	f.Resolve(Result[int]{Value: 7})
	r := <-got
	require.True(t, r.Ok())
	assert.Equal(t, 7, r.Value)

	// 白盒断言：Cond 路径零 channel 分配；计数无泄漏。
	// f.done 此处裸读安全：全部写方已静默（Get goroutine 已经 got 汇合、
	// Resolve 在本 goroutine 完成）；并发场景须持 f.mu 读，见 MixedWaiters。
	assert.Nil(t, f.done, "Get(Background) 不得分配 done channel（T3 Cond 路径）")
	assert.Zero(t, f.condWaiters.Load(), "等待者计数必须归零")
}

// TestFuture_MixedWaiters_ResolveWakesAll — T3 混合等待者：同一 future 上
// 并发挂载 Get(Background)（Cond 路径）、Get(可取消 ctx)（lazy done-chan
// 路径）与 Then 回调，Resolve 必须唤醒/送达全部三类等待者，结果一致。
func TestFuture_MixedWaiters_ResolveWakesAll(t *testing.T) {
	f := NewPendingFuture[int]()

	var wg sync.WaitGroup
	results := make([]Result[int], 3)

	// 2× Get(Background)：Cond 路径
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = f.Get(context.Background())
		}(i)
	}
	// 1× Get(可取消 ctx)：done-chan 路径（长超时，等待 Resolve 唤醒）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[2] = f.Get(ctx)
	}()
	// 1× Then：回调在独立 goroutine 执行，用带缓冲 channel 汇合
	thenCh := make(chan Result[int], 1)
	f.Then(func(r Result[int]) { thenCh <- r })

	// 等待 Cond waiter 登记（condWaiters.Add(1) 是 Cond 路径进入慢路径的
	// 第一步，原子计数即同步点）
	deadline := time.Now().Add(2 * time.Second)
	for f.condWaiters.Load() < 2 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	require.Equal(t, int32(2), f.condWaiters.Load(), "两个 Cond waiter 应已登记")
	// D7-FLAKE-1 修复：chan-waiter 侧 f.done 的 lazy 分配点（Get 慢路径锁内）
	// 与 condWaiters 登记之间无 happens-before——高负载下 chan waiter 可能
	// 尚未被调度到分配点，裸断言会在 0 时刻读到 nil 而误报（原 20ms 兜底
	// sleep 位于断言之后，不起作用）。改为持 f.mu 的有界轮询（白盒读 mu
	// 保护字段必须持锁，Round2 教训 #6），超时才 Fail 并附诊断。
	chanDeadline := time.Now().Add(5 * time.Second)
	var chanAllocated bool
	for {
		f.mu.Lock()
		chanAllocated = f.done != nil
		f.mu.Unlock()
		if chanAllocated || time.Now().After(chanDeadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.True(t, chanAllocated,
		"可取消 ctx 路径应已 lazy 分配 done channel（5s 轮询超时未观察到，condWaiters=%d）",
		f.condWaiters.Load())
	// 分配点后 chan waiter 还需解锁并进入 select 注册；短睡使其大概率处于
	// 阻塞态再 Resolve，钉住"唤醒阻塞等待者"语义（即使未赶上，close 的
	// 广播语义也保证唤醒，不引入 flake）
	time.Sleep(20 * time.Millisecond)

	f.Resolve(Result[int]{Value: 42})
	wg.Wait()

	for i, r := range results {
		require.True(t, r.Ok(), "waiter %d 应收到成功结果", i)
		assert.Equal(t, 42, r.Value, "waiter %d", i)
	}
	select {
	case r := <-thenCh:
		require.True(t, r.Ok())
		assert.Equal(t, 42, r.Value, "Then 回调应收到同一结果")
	case <-time.After(2 * time.Second):
		t.Fatal("Then 回调未被派发")
	}
	assert.Zero(t, f.condWaiters.Load(), "等待者计数必须归零")
}

// TestFuture_GetCancelable_CancelThenResolve — 可取消路径回归：Get(带超时
// ctx) 阻塞中超时返回 ctx 错误后，future 仍可正常 Resolve（close 无接收者
// 的 done chan 合法，不 panic），后续 Get 走快速路径拿到结果。
func TestFuture_GetCancelable_CancelThenResolve(t *testing.T) {
	f := NewPendingFuture[int]()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	r := f.Get(ctx)
	assert.ErrorIs(t, r.Err, context.DeadlineExceeded)

	// 可取消 ctx 慢路径保留了 lazy done-chan：此时已分配
	assert.NotNil(t, f.done, "可取消路径应分配 done channel（原语义保留）")

	// 后续 Resolve 不得 panic（close 无接收者 channel 合法）
	assert.NotPanics(t, func() { f.Resolve(Result[int]{Value: 1}) })

	// resolve 后 Get(Background) 命中快速路径
	r2 := f.Get(context.Background())
	require.True(t, r2.Ok())
	assert.Equal(t, 1, r2.Value)
	assert.Zero(t, f.condWaiters.Load())
}

// TestFuture_ConcurrentGetResolveStress — 并发压力（-race 下运行）：多轮
// Get(Background)/Get(可取消 ctx)/Then 与 Resolve 竞态，验证无死锁、无丢失
// 唤醒、结果一致、condWaiters 计数无泄漏。
func TestFuture_ConcurrentGetResolveStress(t *testing.T) {
	const rounds = 200
	const waiters = 9
	for round := 0; round < rounds; round++ {
		f := NewPendingFuture[int]()
		var wg sync.WaitGroup
		results := make([]Result[int], waiters)
		for i := 0; i < waiters; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				switch idx % 3 {
				case 0: // Cond 路径
					results[idx] = f.Get(context.Background())
				case 1: // lazy done-chan 路径
					cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer ccancel()
					results[idx] = f.Get(cctx)
				default: // Then 回调路径
					cbCh := make(chan Result[int], 1)
					f.Then(func(r Result[int]) { cbCh <- r })
					select {
					case results[idx] = <-cbCh:
					case <-time.After(5 * time.Second):
						results[idx] = Result[int]{Err: errors.New("then 回调超时")}
					}
				}
			}(i)
		}
		// 与 waiter 挂载竞态的 Resolve（部分轮次 waiter 先到位，部分轮次
		// Resolve 先完成——两种交错都必须无丢失唤醒）
		if round%2 == 0 {
			runtime.Gosched()
		}
		f.Resolve(Result[int]{Value: round})
		wg.Wait()

		for i, r := range results {
			require.True(t, r.Ok(), "round %d waiter %d: %v", round, i, r.Err)
			assert.Equal(t, round, r.Value, "round %d waiter %d", round, i)
		}
		assert.Zero(t, f.condWaiters.Load(), "round %d 计数泄漏", round)
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
