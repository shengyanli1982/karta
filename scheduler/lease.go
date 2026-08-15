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

// leaseEntry 记录一次 Dequeue 交付的租约信息。
// leaseID 是底层队列的租约键，用于 Ack；owner 指向底层队列持有的原始
// 任务指针，用于在任务完成时清理 owners 正向索引。
type leaseEntry struct {
	leaseID string
	owner   *karta.TaskEnvelope
}

// leaseScheduler 将 workqueue.LeasedQueue 适配为 karta.Scheduler 接口。
//
// Dequeue 通过 GetWithLease 获取任务并绑定租约；
// Done 通过 Ack 释放租约确认处理完成。
// 租约超时的任务由底层队列自动重新入队。
//
// 所有权契约：Dequeue 总是返回 TaskEnvelope 的浅拷贝，而非底层队列持有的
// 原始指针。租约过期触发重投递时，先后两个消费者拿到的是不同的指针，
// 保证"两个消费者不得同时持有同一 *TaskEnvelope"。底层租约以 leaseID
// 为键（与投递出去的对象指针无关），因此 Done(拷贝) 仍能正确 Ack
// 原始对象的租约；已过期的租约 Ack 失败时被静默忽略。
type leaseScheduler struct {
	queue        workqueue.LeasedQueue
	closed       atomic.Bool
	leaseTimeout time.Duration
	// leases 以交付给消费者的拷贝指针为键，反查租约信息，供 Done 使用。
	leases sync.Map // map[*karta.TaskEnvelope]*leaseEntry
	// owners 以底层原始任务指针为键，记录当前交付在外的拷贝，
	// 用于重投递时清理上一份未完成的 leases 记录，避免映射泄漏。
	owners sync.Map // map[*karta.TaskEnvelope]*karta.TaskEnvelope
	notify chan struct{}
	doneCh chan struct{}
}

// NewLeaseScheduler 创建基于 workqueue.LeasedQueue 的租约调度器。
// leaseTimeout 指定每次 Dequeue 获取的租约超时时间。
func NewLeaseScheduler(leaseTimeout time.Duration) karta.Scheduler {
	cfg := workqueue.NewLeasedQueueConfig().
		WithLeaseDuration(leaseTimeout).
		WithScanInterval(leaseScanInterval(leaseTimeout))
	return &leaseScheduler{
		queue:        workqueue.NewLeasedQueue(cfg),
		leaseTimeout: leaseTimeout,
		notify:       make(chan struct{}, notifyChanCapacity()),
		doneCh:       make(chan struct{}),
	}
}

// leaseScanInterval 计算过期租约的扫描间隔：取租约时长的 1/4，
// 并限定在 [1ms, 100ms] 区间，兼顾重投递的及时性与扫描开销。
func leaseScanInterval(leaseTimeout time.Duration) time.Duration {
	if leaseTimeout <= 0 {
		// 与 workqueue.LeasedQueueConfig 的默认租约时长保持一致
		leaseTimeout = 30 * time.Second
	}
	interval := leaseTimeout / 4
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	if interval > 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	return interval
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
	defer timer.Stop()

	for {
		val, leaseID, err := s.queue.GetWithLease(s.leaseTimeout)
		if err == nil {
			if orig, ok := val.(*karta.TaskEnvelope); ok {
				return s.deliver(orig, leaseID), nil
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
		// Go 1.23+ 保证 Reset 返回后不会收到旧定时值，直接重设即可
		timer.Reset(backoff)
	}
}

// deliver 基于底层原始任务生成一个交付给消费者的浅拷贝并登记租约。
// 重投递场景下（同一原始指针再次出队），用新拷贝替换 owners 索引，
// 并清理上一份未完成拷贝在 leases 中的记录，防止映射泄漏。
func (s *leaseScheduler) deliver(orig *karta.TaskEnvelope, leaseID string) *karta.TaskEnvelope {
	copied := *orig
	delivered := &copied
	if prev, loaded := s.owners.Swap(orig, delivered); loaded {
		s.leases.Delete(prev)
	}
	s.leases.Store(delivered, &leaseEntry{leaseID: leaseID, owner: orig})
	return delivered
}

// Done 释放与任务关联的租约（Ack）。
// task 应为 Dequeue 返回的拷贝。如果租约已过期或不存在，静默忽略
// （过期任务已由底层重新入队）。
func (s *leaseScheduler) Done(task *karta.TaskEnvelope) {
	val, ok := s.leases.LoadAndDelete(task)
	if !ok {
		return
	}
	entry := val.(*leaseEntry)
	// 仅当正向索引仍指向当前拷贝时才清理，避免误删重投递后的新拷贝记录
	s.owners.CompareAndDelete(entry.owner, task)
	_ = s.queue.Ack(entry.leaseID)
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
