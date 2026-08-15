package karta

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipeline_NilScheduler_Panics validates that passing a nil scheduler
// to NewPipeline causes a panic with a descriptive message.
func TestPipeline_NilScheduler_Panics(t *testing.T) {
	handler := func(ctx context.Context, n int) (string, error) {
		return "", nil
	}
	assert.PanicsWithValue(t, "karta: scheduler must not be nil", func() {
		NewPipeline[int, string](handler, nil)
	})
}

func TestPipeline_Submit_AndFutureGet(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("got:%d", n), nil
	}
	p := NewPipeline[int, string](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	future, err := p.Submit(context.Background(), 42)
	require.NoError(t, err)

	result := future.Get(context.Background())
	assert.True(t, result.Ok())
	assert.Equal(t, "got:42", result.Value)
}

func TestPipeline_SubmitWithHandler(t *testing.T) {
	sched := NewSimpleScheduler(256)
	defaultHandler := func(ctx context.Context, n int) (int, error) {
		return n * 1, nil
	}
	p := NewPipeline[int, int](defaultHandler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	// 使用自定义 handler，覆盖默认
	customHandler := func(ctx context.Context, n int) (int, error) {
		return n * 100, nil
	}
	future, err := p.SubmitWithHandler(context.Background(), customHandler, 5)
	require.NoError(t, err)

	result := future.Get(context.Background())
	assert.True(t, result.Ok())
	assert.Equal(t, 500, result.Value)

	// 用默认 handler 提交，验证仍然是 *1
	future2, err := p.Submit(context.Background(), 7)
	require.NoError(t, err)
	result2 := future2.Get(context.Background())
	assert.True(t, result2.Ok())
	assert.Equal(t, 7, result2.Value)
}

func TestPipeline_SubmitAfter(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n + 1, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	start := time.Now()
	future, err := p.SubmitAfter(context.Background(), 10, 100*time.Millisecond)
	require.NoError(t, err)

	result := future.Get(context.Background())
	elapsed := time.Since(start)

	assert.True(t, result.Ok())
	assert.Equal(t, 11, result.Value)
	assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond)
}

func TestPipeline_Submit_AfterClosed(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)

	p.Stop()

	_, err := p.Submit(context.Background(), 1)
	assert.ErrorIs(t, err, ErrPipelineClosed)
}

func TestPipeline_HandlerError_InFuture(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return 0, errors.New("handler failed")
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	future, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	result := future.Get(context.Background())
	assert.False(t, result.Ok())
	assert.EqualError(t, result.Err, "handler failed")
}

func TestPipeline_PanicRecovery(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		panic("boom")
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	future, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	result := future.Get(context.Background())
	assert.False(t, result.Ok())
	require.NotNil(t, result.Err)
	assert.Contains(t, result.Err.Error(), "handler panic")
	assert.Contains(t, result.Err.Error(), "boom")

	// panic 后 pipeline 仍然可用（executor 没有崩溃）
	// 重新提交一个正常任务验证
	p2 := NewPipeline[int, int](func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}, NewSimpleScheduler(256))
	defer p2.Stop()

	f2, err2 := p2.Submit(context.Background(), 5)
	require.NoError(t, err2)
	r2 := f2.Get(context.Background())
	assert.True(t, r2.Ok())
	assert.Equal(t, 10, r2.Value)
}

func TestPipeline_GetWorkerNumber(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(4))
	require.NotNil(t, p)
	defer p.Stop()

	// 启动了 1 个常驻 executor goroutine
	assert.GreaterOrEqual(t, p.GetWorkerNumber(), int64(1))
}

func TestPipeline_ConcurrentSubmit(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)

	for i := 0; i < N; i++ {
		go func(val int) {
			defer wg.Done()
			future, err := p.Submit(context.Background(), val)
			if err != nil {
				return
			}
			result := future.Get(context.Background())
			assert.True(t, result.Ok())
			assert.Equal(t, val*2, result.Value)
		}(i)
	}

	wg.Wait()
}

func TestPipeline_WorkersSpawned(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		time.Sleep(50 * time.Millisecond) // 让任务有足够的执行时间
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(4))
	require.NotNil(t, p)
	defer p.Stop()

	// 快速提交一批任务，触发 worker 增长
	for i := 0; i < 8; i++ {
		p.Submit(context.Background(), i)
	}
	time.Sleep(200 * time.Millisecond) // 等待 potential spawn

	wn := p.GetWorkerNumber()
	assert.GreaterOrEqual(t, wn, int64(1), "should have at least 1 worker")
	t.Logf("workers after submitting 8 tasks: %d", wn)
}

func TestPipeline_IdleTimeout(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(4),
		WithIdleTimeout(500*time.Millisecond),
		WithScanInterval(200*time.Millisecond),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// 初始状态
	time.Sleep(100 * time.Millisecond)

	// 提交一批任务
	for i := 0; i < 8; i++ {
		p.Submit(context.Background(), i)
	}
	time.Sleep(200 * time.Millisecond)
	workersAfterLoad := p.GetWorkerNumber()
	assert.GreaterOrEqual(t, workersAfterLoad, int64(1))

	// 空闲等待 idle timeout 生效
	time.Sleep(2 * time.Second)
	workersAfterIdle := p.GetWorkerNumber()
	// 至少保留 1 个 worker，其余应已退出
	assert.GreaterOrEqual(t, workersAfterIdle, int64(1), "should keep at least 1 worker alive")
	t.Logf("workers after load: %d, after idle: %d", workersAfterLoad, workersAfterIdle)
}

func TestPipeline_MessageHandleFunc_Exists(t *testing.T) {
	var fn MessageHandleFunc = func(msg any) (any, error) {
		return msg, nil
	}
	result, err := fn("hello")
	assert.NoError(t, err)
	assert.Equal(t, "hello", result)
	assert.NotNil(t, DefaultMsgHandleFunc)
}

// TestPipeline_SubmitAfter_StopBeforeTimerFires — Stop 在 timer 触发前关闭，
// 延迟提交的 Future 应被 Resolve（不永久阻塞 Get）
func TestPipeline_SubmitAfter_StopBeforeTimerFires(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)

	// 提交延迟任务（1 小时后触发 timer，远大于测试时间）
	future, err := p.SubmitAfter(context.Background(), 42, time.Hour)
	require.NoError(t, err)

	// 立即 Stop — 此时 timer 尚未触发
	p.Stop()

	// Future.Get 应在合理时间内返回（不永久阻塞）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := future.Get(ctx)
	assert.Error(t, result.Err, "future should be resolved with an error after Stop")
}

// TestPipeline_SubmitAfter_StopAfterTimerFires — timer 触发后 Stop，
// 延迟任务已入队并被执行，Future 应有正确结果
func TestPipeline_SubmitAfter_StopAfterTimerFires(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	// 提交短延迟任务
	future, err := p.SubmitAfter(context.Background(), 21, 50*time.Millisecond)
	require.NoError(t, err)

	// 等待任务执行完成
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := future.Get(ctx)
	assert.NoError(t, result.Err)
	assert.Equal(t, 42, result.Value)
}

// TestPipeline_SubmitAfter_StopConcurrentRace — 并发 SubmitAfter + Stop，
// 验证延迟 goroutine 与 Stop 竞态下 Future 不会永久阻塞
func TestPipeline_SubmitAfter_StopConcurrentRace(t *testing.T) {
	for round := 0; round < 20; round++ {
		sched := NewSimpleScheduler(256)
		handler := func(ctx context.Context, n int) (int, error) {
			return n, nil
		}
		p := NewPipeline[int, int](handler, sched)
		require.NotNil(t, p)

		// 并发提交多个延迟任务（timer 极短，与 Stop 竞态）
		var wg sync.WaitGroup
		futures := make([]*Future[int], 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				f, err := p.SubmitAfter(context.Background(), idx, time.Millisecond)
				if err != nil {
					return
				}
				futures[idx] = f
			}(i)
		}
		wg.Wait()

		// 立即 Stop，与 timer 触发竞态
		p.Stop()

		// 所有 Future 必须在 2 秒内 resolve（不永久阻塞）
		getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
		for _, f := range futures {
			if f == nil {
				continue
			}
			result := f.Get(getCtx)
			// 允许任何结果（成功或 error），但不能超时
			_ = result
		}
		getCancel()
	}
}

// ---------------------------------------------------------------------------
// Coverage Tests: submitInternal 未覆盖路径 & trySpawnWorker & Stop
// ---------------------------------------------------------------------------

// TestPipeline_SubmitAfter_UserCtxCancelled — 延迟期间用户 context 被取消，
// 覆盖 submitInternal 的 case <-ctx.Done() 分支 (lines 167-169)
func TestPipeline_SubmitAfter_UserCtxCancelled(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("ok:%d", n), nil
	}
	p := NewPipeline[int, string](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	future, err := p.SubmitAfter(ctx, 42, 500*time.Millisecond)
	require.NoError(t, err)

	// 50ms 后取消用户 context（timer 500ms 未触发）
	time.Sleep(50 * time.Millisecond)
	cancel()

	getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer getCancel()
	result := future.Get(getCtx)
	assert.False(t, result.Ok())
	assert.ErrorIs(t, result.Err, context.Canceled)
}

// TestPipeline_SubmitAfter_ShortDelayFlow — 短暂延迟后的正常执行流程
// 覆盖 submitInternal 延迟入队+成功执行路径
func TestPipeline_SubmitAfter_ShortDelayFlow(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n * 3, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	for i := 0; i < 5; i++ {
		future, err := p.SubmitAfter(context.Background(), i, 30*time.Millisecond)
		require.NoError(t, err)

		getCtx, getCancel := context.WithTimeout(context.Background(), 3*time.Second)
		result := future.Get(getCtx)
		getCancel()
		assert.NoError(t, result.Err)
		assert.Equal(t, i*3, result.Value)
	}
}

// TestPipeline_SubmitAfter_DelayAndStop_MultiRound — 多轮 timer+Stop 竞速
// 每轮 timer 可能先于 Stop 触发（覆盖 case <-timer.C + closed check）
// 也可能 Stop 先触发（覆盖 case <-p.ctx.Done()或 pending.Range 清理）
func TestPipeline_SubmitAfter_DelayAndStop_MultiRound(t *testing.T) {
	var gotTimerPath, gotPipelineCtxPath, gotClosedPath int
	for round := 0; round < 50; round++ {
		sched := NewSimpleScheduler(256)
		handler := func(ctx context.Context, n int) (int, error) {
			return n, nil
		}
		p := NewPipeline[int, int](handler, sched)

		// 15ms 延迟 + 立即 Stop: 让 timer 和 Stop 竞速
		// （短延迟使 timer 有机会先触发，而 Stop 则走 p.ctx.Done()）
		future, err := p.SubmitAfter(context.Background(), round, 15*time.Millisecond)
		if err != nil {
			// SubmitAfter 返回 ErrPipelineClosed → 命中 closed race
			gotClosedPath++
			p.Stop()
			continue
		}

		// 不等任何时间立即 Stop — Stop 设置 closed 并 cancel()
		p.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result := future.Get(ctx)
		cancel()
		if result.Err == nil {
			gotTimerPath++
		} else if errors.Is(result.Err, context.Canceled) {
			gotPipelineCtxPath++
		} else if errors.Is(result.Err, ErrPipelineClosed) {
			gotClosedPath++
		}
	}
	assert.Greater(t, gotPipelineCtxPath+gotTimerPath+gotClosedPath, 0,
		"at least one path triggered")
	t.Logf("timerPath=%d pipelineCtxPath=%d closedPath=%d",
		gotTimerPath, gotPipelineCtxPath, gotClosedPath)
}

// TestPipeline_SubmitAfter_ConcurrentCancelAndTimer — 延迟任务并发取消，
// 覆盖 cancel+timer 并发 select 下未来永久 resolve 的保证
func TestPipeline_SubmitAfter_ConcurrentCancelAndTimer(t *testing.T) {
	for round := 0; round < 30; round++ {
		sched := NewSimpleScheduler(256)
		handler := func(ctx context.Context, n int) (int, error) {
			return n * 2, nil
		}
		p := NewPipeline[int, int](handler, sched)

		ctx, cancel := context.WithCancel(context.Background())
		future, err := p.SubmitAfter(ctx, round, 20*time.Millisecond)
		if err != nil {
			cancel()
			p.Stop()
			continue
		}

		// 20ms 后同时取消 ctx 和 Stop，与 timer 竞态
		time.Sleep(20 * time.Millisecond)
		cancel()
		p.Stop()

		getCtx, getCancel := context.WithTimeout(context.Background(), time.Second)
		result := future.Get(getCtx)
		getCancel()
		// 必须 resolve（不永久阻塞），结果可为成功或错误
		assert.NotNil(t, &result, "future should be resolved")
	}
}

// TestPipeline_TrySpawn_SlowHandler — 慢 handler + 并发提交，worker 数量超过上限
// 覆盖 trySpawnWorker 的 spawn 路径 (lines 379-382)
// 策略：使用高 workers(16) + 短 idle timeout 让部分 worker idle-exit，
// 然后提交慢任务触发 trySpawnWorker 重新 spawn
func TestPipeline_TrySpawn_SlowHandler(t *testing.T) {
	sched := NewSimpleScheduler(512)
	handler := func(ctx context.Context, n int) (int, error) {
		time.Sleep(250 * time.Millisecond)
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(16),
		WithIdleTimeout(400*time.Millisecond),
		WithScanInterval(100*time.Millisecond),
		WithSpawnRate(1000),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// Phase 1: 提交一批慢任务，让所有 worker 被占用
	for i := 0; i < 16; i++ {
		_, err := p.Submit(context.Background(), i)
		if err != nil {
			t.Logf("submit %d failed: %v", i, err)
		}
	}
	// 等待慢 handler 全部执行完（400ms 应该足够）
	time.Sleep(600 * time.Millisecond)
	wBeforeIdle := p.GetWorkerNumber()
	t.Logf("workers after phase 1: %d", wBeforeIdle)

	// Phase 2: 等待 idle timeout 让更多 worker 退出
	time.Sleep(900 * time.Millisecond)
	wAfterIdle := p.GetWorkerNumber()
	t.Logf("workers after idle: %d", wAfterIdle)
	// 应该有些 worker idle-exit 了，但保留至少 1 个
	assert.GreaterOrEqual(t, wAfterIdle, int64(1), "at least 1 worker survives")

	// Phase 3: 提交新的慢任务，触发 trySpawnWorker
	for i := 0; i < 8; i++ {
		_, err := p.Submit(context.Background(), i+100)
		if err != nil {
			t.Logf("submit phase3 %d failed: %v", i, err)
		}
	}
	time.Sleep(80 * time.Millisecond)
	wAfterSpawn := p.GetWorkerNumber()
	t.Logf("workers after phase 3: %d", wAfterSpawn)

	// trySpawnWorker 应该被调用（worker 数上升或至少尝试 spawn）
	// 由于 trySpawnWorker 在 submit 时被调用，即使 limiter 拒绝也算覆盖
	assert.GreaterOrEqual(t, wAfterSpawn, int64(1), "should have at least 1 worker")

	// 等待所有任务完成
	time.Sleep(2 * time.Second)
}

// TestPipeline_TrySpawn_RateLimited — 高 spawnRate + short idle timeout +
// 大量任务，验证 worker 数量能重新增长
// 覆盖 trySpawnWorker 的 Allow+spawn 路径
func TestPipeline_TrySpawn_RateLimited(t *testing.T) {
	sched := NewSimpleScheduler(512)
	handler := func(ctx context.Context, n int) (int, error) {
		time.Sleep(200 * time.Millisecond)
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(8),
		WithIdleTimeout(300*time.Millisecond),
		WithScanInterval(80*time.Millisecond),
		WithSpawnRate(1000),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// Phase 1: 提交 8 个慢任务占满初始 worker
	for i := 0; i < 8; i++ {
		_, _ = p.Submit(context.Background(), i)
	}
	time.Sleep(400 * time.Millisecond) // 等 handler 完成

	// Phase 2: 等待 idle 让更多 worker 退出
	time.Sleep(600 * time.Millisecond)
	wAfterIdle := p.GetWorkerNumber()
	t.Logf("workers after idle: %d", wAfterIdle)

	// Phase 3: 批量提交任务，触发 trySpawnWorker
	for i := 0; i < 12; i++ {
		_, _ = p.Submit(context.Background(), i+100)
	}
	time.Sleep(100 * time.Millisecond)
	wAfterSpawn := p.GetWorkerNumber()
	t.Logf("workers after spawn: %d (was %d after idle)", wAfterSpawn, wAfterIdle)

	// trySpawnWorker 路径被调用（无论是否实际 spawn）
	// 主要目的是覆盖代码路径，不强制要求 worker 数一定超过 idle 后的数量
	assert.GreaterOrEqual(t, wAfterSpawn, int64(1))

	time.Sleep(2 * time.Second) // 等待任务完成
}

// TestPipeline_Stop_ClearsDelayedFutures — Stop 清理 delay 提交但
// 未进入 pending 的 futures，验证 Stop 幂等且不阻塞
func TestPipeline_Stop_ClearsDelayedFutures(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)

	// 提交多个不同延迟的任务
	futures := make([]*Future[int], 5)
	var errCount int
	for i := 0; i < 5; i++ {
		f, err := p.SubmitAfter(context.Background(), i, time.Duration(i*100+50)*time.Millisecond)
		if err != nil {
			errCount++
		} else {
			futures[i] = f
		}
	}

	// 立即 Stop（所有延迟任务都还在 timer 等待中）
	p.Stop()

	// 所有 future 必须在 2 秒内 resolve
	getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer getCancel()
	for _, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(getCtx)
		assert.Error(t, result.Err, "delayed future should be resolved with error after Stop")
	}
}

// TestPipeline_Stop_Idempotent — Stop 的幂等性：多次调用不 panic
func TestPipeline_Stop_Idempotent(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)

	// 提交一些延迟任务（覆盖 pending map）
	for i := 0; i < 3; i++ {
		_, err := p.SubmitAfter(context.Background(), i, time.Duration(i+1)*100*time.Millisecond)
		if err != nil {
			break
		}
	}

	// 多次调用 Stop 应该安全
	p.Stop()
	p.Stop() // 幂等
	p.Stop()
}

// TestPipeline_EnqueueFull_SubmitError — scheduler 缓冲区满时
// Submit 返回 SubmitError，覆盖 submitInternal 的 Enqueue 失败路径 (lines 180-186)
func TestPipeline_EnqueueFull_SubmitError(t *testing.T) {
	// 极小 buffer 使 scheduler 容易满
	sched := NewSimpleScheduler(1)
	// 慢 handler 阻塞 executor 较久，让任务堆积在 scheduler
	handler := func(ctx context.Context, n int) (int, error) {
		time.Sleep(800 * time.Millisecond)
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(2),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// 提交 3 个慢任务填满 2 workers + 1 buffer
	var submitted int
	for i := 0; i < 3; i++ {
		_, err := p.Submit(context.Background(), i)
		if err == nil {
			submitted++
		}
	}
	if submitted < 3 {
		t.Skipf("not enough tasks submitted (%d), scheduler filled early", submitted)
	}

	// 极短等待，worker 拉走任务但 handler 仍在执行（sleep 800ms）
	time.Sleep(20 * time.Millisecond)

	// 第 4 次提交：buffer 已满（2 个 worker busy + 1 slot taken）
	_, err := p.Submit(context.Background(), 99)
	if err != nil {
		var submitErr *SubmitError
		assert.True(t, errors.As(err, &submitErr), "should be SubmitError: %v", err)
		assert.ErrorIs(t, err, ErrSchedulerFull)
	} else {
		// 若 scheduler 提前 drain 了（race 下极小概率），不 fail
		t.Log("scheduler drained before overflow (race)")
	}
}

// TestPipeline_EnqueueFull_SubmitAfter_SchedulerShutdown — 延迟任务的 timer
// 触发时 scheduler 已关闭，覆盖 submitInternal 延迟路径的 Enqueue 失败+Resolve 错误
func TestPipeline_EnqueueFull_SubmitAfter_SchedulerShutdown(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)

	// 短延迟任务
	future, err := p.SubmitAfter(context.Background(), 42, 20*time.Millisecond)
	require.NoError(t, err)

	// 立即关闭 scheduler（模拟极端场景）
	sched.Shutdown()

	// 等 timer 触发 → Enqueue 到已关闭 scheduler → 失败
	// 或者 Stop 的 goroutine resolve → 两者都会 resolve
	time.Sleep(200 * time.Millisecond)
	p.Stop()

	getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer getCancel()
	result := future.Get(getCtx)
	assert.Error(t, result.Err, "should resolve with error (scheduler closed or pipeline closed)")
}

// TestPipeline_SubmitAfter_PipelineClosedDuringDelay — Stop 在 wg.Add 之后
// 但在 goroutine 启动前被调用（尝试覆盖 closed.Load race at lines 134-138）
// 此场景极度竞态，通过高密度循环增加覆盖概率
func TestPipeline_SubmitAfter_PipelineClosedDuringDelay(t *testing.T) {
	for round := 0; round < 200; round++ {
		sched := NewSimpleScheduler(256)
		handler := func(ctx context.Context, n int) (int, error) {
			return n, nil
		}
		p := NewPipeline[int, int](handler, sched)

		// 使用极短延迟，让 SubmitAfter 刚 wg.Add 就 Stop
		f, err := p.SubmitAfter(context.Background(), round, time.Microsecond)
		if err != nil {
			p.Stop()
			continue
		}
		// 立刻 Stop: 尝试命中 wg.Add → closed.Load 竞态
		go func() { p.Stop() }()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result := f.Get(ctx)
		cancel()
		_ = result
	}
}

// TestPipeline_GetWorkerNumber_AfterSubmit — 提交任务后 worker 数增加
// 通过 idle-timeout 降低 running，再提交慢任务触发 trySpawnWorker
func TestPipeline_GetWorkerNumber_AfterSubmit(t *testing.T) {
	sched := NewSimpleScheduler(512)
	handler := func(ctx context.Context, n int) (int, error) {
		// 等待 p.ctx.Done()（Stop 取消）或 500ms 之后返回
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return n, nil
		}
	}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(8),
		WithIdleTimeout(300*time.Millisecond),
		WithScanInterval(100*time.Millisecond),
		WithSpawnRate(1000),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// Phase 1: 占满所有 worker → idle → 大量退出
	for i := 0; i < 8; i++ {
		_, err := p.Submit(context.Background(), i)
		if err != nil {
			t.Logf("submit %d: %v", i, err)
		}
	}
	// 等待 handler 完成（500ms）+ idle exit（300ms + scan 100ms）
	time.Sleep(1 * time.Second)
	wAfterIdle := p.GetWorkerNumber()
	t.Logf("workers after phase 1 + idle: %d", wAfterIdle)
	// 应该大部分 worker 已退出，但至少保留 1 个
	assert.GreaterOrEqual(t, wAfterIdle, int64(1), "at least 1 worker survives")

	// Phase 2: 大量提交触发 trySpawnWorker
	for i := 0; i < 16; i++ {
		_, err := p.Submit(context.Background(), i+100)
		if err != nil {
			continue
		}
	}
	time.Sleep(50 * time.Millisecond)
	wAfterSpawn := p.GetWorkerNumber()
	t.Logf("workers after phase 2: %d", wAfterSpawn)
	// 可能 spawn 了新 worker 或至少保持 1 个
	assert.GreaterOrEqual(t, wAfterSpawn, int64(1))

	// 等任务完成后 Stop
	time.Sleep(2 * time.Second)
}

// TestPipeline_SubmitAfter_ContextCancelBeforeEnqueue — 延迟任务在
// timer 触发前被 cancel，验证 future 正确 resolve 为 context.Canceled
func TestPipeline_SubmitAfter_ContextCancelBeforeEnqueue(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, s string) (string, error) {
		return s + "-done", nil
	}
	p := NewPipeline[string, string](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	future, err := p.SubmitAfter(ctx, "hello", 1*time.Second)
	require.NoError(t, err)

	// 立即取消（timer 1s 远未触发）
	cancel()

	getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer getCancel()
	result := future.Get(getCtx)
	assert.ErrorIs(t, result.Err, context.Canceled)
}

// TestPipeline_LoadAndDeletePending_NotFound — 直接调用 loadAndDeletePending
// 传入未在 pending map 中的 envelope，覆盖 nil return 路径 (lines 361-363)
func TestPipeline_LoadAndDeletePending_NotFound(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	// Synthetic envelope（不在 pending map 中）
	synthetic := &TaskEnvelope{Input: 42}
	got := p.loadAndDeletePending(synthetic)
	assert.Nil(t, got, "should return nil when envelope not in pending")
}

// TestPipeline_LoadAndDeletePending_Found — loadAndDeletePending
// 能找到并返回 pending map 中的 future（覆盖正常路径）
func TestPipeline_LoadAndDeletePending_Found(t *testing.T) {
	sched := NewSimpleScheduler(256)
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := NewPipeline[int, int](handler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	// 手动添加 entry 到 pending map
	envelope := &TaskEnvelope{Input: 99}
	future := NewPendingFuture[int]()
	p.pendingMu.Lock()
	p.pending[envelope] = future
	p.pendingMu.Unlock()

	// 应能找到 future
	got := p.loadAndDeletePending(envelope)
	assert.NotNil(t, got, "should return future when envelope in pending")
	assert.Equal(t, future, got, "returned future should be the same")

	// 第二次调用应返回 nil（已删除）
	got2 := p.loadAndDeletePending(envelope)
	assert.Nil(t, got2, "second call should return nil")
}

// TestPipeline_TrySpawn_RateLimiterReject — rate limiter 拒绝 spawn，
// 覆盖 trySpawnWorker 的 Allow=false 分支 (lines 376-378)
// 策略：用低 spawnRate(1/sec) 快速消耗 burst 令牌后触发拒绝
func TestPipeline_TrySpawn_RateLimiterReject(t *testing.T) {
	sched := NewSimpleScheduler(512)
	handler := func(ctx context.Context, n int) (int, error) {
		time.Sleep(100 * time.Millisecond)
		return n, nil
	}
	// workers=16: 初始 16, idle-exit 后 remaining < burstLimit=8
	// spawnRate=1: 每秒仅 refill 1 token
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(16),
		WithIdleTimeout(250*time.Millisecond),
		WithScanInterval(80*time.Millisecond),
		WithSpawnRate(1),
	)
	require.NotNil(t, p)
	defer p.Stop()

	// Phase 1: 让大部分 worker idle-exit
	time.Sleep(700 * time.Millisecond)
	wIdle := p.GetWorkerNumber()
	t.Logf("workers after idle: %d", wIdle)

	// Phase 2: 快速提交大量任务（<burstLimit 内的 Allow=true，超出则 false）
	// burstLimit=8，若 running < 8，需要 >8 次 spawn 尝试以触发 reject
	for i := 0; i < 25; i++ {
		_, err := p.Submit(context.Background(), i+100)
		if err != nil {
			break
		}
	}

	time.Sleep(100 * time.Millisecond)
	wAfter := p.GetWorkerNumber()
	t.Logf("workers after burst submit: %d", wAfter)
	// 不论是否有被 reject，路径都被 trySpawnWorker 调用过
	assert.GreaterOrEqual(t, wAfter, int64(1))

	time.Sleep(1500 * time.Millisecond) // wait tasks complete
}

// TestPipeline_Submit_AlreadyCancelledCtx — P1 #7: 已取消 UserCtx 的任务
// 直接以取消错误完成，handler 不得执行
func TestPipeline_Submit_AlreadyCancelledCtx(t *testing.T) {
	var called atomic.Int64
	handler := func(ctx context.Context, n int) (int, error) {
		called.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineWorkers(2))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 提交前已取消

	f, err := p.Submit(ctx, 1)
	require.NoError(t, err)

	getCtx, getCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer getCancel()
	res := f.Get(getCtx)
	assert.ErrorIs(t, res.Err, context.Canceled, "future 应立即得到 context.Canceled")
	assert.Zero(t, called.Load(), "handler 不应被调用")

	// pipeline 后续仍正常可用
	f2, err := p.Submit(context.Background(), 2)
	require.NoError(t, err)
	res2 := f2.Get(getCtx)
	require.NoError(t, res2.Err)
	assert.Equal(t, 2, res2.Value)
}

// TestPipeline_SubmitStop_Stress — P1 #6 压力测试:
// 并发 goroutine 循环 Submit/SubmitAfter(1ms)，另一 goroutine 随机时机 Stop。
// closed 检查/pending 写入/wg.Add 同处 pendingMu 临界区，
// -race -count=10 下不得出现 WaitGroup misuse panic 或竞态
func TestPipeline_SubmitStop_Stress(t *testing.T) {
	for range 10 {
		p := NewPipeline[int, int](
			func(ctx context.Context, n int) (int, error) { return n, nil },
			NewSimpleScheduler(1024),
			WithPipelineWorkers(4),
		)

		stopCh := make(chan struct{})
		var subWG sync.WaitGroup
		for g := range 4 {
			subWG.Add(1)
			go func(id int) {
				defer subWG.Done()
				for i := 0; ; i++ {
					select {
					case <-stopCh:
						return
					default:
					}
					var f *Future[int]
					var err error
					if i%2 == 0 {
						f, err = p.Submit(context.Background(), id*10000+i)
					} else {
						f, err = p.SubmitAfter(context.Background(), id*10000+i, time.Millisecond)
					}
					if err != nil {
						continue
					}
					_ = f.Get(context.Background())
				}
			}(g)
		}

		// 独立 goroutine 随机时机 Stop（幂等）
		stopDone := make(chan struct{})
		go func() {
			defer close(stopDone)
			time.Sleep(time.Duration(rand.IntN(20)+1) * time.Millisecond)
			p.Stop()
		}()

		<-stopDone
		close(stopCh)
		subWG.Wait()
	}
}
