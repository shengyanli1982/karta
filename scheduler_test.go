package karta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleScheduler_EnqueueDequeue_FIFO(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 入队 5 个任务，携带序号作为输入
	const taskCount = 5
	for i := 0; i < taskCount; i++ {
		err := s.Enqueue(&TaskEnvelope{Input: i, CreatedAt: time.Now()})
		require.NoError(t, err)
	}

	// FIFO 顺序出队并验证
	ctx := context.Background()
	for i := 0; i < taskCount; i++ {
		task, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, task.Input, "第 %d 个任务应为 %d", i, i)
	}
}

func TestSimpleScheduler_Dequeue_ContextCancel(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 空队列上 Dequeue，50ms 超时后应返回 context.DeadlineExceeded
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := s.Dequeue(ctx)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSimpleScheduler_Len(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 初始长度应为 0
	assert.Equal(t, 0, s.Len())

	// 入队 2 个任务
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: "a"}))
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: "b"}))
	assert.Equal(t, 2, s.Len())

	// 出队 1 个，长度应减 1
	ctx := context.Background()
	task, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "a", task.Input)
	assert.Equal(t, 1, s.Len())

	// 出队第 2 个
	task, err = s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "b", task.Input)
	assert.Equal(t, 0, s.Len())
}

func TestSimpleScheduler_Shutdown(t *testing.T) {
	s := NewSimpleScheduler(64)

	// 初始未关闭
	assert.False(t, s.IsClosed())

	// Shutdown 后 IsClosed 为 true
	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 再次 Shutdown 应幂等，不 panic
	s.Shutdown()
	assert.True(t, s.IsClosed())

	// Shutdown 后 Enqueue 应返回 ErrSchedulerClosed
	err := s.Enqueue(&TaskEnvelope{Input: "should fail"})
	assert.ErrorIs(t, err, ErrSchedulerClosed)
}

func TestSimpleScheduler_ImplementsInterface(t *testing.T) {
	// 编译期检查：NewSimpleScheduler 返回的类型满足 Scheduler 接口
	var s Scheduler = NewSimpleScheduler(64)
	defer s.Shutdown()
	assert.NotNil(t, s)
}
