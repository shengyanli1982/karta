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

// blockingLeasedQueue 是 workqueue v2.3.4+ leasedQueueImpl 提供的阻塞租约消费
// 可选接口：GetWithLeaseWithContext 阻塞直到「内层队列取到值 / ctx 完成 /
// 队列关闭」，成功后登记租约；timeout<=0 回退 config.leaseDuration。
// LeasedQueue 接口未声明该方法（保护外部实现者兼容性），需类型断言使用。
type blockingLeasedQueue interface {
	GetWithLeaseWithContext(ctx context.Context, timeout time.Duration) (value any, leaseID string, err error)
}

// leaseEntry 记录一次 Dequeue 交付的租约信息。
// leaseID 是底层队列的租约键，用于 Ack；owner 指向底层队列持有的原始
// 任务指针，用于在任务完成时清理 owners 正向索引。
// 以值类型存入 leases map（写入即拷贝 24B 结构），消除指针形态下
// 每次交付的 &leaseEntry{} 堆分配。
type leaseEntry struct {
	leaseID string
	owner   *karta.TaskEnvelope
}

// leaseScheduler 将 workqueue.LeasedQueue 适配为 karta.Scheduler 接口。
//
// Dequeue 通过 GetWithLeaseWithContext 阻塞获取任务并绑定租约；
// Done 通过 Ack 释放租约确认处理完成。
// 租约超时的任务由底层队列自动重新入队。
//
// 所有权契约：Dequeue 总是返回 TaskEnvelope 的浅拷贝，而非底层队列持有的
// 原始指针。租约过期触发重投递时，先后两个消费者拿到的是不同的指针，
// 保证"两个消费者不得同时持有同一 *TaskEnvelope"。底层租约以 leaseID
// 为键（与投递出去的对象指针无关），因此 Done(拷贝) 仍能正确 Ack
// 原始对象的租约；已过期的租约 Ack 失败时被静默忽略。
type leaseScheduler struct {
	queue workqueue.LeasedQueue
	// blocking 在构造时一次性断言（workqueue v2.3.4+ leasedQueueImpl 实现），
	// 失败即 panic，属 fail-fast：Dequeue 热路径不再重复断言。
	blocking blockingLeasedQueue
	closed   atomic.Bool
	// mu 保护 leases/owners 双 map。T4：原双 sync.Map 在写多形态（每次
	// 交付 2 写、每次 Done 1-2 删）下持续触发 Go 1.24+ HashTrieMap 底层
	// 的节点分配（alloc profile 合计 34.3%）；原生 map+Mutex 为写多读少
	// 场景的官方推荐形态，稳态零映射分配。单锁使 deliver 的
	// 「换 owners 索引 + 清旧 leases」成为原子段，消除原双 sync.Map
	// 操作间的中间状态窗口（语义增强，方向安全）。
	mu sync.Mutex
	// leases 以交付给消费者的拷贝指针为键，反查租约信息，供 Done 使用。
	// 值类型条目：map 写入直接拷贝结构体，无独立堆分配。
	leases map[*karta.TaskEnvelope]leaseEntry
	// owners 以底层原始任务指针为键，记录当前交付在外的拷贝，
	// 用于重投递时清理上一份未完成的 leases 记录，避免映射泄漏。
	owners map[*karta.TaskEnvelope]*karta.TaskEnvelope
}

// NewLeaseScheduler 创建基于 workqueue.LeasedQueue 的租约调度器。
// leaseTimeout 指定每次 Dequeue 获取的租约超时时间（<=0 时由底层
// 回退为 workqueue 默认租约时长 30s）。
func NewLeaseScheduler(leaseTimeout time.Duration) karta.Scheduler {
	cfg := workqueue.NewLeasedQueueConfig().
		WithLeaseDuration(leaseTimeout).
		WithScanInterval(leaseScanInterval(leaseTimeout))
	q := workqueue.NewLeasedQueue(cfg)
	return &leaseScheduler{
		queue:    q,
		blocking: q.(blockingLeasedQueue),
		leases:   make(map[*karta.TaskEnvelope]leaseEntry),
		owners:   make(map[*karta.TaskEnvelope]*karta.TaskEnvelope),
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
	return nil
}

// Dequeue 阻塞获取任务并登记租约：取到值返回浅拷贝；队列关闭返回
// ErrSchedulerClosed（底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；
// ctx 完成原样返回 ctx.Err()。timeout 传 0 回退构造时的 config.leaseDuration。
func (s *leaseScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	for {
		val, leaseID, err := s.blocking.GetWithLeaseWithContext(ctx, 0)
		if err != nil {
			if errors.Is(err, workqueue.ErrQueueIsClosed) {
				return nil, karta.ErrSchedulerClosed
			}
			return nil, err
		}
		if orig, ok := val.(*karta.TaskEnvelope); ok {
			return s.deliver(orig, leaseID), nil
		}
		// 类型不匹配时释放租约
		_ = s.queue.Ack(leaseID)
	}
}

// deliver 基于底层原始任务生成一个交付给消费者的浅拷贝并登记租约。
// 重投递场景下（同一原始指针再次出队），用新拷贝替换 owners 索引，
// 并清理上一份未完成拷贝在 leases 中的记录，防止映射泄漏。
// 「替换索引 + 清理旧记录 + 登记新记录」在单个 mu 临界区内原子完成。
func (s *leaseScheduler) deliver(orig *karta.TaskEnvelope, leaseID string) *karta.TaskEnvelope {
	copied := *orig
	delivered := &copied
	s.mu.Lock()
	if prev, ok := s.owners[orig]; ok {
		delete(s.leases, prev)
	}
	s.owners[orig] = delivered
	s.leases[delivered] = leaseEntry{leaseID: leaseID, owner: orig}
	s.mu.Unlock()
	return delivered
}

// Done 释放与任务关联的租约（Ack）。
// task 应为 Dequeue 返回的拷贝。如果租约已过期或不存在，静默忽略
// （过期任务已由底层重新入队）。
func (s *leaseScheduler) Done(task *karta.TaskEnvelope) {
	s.mu.Lock()
	entry, ok := s.leases[task]
	if ok {
		delete(s.leases, task)
		// 仅当正向索引仍指向当前拷贝时才清理，避免误删重投递后的新拷贝
		// 记录（等价于原 owners.CompareAndDelete(entry.owner, task)）
		if cur, exists := s.owners[entry.owner]; exists && cur == task {
			delete(s.owners, entry.owner)
		}
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	// Ack 在临界区外执行：entry 为值拷贝，无锁内依赖；避免与底层
	// 队列的内部锁产生跨层持锁耦合。
	_ = s.queue.Ack(entry.leaseID)
}

func (s *leaseScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithLeaseWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *leaseScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *leaseScheduler) IsClosed() bool {
	return s.closed.Load()
}
