package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shengyanli1982/karta/internal"
	"golang.org/x/time/rate"
)

// 常量定义 Constants definition
const (
	immediateDelay        = 0 // 立即执行的迟值 Immediate execution delay value
	defaultMinWorkerCount = 1 // 默认最小工作协程数 Default minimum number of worker goroutines
)

// 变量定义 Variables definition
var (
	ErrorQueueClosed          = errors.New("pipeline is closed")  // 管道关闭错误 Pipeline closed error
	defaultWorkerIdleTimeout  = (10 * time.Second).Milliseconds() // 默认工作协程空闲超时时间 Default worker idle timeout
	defaultWorkerScanInterval = 3 * time.Second                   // 默认工作协程扫描间隔 Default worker scan interval
	defaultWorkerBurstLimit   = 8                                 // 默认工作协程突发限制 Default worker burst limit
	defaultWorkerSpawnRate    = 4                                 // 默认工作协程生成速率 Default worker spawn rate
)

// Pipeline 结构体定义了一个消息处理管道
// Pipeline struct defines a message processing pipeline
type Pipeline struct {
	queue        DelayingQueue            // 延迟队列 Delaying queue
	config       *Config                  // 配置信息 Configuration
	wg           sync.WaitGroup           // 等待组 Wait group
	once         sync.Once                // 确保只执行一次 Ensure single execution
	ctx          context.Context          // 上下文 Context
	cancel       context.CancelFunc       // 取消函数 Cancel function
	timer        atomic.Int64             // 计时器 Timer
	runningCount atomic.Int64             // 运行中的工作协程数量 Number of running workers
	elementPool  *internal.ElementExtPool // 元素池 Element pool
	workerLimit  *rate.Limiter            // 工作协程限制器 Worker limiter
}

// NewPipeline creates a new pipeline instance with the given queue and configuration
// NewPipeline 使用给定的队列和配置创建一个新的管道实例
func NewPipeline(queue DelayingQueue, config *Config) *Pipeline {
	// Check if queue is nil, return nil if true
	// 检查队列是否为空，如果为空则返回 nil
	if queue == nil {
		return nil
	}

	// Validate and normalize configuration
	// 验证并规范化配置
	config = isConfigValid(config)

	// Create context with cancellation
	// 创建带有取消功能的上下���
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize pipeline instance with basic components
	// 初始化管道实例的基本组件
	pipeline := &Pipeline{
		queue:       queue,
		config:      config,
		elementPool: internal.NewElementExtPool(),
		// Create rate limiter for worker spawning with default settings
		// 使用默认设置创建工作协程生成的速率限制器
		workerLimit: rate.NewLimiter(rate.Limit(defaultWorkerSpawnRate), defaultWorkerBurstLimit),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize timer with current timestamp
	// 使用当前时间戳初始化计时器
	pipeline.timer.Store(time.Now().UnixMilli())

	// Set initial running worker count
	// 设置初始运行的工作协程数量
	pipeline.runningCount.Store(1)

	// Start background goroutines for execution and timer update
	// 启动用于执行和计时器更新的后台协程
	pipeline.wg.Add(2)
	go pipeline.executor()
	go pipeline.updateTimer()

	return pipeline
}

// Stop 停止管道的运行
// Stop stops the pipeline
func (pipeline *Pipeline) Stop() {
	pipeline.once.Do(func() {
		pipeline.cancel()
		pipeline.wg.Wait()
		pipeline.queue.Shutdown()
	})
}

// handleMessage 处理单个消息
// handleMessage 处理单个消息
func (pipeline *Pipeline) handleMessage(element *internal.ElementExt) {
	// Get message data
	// 获取消息数据
	data := element.GetData()

	// Execute callback before message processing
	// 执行消息处理前的回调函数
	pipeline.config.callback.OnBefore(data)

	var (
		result any
		err    error
	)

	// Check if there's a custom handler function, use it if exists, otherwise use default handler
	// 判断是否有自定义处理函数，如果有则使用自定义函数，否则使用默认处理函数
	if handleFunc := element.GetHandleFunc(); handleFunc != nil {
		result, err = handleFunc(data)
	} else {
		result, err = pipeline.config.handleFunc(data)
	}

	// Execute callback after message processing
	// 执行消息处理后的回调函数
	pipeline.config.callback.OnAfter(data, result, err)

	// Return the element to the pool
	// 将元素放回对象池
	pipeline.elementPool.Put(element)
}

// executor 执行器，负责处理队列中的消息
// executor 执行器，负责处理队列中的消息
func (pipeline *Pipeline) executor() {
	// Record last update time
	// 记录上次更新时间
	lastUpdateTime := pipeline.timer.Load()

	// Create state scan ticker
	// 创建状态扫描定时器
	stateScanTicker := time.NewTicker(defaultWorkerScanInterval)

	// Ensure resource cleanup and counter update
	// 确保资源清理和计数更新
	defer func() {
		pipeline.runningCount.Add(-1)
		pipeline.wg.Done()
		stateScanTicker.Stop()
	}()

	// Continue processing queue messages until queue is closed
	// 持续处理队列消息，直到队列关闭
	for !pipeline.queue.IsClosed() {
		// Get element from queue
		// 从队列获取元素
		element, err := pipeline.queue.Get()
		if err != nil {
			select {
			// Check if need to exit
			// 检查是否需要退出
			case <-pipeline.ctx.Done():
				return
			// Check worker goroutine status
			// 检查工作协程状态
			case <-stateScanTicker.C:
				// Exit if idle time exceeds threshold and running workers count is greater than minimum
				// 如果空闲时间超过阈值且运行的工作协程数量大于最小值，则退出
				if pipeline.timer.Load()-lastUpdateTime >= defaultWorkerIdleTimeout &&
					pipeline.runningCount.Load() > defaultMinWorkerCount {
					return
				}
			}
			continue
		}

		// Mark element as done
		// 标记元素已处理
		pipeline.queue.Done(element)
		// Process the message
		// 处理消息
		pipeline.handleMessage(element.(*internal.ElementExt))
		// Update last processing time
		// 更新最后处理时间
		lastUpdateTime = pipeline.timer.Load()
	}
}

// submit 提交消息到管道
// submit 提交消息到管道
func (pipeline *Pipeline) submit(handleFunc MessageHandleFunc, message any, delay int64) error {
	// Check if queue is closed
	// 检查队列是否已关闭
	if pipeline.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// Get element from object pool
	// 从对象池获取元素
	element := pipeline.elementPool.Get()
	// Set message data and handler function
	// 设置消息数据和处理函数
	element.SetData(message)
	element.SetHandleFunc(handleFunc)

	var err error
	// Choose submission method based on delay time
	// 根据延迟时间选择提交方式
	if delay > 0 {
		// Submit with delay
		// 延迟提交
		err = pipeline.queue.PutWithDelay(element, delay)
	} else {
		// Submit immediately
		// 立即提交
		err = pipeline.queue.Put(element)
	}

	// If submission fails, return element to pool
	// 如果提交失败，返回元素到对象池
	if err != nil {
		pipeline.elementPool.Put(element)
		return err
	}

	// Try to create new executor if possible
	// 如果可能，尝试创建新的执行器
	pipeline.tryCreateExecutor()

	return nil
}

// SubmitWithFunc submits a message with a custom handler function
// SubmitWithFunc 使用自定义处理函数提交消息
func (pipeline *Pipeline) SubmitWithFunc(fn MessageHandleFunc, msg any) error {
	return pipeline.submit(fn, msg, immediateDelay)
}

// Submit submits a message using the default handler function
// Submit 提交消息使用默认处理函数
func (pipeline *Pipeline) Submit(msg any) error {
	return pipeline.SubmitWithFunc(nil, msg)
}

// SubmitAfterWithFunc submits a message with delay using a custom handler function
// SubmitAfterWithFunc 延迟提交消息并使用自定义处理函数
func (pipeline *Pipeline) SubmitAfterWithFunc(fn MessageHandleFunc, msg any, delay time.Duration) error {
	return pipeline.submit(fn, msg, delay.Milliseconds())
}

// SubmitAfter submits a message with delay using the default handler function
// SubmitAfter 延迟提交消息使用默认处理函数
func (pipeline *Pipeline) SubmitAfter(msg any, delay time.Duration) error {
	return pipeline.SubmitAfterWithFunc(nil, msg, delay)
}

// updateTimer updates the pipeline timer
// updateTimer 更新管道计时器
func (pipeline *Pipeline) updateTimer() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer pipeline.wg.Done()
	for {
		select {
		case <-pipeline.ctx.Done():
			return
		case <-ticker.C:
			pipeline.timer.Store(time.Now().UnixMilli())
		}
	}
}

// GetWorkerNumber gets the current number of worker goroutines
// GetWorkerNumber 获取当前工作协程数量
func (pipeline *Pipeline) GetWorkerNumber() int64 {
	return pipeline.runningCount.Load()
}

// tryCreateExecutor checks if a new executor can be created
// tryCreateExecutor 检查是否可以创建新的执行器
func (pipeline *Pipeline) tryCreateExecutor() bool {
	// Check if current running count reaches the limit
	// 检查当前运行数量是否达到上限
	if current := pipeline.runningCount.Load(); current >= int64(pipeline.config.num) {
		return false
	}

	// Check if worker token is available
	// 检查是否能获取工作令牌
	if !pipeline.workerLimit.Allow() {
		return false
	}

	// Increment counter atomically
	// 原子操作增加计数
	newCount := pipeline.runningCount.Add(1)
	if newCount > int64(pipeline.config.num) {
		pipeline.runningCount.Add(-1)
		return false
	}

	// Create new executor
	// 创建新的执行器
	pipeline.wg.Add(1)
	go pipeline.executor()

	return true
}
