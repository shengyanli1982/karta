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
  - 队列满时，Enqueue 会阻塞直到有空间或调度器关闭
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
	// 使用 context.Background() 使 Enqueue 可被底层队列 Shutdown 唤醒
	if err := s.queue.PutWithContext(context.Background(), task); err != nil {
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
