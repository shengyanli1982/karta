package karta

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Pipeline 是基于 Scheduler 的异步任务管道 (ADR-014)
// In = 输入类型, Out = 输出类型
// 任务通过 Submit 入队，由内部 executor goroutine 从 Scheduler.Dequeue 取出并执行
type Pipeline[In, Out any] struct {
	handler       Handler[In, Out]
	scheduler     Scheduler
	opts          *pipelineOptions
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	once          sync.Once
	closed        atomic.Bool
	running       atomic.Int64
	pendingMu     sync.Mutex                 // 与 pending 配合，替换 sync.Map
	pending       map[*TaskEnvelope]*Future[Out] // 替换 sync.Map，减少热路径分配
	workerLimiter *rate.Limiter // worker spawn 速率控制
}

// NewPipeline 创建并启动一个 Pipeline
// handler: 默认任务处理函数
// scheduler: 调度器（不可为 nil）
// opts: 可选配置
//
// Panics: scheduler 不可为 nil
func NewPipeline[In, Out any](
	handler Handler[In, Out],
	scheduler Scheduler,
	opts ...PipelineOption,
) *Pipeline[In, Out] {
	if scheduler == nil {
		panic("karta: scheduler must not be nil")
	}

	o := defaultPipelineOptions()
	for _, opt := range opts {
		opt(o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &Pipeline[In, Out]{
		handler:       handler,
		scheduler:     scheduler,
		opts:          o,
		ctx:           ctx,
		cancel:        cancel,
		pending:       make(map[*TaskEnvelope]*Future[Out]),
		workerLimiter: rate.NewLimiter(rate.Limit(o.spawnRate), o.burstLimit),
	}

	// 启动 opts.workers 个 executor goroutine，等待全部就绪后再返回
	for i := 0; i < o.workers; i++ {
		started := make(chan struct{})
		p.wg.Add(1)
		go p.executor(started)
		<-started
	}

	return p
}

// Submit 提交任务，使用默认 handler，返回 Future
func (p *Pipeline[In, Out]) Submit(ctx context.Context, input In) (*Future[Out], error) {
	return p.submitInternal(ctx, p.handler, input, 0)
}

// SubmitWithHandler 提交任务，使用指定的 handler 覆盖默认 handler
func (p *Pipeline[In, Out]) SubmitWithHandler(ctx context.Context, handler Handler[In, Out], input In) (*Future[Out], error) {
	return p.submitInternal(ctx, handler, input, 0)
}

// SubmitAfter 延迟提交任务，delay 后入队
func (p *Pipeline[In, Out]) SubmitAfter(ctx context.Context, input In, delay time.Duration) (*Future[Out], error) {
	return p.submitInternal(ctx, p.handler, input, delay)
}

// Stop 关闭 pipeline，幂等操作
func (p *Pipeline[In, Out]) Stop() {
	p.once.Do(func() {
		p.closed.Store(true)
		p.cancel()
		p.scheduler.Shutdown()
		p.wg.Wait()
		// 清理所有等待中的 pending future，防止 Get() 永久阻塞
		p.pendingMu.Lock()
		for env, f := range p.pending {
			delete(p.pending, env)
			f.Resolve(Result[Out]{Err: ErrPipelineClosed})
		}
		p.pendingMu.Unlock()
	})
}

// GetWorkerNumber 返回当前运行中的 executor goroutine 数量
func (p *Pipeline[In, Out]) GetWorkerNumber() int64 {
	return p.running.Load()
}

// submitInternal 统一的提交入口
func (p *Pipeline[In, Out]) submitInternal(
	ctx context.Context,
	handler Handler[In, Out],
	input In,
	delay time.Duration,
) (*Future[Out], error) {
	if p.closed.Load() {
		return nil, ErrPipelineClosed
	}

	future := NewPendingFuture[Out]()
	envelope := getEnvelope()
	envelope.Input = input
	envelope.Handler = handler
	envelope.Delay = delay
	envelope.CreatedAt = time.Now()
	envelope.UserCtx = ctx

	if delay > 0 {
		// 延迟提交：goroutine 等待 timer 触发后入队
		// wg.Add 在 closed 检查之前，确保 Stop 的 wg.Wait 能等待此 goroutine，
		// 从而 pending 清理能覆盖所有 delayed future
		p.wg.Add(1)
		if p.closed.Load() {
			p.wg.Done()
			putEnvelope(envelope)
			return nil, ErrPipelineClosed
		}
		go func() {
			defer p.wg.Done()
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				// timer 触发时再次检查 closed，缩小与 Stop 的竞态窗口：
				// 若 pipeline 已关闭，直接 resolve future，不存入 pending
				if p.closed.Load() {
					future.Resolve(Result[Out]{Err: ErrPipelineClosed})
					putEnvelope(envelope)
					return
				}
				// 先存 pending 再入队，避免 executor 取走时找不到 future
				p.pendingMu.Lock()
				p.pending[envelope] = future
				p.pendingMu.Unlock()
				err := p.scheduler.Enqueue(envelope)
				if err != nil {
					p.pendingMu.Lock()
					delete(p.pending, envelope)
					p.pendingMu.Unlock()
					future.Resolve(Result[Out]{Err: err})
					putEnvelope(envelope)
				}
			case <-p.ctx.Done():
				future.Resolve(Result[Out]{Err: p.ctx.Err()})
				putEnvelope(envelope)
			case <-ctx.Done():
				future.Resolve(Result[Out]{Err: ctx.Err()})
				putEnvelope(envelope)
			}
		}()
		return future, nil
	}

	// 即时提交：先存 pending 再入队，避免 executor 取走时找不到 future
	p.pendingMu.Lock()
	p.pending[envelope] = future
	p.pendingMu.Unlock()
	err := p.scheduler.Enqueue(envelope)
	if err != nil {
		p.pendingMu.Lock()
		delete(p.pending, envelope)
		p.pendingMu.Unlock()
		putEnvelope(envelope)
		return nil, &SubmitError{Cause: err}
	}
	p.trySpawnWorker() // 尝试按需启动新 worker
	return future, nil
}

// executor 是 worker goroutine，从 scheduler 取任务并执行
// 支持 idle timeout 自动退出（保留至少 1 个 worker）
func (p *Pipeline[In, Out]) executor(started chan<- struct{}) {
	idleExit := false
	defer func() {
		if !idleExit {
			p.running.Add(-1)
		}
		p.wg.Done()
	}()
	p.running.Add(1)
	if started != nil {
		close(started) // 通知调用方 executor 已就绪
	}

	lastActive := time.Now().UnixMilli()

	// middleware pre-wrap: 类型断言 + Chain 包裹只执行一次（per executor，避免共享写竞争）
	// 读取 p.handler 是安全的（NewPipeline 返回后不再写入），但不可写回 p.handler
	defaultHandler := p.handler
	var mws []Middleware[In, Out]
	if len(p.opts.middleware) > 0 {
		mws = toMiddlewareSlice[In, Out](p.opts.middleware)
		if len(mws) > 0 {
			defaultHandler = Chain(mws...)(p.handler)
		}
	}

	// Hot Path 1: 在循环外创建可复用的 timer，避免每轮 context.WithTimeout 分配
	scanTimer := time.NewTimer(p.opts.scanInterval)
	defer scanTimer.Stop()

	// 类型断言到 *SimpleScheduler，executor 可直接 select 其底层 channel，
	// 从而彻底跳过 context 分配（仅对 SimpleScheduler 生效，其他实现走 fallback）
	ss, _ := p.scheduler.(*SimpleScheduler)

	for {
		// 正确 Reset timer：先 Stop + drain，再 Reset
		if !scanTimer.Stop() {
			select {
			case <-scanTimer.C:
			default:
			}
		}
		scanTimer.Reset(p.opts.scanInterval)

		var envelope *TaskEnvelope
		var dequeued bool

		if ss != nil {
			// 快速路径：直接 select scheduler channel，0 context 分配
			select {
			case task, ok := <-ss.ch:
				if !ok {
					return // scheduler 已关闭
				}
				ss.len.Add(-1) // 保持 Len() 一致性（与 Dequeue 等价）
				envelope = task
				dequeued = true
			case <-scanTimer.C:
				// 超时
			case <-p.ctx.Done():
				return
			}
		} else {
			// 回退路径：非 SimpleScheduler 仍使用 Dequeue + context
			dequeueCtx, dequeueCancel := context.WithTimeout(p.ctx, p.opts.scanInterval)
			task, err := p.scheduler.Dequeue(dequeueCtx)
			dequeueCancel()

			if err != nil {
				if p.ctx.Err() != nil || p.scheduler.IsClosed() {
					return
				}
			} else {
				envelope = task
				dequeued = true
			}
		}

		if !dequeued {
			// 超时 → 检查 idle
			idleMs := time.Now().UnixMilli() - lastActive
			if idleMs >= p.opts.idleTimeout.Milliseconds() {
				// 原子地减 1，若减后仍有存活 worker 则安全退出；
				// idleExit=true 抑制 defer 中的 Add(-1)，避免双重递减。
				n := p.running.Add(-1)
				if n < 1 {
					// 我是最后一个 worker，不允许退出，恢复计数
					p.running.Add(1)
				} else {
					// 还有其他 worker，标记 idle 退出并返回
					idleExit = true
					return
				}
			}
			continue
		}

		lastActive = time.Now().UnixMilli()

		future := p.loadAndDeletePending(envelope)
		if future == nil {
			// future 已被取消/超时清理，仍需 Done 释放租约/死信标记
			p.scheduler.Done(envelope)
			putEnvelope(envelope)
			continue
		}

		// 选择 handler: per-task 覆盖 > 默认（仅 override 时重新包裹 middleware）
		h := defaultHandler
		if envelope.Handler != nil {
			if override, ok := envelope.Handler.(Handler[In, Out]); ok {
				h = override
				if len(mws) > 0 {
					h = Chain(mws...)(override)
				}
			}
		}

		// Hot Path 2 v2: 仅在 UserCtx 有可观察取消信号时才创建 cancel context
		// 三条快速路径 (0 extra allocs)：
		//   1. UserCtx == nil：无调用方 context
		//   2. UserCtx.Done() == nil：context.Background()/TODO()，永不取消
		//   3. UserCtx.Err() != nil：已取消，AfterFunc 会立刻 fire 但 handler 已在 p.ctx 下运行
		var taskCtx context.Context
		var taskCancel context.CancelFunc
		var stopAfterFunc func() bool

		if envelope.UserCtx != nil && envelope.UserCtx.Done() != nil && envelope.UserCtx.Err() == nil {
			// UserCtx 有活跃的取消信号需要 merge: WithCancel + AfterFunc ≈ 4 allocs
			taskCtx, taskCancel = context.WithCancel(p.ctx)
			stopAfterFunc = context.AfterFunc(envelope.UserCtx, taskCancel)
		} else {
			// 其余所有场景直接使用 p.ctx (0 extra allocs)
			taskCtx = p.ctx
		}

		// 安全执行（捕获 panic）
		var result Result[Out]
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					result = Result[Out]{Err: fmt.Errorf("karta: handler panic: %v", rec)}
				}
			}()
			p.opts.callback.OnBefore(taskCtx, envelope.Input)
			val, err := h(taskCtx, envelope.Input.(In))
			result = Result[Out]{Value: val, Err: err}
			p.opts.callback.OnAfter(taskCtx, envelope.Input, val, err)
		}()

		// 清理资源
		if stopAfterFunc != nil {
			stopAfterFunc()
		}
		if taskCancel != nil {
			taskCancel()
		}
		future.Resolve(result)
		p.scheduler.Done(envelope)
		putEnvelope(envelope)
	}
}

// loadAndDeletePending 从 pending map 中取出并删除 future
// 使用 envelope 指针作为 key（与 submitInternal 一致）
func (p *Pipeline[In, Out]) loadAndDeletePending(envelope *TaskEnvelope) *Future[Out] {
	p.pendingMu.Lock()
	future, ok := p.pending[envelope]
	if !ok {
		p.pendingMu.Unlock()
		return nil
	}
	delete(p.pending, envelope)
	p.pendingMu.Unlock()
	return future
}

// trySpawnWorker 尝试在 worker 数量未达上限时启动新 worker
// 受 rate.Limiter 控制，避免瞬间 spawn 过多 worker
func (p *Pipeline[In, Out]) trySpawnWorker() {
	if p.running.Load() >= int64(p.opts.workers) {
		return
	}
	if !p.workerLimiter.Allow() {
		return
	}
	p.wg.Add(1)
	started := make(chan struct{})
	go p.executor(started)
	<-started
}

