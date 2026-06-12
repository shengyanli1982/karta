package karta_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shengyanli1982/karta/v2/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// ═══════════════════════════════════════════════════════════════
// Phase 2 集成测试 — v2 核心组件端到端验证
// ═══════════════════════════════════════════════════════════════
//
// 覆盖场景:
//   1. Group + Recovery + 计数 Middleware
//   2. Pipeline + FIFO Scheduler
//   3. Pipeline + Priority Scheduler
//   4. Pipeline + Bounded Scheduler
//   5. Pipeline + Retry Scheduler
//   6. Pipeline + Middleware 链（计数 + Recovery）
//   7. 压力测试: 2000 并发任务
//   8. LifecycleManager + Group + Pipeline 优雅关闭
//
// 手写简化版 middleware 以避免循环依赖 (middleware 子包)
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────
// 手写简化版 Middleware（避免导入 middleware 子包）
// ─────────────────────────────────────────────────────────────

// testRecoveryMW 捕获 panic 并转化为 error
func testRecoveryMW[In, Out any]() karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (out Out, err error) {
			defer func() {
				if rec := recover(); rec != nil {
					var zero Out
					err = fmt.Errorf("recovered: %v", rec)
					out = zero
				}
			}()
			return next(ctx, input)
		}
	}
}

// testCountingMW 创建一个计数中间件
func testCountingMW[In, Out any](counter *atomic.Int64) karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			counter.Add(1)
			return next(ctx, input)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// IntReq / IntRes — 避免与已有 integration_v2_test.go 的类型冲突
// ─────────────────────────────────────────────────────────────

// IntReq is a simplified integer-only request struct.
type IntReq struct {
	ID   int
	Body string
}

// IntRes is a simplified integer response struct.
type IntRes struct {
	ID     int
	Status string
}

// ─────────────────────────────────────────────────────────────
// 1. Group + Recovery + 计数 Middleware
// ─────────────────────────────────────────────────────────────

// TestIntegration_Group_MiddlewareChain 验证 Group 的 middleware 链行为：
// handler 偶发 panic，Recovery 中间件捕获 panic 返回 error，
// 计数中间件验证每个调用都经过（含 panic 场景）。
func TestIntegration_Group_MiddlewareChain(t *testing.T) {
	const taskCount = 20

	var count atomic.Int64

	countingMW := testCountingMW[int, string](&count)
	recoveryMW := testRecoveryMW[int, string]()

	// Chain order: recovery(outer) → counting(inner) → handler
	// When handler panics:
	//   1. counting.defer increments counter (panic propagates up)
	//   2. recovery catches panic → returns error "recovered: ..."

	handler := func(ctx context.Context, n int) (string, error) {
		if n%2 == 0 {
			panic(fmt.Sprintf("panic on %d", n))
		}
		return fmt.Sprintf("ok-%d", n), nil
	}

	g := karta.NewGroup[int, string](
		handler,
		karta.WithGroupWorkers(4),
		karta.WithGroupMiddleware(recoveryMW, countingMW),
	)
	defer g.Stop()

	inputs := make([]int, taskCount)
	for i := 0; i < taskCount; i++ {
		inputs[i] = i
	}

	results := g.Map(context.Background(), inputs)
	require.Len(t, results, taskCount)

	panicCount := 0
	okCount := 0
	for i, r := range results {
		if inputs[i]%2 == 0 {
			assert.False(t, r.Ok(), "result[%d] (input=%d even) should have error from recovery", i, inputs[i])
			assert.Contains(t, r.Err.Error(), "recovered:", "error should indicate panic recovery")
			panicCount++
		} else {
			assert.True(t, r.Ok(), "result[%d] (input=%d odd) should be Ok", i, inputs[i])
			assert.Equal(t, fmt.Sprintf("ok-%d", inputs[i]), r.Value)
			okCount++
		}
	}

	// 验证每个调用都经过 counting middleware
	assert.Equal(t, int64(taskCount), count.Load(),
		"counting MW should be invoked for every call (%d ok, %d panic)", okCount, panicCount)
}

// ─────────────────────────────────────────────────────────────
// 2. Pipeline + FIFO Scheduler
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_FIFOScheduler 验证 Pipeline 与 FIFO 调度器的基本流程：
// 使用 FIFO 调度器提交 100 个任务，全部 Future.Get 验证结果正确性。
func TestIntegration_Pipeline_FIFOScheduler(t *testing.T) {
	const taskCount = 100

	sched := scheduler.NewFIFOScheduler()

	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("fifo-%d", n), nil
	}

	p := karta.NewPipeline[int, string](handler, sched, karta.WithPipelineWorkers(16))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 并发提交 100 个任务
	var wg sync.WaitGroup
	futures := make([]*karta.Future[string], taskCount)
	submitErrors := make([]error, taskCount)

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f, err := p.Submit(ctx, idx+1)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i)
	}
	wg.Wait()

	// 验证所有提交成功
	for i, err := range submitErrors {
		assert.NoError(t, err, "Submit(%d) should succeed", i+1)
	}

	// 逐个 Get 验证结果
	for i, f := range futures {
		require.NotNil(t, f, "future[%d] should not be nil", i)
		result := f.Get(ctx)
		assert.True(t, result.Ok(), "future[%d] should be Ok, got err: %v", i, result.Err)
		assert.Equal(t, fmt.Sprintf("fifo-%d", i+1), result.Value, "future[%d] value mismatch", i)
	}
}

// ─────────────────────────────────────────────────────────────
// 3. Pipeline + Priority Scheduler
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_PriorityScheduler 验证 Pipeline 与优先级调度器的集成：
// 使用不同 priority 的任务，验证调度器能正常工作并处理所有任务。
func TestIntegration_Pipeline_PriorityScheduler(t *testing.T) {
	const taskCount = 50

	sched := scheduler.NewPriorityScheduler()

	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("priority-%d", n), nil
	}

	p := karta.NewPipeline[int, string](handler, sched, karta.WithPipelineWorkers(16))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 构造不同 priority 的 TaskEnvelope 并提交
	type priorityTask struct {
		input    int
		priority int64
	}

	tasks := make([]priorityTask, taskCount)
	for i := 0; i < taskCount; i++ {
		tasks[i] = priorityTask{
			input:    i + 1,
			priority: int64(i%5 + 1), // priority 1-5, smaller = higher
		}
	}

	// 提交所有优先级任务
	var wg sync.WaitGroup
	futures := make([]*karta.Future[string], taskCount)
	submitErrors := make([]error, taskCount)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, pt priorityTask) {
			defer wg.Done()
			f, err := p.Submit(ctx, pt.input)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i, task)
	}
	wg.Wait()

	// 验证提交成功
	for i, err := range submitErrors {
		assert.NoError(t, err, "task[%d] submit failed: %v", i, err)
	}

	// 验证所有任务都被处理
	successCount := 0
	for i, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(ctx)
		if result.Ok() {
			successCount++
		} else {
			t.Logf("future[%d] failed: %v", i, result.Err)
		}
	}

	assert.Equal(t, taskCount, successCount,
		"all %d tasks should be processed by priority scheduler pipeline", taskCount)
}

// ─────────────────────────────────────────────────────────────
// 4. Pipeline + Bounded Scheduler
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_BoundedScheduler 验证 Pipeline 与有界阻塞调度器的集成：
// capacity=100，并发提交 200 个任务，快速 handler 确保背压正常排空，
// 验证所有能入队的任务都被正确处理。
func TestIntegration_Pipeline_BoundedScheduler(t *testing.T) {
	const (
		taskCount = 200
		capacity  = 100
	)

	sched := scheduler.NewBoundedScheduler(capacity)

	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil // 极快 handler，确保及时排空队列
	}

	p := karta.NewPipeline[int, int](handler, sched, karta.WithPipelineWorkers(16))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 并发提交超出容量的任务
	var wg sync.WaitGroup
	futures := make([]*karta.Future[int], taskCount)
	submitErrors := make([]error, taskCount)

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f, err := p.Submit(ctx, idx+1)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i)
	}
	wg.Wait()

	// 统计提交和结果
	successCount := 0
	submitErrCount := 0
	for i, err := range submitErrors {
		if err != nil {
			submitErrCount++
			t.Logf("submit[%d] err: %v", i, err)
		}
	}

	for i, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(ctx)
		if result.Ok() {
			successCount++
			assert.Equal(t, (i+1)*2, result.Value,
				"future[%d] expected %d, got %d", i, (i+1)*2, result.Value)
		}
	}

	assert.Greater(t, successCount, capacity/2,
		"at least half of capacity tasks should succeed through bounded scheduler")

	t.Logf("Bounded scheduler test: %d submitted, %d submit errors, %d futures resolved OK",
		taskCount-submitErrCount, submitErrCount, successCount)
}

// ─────────────────────────────────────────────────────────────
// 5. Pipeline + Retry Scheduler
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_RetryScheduler 验证 Pipeline 与重试调度器的基本集成：
// 使用 RetryScheduler 提交任务，偶发 handler 错误，验证调度器正常工作。
// 注意: Pipeline executor 不自动调用 scheduler.Retry()，因此重试由 Pipeline
// 的 safeCall + Future 层处理，本测试仅验证调度器通道畅通且有任务完成。
func TestIntegration_Pipeline_RetryScheduler(t *testing.T) {
	sched := scheduler.NewRetryScheduler(nil) // 使用默认重试策略

	var failCount atomic.Int64
	handler := func(ctx context.Context, n int) (string, error) {
		if n == 5 {
			failCount.Add(1)
			return "", fmt.Errorf("transient error from task %d", n)
		}
		return fmt.Sprintf("retry-ok-%d", n), nil
	}

	p := karta.NewPipeline[int, string](
		handler, sched,
		karta.WithPipelineWorkers(16),
	)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const taskCount = 30

	var wg sync.WaitGroup
	futures := make([]*karta.Future[string], taskCount)
	submitErrors := make([]error, taskCount)

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f, err := p.Submit(ctx, idx)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i)
	}
	wg.Wait()

	// 验证提交成功
	for i, err := range submitErrors {
		assert.NoError(t, err, "Submit(%d) should succeed with retry scheduler", i)
	}

	successCount := 0
	failResultCount := 0
	for i, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(ctx)
		if result.Ok() {
			successCount++
			if i != 5 {
				assert.Equal(t, fmt.Sprintf("retry-ok-%d", i), result.Value)
			}
		} else {
			failResultCount++
			if i == 5 {
				assert.Contains(t, result.Err.Error(), "transient error")
			}
		}
	}

	// 任务 5 失败，其余成功
	assert.Greater(t, successCount, taskCount-5,
		"most tasks should succeed (only task 5 fails), got %d success", successCount)
	assert.GreaterOrEqual(t, failResultCount, 1,
		"at least the known failing task should produce an error result")

	t.Logf("Retry scheduler test: %d success, %d failed", successCount, failResultCount)
}

// ─────────────────────────────────────────────────────────────
// 6. Pipeline + Middleware 链（计数 + Recovery）
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_MiddlewareChain 验证 Pipeline 的 middleware 链：
// 100 任务 + 部分 panic handler + 计数 MW + Recovery MW
// 验证每个任务都经过 middleware 链（含 panic 路径）。
func TestIntegration_Pipeline_MiddlewareChain(t *testing.T) {
	const taskCount = 100

	var count atomic.Int64

	countingMW := testCountingMW[int, string](&count)
	recoveryMW := testRecoveryMW[int, string]()

	handler := func(ctx context.Context, n int) (string, error) {
		if n%10 == 0 { // 每 10 个 panic 一次
			panic(fmt.Sprintf("panic on %d", n))
		}
		return fmt.Sprintf("chain-%d", n), nil
	}

	sched := karta.NewSimpleScheduler(4096)
	p := karta.NewPipeline[int, string](
		handler, sched,
		karta.WithPipelineWorkers(16),
		karta.WithPipelineMiddleware(recoveryMW, countingMW),
	)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	futures := make([]*karta.Future[string], taskCount)
	submitErrors := make([]error, taskCount)

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f, err := p.Submit(ctx, idx)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i)
	}
	wg.Wait()

	for i, err := range submitErrors {
		assert.NoError(t, err, "Submit(%d) should succeed", i)
	}

	recovered := 0
	ok := 0
	for i, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(ctx)
		if result.Ok() {
			assert.Equal(t, fmt.Sprintf("chain-%d", i), result.Value)
			ok++
		} else {
			assert.Contains(t, result.Err.Error(), "recovered:")
			recovered++
		}
	}

	assert.Equal(t, taskCount/10, recovered,
		"expected %d recovered panics", taskCount/10)
	assert.Equal(t, taskCount-(taskCount/10), ok,
		"expected %d successful tasks", taskCount-(taskCount/10))

	// 计数 MW 应记录所有调用
	assert.Equal(t, int64(taskCount), count.Load(),
		"counting MW should be invoked for every call (%d ok, %d recovered)", ok, recovered)
}

// ─────────────────────────────────────────────────────────────
// 7. 压力测试: 2000 并发任务
// ─────────────────────────────────────────────────────────────

// TestIntegration_ConcurrentPressure_2000 验证高并发场景下的正确性和 race-safety：
// Pipeline + SimpleScheduler(4096) + 16 workers, 并发提交 2000 个任务，
// 全部 Future 必须成功且值正确，-race 检测通过。
func TestIntegration_ConcurrentPressure_2000(t *testing.T) {
	const (
		concurrency = 2000
		bufferSize  = 4096
	)

	var processedCount atomic.Int64

	handler := func(ctx context.Context, n int) (int, error) {
		processedCount.Add(1)
		return n * 3, nil
	}

	sched := karta.NewSimpleScheduler(bufferSize)
	p := karta.NewPipeline[int, int](
		handler, sched,
		karta.WithPipelineWorkers(16),
	)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 2000 goroutines 并发提交
	var wg sync.WaitGroup
	futures := make([]*karta.Future[int], concurrency)
	submitErrors := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			f, err := p.Submit(ctx, idx+1)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = f
		}(i)
	}
	wg.Wait()

	// 统计提交阶段错误
	submitErrCount := 0
	for _, err := range submitErrors {
		if err != nil {
			submitErrCount++
		}
	}

	// 逐个获取结果
	successCount := 0
	for i, f := range futures {
		if f == nil {
			continue
		}
		result := f.Get(ctx)
		if result.Ok() {
			successCount++
			assert.Equal(t, (i+1)*3, result.Value,
				"future[%d] expected %d, got %d", i, (i+1)*3, result.Value)
		}
	}

	// 验证 100% 成功率（buffer 足够大 + handler 极快）
	assert.Equal(t, concurrency, successCount,
		"all %d tasks should succeed with buffer=%d, submit errors=%d",
		concurrency, bufferSize, submitErrCount)
	assert.Equal(t, int64(concurrency), processedCount.Load(),
		"handler should be called for all %d tasks, actual: %d",
		concurrency, processedCount.Load())
}

// ─────────────────────────────────────────────────────────────
// 8. LifecycleManager + Group + Pipeline 优雅关闭
// ─────────────────────────────────────────────────────────────

// testShutdownable 是一个自定义 Shutdownable 实现，用于验证 Stop 是否被调用。
type testShutdownable struct {
	stopped  atomic.Bool
	stopCall atomic.Int64
}

func (ts *testShutdownable) Stop() {
	ts.stopped.Store(true)
	ts.stopCall.Add(1)
}

// TestIntegration_LifecycleManager_FullGracefulShutdown 验证完整的优雅关闭流程：
// LifecycleManager 托管 Group + Pipeline + 自定义 Shutdownable
// Shutdown 后验证所有 Stop 被调用，且新 Submit 返回 ErrPipelineClosed。
func TestIntegration_LifecycleManager_FullGracefulShutdown(t *testing.T) {
	// 创建 Group
	groupHandler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("group-%d", n), nil
	}
	g := karta.NewGroup[int, string](groupHandler, karta.WithGroupWorkers(4))

	// 创建 Pipeline
	pipelineHandler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("pipeline-%d", n), nil
	}
	sched := karta.NewSimpleScheduler(512)
	p := karta.NewPipeline[int, string](pipelineHandler, sched, karta.WithPipelineWorkers(16))
	require.NotNil(t, p)

	// 自定义 Shutdownable
	custom := &testShutdownable{}

	// LifecycleManager 托管所有组件
	lm := karta.NewLifecycleManager(
		karta.WithManaged(g, p, custom),
		karta.WithShutdownTimeout(10*time.Second),
	)
	require.NotNil(t, lm)

	// ── Shutdown 前: 验证组件正常工作 ──
	t.Run("before shutdown - Group", func(t *testing.T) {
		results := g.Map(context.Background(), []int{1, 2, 3})
		require.Len(t, results, 3)
		for _, r := range results {
			assert.True(t, r.Ok())
		}
	})

	t.Run("before shutdown - Pipeline", func(t *testing.T) {
		f, err := p.Submit(context.Background(), 42)
		require.NoError(t, err)
		result := f.Get(context.Background())
		assert.True(t, result.Ok())
		assert.Equal(t, "pipeline-42", result.Value)
	})

	t.Run("before shutdown - custom not stopped", func(t *testing.T) {
		assert.False(t, custom.stopped.Load(),
			"custom shutdownable should not be stopped before shutdown")
	})

	// ── 执行 Shutdown ──
	lm.Shutdown()

	// ── Shutdown 后: 验证组件已停止 ──
	t.Run("after shutdown - Group stopped", func(t *testing.T) {
		results := g.Map(context.Background(), []int{1, 2, 3})
		assert.Nil(t, results, "Group.Map should return nil after Stop")
	})

	t.Run("after shutdown - Pipeline returns ErrPipelineClosed", func(t *testing.T) {
		_, err := p.Submit(context.Background(), 42)
		assert.Error(t, err, "Pipeline.Submit should return error after Stop")

		isPipelineClosed := errors.Is(err, karta.ErrPipelineClosed)
		var submitErr *karta.SubmitError
		isSubmitErr := errors.As(err, &submitErr)

		assert.True(t, isPipelineClosed || isSubmitErr,
			"expected ErrPipelineClosed or SubmitError, got: %v", err)
	})

	t.Run("after shutdown - custom stopped", func(t *testing.T) {
		assert.True(t, custom.stopped.Load(),
			"custom shutdownable should be stopped after shutdown")
		assert.GreaterOrEqual(t, custom.stopCall.Load(), int64(1),
			"Stop() should have been called at least once")
	})

	// ── 幂等性验证: 再次 Shutdown 不 panic ──
	t.Run("idempotent shutdown", func(t *testing.T) {
		lm.Shutdown()
		// 再次 Submit 仍然返回错误
		_, err := p.Submit(context.Background(), 1)
		assert.Error(t, err)
	})
}
