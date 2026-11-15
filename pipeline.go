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
	mws           []Middleware[In, Out] // NewPipeline 时一次性类型断言（类型不匹配 panic），此后只读
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	once          sync.Once
	closed        atomic.Bool
	running       atomic.Int64
	targetWorkers atomic.Int64            // Tune 动态调整的目标 worker 数（spawn 上限），trySpawnWorker 读取
	pendingMu     sync.Mutex              // 保护 pending/dedup/closed/wg.Add 的共享临界区
	pending       map[uint64]*Future[Out] // 键为 TaskEnvelope.id：lease 调度器交付浅拷贝（新指针），指针键控必然 miss（P0 N1）
	dedup         map[string]*Future[Out] // 键为 TaskEnvelope.dedupKey：singleflight 共享首个任务的 Future（P1-3）
	workerLimiter *rate.Limiter           // worker spawn 速率控制
	retryer       Retryer                 // autoRetry 开启时 NewPipeline 一次性断言，executor 只读（未实现为 nil）
}

// NewPipeline 创建并启动一个 Pipeline
// handler: 默认任务处理函数
// scheduler: 调度器（不可为 nil）
// opts: 可选配置
//
// 注意：一个 Scheduler 实例只能服务一个 Pipeline。pending future 映射是
// per-pipeline 的，多个 Pipeline 共享同一 Scheduler 时会互相取走对方的
// envelope（取走方 pending 未命中，任务被静默 Done 丢弃），请为每个
// Pipeline 创建独立的 Scheduler。
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
		mws:           toMiddlewareSlice[In, Out](o.middleware),
		ctx:           ctx,
		cancel:        cancel,
		pending:       make(map[uint64]*Future[Out]),
		dedup:         make(map[string]*Future[Out]),
		workerLimiter: rate.NewLimiter(rate.Limit(o.spawnRate), o.burstLimit),
	}

	// targetWorkers 必须在启动任何 executor 之前设定：trySpawnWorker 以该
	// atomic 值为 spawn 上限，Tune 运行时改写它即可动态伸缩（P1-4）。
	p.targetWorkers.Store(int64(o.workers))

	// autoRetry 开启时一次性断言 Retryer：executor 只读该字段，
	// 断言须在任何 executor goroutine 启动前完成（之后不再写入，无需同步）；
	// scheduler 未实现 Retryer 时 retryer 保持 nil，executor 静默走默认完成路径
	if o.autoRetry {
		p.retryer, _ = p.scheduler.(Retryer)
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

// SubmitOption 为 Submit/SubmitWithHandler 提供可选的调度元数据，
// 写入 TaskEnvelope 对应字段，由匹配的 Scheduler 实现解释执行；
// 不携带 option 时字段保持零值，行为与既有版本完全一致。
type SubmitOption func(*TaskEnvelope)

// WithPriority 设置任务优先级，由 scheduler.NewPriorityScheduler 解释执行：
// 数值越小优先级越高（小顶堆语义）；其他调度器忽略该字段。
func WithPriority(p int64) SubmitOption {
	return func(e *TaskEnvelope) { e.Priority = p }
}

// WithDelay 设置入队延迟，由 scheduler.NewDelayScheduler /
// scheduler.NewTimerScheduler 解释执行：延迟到期后任务才可被 Dequeue。
// 与 SubmitAfter 的 Pipeline 侧 goroutine 等待不同，延迟由调度器内部
// 定时器驱动，Submit 调用本身立即返回。
func WithDelay(d time.Duration) SubmitOption {
	return func(e *TaskEnvelope) { e.Delay = d }
}

// WithDeadline 设置截止时间（绝对时间点），由 scheduler.NewTimerScheduler
// 解释执行：任务在 Deadline 时刻入队（底层 PutAt）；其他调度器忽略该字段。
func WithDeadline(t time.Time) SubmitOption {
	return func(e *TaskEnvelope) { e.Deadline = t }
}

// WithDedupKey 为任务设置去重键，启用 singleflight 语义（P1-3）：
// 同一 key 在任务**完成前**只入队一次，后续相同 key 的提交不再重复执行，
// 而是直接共享首个任务已登记的 Future（所有调用方拿到同一结果）。
//
// 语义细节：
//   - 合并以 key 为准，后续提交的 input / handler 差异被忽略——真实执行的
//     始终是首次登记任务的 input 与 handler。
//   - 共享 future 的取消语义跟随首个提交者的 UserCtx（与
//     golang.org/x/sync/singleflight 先例一致）：首个提交者的 ctx 取消会以
//     该取消错误 resolve 共享 future，后续共享方"连坐"收到并非自己造成的
//     取消；反之，后续共享者的 ctx 取消不影响已入队任务（其 envelope 已在
//     合并时丢弃，任务仍按首个提交者的 ctx 执行）。
//   - "完成"指首个任务最终 Resolve（成功 / 失败 / 重试耗尽 / UserCtx 取消）；
//     完成后 key 登记被清除，同 key 可再次提交并再次真实执行。
//   - 去重仅作用于 Pipeline 即时提交路径；SubmitAfter 不接收 SubmitOption，
//     其 dedupKey 恒为空，不参与去重。
//
// key 为空串等同于不去重。
func WithDedupKey(key string) SubmitOption {
	return func(e *TaskEnvelope) { e.dedupKey = key }
}

// Submit 提交任务，使用默认 handler，返回 Future。
// envelope 不携带 per-task Handler（保持 nil），executor 使用启动时
// 已预包裹 middleware 的 defaultHandler，避免每任务重复 Chain 包裹。
// opts 可携带调度元数据（WithPriority/WithDelay/WithDeadline/WithDedupKey）。
func (p *Pipeline[In, Out]) Submit(ctx context.Context, input In, opts ...SubmitOption) (*Future[Out], error) {
	return p.submitInternal(ctx, nil, input, 0, opts)
}

// SubmitWithHandler 提交任务，使用指定的 handler 覆盖默认 handler。
// 这是唯一设置 envelope.Handler 的提交路径；handler 为 nil 时等同于
// 使用默认 handler（见 TaskEnvelope.Handler 的 nil 语义）。
// opts 可携带调度元数据（WithPriority/WithDelay/WithDeadline/WithDedupKey）。
func (p *Pipeline[In, Out]) SubmitWithHandler(ctx context.Context, handler Handler[In, Out], input In, opts ...SubmitOption) (*Future[Out], error) {
	return p.submitInternal(ctx, handler, input, 0, opts)
}

// SubmitAfter 延迟提交任务，delay 后入队。
// 与 Submit 相同，envelope 不携带 per-task Handler。
// 不接收 SubmitOption：delay 参数已提供延迟语义（防双重延迟，见 submitInternal）。
func (p *Pipeline[In, Out]) SubmitAfter(ctx context.Context, input In, delay time.Duration) (*Future[Out], error) {
	return p.submitInternal(ctx, nil, input, delay, nil)
}

// Stop 关闭 pipeline，幂等操作
func (p *Pipeline[In, Out]) Stop() {
	p.once.Do(func() {
		// closed 必须在 pendingMu 临界区内设置：Submit/trySpawnWorker 在
		// 同一把锁下完成 closed 检查与 wg.Add，保证 wg.Wait 开始后
		// 不会再有新的 wg.Add（杜绝 WaitGroup misuse）
		p.pendingMu.Lock()
		p.closed.Store(true)
		p.pendingMu.Unlock()
		p.cancel()
		p.scheduler.Shutdown()
		p.wg.Wait()
		// 清理所有等待中的 pending future，防止 Get() 永久阻塞；
		// 同一临界区内清空 dedup：Stop 后同 key 不再有共享登记（无泄漏）
		p.pendingMu.Lock()
		for id, f := range p.pending {
			delete(p.pending, id)
			f.Resolve(Result[Out]{Err: ErrPipelineClosed})
		}
		clear(p.dedup)
		p.pendingMu.Unlock()
	})
}

// GetWorkerNumber 返回当前运行中的 executor goroutine 数量
func (p *Pipeline[In, Out]) GetWorkerNumber() int64 {
	return p.running.Load()
}

// Workers 返回当前目标 worker 数（Tune 后的最新值，即 spawn 上限），
// 而非实时运行的 goroutine 数；实时运行数见 GetWorkerNumber。
func (p *Pipeline[In, Out]) Workers() int {
	return int(p.targetWorkers.Load())
}

// Tune 运行时动态调整目标 worker 数（P1-4）。
//   - 调大：立即尝试 spawn 差量 worker（受 spawnRate 限流渐进到位）；
//   - 调小：多余 worker 在空闲后自然退出（复用 idle timeout 机制，至少保留 1 个），
//     Tune 本身不做强制回收，targetWorkers 仅作为后续 spawn 的上限。
//
// workers < 1 返回 ErrInvalidWorkerCount；pipeline 已关闭返回 ErrPipelineClosed；
// workers 超过 defaultMaxWorkerNum 时钳制到上界（与 WithPipelineWorkers 共享上界）。
// 合法下界与构造期刻意不同：WithPipelineWorkers 的域是 [defaultMinWorkerNum,
// defaultMaxWorkerNum]（越界忽略），而运行期允许 Tune(1)——idle 机制本就收敛
// 到 1（最后一个 worker 永不退出），Tune(1) 即"最小稳态"，拒绝它反而与既有
// 收敛行为矛盾。并发安全。
func (p *Pipeline[In, Out]) Tune(workers int) error {
	if workers < 1 {
		return ErrInvalidWorkerCount
	}
	if p.closed.Load() {
		return ErrPipelineClosed
	}
	// 上界钳制（P2-5）：targetWorkers 是 trySpawnWorker 的 CAS 预留上限，
	// 必须自身有界——否则超大值令 spawn 尝试循环失控（仅靠 limiter 渐增
	// 约束，观测值 Workers() 亦失去意义）。
	if int64(workers) > defaultMaxWorkerNum {
		workers = int(defaultMaxWorkerNum)
	}
	p.targetWorkers.Store(int64(workers))
	// 调大：尝试补足差量 worker。trySpawnWorker 内部 CAS 以 targetWorkers 为
	// 上限、并经 limiter 限流，天然不会超限；delta <= 0（调小或已达目标）时
	// 循环不执行，调小依赖既有 idle 退出机制自然收敛。
	for delta := int64(workers) - p.running.Load(); delta > 0; delta-- {
		p.trySpawnWorker()
	}
	return nil
}

// submitInternal 统一的提交入口
//
// handler 为 nil 表示不携带 per-task 覆盖（envelope.Handler 保持 nil），
// executor 将使用启动时预包裹 middleware 的 defaultHandler；
// 仅 SubmitWithHandler 传入非 nil handler 设置 per-task 覆盖。
// opts 为调度元数据 option（可为 nil），在 Enqueue 发布前写入 envelope。
//
// 并发安全不变量：closed 检查、pending 写入、wg.Add(1) 三者必须在同一个
// pendingMu 临界区内完成（与 Stop 中同一把锁下设置 closed 配合），
// 保证 Stop 的 wg.Wait 开始后不会再出现新的 wg.Add。
func (p *Pipeline[In, Out]) submitInternal(
	ctx context.Context,
	handler Handler[In, Out],
	input In,
	delay time.Duration,
	opts []SubmitOption,
) (*Future[Out], error) {
	future := NewPendingFuture[Out]()
	envelope := getEnvelope()
	envelope.Input = input
	// handler 为 nil 时不得赋值 envelope.Handler：Handler 是函数类型，
	// typed nil 赋给 any 字段会得到非 nil 的 interface 值，executor 将误判为
	// per-task 覆盖并调用 nil 函数（panic）。池化 envelope 的 Handler 已被
	// putEnvelope 清零，保持 nil 即走默认 handler 路径。
	if handler != nil {
		envelope.Handler = handler
	}
	envelope.UserCtx = ctx
	envelope.CreatedAt = time.Now()
	// 统一赋值 envelope id（所有调度器路径一致，P0 N1 修复）：pending map 以
	// id 为键，lease 调度器交付的浅拷贝天然继承 id，executor 仍能命中。
	// newEnvelopeID 自增起点为 1，与池化清零值 0 不冲突；赋值发生在 Enqueue
	// 发布之前，经队列锁/channel 与 executor 的读取构成 happens-before。
	envelope.id = newEnvelopeID()

	// 应用调度元数据 option（Priority/Delay/Deadline）：与 id 赋值同理，
	// 写入发生在 Enqueue 发布之前，经队列锁与调度器的读取构成 happens-before
	for _, opt := range opts {
		opt(envelope)
	}

	if delay > 0 {
		// 延迟提交：goroutine 等待 timer 触发后入队
		envelope.Delay = delay
		p.pendingMu.Lock()
		if p.closed.Load() {
			p.pendingMu.Unlock()
			putEnvelope(envelope)
			return nil, ErrPipelineClosed
		}
		p.wg.Add(1)
		p.pendingMu.Unlock()
		go func() {
			defer p.wg.Done()
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				// 延迟等待已由本 goroutine 完成，入队前清零 Delay，
				// 防止 Delay-aware 调度器（如 scheduler 包的 delay/timer）二次施加延迟
				envelope.Delay = 0
				// timer 触发时再次检查 closed：若 pipeline 已关闭，
				// 直接 resolve future，不存入 pending
				p.pendingMu.Lock()
				if p.closed.Load() {
					p.pendingMu.Unlock()
					future.Resolve(Result[Out]{Err: ErrPipelineClosed})
					putEnvelope(envelope)
					return
				}
				// 先存 pending 再入队，避免 executor 取走时找不到 future
				p.pending[envelope.id] = future
				p.pendingMu.Unlock()
				err := p.scheduler.Enqueue(envelope)
				if err != nil {
					p.pendingMu.Lock()
					delete(p.pending, envelope.id)
					p.pendingMu.Unlock()
					// 与即时路径同构：Enqueue 失败包 SubmitError，
					// 保证 errors.As 判型的调用方在两条路径行为一致
					future.Resolve(Result[Out]{Err: &SubmitError{Cause: err}})
					putEnvelope(envelope)
					return
				}
				p.trySpawnWorker() // 与即时路径对称：按需启动新 worker
			case <-p.ctx.Done():
				// p.ctx 仅由 Stop 取消：与 timer 分支的 closed 检查统一，
				// resolve ErrPipelineClosed（而非 context.Canceled），
				// 包 SubmitError 与延迟路径 Enqueue 失败同构
				future.Resolve(Result[Out]{Err: &SubmitError{Cause: ErrPipelineClosed}})
				putEnvelope(envelope)
			case <-ctx.Done():
				future.Resolve(Result[Out]{Err: ctx.Err()})
				putEnvelope(envelope)
			}
		}()
		return future, nil
	}

	// 即时提交：closed 检查、dedup 登记、pending 写入同处一个临界区，
	// 先存 pending 再入队，避免 executor 取走时找不到 future
	p.pendingMu.Lock()
	if p.closed.Load() {
		p.pendingMu.Unlock()
		putEnvelope(envelope)
		return nil, ErrPipelineClosed
	}
	// singleflight 去重（P1-3）：dedupKey 非空且已有同 key 在途登记时，直接
	// 返回共享 future，本次新建的 envelope/future 丢弃（envelope 归还池、
	// future 交 GC）。dedup 直接存 future 而非 envelope id 间接查 pending：
	// executor 执行 handler 前已把 future 从 pending 摘除，间接查在执行期
	// 窗口必然 miss 导致重复执行。miss 时在同一临界区内登记 dedup 与 pending，
	// 保证"登记 dedup"与"入队"之间不存在第二个提交者能插入的窗口。
	if key := envelope.dedupKey; key != "" {
		if shared, ok := p.dedup[key]; ok {
			p.pendingMu.Unlock()
			putEnvelope(envelope)
			return shared, nil
		}
		p.dedup[key] = future
	}
	p.pending[envelope.id] = future
	p.pendingMu.Unlock()
	err := p.scheduler.Enqueue(envelope)
	if err != nil {
		// Enqueue 失败：撤销刚才的 pending 与 dedup 登记。dedup 若残留会指向
		// 一个永不 Resolve 的孤儿 future，后续同 key 提交误共享它而永久阻塞。
		// 读 dedupKey 在 putEnvelope 清零之前、且在锁内完成（map 删除需持锁）。
		p.pendingMu.Lock()
		delete(p.pending, envelope.id)
		if key := envelope.dedupKey; key != "" {
			delete(p.dedup, key)
		}
		p.pendingMu.Unlock()
		// 与延迟路径同构：返回错误前必须 Resolve 本 future。"登记 dedup 后
		// 释放锁再 Enqueue"的窗口内，同 key 提交者可能已 dedup 命中拿到该
		// future；只删登记不 Resolve 会让共享方 Get 永久挂死（future 已不在
		// pending，Stop 清理也覆盖不到）。非去重场景 future 未对外暴露，
		// Resolve 经 CAS 幂等零副作用。置于临界区外保持锁最小化（Resolve
		// 可能触发 Then 回调 goroutine）。
		future.Resolve(Result[Out]{Err: &SubmitError{Cause: err}})
		putEnvelope(envelope)
		return nil, &SubmitError{Cause: err}
	}
	p.trySpawnWorker() // 尝试按需启动新 worker
	return future, nil
}

// executor 是 worker goroutine，从 scheduler 取任务并执行
// 支持 idle timeout 自动退出（保留至少 1 个 worker）
//
// running 记账：started != nil 时（NewPipeline 启动）由本函数登记名额；
// started == nil 时（trySpawnWorker 启动）名额已在调用方 CAS 中预留，此处不再 Add
func (p *Pipeline[In, Out]) executor(started chan<- struct{}) {
	idleExit := false
	defer func() {
		if !idleExit {
			p.running.Add(-1)
		}
		p.wg.Done()
	}()
	if started != nil {
		p.running.Add(1)
		close(started) // 通知调用方 executor 已就绪
	}

	lastActive := time.Now().UnixMilli()

	// middleware pre-wrap: Chain 包裹只执行一次（per executor，避免共享写竞争）
	// p.mws 在 NewPipeline 时一次性完成类型断言（类型不匹配 panic），此后只读；
	// 读取 p.handler 是安全的（NewPipeline 返回后不再写入），但不可写回 p.handler
	defaultHandler := p.handler
	mws := p.mws
	if len(mws) > 0 {
		defaultHandler = Chain(mws...)(p.handler)
	}

	// Hot Path 1: 在循环外创建可复用的 timer，避免每轮 context.WithTimeout 分配
	scanTimer := time.NewTimer(p.opts.scanInterval)
	defer scanTimer.Stop()

	// 类型断言到 *SimpleScheduler，executor 可直接 select 其底层 channel，
	// 从而彻底跳过 context 分配（仅对 SimpleScheduler 生效，其他实现走 fallback）
	ss, _ := p.scheduler.(*SimpleScheduler)

	for {
		// Go 1.23+：Reset 保证调用后不会收到此前未取走的过期 tick，
		// 无需 Stop + drain 样板
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
				// Stop 时序终态一致（P2-3）：closed channel 的剩余缓冲仍可
				// 接收，Stop 后本 case 与 <-p.ctx.Done() 同时就绪、随机选择，
				// 若照常执行会以已取消的 taskCtx 调用 handler，future 以
				// handler 结果 resolve——与 fallback 路径（Shutdown 后 Dequeue
				// 报错即退出）及 workqueue 系调度器（一律 ErrPipelineClosed）
				// 产生终态分叉。检测到 closed 即不执行 handler，就地按 Stop
				// 清理语义终结任务（见 resolveClosed）。
				if p.closed.Load() {
					p.resolveClosed(task)
					continue
				}
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
				// 错误路径 pacing：Dequeue 快速返错（如自定义 Scheduler 持续
				// 立即失败）时不得立刻重试，否则形成忙自旋（实证 500ms 内
				// 114 万次调用、单核 100%）。等待本轮 scanTimer 到期，复用
				// scanInterval 节奏后重试；p.ctx 取消（Stop）立即唤醒退出，
				// 不会因 pacing 卡住 Stop。正常超时场景下 scanTimer 与
				// dequeueCtx 同节奏到期，此等待即刻返回，无额外延迟。
				select {
				case <-scanTimer.C:
				case <-p.ctx.Done():
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
			// future 已被取消/超时清理（lease 重投递或 Stop 清理场景），
			// 仍需 Done 释放租约/死信标记。
			// 归还不变量：envelope 恰好归还一次，且归还原持有者执行——
			// 原持有 executor（或提交路径）完成时 putEnvelope；此处若再归还，
			// 将与原持有者的归还形成双重归还，污染池中其他任务。故只 Done，不 putEnvelope。
			p.scheduler.Done(envelope)
			continue
		}

		// UserCtx 已取消的任务直接以取消错误完成，不执行 handler，
		// 完成路径（Resolve → Done → removeDedup → 归还 envelope）与其他路径保持一致
		if envelope.UserCtx != nil && envelope.UserCtx.Err() != nil {
			future.Resolve(Result[Out]{Err: envelope.UserCtx.Err()})
			p.scheduler.Done(envelope)
			p.removeDedup(envelope) // 必须在 putEnvelope 之前：归还后 dedupKey 被清零
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
		// （UserCtx 已取消的任务已在上方短路，不会执行到此处）
		// 两条快速路径 (0 extra allocs)：
		//   1. UserCtx == nil：无调用方 context
		//   2. UserCtx.Done() == nil：context.Background()/TODO()，永不取消
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
		// 双值断言（P2-4）：In 为接口类型且 Input 为 nil any 时（如
		// Pipeline[error, X] 提交 nil），单值断言会 panic 并被下方 recover
		// 吞为"handler panic: interface conversion..."任务失败；双值断言对
		// 不可转换值返回 In 零值（接口型 In 的零值即 nil），恰为契约语义。
		// 双值形式自身永不 panic，置于闭包外无风险。
		typedInput, _ := envelope.Input.(In)
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					result = Result[Out]{Err: fmt.Errorf("karta: handler panic: %v", rec)}
				}
			}()
			p.opts.callback.OnBefore(taskCtx, envelope.Input)
			val, err := h(taskCtx, typedInput)
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

		// Scheduler 级自动重试（WithAutoRetry）：handler 返回 error 时委托
		// Retryer 重新入队，Future 在最终成功或耗尽后才 Resolve。
		// 并发不变量（与 Stop 的 pending 清理配合，见 submitInternal 同款约束）：
		//   - 必须先放回 future 再调 Retry：Retry 会把 envelope 重新入队，
		//     其他 worker 可能立刻 Dequeue 到它；若 pending 中没有 future，
		//     loadAndDeletePending 返回 nil，任务被静默丢弃且 future 永久阻塞。
		//   - closed 检查与放回同处 pendingMu 临界区：closed 时不放回也不重试，
		//     future 的 resolve 归属唯一落在本函数的最终路径（若 closed 时仍
		//     Retry 且成功，future 既不在 pending 也不会被 Resolve，形成泄漏窗口）。
		//   - Retry 成功路径禁止 scheduler.Done（retryScheduler.Done 内含 Forget，
		//     会清零重试计数导致永不耗尽）、禁止 putEnvelope（envelope 指针仍被
		//     底层队列持有，且重试 key 即指针地址，归还池会污染后续任务）。
		//   - Retry 失败（耗尽/调度器已关闭）撤销放回，落入既有最终路径；
		//     workqueue 耗尽时未调 queue.Done，最终路径的 scheduler.Done 正好补齐。
		//   - Retry 失败后不归还 envelope（skipPut）：耗尽场景死信独占持有该
		//     指针（DeadLetter.Payload 引用原 envelope，不复制），归还池会导致
		//     字段（Input 等）被复用任务覆写，用户检视死信时与池写入构成数据
		//     竞争（interface 撕裂可 segfault）；关闭场景 Retry 未转发死信，
		//     仅损失一次池化收益（低频可忽略）。scheduler.Done 以指针为键
		//     清理计数/processing 集，指针仍有效，不受 skipPut 影响。
		skipPut := false
		if result.Err != nil && p.retryer != nil {
			p.pendingMu.Lock()
			closed := p.closed.Load()
			if !closed {
				p.pending[envelope.id] = future
			}
			p.pendingMu.Unlock()

			if !closed {
				if p.retryer.Retry(envelope, result.Err) == nil {
					continue // 重试已安排：等待下次 Dequeue 继续执行同一 envelope
				}
				skipPut = true // Retry 失败：envelope 交死信独占/GC，不归还池
			}

			p.pendingMu.Lock()
			delete(p.pending, envelope.id)
			p.pendingMu.Unlock()
		}

		future.Resolve(result)
		p.scheduler.Done(envelope)
		// 任务最终完成（含 autoRetry 耗尽后落入本路径）：清除 dedup 登记，
		// 允许同 key 后续提交再次真实执行。必须在 putEnvelope 之前——归还池后
		// dedupKey 被清零，届时将丢失 key 无法定位登记项。
		// autoRetry 的 Retry 成功路径在上方 continue，不经过此处（任务未完成，
		// 重试期间同 key 提交仍应共享同一 future）。
		p.removeDedup(envelope)
		if !skipPut {
			putEnvelope(envelope)
		}
	}
}

// loadAndDeletePending 从 pending map 中取出并删除 future
// 以 envelope.id 为键（与 submitInternal 一致，P0 N1 修复）：lease 调度器
// Dequeue 交付原始 envelope 的浅拷贝（新指针），指针键控必然 miss 导致任务
// 被静默丢弃；浅拷贝继承 id 字段，id 键控对拷贝交付天然命中。
func (p *Pipeline[In, Out]) loadAndDeletePending(envelope *TaskEnvelope) *Future[Out] {
	p.pendingMu.Lock()
	future, ok := p.pending[envelope.id]
	if !ok {
		p.pendingMu.Unlock()
		return nil
	}
	delete(p.pending, envelope.id)
	p.pendingMu.Unlock()
	return future
}

// removeDedup 清除 envelope 对应的 dedup 登记，在任务最终完成时调用。
// dedupKey 为空（非去重任务，绝大多数）时直接返回，跳过加锁；否则在
// pendingMu 锁内 delete。调用方须保证在 putEnvelope 之前调用（归还池会
// 清零 dedupKey）。读 envelope.dedupKey 无需持锁：此时 envelope 由本
// executor 独占（已出队、尚未归还），无其他 goroutine 并发写。
func (p *Pipeline[In, Out]) removeDedup(envelope *TaskEnvelope) {
	key := envelope.dedupKey
	if key == "" {
		return
	}
	p.pendingMu.Lock()
	delete(p.dedup, key)
	p.pendingMu.Unlock()
}

// resolveClosed 将"已出队但 pipeline 已关闭"的任务按 Stop 清理语义就地终结
// （P2-3 fast path 专用）：future resolve 为 ErrPipelineClosed（与 Stop 的
// pending 清理终态一致，不包 SubmitError），随后 Done → removeDedup → 归还
// envelope。所有权：envelope 已被本 worker 从队列取出，Stop 的 pending 清理
// 只 resolve 仍登记在 pending 中的 future、不接触已出队的 envelope，故完成
// 路径必须由本函数走完（含恰好一次 putEnvelope），与 executor 的 UserCtx
// 取消短路路径同款纪律。future 为 nil（已被其他路径摘除）时沿用既有约定：
// 只 Done 不归还，所有权归原持有者。
func (p *Pipeline[In, Out]) resolveClosed(envelope *TaskEnvelope) {
	future := p.loadAndDeletePending(envelope)
	if future == nil {
		p.scheduler.Done(envelope)
		return
	}
	future.Resolve(Result[Out]{Err: ErrPipelineClosed})
	p.scheduler.Done(envelope)
	p.removeDedup(envelope) // 必须在 putEnvelope 之前：归还后 dedupKey 被清零
	putEnvelope(envelope)
}

// trySpawnWorker 尝试在 worker 数量未达上限时启动新 worker
// 受 rate.Limiter 控制，避免瞬间 spawn 过多 worker
//
// CAS 循环先预留 running 名额再 spawn，杜绝 check-then-spawn 的
// TOCTOU 窗口（并发提交时可能超出 workers 上限）；closed 检查与
// wg.Add 同处 pendingMu 临界区（见 spawnWorkerReserved），与 Stop 的
// wg.Wait 互斥
func (p *Pipeline[In, Out]) trySpawnWorker() {
	// 预留名额：超过上限（targetWorkers，可经 Tune 动态调整）则不 spawn
	for {
		cur := p.running.Load()
		if cur >= p.targetWorkers.Load() {
			return
		}
		if p.running.CompareAndSwap(cur, cur+1) {
			break
		}
	}
	// 名额不足（限流）：归还预留名额
	if !p.workerLimiter.Allow() {
		p.running.Add(-1)
		// 最低存活兜底（P1-1）：CAS 预留的"幻影名额"会骗过 executor 的 idle
		// 退出守卫（守卫以 running 为存活依据，Add(-1) 后 n≥1 即退出），最后
		// 一个真实 worker 可能在本次 Allow 期间把幻影当作同伴而退出；此刻归还
		// 名额令 running 归零，即进入"队列可能有任务却无任何真实 worker"的
		// 搁浅态——Future 仅在下次成功 Submit 时被动解救，若无后续提交则永久
		// 挂起，SubmitEvery 的 skip-if-busy 读到搁浅 future 亦永久停摆。
		// 因此检测到归零（且未 closed）就绕过 limiter 强制补 spawn 一次，
		// 恢复"至少 1 worker"的无条件不变量（不加 Len()>0 条件，与守卫的
		// 无条件最低存活语义一致）。CAS(0,1) 同时完成归零检测与名额重新
		// 预留：并发拒绝者至多一个胜出，无需额外锁；CAS 失败说明已有其他
		// 路径补足名额。closed 由 spawnWorkerReserved 在 pendingMu 临界区内
		// 复核，此处的 Load 仅为关闭期的快速短路。
		// 已知良性瞬态（与 P2-9"瞬态高估可接受"同款声明）：executor idle
		// 守卫的 Add(-1)→读到 n=0→restore Add(1) 与此处救援 CAS(0,1) 存在
		// 纳秒级交错窗口——窗口内救援胜出并 spawn 会瞬态多 1 个真实 worker
		//（可短暂超过 targetWorkers）；下一轮 idle 周期该 worker 的守卫读到
		// n≥1 即退出、自愈，记账平衡与"至少 1 worker"不变量均不破。
		if !p.closed.Load() && p.running.CompareAndSwap(0, 1) {
			p.spawnWorkerReserved()
		}
		return
	}
	p.spawnWorkerReserved()
}

// spawnWorkerReserved 启动一个 executor goroutine。调用方必须已在 p.running
// 中预留名额（executor 以 started==nil 启动时不再自行 Add，见 executor 的
// running 记账注释）；已关闭时归还名额并放弃启动。
// closed 检查与 wg.Add 同处 pendingMu 临界区，与 Stop 的 wg.Wait 互斥，
// 保证 Wait 开始后不会再出现新的 Add（杜绝 WaitGroup misuse）。
func (p *Pipeline[In, Out]) spawnWorkerReserved() {
	p.pendingMu.Lock()
	if p.closed.Load() {
		p.pendingMu.Unlock()
		p.running.Add(-1)
		return
	}
	p.wg.Add(1)
	p.pendingMu.Unlock()
	go p.executor(nil)
}
