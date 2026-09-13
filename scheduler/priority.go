package scheduler

import (
	"context"
	"errors"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// priorityScheduler 将 workqueue.PriorityQueue 适配为 karta.Scheduler 接口。
// workqueue PriorityQueue: 数值越小优先级越高（小顶堆）。
//
// 无需外部互斥锁：workqueue v2.3.4 PriorityQueue 内部全程持锁
// （PutWithPriority/Get/Done/Len/Shutdown 均在底层 queueImpl.lock 临界区内），
// 并发安全由库自身保证。
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：堆即内层队列的
// 存储本身，pop/等待者注册/广播唤醒全部复用内层机制。
type priorityScheduler struct {
	queue    workqueue.PriorityQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
}

// NewPriorityScheduler 创建基于 workqueue.PriorityQueue 的优先级调度器。
func NewPriorityScheduler() karta.Scheduler {
	q := workqueue.NewPriorityQueue(workqueue.NewPriorityQueueConfig())
	return &priorityScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
	}
}

func (s *priorityScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	if err := s.queue.PutWithPriority(task, task.Priority); err != nil {
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
func (s *priorityScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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

func (s *priorityScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *priorityScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *priorityScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *priorityScheduler) IsClosed() bool {
	return s.closed.Load()
}
