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
var _ karta.Scheduler = NewTimerScheduler()

func TestTimer_Immediate(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "immediate"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "immediate", env.Input)
	s.Done(env)
}

func TestTimer_PutAfter(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "delayed", Delay: 100 * time.Millisecond}
	require.NoError(t, s.Enqueue(task))

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "delayed", env.Input)

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond, "should wait at least the delay duration")
	assert.Less(t, elapsed, 2*time.Second, "should not take too long")
}

func TestTimer_PutAt(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	at := time.Now().Add(100 * time.Millisecond)
	task := &karta.TaskEnvelope{Input: "scheduled", Deadline: at}
	require.NoError(t, s.Enqueue(task))

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", env.Input)

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond, "should wait until the deadline")
	assert.Less(t, elapsed, 2*time.Second, "should not take too long")
}

func TestTimer_DeadlinePriorityOverDelay(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	// 同时设置 Deadline 和 Delay，Deadline 应优先
	at := time.Now().Add(150 * time.Millisecond)
	task := &karta.TaskEnvelope{
		Input:    "deadline-wins",
		Deadline: at,
		Delay:    5 * time.Second, // 如果被使用会超时
	}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "deadline-wins", env.Input)
}

func TestTimer_DequeueContextCancel(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTimer_Shutdown(t *testing.T) {
	s := NewTimerScheduler()
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 幂等：再次调用不 panic
	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestTimer_DequeueAfterShutdown(t *testing.T) {
	s := NewTimerScheduler()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "before-shutdown"}))
	s.Shutdown()

	ctx := context.Background()
	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestTimer_Len(t *testing.T) {
	s := NewTimerScheduler()
	defer s.Shutdown()

	assert.Equal(t, 0, s.Len())
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	assert.Equal(t, 1, s.Len())
}
