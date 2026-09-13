package scheduler

import (
	"context"
	"errors"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// timerScheduler 将 workqueue.TimerQueue 适配为 karta.Scheduler 接口。
/*

  支持三种入队模式：
  - task.Deadline != zero: 使用 PutAt，在绝对时间点入队
  - task.Delay > 0: 使用 PutAfter，在相对延迟后入队
  - 否则: 使用 Put，立即入队

  Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：定时项到期后由
  底层 scheduler 搬入内层队列（内层 Put 触发广播唤醒），等待内层即正确语义。
*/
// 编译期接口检查：确保 timerScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*timerScheduler)(nil)

type timerScheduler struct {
	queue    workqueue.TimerQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
}

// NewTimerScheduler 创建基于 workqueue.TimerQueue 的定时调度器。
func NewTimerScheduler() karta.Scheduler {
	q := workqueue.NewTimerQueue(workqueue.NewTimerQueueConfig())
	return &timerScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
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
	return nil
}

// Dequeue 阻塞获取任务：取到值返回；队列关闭返回 ErrSchedulerClosed
// （底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；ctx 完成原样返回 ctx.Err()。
func (s *timerScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	for {
		val, err := s.blocking.GetWithContext(ctx)
		if err != nil {
			if errors.Is(err, workqueue.ErrQueueIsClosed) {
				return nil, karta.ErrSchedulerClosed
			}
			return nil, err
		}
		if env, ok := val.(*karta.TaskEnvelope); ok {
			return env, nil
		}
		// 类型不匹配时继续出队（不应在正常使用中出现）
	}
}

func (s *timerScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *timerScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *timerScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *timerScheduler) IsClosed() bool {
	return s.closed.Load()
}
