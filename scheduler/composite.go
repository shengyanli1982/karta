package scheduler

import (
	"context"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口检查：确保 compositeScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*compositeScheduler)(nil)

// compositeScheduler 将多个 karta.Scheduler 组合为单一调度器。
//
// 入队写入第一个 scheduler（入口），出队从最后一个 scheduler 取（出口），
// 适用于需要在处理链路中经过多个调度阶段的场景。
type compositeScheduler struct {
	schedulers []karta.Scheduler
	closed     atomic.Bool
}

// NewCompositeScheduler 创建组合调度器。
// schedulers 按顺序组成处理链路：
//   - Enqueue 写入 schedulers[0]
//   - Dequeue 从 schedulers[len-1] 取出
//   - Shutdown 依次关闭所有 schedulers
//   - Len 返回所有 schedulers 的 Len 之和
func NewCompositeScheduler(schedulers ...karta.Scheduler) karta.Scheduler {
	return &compositeScheduler{
		schedulers: schedulers,
	}
}

func (s *compositeScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	if len(s.schedulers) == 0 {
		return karta.ErrSchedulerClosed
	}
	return s.schedulers[0].Enqueue(task)
}

func (s *compositeScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	if len(s.schedulers) == 0 {
		return nil, karta.ErrSchedulerClosed
	}
	return s.schedulers[len(s.schedulers)-1].Dequeue(ctx)
}

func (s *compositeScheduler) Done(task *karta.TaskEnvelope) {
	if len(s.schedulers) == 0 {
		return
	}
	s.schedulers[len(s.schedulers)-1].Done(task)
}

func (s *compositeScheduler) Len() int {
	total := 0
	for _, sched := range s.schedulers {
		total += sched.Len()
	}
	return total
}

func (s *compositeScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		for _, sched := range s.schedulers {
			sched.Shutdown()
		}
	}
}

func (s *compositeScheduler) IsClosed() bool {
	return s.closed.Load()
}
