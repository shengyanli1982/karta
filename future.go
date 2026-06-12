package karta

import (
	"context"
	"sync"
	"sync/atomic"
)

// Future 是异步结果容器 (ADR-004/005: Pipeline 使用)
// 线程安全，支持 Get(ctx) 超时取消和 Then(fn) 链式回调
//
// 内存优化策略（对比原始实现）:
//  - Get 添加 atomic 快速路径：已 resolved 时跳过 select 开销
//  - Resolve 去掉 sync.Once，改为 atomic.CompareAndSwap（CAS）+ close(done) 替代
//  - result 作为普通字段，通过 claimed/resolved 的 SeqCst atomic fence 保证可见性
//    （resolved.Store 之前的写操作对 resolved.Load 之后的读操作可见）
//  - done channel 延迟分配（lazy）：仅在 Get 慢路径确实需要阻塞时才分配，
//    消除热路径（Resolve→Get 快速路径场景）中的 channel 分配
//  - NewResolvedFuture 使用共享已关闭 channel（resolvedSentinel），消除每次分配
type Future[T any] struct {
	done      chan struct{}       // 延迟分配：nil 表示尚未有阻塞 Get；非 nil 时为等待通道
	result    Result[T]           // 普通字段，受 resolved atomic fence 保护
	resolved  atomic.Bool         // true 表示 result 已可读取（在写入 result 之后设置）
	claimed   atomic.Bool         // CAS 守卫：仅一个 goroutine 能完成 Resolve（替代 sync.Once）
	mu        sync.Mutex
	callbacks []func(Result[T])
}

// doneChanPool 预留的 done channel 池。
// 当前设计下 Resolve 不归还已关闭 channel（避免 sync.Pool.Put 引入额外分配），
// 但在未来 sync.Pool 实现优化后可启用归还逻辑以复用 channel。
// 使用指针 *chan struct{} 放入 pool，避免 interface 装箱导致的额外分配。
var doneChanPool = sync.Pool{
	New: func() any {
		ch := make(chan struct{})
		return &ch
	},
}

// resolvedSentinel 是全局已关闭 channel，被 NewResolvedFuture 共享，
// 避免每次 NewResolvedFuture 都分配并关闭一个新 channel。
var resolvedSentinel = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// NewPendingFuture 创建待完成的 Future。
// done channel 延迟分配（nil），仅在 Get 慢路径需要阻塞时才分配，
// 消除热路径中的 channel 分配。
func NewPendingFuture[T any]() *Future[T] {
	return &Future[T]{}
}

// NewResolvedFuture 创建已完成的 Future。
// 使用 shared resolvedSentinel（已关闭 channel），无需每次分配。
func NewResolvedFuture[T any](r Result[T]) *Future[T] {
	f := &Future[T]{
		done:   resolvedSentinel,
		result: r,
	}
	f.resolved.Store(true)
	return f
}

// Get 阻塞等待结果，支持 context 取消/超时
func (f *Future[T]) Get(ctx context.Context) Result[T] {
	// 快速路径：已 resolved，直接返回（跳过 select 开销）
	if f.resolved.Load() {
		return f.result
	}
	// 慢路径：确保 done channel 存在（延迟分配）
	f.mu.Lock()
	if f.done == nil {
		// 锁内二次检查：可能在等待锁期间被 Resolve 完成，
		// 此时直接返回以避免无谓的 channel 分配。
		// 注意：此检查与 Resolve 的锁互斥，保证不会有另一个 goroutine
		// 同时对同一 channel 执行 close（消除 double-close 竞态）。
		if f.resolved.Load() {
			f.mu.Unlock()
			return f.result
		}
		f.done = make(chan struct{})
	}
	ch := f.done
	f.mu.Unlock()
	select {
	case <-ch:
		return f.result
	case <-ctx.Done():
		var zero T
		return Result[T]{Value: zero, Err: ctx.Err()}
	}
}

// Resolve 设置结果并通知所有等待者（仅第一次生效）
// 使用 CAS 替代 sync.Once，减少分配和锁竞争
func (f *Future[T]) Resolve(r Result[T]) {
	if !f.claimed.CompareAndSwap(false, true) {
		return // 已被其他 goroutine resolve，忽略
	}
	// 严格顺序：先写入 result，再设置 resolved=true（SeqCst atomic fence 保证可见性）
	f.result = r
	f.resolved.Store(true)

	// 持有锁期间关闭 done（若有 goroutine 正在等待）。
	// 与 Get 的锁互斥保证：若 Get 持有锁时检测到 resolved=true，则直接返回
	// （不分配/不关闭 channel），因此 Resolve 在此处看到的 f.done 若不为 nil，
	// 则一定是未关闭的（仅由 Get 慢路径在 resolved=false 时分配）。
	f.mu.Lock()
	if f.done != nil {
		close(f.done) // close 是并发安全的广播机制：所有 <-f.done 阻塞的 goroutine 都将解除
	}
	cbs := f.callbacks
	f.callbacks = nil
	f.mu.Unlock()

	for _, cb := range cbs {
		go cb(r)
	}
}

// Then 注册回调，返回自身以支持链式调用
// 如果 Future 已 resolve，回调立即在独立 goroutine 中执行
func (f *Future[T]) Then(fn func(Result[T])) *Future[T] {
	f.mu.Lock()
	if f.resolved.Load() {
		r := f.result
		f.mu.Unlock()
		go fn(r)
		return f
	}
	f.callbacks = append(f.callbacks, fn)
	f.mu.Unlock()
	return f
}
