package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

var _ karta.Scheduler = NewDelayScheduler()

func TestDelay_ImmediateNoDelay(t *testing.T) {
	s := NewDelayScheduler()
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "immediate", Delay: 0}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "immediate", env.Input)
	s.Done(env)
}

func TestDelay_WithDelay(t *testing.T) {
	s := NewDelayScheduler()
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
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "should wait at least the delay duration")
	assert.Less(t, elapsed, 2*time.Second, "should not take too long")
}

func TestDelay_Shutdown(t *testing.T) {
	s := NewDelayScheduler()
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestDelay_DequeueContextCancel(t *testing.T) {
	s := NewDelayScheduler()
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
