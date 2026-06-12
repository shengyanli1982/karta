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
var _ karta.Scheduler = NewCompositeScheduler()

func TestComposite_EnqueueFirst(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2)
	defer composite.Shutdown()

	task := &karta.TaskEnvelope{Input: "composite-test"}
	require.NoError(t, composite.Enqueue(task))

	// Enqueue 写入第一个 scheduler
	assert.Equal(t, 1, s1.Len())
	assert.Equal(t, 0, s2.Len())
	assert.Equal(t, 1, composite.Len())

	// 从第一个 scheduler 可以取到
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s1.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "composite-test", env.Input)
	s1.Done(env)
}

func TestComposite_DequeueFromLast(t *testing.T) {
	s1 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1)
	defer composite.Shutdown()

	task := &karta.TaskEnvelope{Input: "single-sched"}
	require.NoError(t, composite.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Dequeue 从最后一个 scheduler (也是 s1) 取
	env, err := composite.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "single-sched", env.Input)
	composite.Done(env)
}

func TestComposite_Shutdown(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	s3 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2, s3)

	assert.False(t, composite.IsClosed())

	composite.Shutdown()
	assert.True(t, composite.IsClosed())

	// 所有子 scheduler 都已关闭
	assert.True(t, s1.IsClosed())
	assert.True(t, s2.IsClosed())
	assert.True(t, s3.IsClosed())

	// 幂等
	composite.Shutdown()
	assert.True(t, composite.IsClosed())

	err := composite.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestComposite_Len(t *testing.T) {
	s1 := NewFIFOScheduler()
	s2 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1, s2)
	defer composite.Shutdown()

	require.NoError(t, s1.Enqueue(&karta.TaskEnvelope{Input: 1}))
	require.NoError(t, s1.Enqueue(&karta.TaskEnvelope{Input: 2}))
	require.NoError(t, s2.Enqueue(&karta.TaskEnvelope{Input: 3}))

	assert.Equal(t, 3, composite.Len())
}

func TestComposite_Empty(t *testing.T) {
	composite := NewCompositeScheduler()
	defer composite.Shutdown()

	err := composite.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = composite.Dequeue(ctx)
	assert.Error(t, err)

	assert.Equal(t, 0, composite.Len())
}

func TestComposite_DequeueContextCancel(t *testing.T) {
	s1 := NewFIFOScheduler()
	composite := NewCompositeScheduler(s1)
	defer composite.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := composite.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
