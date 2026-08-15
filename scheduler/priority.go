package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// priorityScheduler 将 workqueue.PriorityQueue 适配为 karta.Scheduler 接口。
// workqueue PriorityQueue: 数值越小优先级越高（小顶堆）。
type priorityScheduler struct {
	queue  workqueue.PriorityQueue
	mu     sync.Mutex // 保护 queue 的并发访问（workqueue RBTree 非线程安全）
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewPriorityScheduler 创建基于 workqueue.PriorityQueue 的优先级调度器。
func NewPriorityScheduler() karta.Scheduler {
	return &priorityScheduler{
		queue:  workqueue.NewPriorityQueue(workqueue.NewPriorityQueueConfig()),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *priorityScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	s.mu.Lock()
	err := s.queue.PutWithPriority(task, task.Priority)
	s.mu.Unlock()
	if err != nil {
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

func (s *priorityScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for {
		s.mu.Lock()
		val, err := s.queue.Get()
		s.mu.Unlock()
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
		// Go 1.23+ 保证 Reset 返回后不会收到旧定时值，直接重设即可
		timer.Reset(backoff)
	}
}

func (s *priorityScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *priorityScheduler) Len() int {
	return s.queue.Len()
}

func (s *priorityScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.mu.Lock()
		s.queue.Shutdown()
		s.mu.Unlock()
		close(s.doneCh)
	}
}

func (s *priorityScheduler) IsClosed() bool {
	return s.closed.Load()
}
