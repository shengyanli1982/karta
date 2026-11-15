package karta_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shengyanli1982/karta/v2/scheduler"
	"github.com/shengyanli1982/workqueue/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// ═══════════════════════════════════════════════════════════════
// P0-2 验收测试 — WithAutoRetry：executor 自动驱动 Scheduler 级重试 + DLQ
// ═══════════════════════════════════════════════════════════════

// errTransient 模拟 handler 的瞬时故障
var errTransient = errors.New("transient failure")

// deadLetterReader 匹配 NewRetrySchedulerWithDLQ 暴露的 GetDeadLetters
// 扩展方法（不在 karta.Scheduler 接口中，经类型断言访问）
type deadLetterReader interface {
	GetDeadLetters() []*workqueue.DeadLetter
}

// fastRetryPolicy 短延迟指数退避策略，保证测试快速且确定
func fastRetryPolicy(maxRetries int) workqueue.RetryPolicy {
	return workqueue.NewExponentialRetryPolicy(2*time.Millisecond, 10*time.Millisecond, maxRetries)
}

// getWithTimeout 带超时的 Future.Get，超时视为测试失败
func getWithTimeout[T any](t *testing.T, f *karta.Future[T]) karta.Result[T] {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return f.Get(ctx)
}

// TestAutoRetry_SucceedsAfterFailures 验证失败 N 次后成功：
// handler 调用计数 = N+1，Future 最终 Resolve 成功值，
// 中间重试状态对调用方透明（Spec P0-2 验收）
func TestAutoRetry_SucceedsAfterFailures(t *testing.T) {
	const failures = 2
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (string, error) {
		if calls.Add(1) <= failures {
			return "", errTransient
		}
		return "recovered", nil
	}
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(3))
	p := karta.NewPipeline[int, string](handler, sched, karta.WithAutoRetry(true))
	defer p.Stop()

	f, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	res := getWithTimeout(t, f)
	require.NoError(t, res.Err, "重试成功后 Future 应 Resolve 成功值")
	assert.Equal(t, "recovered", res.Value)
	assert.Equal(t, int32(failures+1), calls.Load(), "handler 调用次数应为失败次数+1")
}

// TestAutoRetry_Exhausted_ResolvesOriginalError 验证重试耗尽：
// Future Resolve 为原始 handler 错误（而非底层 ErrRetryExhausted），
// handler 调用次数 = 1 + maxRetries
func TestAutoRetry_Exhausted_ResolvesOriginalError(t *testing.T) {
	const maxRetries = 2
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (string, error) {
		calls.Add(1)
		return "", errTransient
	}
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(maxRetries))
	p := karta.NewPipeline[int, string](handler, sched, karta.WithAutoRetry(true))
	defer p.Stop()

	f, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	res := getWithTimeout(t, f)
	assert.ErrorIs(t, res.Err, errTransient, "耗尽后应 Resolve 原始 handler 错误")
	assert.Equal(t, int32(1+maxRetries), calls.Load(), "handler 调用次数应为 1+maxRetries")
}

// TestAutoRetry_DLQ_ReceivesExhaustedTask 验证 NewRetrySchedulerWithDLQ：
// 重试耗尽后死信自动进入 DLQ，经类型断言 GetDeadLetters 可取到
func TestAutoRetry_DLQ_ReceivesExhaustedTask(t *testing.T) {
	handler := func(ctx context.Context, n int) (string, error) {
		return "", errTransient
	}
	sched := scheduler.NewRetrySchedulerWithDLQ(fastRetryPolicy(1))
	p := karta.NewPipeline[int, string](handler, sched, karta.WithAutoRetry(true))
	defer p.Stop()

	f, err := p.Submit(context.Background(), 42)
	require.NoError(t, err)

	res := getWithTimeout(t, f)
	assert.ErrorIs(t, res.Err, errTransient)

	// 死信通过类型断言访问（与 dlqScheduler 同模式）；
	// 转发在 Retry 返回 ErrRetryExhausted 前同步完成，此时必已入队
	dlq, ok := sched.(deadLetterReader)
	require.True(t, ok, "NewRetrySchedulerWithDLQ 应暴露 GetDeadLetters")
	letters := dlq.GetDeadLetters()
	require.Len(t, letters, 1, "耗尽任务应转发至死信队列")
	assert.Equal(t, "karta-retry", letters[0].SourceQueue)
	assert.Equal(t, errTransient.Error(), letters[0].LastError)
	assert.Equal(t, 2, letters[0].Attempts, "Attempts 应为耗尽时的重试计数（maxRetries+1）")
	// 耗尽任务的 envelope 不归还对象池（Retry 失败后 executor skipPut，
	// 死信独占持有指针）：Payload 字段可稳定检视，正向断言钉住该修复
	// （修复前 Input 会被 putEnvelope 清零并被复用任务覆写，构成数据竞争）
	env, isEnvelope := letters[0].Payload.(*karta.TaskEnvelope)
	require.True(t, isEnvelope)
	assert.Equal(t, 42, env.Input, "死信 Payload 的 Input 应保留提交值（envelope 未归还池）")
}

// TestAutoRetry_DLQ_NilWithoutConstruction 验证普通 NewRetryScheduler
// 未接通死信：GetDeadLetters 返回 nil
func TestAutoRetry_DLQ_NilWithoutConstruction(t *testing.T) {
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(1))
	defer sched.Shutdown()

	dlq, ok := sched.(deadLetterReader)
	require.True(t, ok)
	assert.Nil(t, dlq.GetDeadLetters())
}

// TestAutoRetry_DefaultOff 验证 WithAutoRetry 默认关闭：
// 失败直接 Resolve，handler 只调用 1 次（向后兼容）
func TestAutoRetry_DefaultOff(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (string, error) {
		calls.Add(1)
		return "", errTransient
	}
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(3))
	p := karta.NewPipeline[int, string](handler, sched) // 不开启 WithAutoRetry
	defer p.Stop()

	f, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	res := getWithTimeout(t, f)
	assert.ErrorIs(t, res.Err, errTransient)
	assert.Equal(t, int32(1), calls.Load(), "默认关闭时不应触发重试")
}

// TestAutoRetry_NonRetryerScheduler_FallsBack 验证非 Retryer 调度器
// （SimpleScheduler）+ WithAutoRetry(true)：断言失败静默走默认路径，不 panic
func TestAutoRetry_NonRetryerScheduler_FallsBack(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (string, error) {
		calls.Add(1)
		return "", errTransient
	}
	p := karta.NewPipeline[int, string](handler, karta.NewSimpleScheduler(16), karta.WithAutoRetry(true))
	defer p.Stop()

	f, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)

	res := getWithTimeout(t, f)
	assert.ErrorIs(t, res.Err, errTransient)
	assert.Equal(t, int32(1), calls.Load(), "非 Retryer 调度器应直接走默认完成路径")
}

// TestAutoRetry_NoPendingResidue_EnvelopeNotPolluted 验证重试路径无泄漏：
// 耗尽任务的 envelope 在 Retry 失败后不归还池（skipPut，见 pipeline.go 重试
// 不变量），后续任务不受遗留重试计数影响（重试 key 为指针地址，
// Enqueue 时 Forget 双重防护）；全部 Future 正常 Resolve；Stop 限时返回
// （pending 无残留、wg 记账正确、无卡死 goroutine）
func TestAutoRetry_NoPendingResidue_EnvelopeNotPolluted(t *testing.T) {
	var failPhase atomic.Bool
	failPhase.Store(true)
	handler := func(ctx context.Context, n int) (int, error) {
		if failPhase.Load() {
			return 0, errTransient
		}
		return n * 10, nil
	}
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(1))
	p := karta.NewPipeline[int, int](handler, sched, karta.WithAutoRetry(true))

	// 耗尽路径：envelope 走 Resolve + Done，不归还池（Retry 失败 skipPut）
	f1, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)
	res1 := getWithTimeout(t, f1)
	assert.ErrorIs(t, res1.Err, errTransient)

	// 后续任务（池取 envelope）必须全部正常完成且结果正确
	failPhase.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		f, err := p.Submit(ctx, i)
		require.NoError(t, err)
		res := f.Get(ctx)
		require.NoError(t, res.Err, "第 %d 个后续任务应正常完成", i)
		assert.Equal(t, i*10, res.Value)
	}

	// Stop 限时返回：pending 无残留、executor wg 记账正确
	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop 阻塞：pending 残留或 goroutine 泄漏")
	}
}

// TestAutoRetry_RetrySuccessThenExhaust_MixedSequence 混合序列验证：
// 重试成功任务与耗尽任务交错提交，各自 Future 独立正确 Resolve，
// 重试计数互不串扰（同一 envelope 指针池化复用时 Forget/claim-once 生效）
func TestAutoRetry_RetrySuccessThenExhaust_MixedSequence(t *testing.T) {
	var failAll atomic.Bool
	handler := func(ctx context.Context, n int) (int, error) {
		if failAll.Load() {
			return 0, errTransient
		}
		return n, nil
	}
	sched := scheduler.NewRetryScheduler(fastRetryPolicy(2))
	p := karta.NewPipeline[int, int](handler, sched, karta.WithAutoRetry(true))
	defer p.Stop()

	// 先耗尽一个任务
	failAll.Store(true)
	fExhaust, err := p.Submit(context.Background(), 1)
	require.NoError(t, err)
	resExhaust := getWithTimeout(t, fExhaust)
	assert.ErrorIs(t, resExhaust.Err, errTransient)

	// 再提交正常任务：不得继承失败计数（耗尽任务的 envelope 不归还池，
	// 其余池化指针经 Enqueue 时 Forget 防护）
	failAll.Store(false)
	for i := 0; i < 10; i++ {
		f, err := p.Submit(context.Background(), i)
		require.NoError(t, err)
		res := getWithTimeout(t, f)
		require.NoError(t, res.Err, "正常任务不应被遗留重试计数影响")
		assert.Equal(t, i, res.Value)
	}
}
