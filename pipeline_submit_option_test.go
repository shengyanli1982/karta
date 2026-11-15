package karta_test

import (
	"context"
	"testing"
	"time"

	"github.com/shengyanli1982/karta/v2/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// ═══════════════════════════════════════════════════════════════
// P0-1 验收测试 — SubmitOption 透传调度字段（Priority / Delay / Deadline）
// ═══════════════════════════════════════════════════════════════

// TestSubmitOption_FieldsApplied 验证三个 option 直接写入 envelope 对应字段
func TestSubmitOption_FieldsApplied(t *testing.T) {
	deadline := time.Now().Add(time.Second)
	env := &karta.TaskEnvelope{}
	karta.WithPriority(7)(env)
	karta.WithDelay(50 * time.Millisecond)(env)
	karta.WithDeadline(deadline)(env)

	assert.Equal(t, int64(7), env.Priority)
	assert.Equal(t, 50*time.Millisecond, env.Delay)
	assert.True(t, env.Deadline.Equal(deadline))
}

// TestSubmitOption_PriorityScheduler_Ordering 验证 PriorityScheduler 下
// WithPriority 小值任务先出队（Spec P0-1 验收：高优先级先出队）。
//
// 确定性构造：workers=2，两个 gate 任务分别占住两个 worker；随后先提交
// 低优先级（P=10）、后提交高优先级（P=1）——若退化为 FIFO，释放的 worker
// 必先取到低优先级任务；仅释放 worker A（worker B 仍被 gate2 阻塞），
// 出队→执行完全串行，断言首个执行的是高优先级任务。
func TestSubmitOption_PriorityScheduler_Ordering(t *testing.T) {
	const (
		lowInput  = 100 // 低优先级任务（先提交，FIFO 会先出队）
		highInput = 200 // 高优先级任务（后提交，优先级堆应先出队）
	)
	gate1 := make(chan struct{})
	gate2 := make(chan struct{})
	started1 := make(chan struct{})
	started2 := make(chan struct{})
	executed := make(chan int, 2)

	handler := func(ctx context.Context, n int) (int, error) {
		switch n {
		case 1:
			close(started1)
			<-gate1
		case 2:
			close(started2)
			<-gate2
		default:
			executed <- n
		}
		return n, nil
	}
	p := karta.NewPipeline[int, int](handler, scheduler.NewPriorityScheduler(), karta.WithPipelineWorkers(2))
	defer p.Stop()

	ctx := context.Background()
	f1, err := p.Submit(ctx, 1)
	require.NoError(t, err)
	f2, err := p.Submit(ctx, 2)
	require.NoError(t, err)
	<-started1
	<-started2 // 两个 worker 均被占住

	lowF, err := p.Submit(ctx, lowInput, karta.WithPriority(10))
	require.NoError(t, err)
	highF, err := p.Submit(ctx, highInput, karta.WithPriority(1))
	require.NoError(t, err)

	close(gate1) // 仅释放 worker A，下一个出队任务由优先级堆决定

	select {
	case first := <-executed:
		assert.Equal(t, highInput, first, "PriorityScheduler 下小值优先级任务应先出队（FIFO 退化会先出低优先级）")
	case <-time.After(5 * time.Second):
		t.Fatal("等待高优先级任务执行超时")
	}

	close(gate2)
	select {
	case second := <-executed:
		assert.Equal(t, lowInput, second)
	case <-time.After(5 * time.Second):
		t.Fatal("等待低优先级任务执行超时")
	}

	for _, f := range []*karta.Future[int]{f1, f2, lowF, highF} {
		getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res := f.Get(getCtx)
		cancel()
		require.NoError(t, res.Err)
	}
}

// TestSubmitOption_DelayScheduler 验证 DelayScheduler 下 WithDelay 生效：
// 延迟到期前任务不可执行，到期后正常执行（Spec P0-1 验收）
func TestSubmitOption_DelayScheduler(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}
	p := karta.NewPipeline[int, int](handler, scheduler.NewDelayScheduler(), karta.WithPipelineWorkers(2))
	defer p.Stop()

	start := time.Now()
	f, err := p.Submit(context.Background(), 21, karta.WithDelay(150*time.Millisecond))
	require.NoError(t, err)

	// 延迟到期前不可执行
	earlyCtx, cancelEarly := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelEarly()
	early := f.Get(earlyCtx)
	assert.ErrorIs(t, early.Err, context.DeadlineExceeded, "延迟到期前任务不应被执行")

	// 延迟到期后执行
	getCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err)
	assert.Equal(t, 42, res.Value)
	assert.GreaterOrEqual(t, time.Since(start), 140*time.Millisecond,
		"总耗时应不小于 WithDelay 指定的延迟")
}

// TestSubmitOption_TimerScheduler_Deadline 验证 TimerScheduler 下
// WithDeadline 生效：截止时间到达前不触发，到达后执行（Spec P0-1 验收）
func TestSubmitOption_TimerScheduler_Deadline(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) {
		return n + 1, nil
	}
	p := karta.NewPipeline[int, int](handler, scheduler.NewTimerScheduler(), karta.WithPipelineWorkers(2))
	defer p.Stop()

	start := time.Now()
	f, err := p.Submit(context.Background(), 10, karta.WithDeadline(time.Now().Add(150*time.Millisecond)))
	require.NoError(t, err)

	// 截止时间前不可执行
	earlyCtx, cancelEarly := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelEarly()
	early := f.Get(earlyCtx)
	assert.ErrorIs(t, early.Err, context.DeadlineExceeded, "Deadline 到达前任务不应被执行")

	// 截止时间到达后执行
	getCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err)
	assert.Equal(t, 11, res.Value)
	assert.GreaterOrEqual(t, time.Since(start), 140*time.Millisecond,
		"总耗时应不小于 Deadline 相对当前的延迟")
}

// TestSubmitOption_ZeroValue_BackwardCompatible 验证无 option 时行为与
// 现状一致（Spec P0-1 验收：零值兼容）：DelayScheduler 立即执行（Delay=0），
// SubmitWithHandler 携带 option 同样透传
func TestSubmitOption_ZeroValue_BackwardCompatible(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}
	p := karta.NewPipeline[int, int](handler, scheduler.NewDelayScheduler(), karta.WithPipelineWorkers(2))
	defer p.Stop()

	// 无 option：Delay=0 → 立即入队执行，无额外延迟
	start := time.Now()
	f, err := p.Submit(context.Background(), 5)
	require.NoError(t, err)
	getCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err)
	assert.Equal(t, 5, res.Value)
	assert.Less(t, time.Since(start), 100*time.Millisecond, "无 option 时不应引入任何延迟")

	// SubmitWithHandler 携带 option：per-task handler 生效且 option 不影响结果
	custom := karta.Handler[int, int](func(ctx context.Context, n int) (int, error) {
		return n * 100, nil
	})
	f2, err := p.SubmitWithHandler(context.Background(), custom, 3, karta.WithPriority(1))
	require.NoError(t, err)
	getCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	res2 := f2.Get(getCtx2)
	require.NoError(t, res2.Err)
	assert.Equal(t, 300, res2.Value)
}
