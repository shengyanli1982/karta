package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

var _ karta.Scheduler = NewPriorityScheduler()

func TestPriority_Ordering(t *testing.T) {
	s := NewPriorityScheduler()
	defer s.Shutdown()

	priorities := []int64{50, 10, 30, 5, 20}
	for _, p := range priorities {
		task := &karta.TaskEnvelope{Input: p, Priority: p}
		require.NoError(t, s.Enqueue(task))
	}
	assert.Equal(t, 5, s.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	expectedOrder := []int64{5, 10, 20, 30, 50}
	for _, expected := range expectedOrder {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, env.Input, "priority should be ascending")
		assert.Equal(t, expected, env.Priority)
		s.Done(env)
	}
	assert.Equal(t, 0, s.Len())
}

func TestPriority_SamePriority_FIFO(t *testing.T) {
	s := NewPriorityScheduler()
	defer s.Shutdown()

	for i := 0; i < 5; i++ {
		task := &karta.TaskEnvelope{Input: i, Priority: 10}
		require.NoError(t, s.Enqueue(task))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input, "same priority should maintain FIFO order")
		s.Done(env)
	}
}

func TestPriority_Shutdown(t *testing.T) {
	s := NewPriorityScheduler()
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestPriority_DequeueContextCancel(t *testing.T) {
	s := NewPriorityScheduler()
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
