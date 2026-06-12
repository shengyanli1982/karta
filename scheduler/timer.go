package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// timerScheduler 将 workqueue.TimerQueue 适配为 karta.Scheduler 接口。
/*

  支持三种入队模式：
  - task.Deadline != zero: 使用 PutAt，在绝对时间点入队
  - task.Delay > 0: 使用 PutAfter，在相对延迟后入队
  - 否则: 使用 Put，立即入队

*/
// 编译期接口检查：确保 timerScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*timerScheduler)(nil)

type timerScheduler struct {
	queue  workqueue.TimerQueue
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewTimerScheduler 创建基于 workqueue.TimerQueue 的定时调度器。
func NewTimerScheduler() karta.Scheduler {
	return &timerScheduler{
		queue:  workqueue.NewTimerQueue(workqueue.NewTimerQueueConfig()),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *timerScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	var err error
	if !task.Deadline.IsZero() {
		err = s.queue.PutAt(task, task.Deadline)
	} else if task.Delay > 0 {
		err = s.queue.PutAfter(task, task.Delay)
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

func (s *timerScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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

func (s *timerScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *timerScheduler) Len() int {
	return s.queue.Len()
}

func (s *timerScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *timerScheduler) IsClosed() bool {
	return s.closed.Load()
}
