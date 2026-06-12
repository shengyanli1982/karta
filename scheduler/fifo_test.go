package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

var _ karta.Scheduler = NewFIFOScheduler()

func TestFIFO_EnqueueDequeue(t *testing.T) {
	s := NewFIFOScheduler()
	defer s.Shutdown()

	tasks := make([]*karta.TaskEnvelope, 5)
	for i := 0; i < 5; i++ {
		tasks[i] = &karta.TaskEnvelope{Input: i}
	}

	for _, task := range tasks {
		require.NoError(t, s.Enqueue(task))
	}
	assert.Equal(t, 5, s.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input, "FIFO order should be preserved")
		s.Done(env)
	}
	assert.Equal(t, 0, s.Len())
}

func TestFIFO_DequeueContextCancel(t *testing.T) {
	s := NewFIFOScheduler()
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFIFO_Shutdown(t *testing.T) {
	s := NewFIFOScheduler()
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestFIFO_DequeueAfterShutdown(t *testing.T) {
	s := NewFIFOScheduler()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "before-shutdown"}))

	s.Shutdown()

	ctx := context.Background()
	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

// BenchmarkFIFOScheduler_EnqueueDequeue 测量 FIFO 调度器在并发场景下
// Enqueue + Dequeue 的吞吐（单 goroutine 循环：入队后立即出队）。
func BenchmarkFIFOScheduler_EnqueueDequeue(b *testing.B) {
	s := NewFIFOScheduler()
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
