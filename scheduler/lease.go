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

// 编译期接口检查：确保 leaseScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*leaseScheduler)(nil)

// leaseScheduler 将 workqueue.LeasedQueue 适配为 karta.Scheduler 接口。
//
// Dequeue 通过 GetWithLease 获取任务并绑定租约；
// Done 通过 Ack 释放租约确认处理完成。
// 租约超时的任务由底层队列自动 Nack 并重新入队。
type leaseScheduler struct {
	queue        workqueue.LeasedQueue
	closed       atomic.Bool
	leaseTimeout time.Duration
	leases       sync.Map // map[*karta.TaskEnvelope]string (leaseID)
	notify       chan struct{}
	doneCh       chan struct{}
}

// NewLeaseScheduler 创建基于 workqueue.LeasedQueue 的租约调度器。
// leaseTimeout 指定每次 Dequeue 获取的租约超时时间。
func NewLeaseScheduler(leaseTimeout time.Duration) karta.Scheduler {
	cfg := workqueue.NewLeasedQueueConfig().
		WithLeaseDuration(leaseTimeout)
	return &leaseScheduler{
		queue:        workqueue.NewLeasedQueue(cfg),
		leaseTimeout: leaseTimeout,
		notify:       make(chan struct{}, notifyChanCapacity()),
		doneCh:       make(chan struct{}),
	}
}

func (s *leaseScheduler) Enqueue(task *karta.TaskEnvelope) error {
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
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *leaseScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer stopAndDrainTimer(timer)

	for {
		val, leaseID, err := s.queue.GetWithLease(s.leaseTimeout)
		if err == nil {
			if env, ok := val.(*karta.TaskEnvelope); ok {
				s.leases.Store(env, leaseID)
				return env, nil
			}
			// 类型不匹配时释放租约
			_ = s.queue.Ack(leaseID)
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

// Done 释放与任务关联的租约（Ack）。
// 如果租约已过期或不存在，静默忽略（底层会重新入队）。
func (s *leaseScheduler) Done(task *karta.TaskEnvelope) {
	if val, ok := s.leases.LoadAndDelete(task); ok {
		leaseID := val.(string)
		_ = s.queue.Ack(leaseID)
	}
}

func (s *leaseScheduler) Len() int {
	return s.queue.Len()
}

func (s *leaseScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *leaseScheduler) IsClosed() bool {
	return s.closed.Load()
}
