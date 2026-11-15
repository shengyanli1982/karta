package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrSchedulerFull 表示调度器缓冲区已满，无法入队
var ErrSchedulerFull = errors.New("karta: scheduler buffer full")

// Scheduler 调度器接口，负责任务的入队、出队与生命周期管理 (ADR-008)
type Scheduler interface {
	// Enqueue 将任务入队到调度器
	// 若调度器已关闭，返回 ErrSchedulerClosed
	// 若缓冲区已满，返回 ErrSchedulerFull
	// 注意：Bounded 调度器（scheduler.NewBoundedScheduler）满时可能短暂阻塞
	// 后再返回 ErrSchedulerFull（容量预检与 Put 之间的残余 TOCTOU，见 bounded.go）
	Enqueue(task *TaskEnvelope) error

	// Dequeue 从调度器中获取任务，支持 context 取消
	Dequeue(ctx context.Context) (*TaskEnvelope, error)

	// Done 标记任务完成，SimpleScheduler 为 no-op
	Done(task *TaskEnvelope)

	// Len 返回当前队列中的任务数量
	Len() int

	// Shutdown 关闭调度器，幂等操作
	Shutdown()

	// IsClosed 返回调度器是否已关闭
	IsClosed() bool
}

// Retryer 是 Scheduler 的可选扩展接口，声明调度器具备重试语义。
// 不并入 Scheduler 主接口：重试是可选能力，Pipeline 在 WithAutoRetry 开启时
// 对 scheduler 做一次类型断言（见 NewPipeline），未实现者静默走默认完成路径。
// scheduler.NewRetryScheduler / NewRetrySchedulerWithDLQ 的实现已满足本接口。
type Retryer interface {
	// Retry 标记任务失败并按调度器策略重新入队。
	// 重试耗尽或调度器已关闭时返回 error；调用方须据此走最终完成路径。
	Retry(task *TaskEnvelope, reason error) error

	// NumRequeues 返回任务已重试的次数。
	NumRequeues(task *TaskEnvelope) int
}

// TaskEnvelope 任务信封，封装任务元数据 (ADR-014: per-task handler override)
type TaskEnvelope struct {
	Input     any             // 任务输入数据
	Handler   any             // 可选 per-task Handler，nil 用默认
	Priority  int64           // 优先级
	Delay     time.Duration   // 延迟时间
	Deadline  time.Time       // 截止时间
	CreatedAt time.Time       // 创建时间
	UserCtx   context.Context // 用户 Submit 时传入的 ctx
	// id 内部 ID：pipeline.submitInternal 统一赋值（所有调度器路径一致），
	// 作为 Pipeline pending map 的键；lease 调度器交付的浅拷贝天然继承该值。
	id uint64
	// dedupKey 去重键（singleflight 语义）：由 WithDedupKey 写入，Pipeline 用它
	// 在任务完成前合并同 key 的重复提交（共享首个任务的 Future）。空串表示
	// 不去重。仅作 Pipeline 内部记账，调度器实现不解释该字段。
	dedupKey string
}

// envelopeIDCounter 任务信封全局自增 ID 计数器
var envelopeIDCounter atomic.Uint64

// newEnvelopeID 生成下一个任务信封 ID
func newEnvelopeID() uint64 { return envelopeIDCounter.Add(1) }

// 编译期接口检查：确保 SimpleScheduler 实现 Scheduler 接口
var _ Scheduler = (*SimpleScheduler)(nil)

// SimpleScheduler 基于 channel 的 FIFO 调度器 (Phase 1 默认实现)
// 支持非阻塞入队、context-aware 出队和安全关闭
type SimpleScheduler struct {
	ch     chan *TaskEnvelope
	closed atomic.Bool
	once   sync.Once
	len    atomic.Int64
	// mu 保护 send on ch 与 close(ch) 的互斥。读锁（Enqueue）允许多生产者
	// 并发 send——channel 的并发 send 本身是原生安全操作，锁的唯一职责是
	// send 与 close 的互斥；写锁仅 Shutdown close 时独占所有在飞 send，
	// 且写锁等待期间新的 RLock 被阻塞（写者优先），互斥语义与 sync.Mutex
	// 等价：close 时不存在在飞 send，send 时 channel 必然未关闭。
	mu sync.RWMutex
}

// NewSimpleScheduler 创建指定缓冲区大小的 FIFO 调度器
func NewSimpleScheduler(bufferSize int) *SimpleScheduler {
	return &SimpleScheduler{
		ch: make(chan *TaskEnvelope, bufferSize),
	}
}

// Enqueue 将任务非阻塞地入队
// 若调度器已关闭返回 ErrSchedulerClosed，缓冲区已满返回 ErrSchedulerFull
func (s *SimpleScheduler) Enqueue(task *TaskEnvelope) error {
	if s.closed.Load() {
		return ErrSchedulerClosed
	}
	// 读锁 + 显式 RUnlock（热路径省去 defer 开销）：多生产者并发 Enqueue
	// 在 RLock 下并行执行 select-send，无需互斥；RWMutex 的读写排斥仅用于
	// 保证 Shutdown 的写锁 close(s.ch) 时无任何在飞 send。所有 return 路径
	// 均先 RUnlock，保持无 defer 的显式解锁契约。
	s.mu.RLock()
	if s.closed.Load() { // double-check under lock
		s.mu.RUnlock()
		return ErrSchedulerClosed
	}
	// 注意：不在此处赋值 task.id —— id 由 pipeline.submitInternal 在写入
	// pending map 之前统一赋值，Enqueue 后置改写会导致 pending 键失配。
	//
	// 计数先于 send 递增（P2-9）：接收方（Dequeue / executor 快速路径）在
	// 收到任务的瞬间即 Add(-1)；若 send 成功后才 Add(1)，直送交接（接收方
	// 已阻塞在空队列上）时接收方可能抢先递减，Len() 出现瞬态负值，违反
	// 接口契约"当前队列中的任务数量"非负。先增后减只产生瞬态高估（send
	// 失败的回退窗口内多计 1），语义可接受。
	s.len.Add(1)
	select {
	case s.ch <- task:
		s.mu.RUnlock()
		return nil
	default:
		s.len.Add(-1) // 缓冲已满，send 未发生：回退预增计数
		s.mu.RUnlock()
		return ErrSchedulerFull
	}
}

// Dequeue 从队列中获取任务，支持 context 取消
// 若 channel 已关闭返回 ErrSchedulerClosed，context 取消返回 ctx.Err()
func (s *SimpleScheduler) Dequeue(ctx context.Context) (*TaskEnvelope, error) {
	select {
	case task, ok := <-s.ch:
		if !ok {
			return nil, ErrSchedulerClosed
		}
		s.len.Add(-1)
		return task, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Done 标记任务完成 (SimpleScheduler 为 no-op)
func (s *SimpleScheduler) Done(task *TaskEnvelope) {
	// no-op for SimpleScheduler
}

// Len 返回当前队列中的任务数量。
// 永不为负；并发下 send 失败回退的瞬间可能瞬态高估 1（见 Enqueue 注释）。
func (s *SimpleScheduler) Len() int {
	return int(s.len.Load())
}

// Shutdown 关闭调度器，幂等操作
// 关闭后 Enqueue 将返回 ErrSchedulerClosed
func (s *SimpleScheduler) Shutdown() {
	s.once.Do(func() {
		s.closed.Store(true)
		// 写锁排他所有在飞 send（Enqueue 的 RLock 持有者），并阻塞写锁
		// 等待期间的新 RLock（写者优先）：close 执行时不存在、也不会再出现
		// 并发 send，杜绝 send on closed channel panic。
		s.mu.Lock()
		close(s.ch)
		s.mu.Unlock()
	})
}

// IsClosed 返回调度器是否已关闭
func (s *SimpleScheduler) IsClosed() bool {
	return s.closed.Load()
}

// envelopePool 复用 TaskEnvelope，减少每轮 submit 的堆分配
var envelopePool = sync.Pool{
	New: func() any {
		return &TaskEnvelope{}
	},
}

// getEnvelope 从 pool 获取一个清空过的 TaskEnvelope
func getEnvelope() *TaskEnvelope {
	return envelopePool.Get().(*TaskEnvelope)
}

// putEnvelope 清空字段后归还 pool，防止指针泄漏
func putEnvelope(e *TaskEnvelope) {
	e.Input = nil
	e.Handler = nil
	e.UserCtx = nil
	e.Priority = 0
	e.Delay = 0
	e.CreatedAt = time.Time{}
	e.Deadline = time.Time{}
	e.id = 0
	e.dedupKey = ""
	envelopePool.Put(e)
}
