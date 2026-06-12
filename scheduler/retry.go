package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口检查：确保 retryScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*retryScheduler)(nil)

// retryScheduler 将 workqueue.RetryQueue 适配为 karta.Scheduler 接口。
//
// Done 标记任务成功完成并清除重试计数。
// 如需重试，通过类型断言访问 Retry 方法：
//
//	rs := sched.(*retryScheduler)  // 或使用接口断言
//	rs.Retry(task, reason)
type retryScheduler struct {
	queue  workqueue.RetryQueue
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewRetryScheduler 创建基于 workqueue.RetryQueue 的重试调度器。
// policy 为重试策略，nil 时使用 workqueue 默认的指数退避策略。
func NewRetryScheduler(policy workqueue.RetryPolicy) karta.Scheduler {
	cfg := workqueue.NewRetryQueueConfig().
		WithKeyFunc(retryTaskKeyFunc)
	if policy != nil {
		cfg = cfg.WithPolicy(policy)
	}
	return &retryScheduler{
		queue:  workqueue.NewRetryQueue(cfg),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *retryScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	if err := s.queue.Put(task); err != nil {
		if errors.Is(err, workqueue.ErrQueueIsClosed) {
			return karta.ErrSchedulerClosed
		}
		// double-check: 其他错误也可能由并发 Shutdown 导致
		if s.closed.Load() {
			return karta.ErrSchedulerClosed
		}
		return err
	}
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *retryScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer stopAndDrainTimer(timer)

	for {
		val, err := s.queue.Get()
		if err == nil {
			if env, ok := val.(*karta.TaskEnvelope); ok {
				return env, nil
			}
			continue
		}
		if s.closed.Load() {
			return nil, karta.ErrSchedulerClosed
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.notify:
			backoff = minBackoff
		case <-s.doneCh:
			return nil, karta.ErrSchedulerClosed
		case <-timer.C:
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		stopAndDrainTimer(timer)
		timer.Reset(backoff)
	}
}

// Done 标记任务成功完成，清除重试计数。
func (s *retryScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Forget(task)
	s.queue.Done(task)
}

func (s *retryScheduler) Len() int {
	return s.queue.Len()
}

func (s *retryScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *retryScheduler) IsClosed() bool {
	return s.closed.Load()
}

// Retry 将任务标记为失败并按策略重新入队。
// 此方法不在 karta.Scheduler 接口中，需通过类型断言调用：
//
//	if rs, ok := sched.(interface {
//		Retry(task *karta.TaskEnvelope, reason error) error
//	}); ok {
//	    rs.Retry(task, reason)
//	}
func (s *retryScheduler) Retry(task *karta.TaskEnvelope, reason error) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	return s.queue.Retry(task, reason)
}

// NumRequeues 返回任务已被重试的次数。
func (s *retryScheduler) NumRequeues(task *karta.TaskEnvelope) int {
	return s.queue.NumRequeues(task)
}

// retryTaskKeyFunc 使用指针地址作为重试 key，确保同一 *TaskEnvelope 的 key 稳定。
var retryTaskKeyFunc workqueue.RetryKeyFunc = func(value interface{}) string {
	return fmt.Sprintf("%p", value)
}
