package karta

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// seqThreshold 是顺序快速路径的输入长度阈值。
// 低于此值时，goroutine 调度开销超过并发收益，直接顺序执行。
const seqThreshold = 128

// Group 是泛型同步批处理组件 (ADR-002)。
// 使用 Map 对输入切片做并发处理，结果按输入顺序排列。
type Group[In, Out any] struct {
	handler   Handler[In, Out]
	wrapped   Handler[In, Out] // handler 预包裹 middleware chain（NewGroup 时一次性计算）
	opts      *groupOptions
	ctx       context.Context
	cancel    context.CancelFunc
	stopped   atomic.Bool
	pool      sync.Pool // mapWorkCtx 对象池，避免每次 Map 调用分配堆对象
	isEmptyCb bool      // callback 是否为 emptyCallback（NewGroup 时一次类型断言，避免每次 Map 重复断言）
}

// NewGroup 创建泛型工作组。
// 可通过 WithGroupWorkers 等选项定制行为。
func NewGroup[In, Out any](handler Handler[In, Out], opts ...GroupOption) *Group[In, Out] {
	o := defaultGroupOptions()
	for _, opt := range opts {
		opt(o)
	}
	wrapped := handler
	if len(o.middleware) > 0 {
		mws := toMiddlewareSlice[In, Out](o.middleware)
		if len(mws) > 0 {
			wrapped = Chain(mws...)(handler)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	// 缓存 isEmptyCb：类型断言在创建时做一次，后续 Map 调用直接读 bool 字段
	_, isEmptyCb := o.callback.(*emptyCallback)
	g := &Group[In, Out]{
		handler:   handler,
		wrapped:   wrapped,
		opts:      o,
		ctx:       ctx,
		cancel:    cancel,
		isEmptyCb: isEmptyCb,
	}
	g.pool.New = func() any { return &mapWorkCtx[In, Out]{} }
	return g
}

// Map 并发处理 inputs 切片，返回有序结果切片（results[i] 对应 inputs[i]）。
//
// - 若 Group 已 Stop 或 inputs 为空/nil，返回 nil。
// - 若外部 ctx 或内部 ctx（Stop 触发）被取消，剩余项记录 ctx.Err()。
// - handler panic 会被捕获为 error，不会导致 Group 崩溃。
func (g *Group[In, Out]) Map(ctx context.Context, inputs []In) []Result[Out] {
	if g.stopped.Load() || len(inputs) == 0 {
		return nil
	}

	n := len(inputs)
	results := make([]Result[Out], n)

	// 实际 worker 数不超过输入长度
	workers := g.opts.workers
	if workers > n {
		workers = n
	}

	// 顺序快速路径：输入量太小时，goroutine 调度开销超过并发收益，直接顺序执行。
	if workers < 2 || n <= seqThreshold {
		g.mapSequ(ctx, inputs, results)
		return results
	}

	// 并发路径：从对象池获取工作上下文（复用避免堆分配）
	sh := g.pool.Get().(*mapWorkCtx[In, Out])
	sh.reset(g, ctx, results, inputs, n)
	sh.targetCount = int32(workers - 1)

	// caller-as-worker：启动 (workers-1) 个 goroutine，调用方直接执行 run()，省一个 goroutine 分配。
	for i := 0; i < workers-1; i++ {
		go sh.run()
	}
	sh.runAsCaller()

	// 用 atomic counter + Gosched 替代 WaitGroup（对于短等待避免 pthread_cond_wait 开销）
	for sh.doneCount.Load() < sh.targetCount {
		runtime.Gosched()
	}

	g.pool.Put(sh)
	return results
}

// mapWorkCtx 是 Map 操作的工作上下文，将所有共享状态打包为一个堆对象。
// 通过 sync.Pool 复用，避免每次 Map 调用分配堆对象。
type mapWorkCtx[In, Out any] struct {
	doneCount   atomic.Int32 // worker 完成计数（替代 WaitGroup）
	targetCount int32        // 需等待的 worker 数
	nextIdx     atomic.Int64
	g           *Group[In, Out]
	gctx        context.Context
	cctx        context.Context
	h           Handler[In, Out]
	cb          Callback
	results     []Result[Out]
	inputs      []In
	n           int
	isEmptyCb   bool // 优化：true 时跳过 OnBefore/OnAfter 的接口调用开销
}

// reset 重新初始化 mapWorkCtx 以供复用。
func (sh *mapWorkCtx[In, Out]) reset(g *Group[In, Out], ctx context.Context, results []Result[Out], inputs []In, n int) {
	// doneCount/nextIdx 需显式归零；targetCount 由 Map 方法在 reset 后设置。
	sh.doneCount.Store(0)
	sh.nextIdx.Store(0)
	sh.g = g
	sh.gctx = g.ctx
	sh.cctx = ctx
	sh.h = g.wrapped
	sh.cb = g.opts.callback
	sh.isEmptyCb = g.isEmptyCb
	sh.results = results
	sh.inputs = inputs
	sh.n = n
}

// run 是 goroutine worker 的执行体，完成后递增 doneCount。
func (sh *mapWorkCtx[In, Out]) run() {
	sh.runCore()
	sh.doneCount.Add(1)
}

// runAsCaller 是调用方 goroutine 的执行体（不参与 doneCount 计数）。
func (sh *mapWorkCtx[In, Out]) runAsCaller() {
	sh.runCore()
}

// runCore 是 worker 的核心逻辑，不处理同步计数，由 run/runAsCaller 包装。
func (sh *mapWorkCtx[In, Out]) runCore() {
	// 快速路径：当外部 ctx 无取消信号时（如 context.Background()），
	// 跳过 mergedCtx 和 merge goroutine，直接使用 gctx。
	var workerCtx context.Context
	var workerCancel context.CancelFunc
	var stopAfterFunc func() bool

	if sh.cctx.Done() != nil {
		// 外部 ctx 可取消：用 context.AfterFunc 替代 merge goroutine，
		// workerCtx 以 gctx 为父，AfterFunc 在外部 ctx 取消时触发 workerCancel。
		workerCtx, workerCancel = context.WithCancel(sh.gctx)
		stopAfterFunc = context.AfterFunc(sh.cctx, workerCancel)
	} else {
		// 外部 ctx 无取消信号：只用 gctx（节省 WithCancel + merge goroutine 分配）。
		workerCtx = sh.gctx
	}
	defer func() {
		if stopAfterFunc != nil {
			stopAfterFunc()
		}
		if workerCancel != nil {
			workerCancel()
		}
	}()

	// 优化 2：批量 panic 保护（整批次一个 defer，而非每个 item 一个 defer）
	var panicErr error
	var lastIdx int = -1 // 跟踪当前正在处理的索引
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("karta: handler panic: %v", r)
			}
		}()

		for {
			idx := int(sh.nextIdx.Add(1) - 1)
			if idx >= sh.n {
				return
			}
			if workerCtx.Err() != nil {
				sh.results[idx] = Result[Out]{Err: workerCtx.Err()}
				continue
			}

			lastIdx = idx
			// 优化 1：emptyCallback 快速路径，跳过接口方法调用
			if !sh.isEmptyCb {
				sh.cb.OnBefore(workerCtx, sh.inputs[idx])
			}
			val, err := sh.h(workerCtx, sh.inputs[idx])
			sh.results[idx] = Result[Out]{Value: val, Err: err}
			if !sh.isEmptyCb {
				sh.cb.OnAfter(workerCtx, sh.inputs[idx], val, err)
			}
		}
	}()

	// panic 处理：将 panic 发生的索引位置填充错误
	if panicErr != nil && lastIdx >= 0 {
		sh.results[lastIdx] = Result[Out]{Err: panicErr}
	}
}

// mapSequ 是顺序执行的快速路径，跳过 goroutine 创建和同步开销。
// 用于输入量较小（n <= seqThreshold）的场景。
//
// 性能优化（三项）：
//  1. Batch-level panic 保护：整个批次一个 defer，而非 per-item defer/recover。
//     若 handler panic，通过外层循环重新进入，确保后续项继续处理。
//  2. 缓存 isEmptyCb：从 Group 字段直接读取 bool，避免每次 Map 调用做类型断言。
//  3. Err() 检查降级为每 16 项一次：减少 context.WithCancel 的原子加载次数。
func (g *Group[In, Out]) mapSequ(ctx context.Context, inputs []In, results []Result[Out]) {
	h := g.wrapped
	cb := g.opts.callback
	isEmptyCb := g.isEmptyCb
	n := len(inputs)
	gctx := g.ctx

	// 外部 ctx 不可取消时：仅检查 gctx（Group.Stop 信号），无 WithCancel/AfterFunc 分配。
	if ctx.Done() == nil {
		g.mapSequCore(gctx, h, cb, isEmptyCb, inputs, results, n)
		return
	}

	// 外部 ctx 可取消：创建合并上下文（gctx + 外部 ctx）
	workerCtx, workerCancel := context.WithCancel(gctx)
	stopAfterFunc := context.AfterFunc(ctx, workerCancel)
	defer func() {
		stopAfterFunc()
		workerCancel()
	}()

	g.mapSequCore(workerCtx, h, cb, isEmptyCb, inputs, results, n)
}

// mapSequCore 是 mapSequ 的核心循环体，抽取以便两个分支复用同一逻辑。
//
//   - 整个批次只使用一个 defer/recover（batch-level panic protection）。
//   - panic 后通过外层 for 重新进入闭包，确保后续项继续处理（与 per-item safeCall 语义一致）。
//   - 每 16 项检查一次 ctx.Err()，减少原子加载开销。
func (g *Group[In, Out]) mapSequCore(
	ctx context.Context,
	h Handler[In, Out],
	cb Callback,
	isEmptyCb bool,
	inputs []In,
	results []Result[Out],
	n int,
) {
	for i := 0; i < n; {
		var panicErr error

		func() {
			defer func() {
				if r := recover(); r != nil {
					panicErr = fmt.Errorf("karta: handler panic: %v", r)
				}
			}()

			for ; i < n; i++ {
				// 每 16 项做一次 ctx.Err() 原子加载（优化 3）
				if i&0xf == 0 {
					if ctx.Err() != nil {
						err := ctx.Err()
						for j := i; j < n; j++ {
							results[j] = Result[Out]{Err: err}
						}
						return
					}
				}
				// 优化 2：emptyCallback 快速路径，跳过接口方法调用
				if !isEmptyCb {
					cb.OnBefore(ctx, inputs[i])
				}
				val, err := h(ctx, inputs[i])
				results[i] = Result[Out]{Value: val, Err: err}
				if !isEmptyCb {
					cb.OnAfter(ctx, inputs[i], val, err)
				}
			}
		}()

		if panicErr != nil {
			// panic 发生在索引 i 处：填充错误并跳过该项，继续处理后续
			results[i] = Result[Out]{Err: panicErr}
			i++
		} else {
			// 正常完成或因 ctx cancel 退出：结束外层循环
			break
		}
	}
}

// Stop 幂等地停止工作组，后续 Map 调用返回 nil。
func (g *Group[In, Out]) Stop() {
	if g.stopped.CompareAndSwap(false, true) {
		g.cancel()
	}
}


