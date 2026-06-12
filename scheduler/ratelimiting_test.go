package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewRateLimitingScheduler(nil)

func TestRateLimiting_EnqueueDequeue(t *testing.T) {
	s := NewRateLimitingScheduler(nil)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "rate-test"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "rate-test", env.Input)
	s.Done(env)
}

func TestRateLimiting_MultipleTasks(t *testing.T) {
	s := NewRateLimitingScheduler(nil)
	defer s.Shutdown()

	for i := 0; i < 5; i++ {
		require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: i}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input)
		s.Done(env)
	}
}

func TestRateLimiting_WithLimiter(t *testing.T) {
	// 使用高速率限流器，保证测试不会因为限流而超时
	limiter := rate.NewLimiter(rate.Every(time.Millisecond), 100)
	s := NewRateLimitingScheduler(limiter)
	defer s.Shutdown()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "limited"}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "limited", env.Input)
	s.Done(env)
}

func TestRateLimiting_DequeueContextCancel(t *testing.T) {
	s := NewRateLimitingScheduler(nil)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRateLimiting_Shutdown(t *testing.T) {
	s := NewRateLimitingScheduler(nil)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 幂等：再次调用不 panic
	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestRateLimiting_DequeueAfterShutdown(t *testing.T) {
	s := NewRateLimitingScheduler(nil)

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "before-shutdown"}))
	s.Shutdown()

	ctx := context.Background()
	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestRateLimiting_Len(t *testing.T) {
	s := NewRateLimitingScheduler(nil)
	defer s.Shutdown()

	assert.Equal(t, 0, s.Len())
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 1}))
	assert.Equal(t, 1, s.Len())
	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: 2}))
	assert.Equal(t, 2, s.Len())
}
