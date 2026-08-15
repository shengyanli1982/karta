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
var _ karta.Scheduler = NewBoundedScheduler(10)

func TestBounded_EnqueueDequeue(t *testing.T) {
	s := NewBoundedScheduler(10)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "bounded-test"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "bounded-test", env.Input)
	s.Done(env)
}

func TestBounded_MultipleTasks(t *testing.T) {
	s := NewBoundedScheduler(10)
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

func TestBounded_DequeueContextCancel(t *testing.T) {
	s := NewBoundedScheduler(10)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBounded_Shutdown(t *testing.T) {
	s := NewBoundedScheduler(10)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 幂等：再次调用不 panic
	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestBounded_DequeueAfterShutdown(t *testing.T) {
	s := NewBoundedScheduler(10)

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "before-shutdown"}))
	s.Shutdown()

	ctx := context.Background()
	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestBounded_FullReturnsErrSchedulerFull(t *testing.T) {
	// capacity=2，填满后 Enqueue 应立即返回 ErrSchedulerFull（不阻塞）
	s := NewBoundedScheduler(2)
	defer s.Shutdown()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 2}))
	assert.Equal(t, 2, s.Len())

	err := s.Enqueue(&karta.TaskEnvelope{Input: 3})
	assert.ErrorIs(t, err, karta.ErrSchedulerFull)
	assert.Equal(t, 2, s.Len())

	// 消费一个任务释放空间后，Enqueue 重新成功
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, env.Input)
	s.Done(env)

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 3}))
	assert.Equal(t, 2, s.Len())
}

func TestBounded_EnqueueAfterShutdown(t *testing.T) {
	s := NewBoundedScheduler(1)

	// 填满队列后关闭
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	s.Shutdown()

	// 关闭优先于满：返回 ErrSchedulerClosed 而非 ErrSchedulerFull
	err := s.Enqueue(&karta.TaskEnvelope{Input: 2})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestBounded_Len(t *testing.T) {
	s := NewBoundedScheduler(10)
	defer s.Shutdown()

	assert.Equal(t, 0, s.Len())
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	assert.Equal(t, 1, s.Len())
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 2}))
	assert.Equal(t, 2, s.Len())
}
