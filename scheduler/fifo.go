package scheduler

import (
	"context"
	"errors"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// fifoScheduler 将 workqueue.Queue 适配为 karta.Scheduler 接口。
//
// Dequeue 基于 workqueue v2.3.4+ 的可选接口 BlockingGetQueue.GetWithContext
// 阻塞消费：阻塞直到「取到值 / ctx 完成 / 队列关闭」三者之一，永不返回
// ErrQueueIsEmpty。入队唤醒（Put 触发广播）与关闭唤醒（Shutdown 关闭
// closedCh 唤醒全部等待者）均由底层队列保证，适配器无需自建通知设施。
type fifoScheduler struct {
	queue workqueue.Queue
	// blocking 在构造时一次性断言（workqueue v2.3.4+ 全部队列类型均实现），
	// 失败即 panic，属 fail-fast：Dequeue 热路径不再重复断言。
	blocking workqueue.BlockingGetQueue
	closed   atomic.Bool
}

// NewFIFOScheduler 创建基于 workqueue.Queue 的 FIFO 调度器。
func NewFIFOScheduler() karta.Scheduler {
	q := workqueue.NewQueue(workqueue.NewQueueConfig())
	return &fifoScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
	}
}

func (s *fifoScheduler) Enqueue(task *karta.TaskEnvelope) error {
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
	return nil
}

// Dequeue 阻塞获取任务：取到值返回；队列关闭返回 ErrSchedulerClosed
// （底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；ctx 完成原样返回 ctx.Err()。
func (s *fifoScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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

func (s *fifoScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *fifoScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *fifoScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *fifoScheduler) IsClosed() bool {
	return s.closed.Load()
}
