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

// TaskEnvelope 任务信封，封装任务元数据 (ADR-014: per-task handler override)
type TaskEnvelope struct {
	Input     any             // 任务输入数据
	Handler   any             // 可选 per-task Handler，nil 用默认
	Priority  int64           // 优先级
	Delay     time.Duration   // 延迟时间
	Deadline  time.Time       // 截止时间
	CreatedAt time.Time       // 创建时间
	UserCtx   context.Context // 用户 Submit 时传入的 ctx
	id        uint64          // 内部 ID，用于 pending map
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
	mu     sync.Mutex // 保护 send on ch 与 close(ch) 的互斥
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() { // double-check under lock
		return ErrSchedulerClosed
	}
	task.id = newEnvelopeID()
	select {
	case s.ch <- task:
		s.len.Add(1)
		return nil
	default:
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

// Len 返回当前队列中的任务数量
func (s *SimpleScheduler) Len() int {
	return int(s.len.Load())
}

// Shutdown 关闭调度器，幂等操作
// 关闭后 Enqueue 将返回 ErrSchedulerClosed
func (s *SimpleScheduler) Shutdown() {
	s.once.Do(func() {
		s.closed.Store(true)
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
	envelopePool.Put(e)
}
