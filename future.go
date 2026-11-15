package karta

import (
	"context"
	"sync"
	"sync/atomic"
)

// Future 状态字位定义：合并为单个 atomic.Uint32（替代原先的 claimed/resolved
// 两个 atomic.Bool），将 NewResolvedFuture 的原子写从 2 次压缩为 1 次。
const (
	futureClaimed  uint32 = 1 << iota // Resolve 认领位：仅 CAS(0, futureClaimed) 成功者可写 result；NewResolvedFuture 直接置位
	futureResolved                    // 结果可读位：该位的 Store 发生在 result 写入之后，与 Get 快速路径的 Load 构成 SeqCst fence
)

// Future 是异步结果容器 (ADR-004/005: Pipeline 使用)
// 线程安全，支持 Get(ctx) 超时取消和 Then(fn) 链式回调
//
// 必须经 NewPendingFuture / NewResolvedFuture 创建：零值 Future 的
// cond.L 为 nil（未绑定），不可取消 ctx 的阻塞 Get 进入 Cond 慢路径
// 时将 panic。
//
// 内存优化策略（对比原始实现）:
//   - Get 添加 atomic 快速路径：已 resolved 时跳过 select 开销
//   - Resolve 去掉 sync.Once，改为 atomic.CompareAndSwap（CAS）+ close(done) 替代
//   - result 作为普通字段，通过 state 状态字的 SeqCst atomic fence 保证可见性
//     （resolved 位的 Store 在 result 写入之后，与 Get 的 Load 构成同一变量上的
//     fence：Store 之前的写操作对检测到 resolved 位后的读操作可见）
//   - claimed/resolved 合并为单一 state 状态字（atomic.Uint32），
//     NewResolvedFuture 仅需 1 次原子 Store，热路径少一次 SeqCst 写
//   - done channel 延迟分配（lazy）：仅在 Get 慢路径确实需要阻塞时才分配，
//     消除热路径（Resolve→Get 快速路径场景）中的 channel 分配
//   - NewResolvedFuture 使用共享已关闭 channel（resolvedSentinel），消除每次分配
//   - T3：Get 慢路径按 ctx 可取消性分流——不可取消 ctx（Done()==nil，如
//     context.Background()）使用内嵌 sync.Cond 阻塞等待，消除慢路径每次
//     96B 的 hchan 分配；可取消 ctx 保留 lazy done-chan + select 路径
//     （取消/超时语义逐字不变）
type Future[T any] struct {
	done   chan struct{} // 延迟分配：nil 表示尚未有阻塞 Get；非 nil 时为等待通道（仅可取消 ctx 路径使用）
	result Result[T]     // 普通字段，受 state 的 resolved 位 fence 保护
	state  atomic.Uint32 // 状态字：futureClaimed|futureResolved 位组合（见上方常量注释）
	// condWaiters 是 Cond 路径等待者计数：waiter 在进入 mu 之前 Add(1)、
	// 离开 mu 之后 Add(-1)，Resolve 在 mu 临界区内 Load 作为 Broadcast
	// 守卫（见 Get 的唤醒正确性论证）。声明紧跟 state：编译器将两个 4B
	// 原子字段打包进相邻槽位，Future[int] sizeof 保持 128B（无尾部 padding）。
	condWaiters atomic.Int32
	mu          sync.Mutex
	callbacks   []func(Result[T])
	// cond 是不可取消 ctx 慢路径的阻塞原语。cond.L 由两个构造器固定绑定
	// &mu——内嵌零值 Cond 不可直接使用（L 为 nil），必须经构造器创建。
	cond sync.Cond
}

// resolvedSentinel 是全局已关闭 channel，被 NewResolvedFuture 共享，
// 避免每次 NewResolvedFuture 都分配并关闭一个新 channel。
var resolvedSentinel = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// NewPendingFuture 创建待完成的 Future。
// done channel 延迟分配（nil），仅在可取消 ctx 的 Get 慢路径需要阻塞时才
// 分配；不可取消 ctx 的慢路径使用内嵌 Cond（cond.L 在此绑定 &mu）。
func NewPendingFuture[T any]() *Future[T] {
	f := &Future[T]{}
	f.cond.L = &f.mu
	return f
}

// NewResolvedFuture 创建已完成的 Future。
// 使用 shared resolvedSentinel（已关闭 channel），无需每次分配。
// 单次原子 Store 同时置位 claimed|resolved：该 Future 已持有结果，
// 后续 Resolve 的 CAS(0, futureClaimed) 必然失败，禁止覆写已读值。
// Get 永远命中 resolved 快速路径、不触 Cond——cond.L 初始化是构造
// 完整性（非防御分支），保证类型不变式：cond.L 恒等于 &mu。
func NewResolvedFuture[T any](r Result[T]) *Future[T] {
	f := &Future[T]{
		done:   resolvedSentinel,
		result: r,
	}
	f.cond.L = &f.mu
	f.state.Store(futureClaimed | futureResolved)
	return f
}

// Get 阻塞等待结果，支持 context 取消/超时
func (f *Future[T]) Get(ctx context.Context) Result[T] {
	// 快速路径：resolved 位已置，直接返回（跳过 select 开销）
	if f.state.Load()&futureResolved != 0 {
		return f.result
	}
	// T3 慢路径分流：不可取消 ctx（Done()==nil，如 Background/TODO）走
	// 内嵌 Cond，消除 lazy done-chan 的 96B hchan 分配。f.result 的读取
	// 依赖 state resolved 位的 SeqCst fence（与快速路径同一可见性契约）。
	//
	// 唤醒正确性论证（三种交错穷举，无丢失唤醒）：
	// waiter 先 condWaiters.Add(1)，再 mu.Lock，锁内 recheck 后才 Wait
	// （Cond.Wait 在释放 mu 之前已 notifyListAdd 入列）；resolver 的
	// state.Store 在其 mu.Lock 之前，Broadcast 在其 mu 临界区内。
	//   (a) resolver 的 mu 段先完成 → waiter 锁内 recheck 见 resolved，
	//       不进 Wait 直接返回；
	//   (b) waiter 已进入 Wait（入列且已释 mu）→ resolver 的 mu.Lock
	//       必然发生在入列之后 → Broadcast 命中该 waiter；
	//   (c) waiter 已 Add(1) 但未及 mu.Lock，resolver 守卫见计数 >0，
	//       Broadcast 空放（无人在列）→ waiter 随后锁内 recheck 兜底
	//       （必见 resolved，因 state.Store 先于 resolver 的 mu 段）。
	// for 循环 recheck 同时覆盖虚假唤醒。
	if ctx.Done() == nil {
		f.condWaiters.Add(1)
		f.mu.Lock()
		for f.state.Load()&futureResolved == 0 {
			f.cond.Wait()
		}
		f.mu.Unlock()
		f.condWaiters.Add(-1)
		return f.result
	}
	// 慢路径（可取消 ctx，原实现逐字保留）：确保 done channel 存在（延迟分配）
	f.mu.Lock()
	if f.done == nil {
		// 锁内二次检查：可能在等待锁期间被 Resolve 完成，
		// 此时直接返回以避免无谓的 channel 分配。
		// 注意：此检查与 Resolve 的锁互斥，保证不会有另一个 goroutine
		// 同时对同一 channel 执行 close（消除 double-close 竞态）。
		if f.state.Load()&futureResolved != 0 {
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
	if !f.state.CompareAndSwap(0, futureClaimed) {
		return // 已被其他 goroutine resolve，或由 NewResolvedFuture 构造（state≠0），忽略
	}
	// 严格顺序：先写入 result，再置 resolved 位。
	// state.Store 与 Get 快速路径的 state.Load 构成同一变量上的 SeqCst fence：
	// result 的写入（Store 之前）对检测到 resolved 位后的读取可见。
	f.result = r
	f.state.Store(futureClaimed | futureResolved)

	// 持有锁期间关闭 done（若有 goroutine 正在等待）。
	// 与 Get 的锁互斥保证：若 Get 持有锁时检测到 resolved 位，则直接返回
	// （不分配/不关闭 channel），因此 Resolve 在此处看到的 f.done 若不为 nil，
	// 则一定是未关闭的（仅由 Get 慢路径在 resolved 位未置时分配）。
	f.mu.Lock()
	if f.done != nil {
		close(f.done) // close 是并发安全的广播机制：所有 <-f.done 阻塞的 goroutine 都将解除
	}
	// T3 计数守卫：仅当存在 Cond 路径等待者时才 Broadcast。
	// notifyListBroadcast 即使零等待者也会取内部列表锁——守卫使异步/
	// Then-only/纯快速路径形态的 Resolve 零 Broadcast 成本（防热路径回退）。
	// 与 waiter 的锁外 Add(1)/Add(-1) 均为同一 atomic 变量访问，无数据竞争；
	// 空放场景（waiter 已计数未入列）由 Get 锁内 recheck 兜底，见其论证 (c)。
	if f.condWaiters.Load() > 0 {
		f.cond.Broadcast()
	}
	cbs := f.callbacks
	f.callbacks = nil
	f.mu.Unlock()

	for _, cb := range cbs {
		go safeCallback(cb, r)
	}
}

// safeCallback 在独立 goroutine 中执行用户回调并捕获其 panic。
// 与库内"用户代码 panic 必捕获"的约定一致（pipeline executor、group
// worker 均 recover 并转为错误结果）；Then 回调没有错误返回通道
// （Future 已 resolve，回调签名为 func(Result[T])），故 panic 在此
// 静默丢弃，仅保证不击穿进程。
func safeCallback[T any](fn func(Result[T]), r Result[T]) {
	defer func() {
		_ = recover() // 静默丢弃：无错误通道可转换，见上方说明
	}()
	fn(r)
}

// Then 注册回调，返回自身以支持链式调用
// 如果 Future 已 resolve，回调立即在独立 goroutine 中执行
func (f *Future[T]) Then(fn func(Result[T])) *Future[T] {
	f.mu.Lock()
	if f.state.Load()&futureResolved != 0 {
		r := f.result
		f.mu.Unlock()
		go safeCallback(fn, r)
		return f
	}
	f.callbacks = append(f.callbacks, fn)
	f.mu.Unlock()
	return f
}
