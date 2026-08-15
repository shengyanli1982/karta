package scheduler

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewCompositeScheduler()

func TestComposite_TwoStageFlowThrough(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2)
	defer composite.Shutdown()

	task := &karta.TaskEnvelope{Input: "composite-test"}
	require.NoError(t, composite.Enqueue(task))

	// Enqueue 写入入口级，任务总数收敛为 1（位置取决于搬运泵进度）
	assert.Eventually(t, func() bool {
		return composite.Len() == 1
	}, time.Second, 5*time.Millisecond)

	// 搬运泵应把任务从 s1 迁移到 s2，最终可从出口级取出
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := composite.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "composite-test", env.Input)
	composite.Done(env)

	assert.Eventually(t, func() bool {
		return composite.Len() == 0
	}, time.Second, 5*time.Millisecond)
}

func TestComposite_ThreeStageFlowThrough(t *testing.T) {
	composite := NewCompositeScheduler(NewFIFOScheduler(), NewFIFOScheduler(), NewFIFOScheduler())
	defer composite.Shutdown()

	for i := 0; i < 3; i++ {
		require.NoError(t, composite.Enqueue(&karta.TaskEnvelope{Input: i}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 三级链路（两级搬运泵）下任务按序到达出口
	for i := 0; i < 3; i++ {
		env, err := composite.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input)
		composite.Done(env)
	}
	assert.Eventually(t, func() bool {
		return composite.Len() == 0
	}, time.Second, 5*time.Millisecond)
}

func TestComposite_PumpRetriesFullStage(t *testing.T) {
	// 中间级容量极小：泵在下一级满时应退避重试而非丢弃任务
	s1 := NewFIFOScheduler()
	s2 := NewBoundedScheduler(1)
	composite := NewCompositeScheduler(s1, s2)
	defer composite.Shutdown()

	require.NoError(t, composite.Enqueue(&karta.TaskEnvelope{Input: 1}))
	require.NoError(t, composite.Enqueue(&karta.TaskEnvelope{Input: 2}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 两个任务都应最终穿过容量为 1 的中间级，无一丢失
	for i := 1; i <= 2; i++ {
		env, err := composite.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input)
		composite.Done(env)
	}
}

func TestComposite_DequeueFromLast(t *testing.T) {
	s1 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1)
	defer composite.Shutdown()

	task := &karta.TaskEnvelope{Input: "single-sched"}
	require.NoError(t, composite.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Dequeue 从最后一个 scheduler (也是 s1) 取
	env, err := composite.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "single-sched", env.Input)
	composite.Done(env)
}

func TestComposite_Shutdown(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	s3 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2, s3)

	assert.False(t, composite.IsClosed())

	composite.Shutdown()
	assert.True(t, composite.IsClosed())

	// 所有子 scheduler 都已关闭
	assert.True(t, s1.IsClosed())
	assert.True(t, s2.IsClosed())
	assert.True(t, s3.IsClosed())

	// 幂等
	composite.Shutdown()
	assert.True(t, composite.IsClosed())

	err := composite.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

// TestComposite_ShutdownStopsPumps 用放大法验证搬运泵无泄漏：
//
//	repeatedly 创建带 2 个泵的组合调度器再关闭，若泵泄漏，
//
// goroutine 数量会随轮次线性增长。Shutdown 内部已 wg.Wait()
// 等待泵退出，此处提供可观察的回归证据。
func TestComposite_ShutdownStopsPumps(t *testing.T) {
	before := runtime.NumGoroutine()

	const rounds = 20
	for i := 0; i < rounds; i++ {
		c := NewCompositeScheduler(NewFIFOScheduler(), NewFIFOScheduler(), NewFIFOScheduler())
		require.NoError(t, c.Enqueue(&karta.TaskEnvelope{Input: i}))
		c.Shutdown()
	}

	// 若泵泄漏，rounds 轮至少新增 2*rounds 个 goroutine；
	// 给运行时瞬时 goroutine（GC 等）留出少量余量。
	after := runtime.NumGoroutine()
	assert.Less(t, after-before, rounds)
}

func TestComposite_DequeueAfterShutdown(t *testing.T) {
	composite := NewCompositeScheduler(NewFIFOScheduler(), NewFIFOScheduler())
	require.NoError(t, composite.Enqueue(&karta.TaskEnvelope{Input: "pending"}))

	composite.Shutdown()

	// Shutdown 后出口级已关闭：Dequeue 立即返回错误，不阻塞、不死锁
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	env, err := composite.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestComposite_Len(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2)
	defer composite.Shutdown()

	require.NoError(t, s1.Enqueue(&karta.TaskEnvelope{Input: 1}))
	require.NoError(t, s1.Enqueue(&karta.TaskEnvelope{Input: 2}))
	require.NoError(t, s2.Enqueue(&karta.TaskEnvelope{Input: 3}))

	// Len 为最终一致快照：任务跨级迁移的瞬间可能偏差 1，静置后收敛为 3
	assert.Eventually(t, func() bool {
		return composite.Len() == 3
	}, time.Second, 5*time.Millisecond)
}

func TestComposite_Empty(t *testing.T) {
	composite := NewCompositeScheduler()
	defer composite.Shutdown()

	err := composite.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = composite.Dequeue(ctx)
	assert.Error(t, err)

	assert.Equal(t, 0, composite.Len())
}

func TestComposite_DequeueContextCancel(t *testing.T) {
	s1 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1)
	defer composite.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := composite.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
