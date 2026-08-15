package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewLeaseScheduler(5 * time.Second)

func TestLease_Basic(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-basic"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-basic", env.Input)
	s.Done(env)
}

func TestLease_AckAfterDone(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-ack"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-ack", env.Input)

	// Done 应成功（内部 Ack）
	s.Done(env)

	// 再次调用 Done 不应 panic（leaseID 已不存在，静默忽略）
	s.Done(env)
}

func TestLease_Shutdown(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestLease_DequeueContextCancel(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLease_MultipleTasks(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	for i := 0; i < 5; i++ {
		require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: i}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input)
		s.Done(env)
	}
}

// TestLease_RedeliveryOwnership 验证租约过期重投递的所有权契约：
// 第一个消费者持有任务但不 Done，租约过期后任务被重新投递，
// 第二个消费者拿到的必须是不同的 *TaskEnvelope（浅拷贝），
// 且两者的 Done 互不干扰。
func TestLease_RedeliveryOwnership(t *testing.T) {
	s := NewLeaseScheduler(20 * time.Millisecond)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-redeliver"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 消费者 A 取走任务但不 Done，等待租约过期触发重投递
	envA, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-redeliver", envA.Input)

	time.Sleep(30 * time.Millisecond)

	// 消费者 B 拿到重投递的任务
	envB, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-redeliver", envB.Input)

	// 拷贝交付：两个消费者不得持有同一指针
	assert.NotSame(t, envA, envB)

	// A、B 各自安全完成：A 的租约已过期（Ack 失败被静默忽略），
	// B 的 Done 通过拷贝找回 leaseID，正确 Ack 底层对原对象的租约
	s.Done(envA)
	s.Done(envB)

	// B 已完成确认：任务不应再次被投递出来
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()
	envC, err := s.Dequeue(shortCtx)
	assert.Nil(t, envC)
	assert.Error(t, err)
}

// TestLease_DoneCopyAcksUnderlyingLease 验证正常路径下 Done(拷贝)
// 能正确 Ack 底层租约：完成确认后的任务不会被重投递。
func TestLease_DoneCopyAcksUnderlyingLease(t *testing.T) {
	s := NewLeaseScheduler(20 * time.Millisecond)
	defer s.Shutdown()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "ack-check"}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	s.Done(env)

	// 等待超过租约时长：若 Ack 未生效，任务会被当作过期租约重新投递
	idleCtx, idleCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer idleCancel()
	env2, err := s.Dequeue(idleCtx)
	assert.Nil(t, env2)
	assert.Error(t, err)
}

// BenchmarkLeaseScheduler_EnqueueDequeue 测量 Lease 调度器在并发场景下
// Enqueue + Dequeue 的吞吐（单 goroutine 循环：入队后立即出队）。
func BenchmarkLeaseScheduler_EnqueueDequeue(b *testing.B) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		env := &karta.TaskEnvelope{Input: 1}
		for pb.Next() {
			_ = s.Enqueue(env)
			_, _ = s.Dequeue(context.Background())
		}
	})
}
