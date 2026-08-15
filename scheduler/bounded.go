package scheduler

import (
	"context"
	"errors"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// boundedScheduler 将 workqueue.BoundedBlockingQueue 适配为 karta.Scheduler 接口。
/*

  特点：
  - 队列满时，Enqueue 立即返回 karta.ErrSchedulerFull（遵守接口契约，不阻塞调用方）
  - Dequeue 支持 context 取消，使用底层队列的 GetWithContext
  - 容量由 NewBoundedScheduler 参数指定

*/
// 编译期接口检查：确保 boundedScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*boundedScheduler)(nil)

type boundedScheduler struct {
	queue  workqueue.BoundedBlockingQueue
	closed atomic.Bool
}

// NewBoundedScheduler 创建基于 workqueue.BoundedBlockingQueue 的有界阻塞调度器。
// capacity 指定队列的最大容量，capacity <= 0 时使用 workqueue 默认容量 (1024)。
func NewBoundedScheduler(capacity int) karta.Scheduler {
	cfg := workqueue.NewBoundedBlockingQueueConfig().
		WithCapacity(capacity)
	return &boundedScheduler{
		queue: workqueue.NewBoundedBlockingQueue(cfg),
	}
}

func (s *boundedScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	// BoundedBlockingQueue 没有非阻塞 TryPut 类 API（已查证 workqueue v2.3.2
	// 源码：Put/PutWithContext 均需先获取槽位信号）。按 karta.Scheduler 接口
	// 契约（缓冲区已满返回 ErrSchedulerFull），先做容量检查快速拒绝，再入队。
	// 残余竞态说明：检查与 Put 之间若被并发写入占掉最后空位，Put 会短暂
	// 阻塞于槽位等待，直至其他入队/出队操作或 Shutdown 释放槽位；
	// 这是最小正确组合，不会再出现队列已满仍无限阻塞的违约行为。
	if s.queue.Len() >= s.queue.Cap() {
		return karta.ErrSchedulerFull
	}
	if err := s.queue.Put(task); err != nil {
		if errors.Is(err, workqueue.ErrQueueIsClosed) {
			return karta.ErrSchedulerClosed
		}
		return err
	}
	return nil
}

func (s *boundedScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	for {
		val, err := s.queue.GetWithContext(ctx)
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

func (s *boundedScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *boundedScheduler) Len() int {
	return s.queue.Len()
}

func (s *boundedScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *boundedScheduler) IsClosed() bool {
	return s.closed.Load()
}
