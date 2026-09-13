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

// BenchmarkPriorityScheduler_EnqueueDequeue 测量优先级调度器在并发场景下
// Enqueue + Dequeue 的吞吐（单 goroutine 循环：入队后立即出队）。
//
// 卫生约束（与 fifo/lease bench 一致）：每迭代新建 envelope（跨迭代共享
// 指针违反"不同任务不同信封"的真实使用模式，且底层簿记按值追踪时会把
// 迭代混为同一任务）；出队后必须 Done，满足 Queue 消费契约（Get 成功后
// 应 Done），保持底层在途簿记收支平衡。
func BenchmarkPriorityScheduler_EnqueueDequeue(b *testing.B) {
	s := NewPriorityScheduler()
	defer s.Shutdown()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			env := &karta.TaskEnvelope{Input: 1}
			if err := s.Enqueue(env); err != nil {
				continue
			}
			got, err := s.Dequeue(ctx)
			if err == nil {
				s.Done(got)
			}
		}
	})
}
