package karta

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════
// P2-5 验收测试 — SubmitEvery：周期触发薄封装
//
// 核心语义：非阻塞、skip-if-busy（上次 Future 未完成则跳过本次 tick，
// 慢任务不堆积，规避 gocron 式 piled-up executions）；stop()/ctx 取消/
// Pipeline 关闭（ErrPipelineClosed）均使内部 ticker goroutine 退出（无泄漏）；
// 其他提交错误（如调度器满）仅跳过本次，下个 tick 继续。
// 本文件位于 package karta（内部测试），与 pipeline_dedup_test.go 同风格。
// ═══════════════════════════════════════════════════════════════

// TestSubmitEvery_PeriodicExecution 周期执行：短 interval 下 handler 调用计数
// 达到预期下限（留调度容差），且每次提交携带构造时指定的 input
func TestSubmitEvery_PeriodicExecution(t *testing.T) {
	var calls atomic.Int32
	var lastInput atomic.Int64
	handler := func(ctx context.Context, n int) (int, error) {
		lastInput.Store(int64(n))
		calls.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	stop := SubmitEvery(context.Background(), p, 20*time.Millisecond, 42)
	defer stop()

	// 250ms 内约 12 个 tick；下限 5 给 -race 与慢机留足调度容差
	assert.Eventually(t, func() bool { return calls.Load() >= 5 },
		2*time.Second, 5*time.Millisecond, "handler 应被周期性调用")
	assert.Equal(t, int64(42), lastInput.Load(), "每次提交应携带构造时指定的 input")
}

// TestSubmitEvery_SlowHandler_NoPileup 慢任务不堆积（Spec P2-5 验收）：
// handler 阻塞时长（300ms）>> interval（10ms）时，执行期间的 tick 全部被
// skip-if-busy 跳过——调用计数远低于 tick 数，且队列中不积压任务
func TestSubmitEvery_SlowHandler_NoPileup(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-time.After(300 * time.Millisecond): // 阻塞时长 >> interval
		}
		return n, nil
	}
	sched := NewSimpleScheduler(64)
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(2))
	defer p.Stop()

	stop := SubmitEvery(context.Background(), p, 10*time.Millisecond, 1)

	time.Sleep(700 * time.Millisecond) // 约 70 个 tick
	stop()                             // 先冻结提交再统计（stop 幂等）
	close(release)                     // 放行仍在阻塞的 handler，让在途任务收尾

	// 若无 skip 语义，70 个 tick 将提交约 70 个任务并排队堆积；
	// skip-if-busy 下仅约 3 次执行（每 ~310ms 一轮：t≈10/320/630ms），
	// 区间 [2,5] 覆盖慢机调度漂移与 stop 竞态窗口的至多 1 次额外提交
	n := calls.Load()
	assert.GreaterOrEqual(t, n, int32(2), "慢任务完成后应继续在下个 tick 提交")
	assert.LessOrEqual(t, n, int32(5), "慢任务执行期间应跳过 tick，不得堆积提交")
	assert.LessOrEqual(t, sched.Len(), 1, "队列不应积压任务（至多 1 个在途）")
}

// TestSubmitEvery_Stop_HaltsSubmission stop() 后停止提交：计数不再增长
func TestSubmitEvery_Stop_HaltsSubmission(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	stop := SubmitEvery(context.Background(), p, 10*time.Millisecond, 1)

	require.Eventually(t, func() bool { return calls.Load() >= 2 },
		5*time.Second, 5*time.Millisecond, "周期提交应先实际生效")

	stop()
	// stop 为异步唤醒：goroutine 可能正处于 ticker 分支，至多再完成 1 次提交；
	// 等待在途任务落定后取基线，再观察若干 tick 周期确认计数冻结
	time.Sleep(50 * time.Millisecond)
	before := calls.Load()
	time.Sleep(150 * time.Millisecond) // 约 15 个 tick 周期
	assert.Equal(t, before, calls.Load(), "stop() 后调用计数不应再增长")
}

// TestSubmitEvery_ContextCancel_HaltsSubmission ctx 取消后同样停止提交
// （goroutine 经 ctx.Done 分支退出）
func TestSubmitEvery_ContextCancel_HaltsSubmission(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := SubmitEvery(ctx, p, 10*time.Millisecond, 1)
	defer stop()

	require.Eventually(t, func() bool { return calls.Load() >= 2 },
		5*time.Second, 5*time.Millisecond, "周期提交应先实际生效")

	cancel()
	time.Sleep(50 * time.Millisecond) // 等待在途提交落定（同 stop 竞态窗口）
	before := calls.Load()
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, before, calls.Load(), "ctx 取消后调用计数不应再增长")
}

// TestSubmitEvery_NoGoroutineLeak 放大法泄漏断言（与 scheduler/composite_test.go
// 同款手法）：20 轮 SubmitEvery+stop，若 ticker goroutine 泄漏，
// goroutine 数量会随轮次线性增长
func TestSubmitEvery_NoGoroutineLeak(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	before := runtime.NumGoroutine()

	const rounds = 20
	for i := 0; i < rounds; i++ {
		// 长 interval：轮次内 tick 不触发，纯粹验证 goroutine 创建/退出
		stop := SubmitEvery(context.Background(), p, time.Hour, i)
		stop()
	}

	// 若泄漏，rounds 轮至少新增 rounds 个 goroutine；
	// 给运行时瞬时 goroutine（GC 等）留出少量余量
	assert.Eventually(t, func() bool {
		return runtime.NumGoroutine()-before < rounds
	}, 5*time.Second, 10*time.Millisecond, "stop 后 ticker goroutine 应全部退出")
}

// TestSubmitEvery_Stop_Idempotent stop 幂等：多次调用不 panic
// （sync.Once 保护 close(stopCh)；goroutine 已随 ctx 退出后再调用同样安全）
func TestSubmitEvery_Stop_Idempotent(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := SubmitEvery(ctx, p, time.Hour, 1)

	assert.NotPanics(t, func() {
		stop()
		stop()
		stop()
	})

	// ctx 取消使 goroutine 退出后再调 stop，同样不 panic
	cancel()
	time.Sleep(50 * time.Millisecond)
	assert.NotPanics(t, stop)
}

// TestSubmitEvery_InvalidInterval_Panics interval <= 0 时构造即 panic
// （与 NewPipeline 对 nil scheduler 的参数校验风格一致）
func TestSubmitEvery_InvalidInterval_Panics(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineWorkers(2))
	defer p.Stop()

	for _, interval := range []time.Duration{0, -time.Second} {
		assert.PanicsWithValue(t, "karta: interval must be positive", func() {
			SubmitEvery(context.Background(), p, interval, 1)
		}, "interval=%v 应 panic", interval)
	}
}

// TestSubmitEvery_PipelineClosed_GoroutineExits Pipeline 关闭后 Submit 返回
// ErrPipelineClosed，ticker goroutine 自动退出——无需显式调用 stop，无泄漏
func TestSubmitEvery_PipelineClosed_GoroutineExits(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))

	before := runtime.NumGoroutine()
	SubmitEvery(context.Background(), p, 10*time.Millisecond, 1) // 故意不持有 stop

	require.Eventually(t, func() bool { return calls.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "周期提交应先实际生效")

	p.Stop() // 关闭后下一 tick 的 Submit 返回 ErrPipelineClosed → goroutine 退出

	// executor 与 ticker goroutine 均应退出，总数回落到基线以下
	assert.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= before
	}, 5*time.Second, 10*time.Millisecond, "Pipeline 关闭后 ticker goroutine 应自动退出")
}

// flakyEnqueueScheduler 前 failures 次 Enqueue 返回 ErrSchedulerFull，
// 之后正常入队（委托内嵌 SimpleScheduler）；attempts 累计 Enqueue 尝试次数，
// 用于验证 SubmitEvery 对非关闭类提交错误"跳过本次、下个 tick 继续"的语义
type flakyEnqueueScheduler struct {
	*SimpleScheduler
	failures int32 // 构造后只读：前 N 次 Enqueue 失败
	attempts atomic.Int32
}

var _ Scheduler = (*flakyEnqueueScheduler)(nil)

func (s *flakyEnqueueScheduler) Enqueue(task *TaskEnvelope) error {
	if n := s.attempts.Add(1); n <= s.failures {
		return ErrSchedulerFull
	}
	return s.SimpleScheduler.Enqueue(task)
}

// TestSubmitEvery_SubmitError_SkipsAndRetriesNextTick 非关闭类提交错误
// （如调度器满）仅跳过本次：后续 tick 持续重试，调度器恢复后成功提交，
// 周期 goroutine 不会因错误提前退出
func TestSubmitEvery_SubmitError_SkipsAndRetriesNextTick(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n, nil
	}
	sched := &flakyEnqueueScheduler{SimpleScheduler: NewSimpleScheduler(64), failures: 2}
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(2))
	defer p.Stop()

	stop := SubmitEvery(context.Background(), p, 10*time.Millisecond, 1)
	defer stop()

	// 前 2 个 tick 因 ErrSchedulerFull 跳过，第 3 个 tick 成功入队并执行
	require.Eventually(t, func() bool { return calls.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "调度器恢复后应成功提交")
	assert.GreaterOrEqual(t, sched.attempts.Load(), int32(3),
		"提交错误应仅跳过本次，后续 tick 继续尝试")

	// 错误不应导致 goroutine 退出：任务快速完成，后续 tick 继续周期提交
	assert.Eventually(t, func() bool { return calls.Load() >= 3 },
		5*time.Second, 5*time.Millisecond, "非关闭类提交错误不应终止周期提交")
}
