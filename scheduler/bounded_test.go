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

func TestBounded_BlockingEnqueue(t *testing.T) {
	// capacity=2，填满后第三次 Enqueue 应阻塞
	s := NewBoundedScheduler(2)
	defer s.Shutdown()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 2}))
	assert.Equal(t, 2, s.Len())

	// 第三次 Enqueue 应该阻塞，在另一个 goroutine 中执行
	done := make(chan error, 1)
	go func() {
		done <- s.Enqueue(&karta.TaskEnvelope{Input: 3})
	}()

	// 等待短暂时间确认 Enqueue 确实在阻塞
	select {
	case <-done:
		t.Fatal("third Enqueue should have blocked")
	case <-time.After(100 * time.Millisecond):
		// 预期行为：Enqueue 在阻塞
	}

	// 消费一个任务以释放空间
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, env.Input)
	s.Done(env)

	// 第三次 Enqueue 现在应该成功
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("third Enqueue should have unblocked after Dequeue")
	}
}

func TestBounded_ShutdownUnblocksEnqueue(t *testing.T) {
	s := NewBoundedScheduler(1)

	// 填满队列
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))

	// 在另一个 goroutine 中阻塞 Enqueue
	done := make(chan error, 1)
	go func() {
		done <- s.Enqueue(&karta.TaskEnvelope{Input: 2})
	}()

	// 确认 Enqueue 在阻塞
	select {
	case <-done:
		t.Fatal("Enqueue should have blocked")
	case <-time.After(100 * time.Millisecond):
	}

	// Shutdown 应唤醒阻塞的 Enqueue
	s.Shutdown()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown should have unblocked Enqueue")
	}
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
