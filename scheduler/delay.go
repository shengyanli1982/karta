package scheduler

import (
	"context"
	"errors"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// delayScheduler 将 workqueue.DelayingQueue 适配为 karta.Scheduler 接口。
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：延迟项到期后由
// 底层 scheduler 搬入内层队列（内层 Put 触发广播唤醒），等待内层即正确语义。
type delayScheduler struct {
	queue    workqueue.DelayingQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
}

// NewDelayScheduler 创建基于 workqueue.DelayingQueue 的延迟调度器。
// TaskEnvelope.Delay > 0 时使用 PutWithDelay，否则立即入队。
func NewDelayScheduler() karta.Scheduler {
	q := workqueue.NewDelayingQueue(workqueue.NewDelayingQueueConfig())
	return &delayScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
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
	return nil
}

// Dequeue 阻塞获取任务：取到值返回；队列关闭返回 ErrSchedulerClosed
// （底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；ctx 完成原样返回 ctx.Err()。
func (s *delayScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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

func (s *delayScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *delayScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *delayScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *delayScheduler) IsClosed() bool {
	return s.closed.Load()
}
