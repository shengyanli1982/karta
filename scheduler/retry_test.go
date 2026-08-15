package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewRetryScheduler(nil)

func TestRetry_Basic(t *testing.T) {
	policy := workqueue.NewExponentialRetryPolicy(10*time.Millisecond, 100*time.Millisecond, 3)
	s := NewRetryScheduler(policy)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "retry-basic"}
	require.NoError(t, s.Enqueue(task))
	assert.Equal(t, 1, s.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "retry-basic", env.Input)
	s.Done(env)
	assert.Equal(t, 0, s.Len())
}

func TestRetry_Shutdown(t *testing.T) {
	s := NewRetryScheduler(nil)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 幂等
	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestRetry_DequeueContextCancel(t *testing.T) {
	s := NewRetryScheduler(nil)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRetry_RetryAndRequeue(t *testing.T) {
	policy := workqueue.NewExponentialRetryPolicy(10*time.Millisecond, 50*time.Millisecond, 3)
	s := NewRetryScheduler(policy)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "retry-me"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 第一次出队
	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "retry-me", env.Input)

	// 通过类型断言调用 Retry 方法
	type retryable interface {
		Retry(task *karta.TaskEnvelope, reason error) error
		NumRequeues(task *karta.TaskEnvelope) int
	}
	rs, ok := s.(retryable)
	require.True(t, ok, "scheduler should expose Retry method via type assertion")

	// 标记需要重试
	reason := errors.New("transient error")
	require.NoError(t, rs.Retry(env, reason))

	// 第二次出队（重试后）
	env2, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "retry-me", env2.Input)

	// 确认重试次数
	assert.GreaterOrEqual(t, rs.NumRequeues(env2), 1)

	// 成功完成
	s.Done(env2)
}

// TestRetry_ReusePointerResetsCount 验证指针复用场景下的计数清零：
// 根包 TaskEnvelope 池会把同一指针用于新任务，旧任务遗留的重试计数
// 不得串到新任务。同一指针再次 Enqueue 时计数必须从零开始。
func TestRetry_ReusePointerResetsCount(t *testing.T) {
	policy := workqueue.NewExponentialRetryPolicy(10*time.Millisecond, 50*time.Millisecond, -1)
	s := NewRetryScheduler(policy)
	defer s.Shutdown()

	type retryable interface {
		Retry(task *karta.TaskEnvelope, reason error) error
		NumRequeues(task *karta.TaskEnvelope) int
	}
	rs, ok := s.(retryable)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	task := &karta.TaskEnvelope{Input: "first-lifetime"}
	require.NoError(t, s.Enqueue(task))

	// 第一个任务生命周期：出队并重试一次，累积计数
	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	require.NoError(t, rs.Retry(env, errors.New("transient")))
	assert.Equal(t, 1, rs.NumRequeues(task))

	// 出队重试后的任务但不 Done，模拟任务未正常终结、指针被归还池复用
	env2, err := s.Dequeue(ctx)
	require.NoError(t, err)
	require.Equal(t, task, env2)

	// 同一指针承载新任务：再次入队必须清除遗留计数
	task.Input = "second-lifetime"
	require.NoError(t, s.Enqueue(task))
	assert.Equal(t, 0, rs.NumRequeues(task))

	// 清理在途任务，避免影响后续断言
	s.Done(env2)
}

func TestRetry_MaxRetries_Respected(t *testing.T) {
	// maxRetries=1: 只允许重试 1 次
	policy := workqueue.NewExponentialRetryPolicy(10*time.Millisecond, 50*time.Millisecond, 1)
	s := NewRetryScheduler(policy)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "limited-retry"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type retryable interface {
		Retry(task *karta.TaskEnvelope, reason error) error
	}
	rs := s.(retryable)
	reason := errors.New("fail")

	// 第 1 次出队 + 重试
	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.NoError(t, rs.Retry(env, reason))

	// 第 2 次出队（第 1 次重试）+ 再次重试 → 应返回 ErrRetryExhausted
	env2, err := s.Dequeue(ctx)
	require.NoError(t, err)
	err = rs.Retry(env2, reason)
	assert.ErrorIs(t, err, workqueue.ErrRetryExhausted)

	// 任务仍然可以 Done
	s.Done(env2)
}
