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
