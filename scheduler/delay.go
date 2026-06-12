package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// delayScheduler 将 workqueue.DelayingQueue 适配为 karta.Scheduler 接口。
type delayScheduler struct {
	queue  workqueue.DelayingQueue
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewDelayScheduler 创建基于 workqueue.DelayingQueue 的延迟调度器。
// TaskEnvelope.Delay > 0 时使用 PutWithDelay，否则立即入队。
func NewDelayScheduler() karta.Scheduler {
	return &delayScheduler{
		queue:  workqueue.NewDelayingQueue(workqueue.NewDelayingQueueConfig()),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *delayScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	var err error
	if task.Delay > 0 {
		err = s.queue.PutWithDelay(task, task.Delay.Milliseconds())
	} else {
		err = s.queue.Put(task)
	}
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

func (s *delayScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()

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

func (s *delayScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *delayScheduler) Len() int {
	return s.queue.Len()
}

func (s *delayScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *delayScheduler) IsClosed() bool {
	return s.closed.Load()
}
