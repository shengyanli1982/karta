# Karta 任务调度库设计规格

**版本**: v1.0  
**日期**: 2026-04-08  
**状态**: 已批准

---

## 目录

- [1. 背景与目标](#1-背景与目标)
- [2. 架构设计](#2-架构设计)
- [3. 包结构](#3-包结构)
- [4. 核心接口设计](#4-核心接口设计)
- [5. 队列层设计](#5-队列层设计)
- [6. 执行层设计](#6-执行层设计)
- [7. 调度器设计](#7-调度器设计)
- [8. 工作流设计](#8-工作流设计)
- [9. 使用示例](#9-使用示例)
- [10. 实施路径](#10-实施路径)

---

## 1. 背景与目标

### 1.1 项目定位

基于现有 Karta 项目，设计一个**通用任务调度库 + 工作流编排能力**的升级版本。

### 1.2 核心决策

| 维度 | 选择 |
|------|------|
| **定位** | 通用任务调度库 + 工作流编排 |
| **架构** | 三层架构（调度层/队列层/执行层分离） |
| **DAG 能力** | 简单链式依赖 |
| **队列实现** | 复用 WorkQueue v2 |
| **Worker 池** | 自研轻量实现 |
| **包名** | karta 子包扩展 |

### 1.3 设计原则

1. **三层解耦**: 调度层、队列层、执行层职责清晰，可独立替换
2. **接口抽象**: 面向接口编程，便于扩展和测试
3. **渐进增强**: 在 Karta 基础上逐步增加新能力
4. **向后兼容**: 保持现有 API 兼容

---

## 2. 架构设计

### 2.1 三层架构全景图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Karta 任务调度体系                              │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│   ┌─────────────────────────────────────────────────────────────────┐   │
│   │                      Scheduler (调度层)                          │   │
│   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐            │   │
│   │  │   Priority  │  │   Delay     │  │   DAG /     │            │   │
│   │  │  Scheduler  │  │  Scheduler  │  │  Chain      │            │   │
│   │  │  (优先级)    │  │  (延迟)     │  │  Scheduler  │            │   │
│   │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘            │   │
│   │         │                │                │                    │   │
│   │         └────────────────┼────────────────┘                    │   │
│   │                          │                                        │   │
│   │                   ┌──────▼──────┐                                │   │
│   │                   │ TaskBridge │ (任务转换层)                    │   │
│   │                   └──────┬──────┘                                │   │
│   └──────────────────────────┼──────────────────────────────────────┘   │
│                              │                                          │
│   ┌──────────────────────────▼──────────────────────────────────────┐   │
│   │                        Queue (队列层)                              │   │
│   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │   │
│   │  │  Priority   │  │  Delaying   │  │   Retry     │              │   │
│   │  │  Queue      │  │  Queue      │  │   Queue     │              │   │
│   │  └─────────────┘  └─────────────┘  └──────┬──────┘              │   │
│   │         │                │                │                      │   │
│   │         └────────────────┼────────────────┘                      │   │
│   │                          │                                        │   │
│   │                   ┌──────▼──────┐                                │   │
│   │                   │    Base     │                                │   │
│   │                   │   Queue     │                                │   │
│   │                   └─────────────┘                                │   │
│   │                          │                                        │   │
│   │         ┌────────────────┼────────────────┐                      │   │
│   │         │                │                │                      │   │
│   │  ┌──────▼──────┐  ┌──────▼──────┐  ┌──────▼──────┐              │   │
│   │  │  RateLimit  │  │   Dead      │  │   Bounded   │              │   │
│   │  │  Queue      │  │   Letter    │  │   Queue     │              │   │
│   │  └─────────────┘  └─────────────┘  └─────────────┘              │   │
│   └─────────────────────────────────────────────────────────────────┘   │
│                              │                                          │
│   ┌──────────────────────────▼──────────────────────────────────────┐   │
│   │                      Executor (执行层)                             │   │
│   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │   │
│   │  │   Worker    │  │   Worker    │  │   Callback   │              │   │
│   │  │   Pool      │  │   Group     │  │   Chain     │              │   │
│   │  └─────────────┘  └─────────────┘  └─────────────┘              │   │
│   └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 层级职责

| 层级 | 职责 | 关键组件 |
|------|------|----------|
| **调度层** | 任务路由、优先级、延迟、链式编排 | Scheduler, PriorityScheduler, Chain |
| **队列层** | 任务存储、排序、重试、死信 | PriorityQueue, RetryQueue, DeadLetterQueue |
| **执行层** | 任务执行、Worker 管理、限流熔断 | WorkerPool, Worker, Limiter, CircuitBreaker |

---

## 3. 包结构

```
github.com/shengyanli1982/karta/
├── karta/                    # 原有包 (保持兼容)
│   ├── group.go
│   ├── pipeline.go
│   └── ...
│
├── scheduler/                # 🆕 调度层 (核心调度逻辑)
│   ├── scheduler.go         # 调度器核心接口
│   ├── priority.go          # 优先级调度器
│   ├── delay.go             # 延迟调度器
│   ├── chain.go             # 链式依赖调度器
│   └── context.go           # 调度上下文
│
├── queue/                   # 🆕 队列层 (封装 WorkQueue)
│   ├── queue.go             # 队列封装
│   ├── priority.go          # 优先级队列
│   ├── delay.go             # 延迟队列
│   ├── retry.go             # 重试队列
│   ├── deadletter.go        # 死信队列
│   └── ratelimit.go         # 限流队列
│
├── executor/                # 🆕 执行层 (Worker 池)
│   ├── executor.go          # 执行器核心接口
│   ├── worker.go            # Worker 实现
│   ├── pool.go              # Worker 池
│   ├── callback.go          # 回调机制
│   └── limiter.go           # 执行限流
│
├── workflow/                # 🆕 工作流层 (编排能力)
│   ├── workflow.go          # 工作流定义
│   ├── step.go              # 步骤定义
│   ├── chain.go             # 链式执行
│   ├── dag.go               # DAG 编排 (简化版)
│   └── builder.go            # 工作流构建器
│
└── internal/                # 内部工具
    ├── types.go             # 通用类型
    └── utils.go             # 工具函数
```

---

## 4. 核心接口设计

### 4.1 Task 抽象

```go
// Task 代表一个可执行的任务单元
type Task interface {
    // ID 返回任务的唯一标识
    ID() string
    
    // Type 返回任务类型，用于路由
    Type() string
    
    // Payload 返回任务携带的数据
    Payload() any
    
    // Execute 执行任务，返回结果和错误
    Execute(ctx context.Context) (any, error)
    
    // Metadata 返回任务的元数据
    Metadata() TaskMetadata
}

// TaskMetadata 任务元数据
type TaskMetadata struct {
    Priority    int64         // 优先级 (数值越小优先级越高)
    Delay      time.Duration  // 延迟执行时间
    Timeout    time.Duration  // 超时时间
    RetryPolicy RetryPolicy   // 重试策略
    MaxRetries int            // 最大重试次数
    NextTask   Task           // 下一步任务 (用于链式)
}

// RetryPolicy 重试策略接口
type RetryPolicy = interface {
    // OnFailure 当任务失败时被调用
    // 返回: 是否可重试, 下次延迟时间
    OnFailure(attempts int, err error) (bool, time.Duration)
}

// ExponentialBackoff 指数退避重试策略
type ExponentialBackoff struct {
    BaseDelay  time.Duration
    MaxDelay   time.Duration
    MaxRetries int
    Multiplier float64
}

func (p *ExponentialBackoff) OnFailure(attempts int, err error) (bool, time.Duration) {
    if attempts >= p.MaxRetries {
        return false, 0
    }
    
    delay := float64(p.BaseDelay) * math.Pow(p.Multiplier, float64(attempts-1))
    if delay > float64(p.MaxDelay) {
        delay = float64(p.MaxDelay)
    }
    
    return true, time.Duration(delay)
}
```

### 4.2 调度器接口

```go
// Scheduler 调度器接口
type Scheduler = interface {
    // Submit 提交任务
    Submit(task Task) error
    
    // SubmitWithPriority 提交带优先级的任务
    SubmitWithPriority(priority int64, task Task) error
    
    // SubmitAfter 延迟提交任务
    SubmitAfter(task Task, delay time.Duration) error
    
    // SubmitChain 提交任务链
    SubmitChain(tasks ...Task) (Chain, error)
    
    // Len 返回队列长度
    Len() int
    
    // Shutdown 关闭调度器
    Shutdown() error
}

// Chain 任务链接口
type Chain = interface {
    // Then 添加下一步任务 (串行)
    Then(task Task) Chain
    
    // Wait 等待链执行完成
    Wait() ([]any, error)
    
    // Result 获取链的执行结果
    Result() []any
}
```

### 4.3 执行器接口

```go
// Executor 执行器接口
type Executor = interface {
    // Execute 执行任务
    Execute(ctx context.Context, task Task) (any, error)
    
    // Submit 提交任务到执行队列
    Submit(task Task) error
    
    // WorkerNumber 返回工作协程数
    WorkerNumber() int
    
    // SetWorkerNumber 设置工作协程数
    SetWorkerNumber(n int) error
    
    // Shutdown 关闭执行器
    Shutdown() error
}

// Worker Worker 接口
type Worker = interface {
    // ID 返回 Worker ID
    ID() int
    
    // Start 启动 Worker
    Start()
    
    // Stop 停止 Worker
    Stop()
    
    // IsRunning 返回是否在运行
    IsRunning() bool
}
```

### 4.4 回调机制

```go
// TaskCallback 任务生命周期回调
type TaskCallback = interface {
    // OnSubmit 任务提交时调用
    OnSubmit(task Task)
    
    // OnStart 任务开始执行时调用
    OnStart(workerID int, task Task)
    
    // OnComplete 任务完成时调用 (成功或失败)
    OnComplete(workerID int, task Task, result any, err error)
    
    // OnRetry 任务重试时调用
    OnRetry(task Task, attempts int, err error)
    
    // OnDeadLetter 任务进入死信队列时调用
    OnDeadLetter(task Task, err error)
}

// CallbackChain 回调链，支持多个回调组合
type CallbackChain []TaskCallback

func (cc CallbackChain) OnSubmit(task Task) {
    for _, cb := range cc {
        cb.OnSubmit(task)
    }
}
```

---

## 5. 队列层设计

### 5.1 队列类型

```
Queue (队列层)
├── BaseQueue (基于 WorkQueue)
│   ├── PriorityQueue (优先级队列)
│   ├── DelayingQueue (延迟队列)
│   ├── RetryQueue (重试队列)
│   │   └── RetryPolicy (可插拔重试策略)
│   ├── DeadLetterQueue (死信队列)
│   ├── RateLimitingQueue (限流队列)
│   └── BoundedQueue (有界阻塞队列)
```

### 5.2 优先级队列实现

```go
// queue/priority.go
package queue

import (
    "container/heap"
    "sync"
    wq "github.com/shengyanli1982/workqueue/v2"
)

// PriorityItem 优先级队列元素
type PriorityItem struct {
    Value    interface{}
    Priority int64
    Index    int // heap.Interface 要求
}

// PriorityQueue 基于 container/heap 的优先级队列
type PriorityQueue struct {
    items []PriorityItem
    mu    sync.RWMutex
    wq    wq.Queue // 底层队列
}

func (pq *PriorityQueue) Len() int {
    pq.mu.RLock()
    defer pq.mu.RUnlock()
    return len(pq.items)
}

func (pq *PriorityQueue) Less(i, j int) bool {
    pq.mu.RLock()
    defer pq.mu.RUnlock()
    return pq.items[i].Priority < pq.items[j].Priority
}

func (pq *PriorityQueue) Swap(i, j int) {
    pq.mu.Lock()
    defer pq.mu.Unlock()
    pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
    pq.items[i].Index = i
    pq.items[j].Index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
    pq.mu.Lock()
    defer pq.mu.Unlock()
    item := x.(PriorityItem)
    item.Index = len(pq.items)
    pq.items = append(pq.items, item)
    heap.Fix(pq, len(pq.items)-1)
}

func (pq *PriorityQueue) Pop() interface{} {
    pq.mu.Lock()
    defer pq.mu.Unlock()
    n := len(pq.items)
    if n == 0 {
        return nil
    }
    item := pq.items[n-1]
    pq.items = pq.items[:n-1]
    return item
}

// PutWithPriority 添加带优先级的元素
func (pq *PriorityQueue) PutWithPriority(value interface{}, priority int64) error {
    pq.Push(PriorityItem{Value: value, Priority: priority})
    return nil
}

// Get 取出最高优先级元素
func (pq *PriorityQueue) Get() (interface{}, error) {
    item := pq.Pop()
    if item == nil {
        return nil, ErrQueueEmpty
    }
    return item.(PriorityItem).Value, nil
}
```

### 5.3 重试队列实现

```go
// queue/retry.go
package queue

import (
    "sync"
    "time"
    
    "github.com/shengyanli1982/workqueue/v2"
)

// RetryQueue 带重试能力的队列
type RetryQueue struct {
    wq       wq.Queue
    policy   RetryPolicy
    attempts sync.Map // value -> attempts count
    mu       sync.RWMutex
}

var (
    ErrRetryExhausted = errors.New("retry exhausted")
)

// RetryPolicy 重试策略接口
type RetryPolicy = interface {
    OnFailure(attempts int, err error) (canRetry bool, nextDelay time.Duration)
}

// ExponentialBackoff 指数退避重试策略
type ExponentialBackoff struct {
    BaseDelay  time.Duration
    MaxDelay   time.Duration
    MaxRetries int
    Multiplier float64
}

func (p *ExponentialBackoff) OnFailure(attempts int, err error) (bool, time.Duration) {
    if attempts >= p.MaxRetries {
        return false, 0
    }
    
    delay := float64(p.BaseDelay) * math.Pow(p.Multiplier, float64(attempts-1))
    if delay > float64(p.MaxDelay) {
        delay = float64(p.MaxDelay)
    }
    
    return true, time.Duration(delay)
}

// Put 添加任务，失败时自动重试
func (rq *RetryQueue) Put(value interface{}) error {
    return rq.wq.Put(value)
}

// Done 标记任务完成，重试计数清除
func (rq *RetryQueue) Done(value interface{}) {
    rq.attempts.Delete(value)
    rq.wq.Done(value)
}

// DoneWithRetry 标记任务失败，触发重试逻辑
func (rq *RetryQueue) DoneWithRetry(value interface{}, err error) error {
    // 获取当前重试次数
    attempts, _ := rq.attempts.LoadOrStore(value, 0).(int)
    attempts++
    rq.attempts.Store(value, attempts)
    
    // 调用重试策略
    canRetry, delay := rq.policy.OnFailure(attempts, err)
    
    if canRetry {
        // 延迟重新入队
        rq.wq.Done(value)
        rq.wq.PutWithDelay(value, delay.Milliseconds())
        return nil
    }
    
    // 不可重试，发送到死信队列或直接丢弃
    rq.wq.Done(value)
    return ErrRetryExhausted
}
```

### 5.4 死信队列实现

```go
// queue/deadletter.go
package queue

// DeadLetter 死信结构
type DeadLetter struct {
    ID        string
    Payload   interface{}
    Attempts  int
    LastError string
    FailedAt  time.Time
    Meta      map[string]string
}

// DeadLetterQueue 死信队列
type DeadLetterQueue struct {
    items []DeadLetter
    mu    sync.RWMutex
}

// Put 添加死信
func (dlq *DeadLetterQueue) Put(letter *DeadLetter) error {
    dlq.mu.Lock()
    defer dlq.mu.Unlock()
    letter.ID = generateID()
    letter.FailedAt = time.Now()
    dlq.items = append(dlq.items, *letter)
    return nil
}

// Get 获取死信
func (dlq *DeadLetterQueue) Get() (*DeadLetter, error) {
    dlq.mu.Lock()
    defer dlq.mu.Unlock()
    if len(dlq.items) == 0 {
        return nil, ErrDeadLetterEmpty
    }
    letter := dlq.items[0]
    dlq.items = dlq.items[1:]
    return &letter, nil
}

// Ack 确认死信已处理
func (dlq *DeadLetterQueue) Ack(letter *DeadLetter) error {
    return nil
}

// Requeue 重新入队到目标队列
func (dlq *DeadLetterQueue) Requeue(letter *DeadLetter, target Queue) error {
    return target.Put(letter.Payload)
}
```

---

## 6. 执行层设计

### 6.1 Worker 池架构

```
┌─────────────────────────────────────────────────────────────┐
│                        WorkerPool                           │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐   │
│   │                    TaskQueue                         │   │
│   │            (待执行任务队列)                          │   │
│   └─────────────────────┬───────────────────────────────┘   │
│                         │                                     │
│          ┌──────────────┼──────────────┐                   │
│          │              │              │                     │
│          ▼              ▼              ▼                   │
│   ┌───────────┐  ┌───────────┐  ┌───────────┐           │
│   │  Worker 1 │  │  Worker 2 │  │  Worker N │           │
│   │  (执行中)  │  │  (执行中)  │  │  (空闲)    │           │
│   └───────────┘  └───────────┘  └───────────┘           │
│                                                             │
│   动态伸缩: 根据负载自动增减 Worker 数量                    │
│   限流保护: 防止 Worker 爆炸性创建                          │
└─────────────────────────────────────────────────────────────┘
```

### 6.2 Worker 实现

```go
// executor/worker.go
package executor

// Worker 工作协程
type Worker struct {
    id        int
    pool      *WorkerPool
    taskQueue chan Task
    ctx       context.Context
    cancel    context.CancelFunc
    running   atomic.Bool
}

func (w *Worker) Start() {
    if w.running.Load() {
        return
    }
    w.running.Store(true)
    
    go func() {
        defer w回收资源()
        for {
            select {
            case <-w.ctx.Done():
                return
            case task := <-w.taskQueue:
                w.executeTask(task)
            }
        }
    }()
}

func (w *Worker) executeTask(task Task) {
    start := time.Now()
    
    // 触发回调: OnStart
    w.pool.callback.OnStart(w.id, task)
    
    // 执行任务
    result, err := w.doExecute(task)
    
    elapsed := time.Since(start)
    
    metadata := task.Metadata()
    if metadata.Timeout > 0 && elapsed > metadata.Timeout {
        err = ErrTaskTimeout
    }
    
    // 触发回调: OnComplete
    w.pool.callback.OnComplete(w.id, task, result, err)
    
    // 处理错误和重试
    if err != nil {
        w.handleError(task, err)
    }
}

func (w *Worker) doExecute(task Task) (any, error) {
    ctx := w.ctx
    metadata := task.Metadata()
    if metadata.Timeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, metadata.Timeout)
        defer cancel()
    }
    
    return task.Execute(ctx)
}

func (w *Worker) Stop() {
    w.running.Store(false)
    w.cancel()
}
```

### 6.3 Worker 池实现

```go
// executor/pool.go
package executor

// WorkerPool 工作池
type WorkerPool struct {
    // 配置
    minWorkers    int
    maxWorkers    int
    maxQueueSize  int
    spawnRate     rate.Limiter
    
    // 状态
    workers       map[int]*Worker
    workerIDGen   int64
    runningCount  atomic.Int64
    taskQueue     chan Task
    
    // 同步
    mu            sync.Mutex
    wg            sync.WaitGroup
    ctx           context.Context
    cancel        context.CancelFunc
    
    // 回调
    callback      CallbackChain
}

// NewWorkerPool 创建工作池
func NewWorkerPool(opts ...PoolOption) *WorkerPool {
    pool := &WorkerPool{
        minWorkers:   2,
        maxWorkers:   100,
        maxQueueSize: 10000,
        workers:      make(map[int]*Worker),
        taskQueue:    make(chan Task, 10000),
        callback:     CallbackChain{},
    }
    
    for _, opt := range opts {
        opt(pool)
    }
    
    pool.ctx, pool.cancel = context.WithCancel(context.Background())
    
    for i := 0; i < pool.minWorkers; i++ {
        pool.spawnWorker()
    }
    
    go pool.dynamicScaler()
    
    return pool
}

// PoolOption Pool 配置选项
type PoolOption func(*WorkerPool)

func WithMinWorkers(n int) PoolOption {
    return func(p *WorkerPool) { p.minWorkers = n }
}

func WithMaxWorkers(n int) PoolOption {
    return func(p *WorkerPool) { p.maxWorkers = n }
}

func WithMaxQueueSize(n int) PoolOption {
    return func(p *WorkerPool) { p.maxQueueSize = n }
}

// Submit 提交任务
func (p *WorkerPool) Submit(task Task) error {
    p.tryScaleUp()
    
    select {
    case p.taskQueue <- task:
        return nil
    case <-p.ctx.Done():
        return ErrPoolShutdown
    default:
        return ErrQueueFull
    }
}

// dynamicScaler 动态调度器
func (p *WorkerPool) dynamicScaler() {
    ticker := time.NewTicker(3 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-p.ctx.Done():
            return
        case <-ticker.C:
            p.rebalance()
        }
    }
}

// rebalance 重新平衡 Worker 数量
func (p *WorkerPool) rebalance() {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    queueLen := len(p.taskQueue)
    running := p.runningCount.Load()
    
    if queueLen > 100 && running < int64(p.maxWorkers) {
        p.spawnWorker()
        return
    }
    
    if queueLen == 0 && running > int64(p.minWorkers) {
        p.scaleDown()
    }
}
```

### 6.4 熔断器实现

```go
// executor/limiter.go

// CircuitBreaker 熔断器
type CircuitBreaker struct {
    name             string
    failureThreshold int
    resetTimeout     time.Duration
    halfOpenMax      int
    
    state         atomic.Int64 // 0=closed, 1=open, 2=half-open
    failures       atomic.Int64
    successes      atomic.Int64
    lastFailure    atomic.Int64
}

const (
    StateClosed   int64 = 0
    StateOpen     int64 = 1
    StateHalfOpen int64 = 2
)

// Allow 检查是否允许执行
func (cb *CircuitBreaker) Allow() bool {
    switch cb.state.Load() {
    case StateClosed:
        return true
    case StateOpen:
        if time.Since(time.UnixMilli(cb.lastFailure.Load())) > cb.resetTimeout {
            cb.state.Store(StateHalfOpen)
            cb.successes.Store(0)
            return true
        }
        return false
    case StateHalfOpen:
        return true
    }
    return false
}

// RecordSuccess 记录成功
func (cb *CircuitBreaker) RecordSuccess() {
    if cb.state.Load() == StateHalfOpen {
        if cb.successes.Add(1) >= int64(cb.halfOpenMax) {
            cb.state.Store(StateClosed)
            cb.failures.Store(0)
        }
    }
}

// RecordFailure 记录失败
func (cb *CircuitBreaker) RecordFailure() {
    cb.lastFailure.Store(time.Now().UnixMilli())
    
    if cb.state.Load() == StateHalfOpen {
        cb.state.Store(StateOpen)
        return
    }
    
    if cb.failures.Add(1) >= int64(cb.failureThreshold) {
        cb.state.Store(StateOpen)
    }
}
```

---

## 7. 调度器设计

### 7.1 调度器实现

```go
// scheduler/scheduler.go
package scheduler

// Scheduler 调度器
type Scheduler struct {
    queue         queue.Queue
    executor      executor.Executor
    callbacks     CallbackChain
    chainExecutor *ChainExecutor
    mu            sync.Mutex
    closed        bool
    ctx           context.Context
    cancel        context.CancelFunc
}

// NewScheduler 创建调度器
func NewScheduler(q queue.Queue, exec executor.Executor) *Scheduler {
    ctx, cancel := context.WithCancel(context.Background())
    
    s := &Scheduler{
        queue:         q,
        executor:      exec,
        callbacks:     CallbackChain{},
        chainExecutor: NewChainExecutor(exec),
        ctx:           ctx,
        cancel:        cancel,
    }
    
    go s.run()
    
    return s
}

// Submit 提交任务
func (s *Scheduler) Submit(task Task) error {
    s.mu.Lock()
    if s.closed {
        s.mu.Unlock()
        return ErrSchedulerClosed
    }
    s.mu.Unlock()
    
    s.callbacks.OnSubmit(task)
    
    if task.Metadata().Delay > 0 {
        return s.queue.PutWithDelay(task, task.Metadata().Delay.Milliseconds())
    }
    return s.queue.Put(task)
}

// SubmitWithPriority 提交带优先级的任务
func (s *Scheduler) SubmitWithPriority(priority int64, task Task) error {
    task.Metadata().Priority = priority
    return s.Submit(task)
}

// SubmitAfter 延迟提交
func (s *Scheduler) SubmitAfter(task Task, delay time.Duration) error {
    task.Metadata().Delay = delay
    return s.Submit(task)
}

// SubmitChain 提交任务链
func (s *Scheduler) SubmitChain(tasks ...Task) (Chain, error) {
    return s.chainExecutor.SubmitChain(tasks...)
}

// Len 返回队列长度
func (s *Scheduler) Len() int {
    return s.queue.Len()
}

// Shutdown 关闭调度器
func (s *Scheduler) Shutdown() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.closed {
        return nil
    }
    
    s.closed = true
    s.cancel()
    s.queue.Shutdown()
    s.executor.Shutdown()
    
    return nil
}

// run 队列消费循环
func (s *Scheduler) run() {
    for {
        select {
        case <-s.ctx.Done():
            return
        default:
            element, err := s.queue.Get()
            if err != nil {
                continue
            }
            
            task := element.(Task)
            s.queue.Done(element)
            
            s.executor.Submit(task)
        }
    }
}
```

### 7.2 链式执行器

```go
// scheduler/chain.go
package scheduler

// Chain 任务链
type Chain struct {
    steps    []Task
    executor executor.Executor
    results  []any
    mu       sync.Mutex
    wg       sync.WaitGroup
    ctx      context.Context
    cancel   context.CancelFunc
}

// SubmitChain 提交任务链
func (s *Scheduler) SubmitChain(tasks ...Task) (Chain, error) {
    if len(tasks) == 0 {
        return nil, ErrEmptyChain
    }
    
    chain := &Chain{
        steps:    tasks,
        executor: s.executor,
        results:  make([]any, len(tasks)),
        ctx:      context.Background(),
    }
    
    for i := 0; i < len(tasks)-1; i++ {
        tasks[i].Metadata().NextTask = tasks[i+1]
    }
    
    return chain, nil
}

// Then 添加下一步到链
func (c *Chain) Then(task Task) Chain {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if len(c.steps) > 0 {
        c.steps[len(c.steps)-1].Metadata().NextTask = task
    }
    
    c.steps = append(c.steps, task)
    return c
}

// Wait 等待链执行完成
func (c *Chain) Wait() ([]any, error) {
    c.wg.Add(len(c.steps))
    
    go c.executeStep(0, nil)
    
    done := make(chan struct{})
    go func() {
        c.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        return c.results, nil
    case <-c.ctx.Done():
        return c.results, c.ctx.Err()
    }
}

// executeStep 执行步骤
func (c *Chain) executeStep(index int, input any) {
    defer c.wg.Done()
    
    if index >= len(c.steps) {
        return
    }
    
    task := c.steps[index]
    
    result, err := c.executor.Execute(c.ctx, task)
    if err != nil {
        c.handleError(index, err)
        return
    }
    
    c.mu.Lock()
    c.results[index] = result
    c.mu.Unlock()
    
    c.wg.Add(1)
    go c.executeStep(index+1, result)
}
```

---

## 8. 工作流设计

### 8.1 工作流抽象

```go
// workflow/workflow.go
package workflow

// Step 代表工作流中的一个步骤
type Step struct {
    ID          string
    Name        string
    Task        scheduler.Task
    OnSuccess   NextStep
    OnFailure   ErrorHandler
    RetryPolicy scheduler.RetryPolicy
}

// NextStep 下一步执行策略
type NextStep interface {
    Next(result any) ([]*Step, error)
}

// ChainNext 串行链式下一步
type ChainNext struct {
    next *Step
}

func (c *ChainNext) Next(result any) ([]*Step, error) {
    return []*Step{c.next}, nil
}

// ParallelNext 并行下一步
type ParallelNext struct {
    steps []*Step
}

func (p *ParallelNext) Next(result any) ([]*Step, error) {
    return p.steps, nil
}

// ErrorHandler 错误处理策略
type ErrorHandler interface {
    Handle(err error) (retry bool, next *Step)
}

// DefaultErrorHandler 默认错误处理
type DefaultErrorHandler struct{}

func (h *DefaultErrorHandler) Handle(err error) (bool, *Step) {
    return false, nil
}

// Workflow 工作流
type Workflow struct {
    ID        string
    Name      string
    Steps     []*Step
    Timeout   time.Duration
    onStart   Callback
    onComplete Callback
}

// New 创建工作流
func New(id, name string) *Workflow {
    return &Workflow{
        ID:    id,
        Name:  name,
        Steps: make([]*Step, 0),
    }
}

// AddStep 添加步骤
func (w *Workflow) AddStep(step *Step) *Workflow {
    w.Steps = append(w.Steps, step)
    return w
}
```

### 8.2 工作流执行器

```go
// workflow/executor.go
package workflow

// Executor 工作流执行器
type Executor struct {
    scheduler scheduler.Scheduler
    executor  executor.Executor
    callbacks CallbackChain
}

// NewExecutor 创建工作流执行器
func NewExecutor(sched scheduler.Scheduler, exec executor.Executor) *Executor {
    return &Executor{
        scheduler: sched,
        executor:  exec,
        callbacks: CallbackChain{},
    }
}

// Execute 执行工作流
func (e *Executor) Execute(w *Workflow) (*Result, error) {
    ctx, cancel := context.WithTimeout(context.Background(), w.Timeout)
    defer cancel()
    
    result := &Result{
        WorkflowID: w.ID,
        StartTime:  time.Now(),
    }
    
    err := e.executeStep(ctx, w.Steps[0], nil)
    if err != nil {
        result.Error = err
        result.Status = StatusFailed
    } else {
        result.Status = StatusCompleted
    }
    
    result.EndTime = time.Now()
    return result, nil
}

// Result 工作流执行结果
type Result struct {
    WorkflowID string
    Status     Status
    Output     any
    Error      error
    StartTime  time.Time
    EndTime    time.Time
    Steps      []*StepResult
}

// Status 工作流状态
type Status string

const (
    StatusPending    Status = "pending"
    StatusRunning    Status = "running"
    StatusCompleted  Status = "completed"
    StatusFailed     Status = "failed"
    StatusCancelled  Status = "cancelled"
)
```

---

## 9. 使用示例

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "github.com/shengyanli1982/karta/queue"
    "github.com/shengyanli1982/karta/scheduler"
    "github.com/shengyanli1982/karta/executor"
    "github.com/shengyanli1982/karta/workflow"
    "github.com/shengyanli1982/workqueue/v2"
)

func main() {
    // 1. 创建队列
    q := queue.NewPriorityQueue(workqueue.NewQueue(nil))
    
    // 2. 创建执行器
    exec := executor.NewWorkerPool(
        executor.WithMinWorkers(2),
        executor.WithMaxWorkers(10),
    )
    
    // 3. 创建调度器
    sched := scheduler.NewScheduler(q, exec)
    
    // 4. 创建工作流
    wf := workflow.New("download-process-upload").
        AddStep(workflow.NewStep("download", downloadTask, nil)).
        AddStep(workflow.NewStep("process", processTask, nil)).
        AddStep(workflow.NewStep("upload", uploadTask, nil))
    
    // 5. 执行工作流
    result, err := workflow.NewExecutor(sched, exec).Execute(wf)
    if err != nil {
        fmt.Printf("Workflow failed: %v\n", err)
        return
    }
    
    fmt.Printf("Workflow completed: %v\n", result)
}

// Task 定义
func downloadTask(ctx context.Context, payload any) (any, error) {
    fmt.Println("Downloading...")
    time.Sleep(100 * time.Millisecond)
    return "downloaded_data", nil
}

func processTask(ctx context.Context, payload any) (any, error) {
    fmt.Println("Processing...")
    time.Sleep(100 * time.Millisecond)
    return "processed_data", nil
}

func uploadTask(ctx context.Context, payload any) (any, error) {
    fmt.Println("Uploading...")
    time.Sleep(100 * time.Millisecond)
    return "uploaded", nil
}
```

---

## 10. 实施路径

### 10.1 阶段划分

| 阶段 | 内容 | 优先级 | 工作量 |
|------|------|--------|--------|
| **Phase 1** | 基础框架、Task 抽象、Worker 池 | P0 | 1-2 周 |
| **Phase 2** | 优先级队列、重试队列、死信队列 | P0 | 1 周 |
| **Phase 3** | 调度器、链式依赖、工作流基础 | P1 | 1-2 周 |
| **Phase 4** | 限流、熔断、回调机制 | P1 | 1 周 |
| **Phase 5** | 完善文档、测试、性能优化 | P2 | 持续 |

### 10.2 核心优势总结

| 特性 | 实现 | 价值 |
|------|------|------|
| **三层解耦** | Scheduler/Queue/Executor 分离 | 可独立替换、测试 |
| **Task 抽象** | 统一任务接口 | 不同任务类型统一管理 |
| **链式依赖** | `SubmitChain().Then()` | 简化串行任务编排 |
| **优先级调度** | 基于 container/heap | 紧急任务优先 |
| **重试机制** | 可插拔 RetryPolicy | 灵活的错误恢复 |
| **死信队列** | 失败任务隔离 | 可追溯、可处理 |
| **动态 Worker** | 自动扩缩容 | 资源高效利用 |
| **熔断保护** | CircuitBreaker | 防止故障传播 |

---

**文档版本**: v1.0  
**批准状态**: 已批准  
**下次审查**: 待定
