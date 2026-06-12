package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewDLQScheduler(3)

func TestDLQ_Basic(t *testing.T) {
	s := NewDLQScheduler(5)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "dlq-basic"}
	require.NoError(t, s.Enqueue(task))
	assert.Equal(t, 1, s.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dlq-basic", env.Input)
	s.Done(env)
}

func TestDLQ_Shutdown(t *testing.T) {
	s := NewDLQScheduler(3)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestDLQ_DequeueContextCancel(t *testing.T) {
	s := NewDLQScheduler(3)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDLQ_GetDeadLetters(t *testing.T) {
	s := NewDLQScheduler(3)
	defer s.Shutdown()

	// 入队 3 个任务
	for i := 0; i < 3; i++ {
		require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: i}))
	}

	// 通过类型断言访问 GetDeadLetters
	type dlqAccessor interface {
		GetDeadLetters() []*workqueue.DeadLetter
	}
	accessor, ok := s.(dlqAccessor)
	require.True(t, ok, "scheduler should expose GetDeadLetters via type assertion")

	letters := accessor.GetDeadLetters()
	assert.Len(t, letters, 3)

	// 验证每个 DeadLetter 的 Payload 是 *TaskEnvelope
	for _, letter := range letters {
		assert.NotNil(t, letter.Payload)
		_, ok := letter.Payload.(*karta.TaskEnvelope)
		assert.True(t, ok, "DeadLetter.Payload should be *TaskEnvelope")
		assert.NotEmpty(t, letter.ID, "DeadLetter should have an ID")
	}
}

func TestDLQ_MultipleTasksFIFO(t *testing.T) {
	s := NewDLQScheduler(10)
	defer s.Shutdown()

	for i := 0; i < 5; i++ {
		require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: i}))
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
}
