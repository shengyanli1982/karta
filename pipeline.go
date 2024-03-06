package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// / 立即执行的常量，时间长度为0
// Constant for immediate execution, time duration is 0
const immediate = time.Duration(0)

var (
	// 错误类型，表示管道已经关闭
	// Error type, indicating that the pipeline is closed
	ErrorQueueClosed = errors.New("pipeline is closed")
)

var (
	// 默认的工作者空闲超时时间，单位为毫秒，值为10秒
	// Default worker idle timeout, in milliseconds, value is 10 seconds
	defaultWorkerIdleTimeout = (10 * time.Second).Milliseconds()

	// 默认的工作者状态扫描间隔，值为3秒
	// Default worker status scan interval, value is 3 seconds
	defaultWorkerStatusScanInterval = 3 * time.Second

	// 默认新建工作者的突发数量，值为8
	// Default burst of new workers, value is 8
	defaultNewWorkersBurst = 8

	// 默认每秒新建工作者的数量，值为4
	// Default number of new workers per second, value is 4
	defaultNewWorkersPerSecond = 4
)

// Pipeline 是一个结构体，表示管道，用于存储和处理数据
// Pipeline is a struct that represents a pipeline, used for storing and processing data
type Pipeline struct {
	queue       DelayingQueueInterface // 用于存储和处理数据的队列 (The queue used for storing and processing data)
	config      *Config                // 管道的配置设置 (The configuration settings for the pipeline)
	wg          sync.WaitGroup         // 用于管理 goroutines 的 WaitGroup (WaitGroup for managing goroutines)
	once        sync.Once              // 用于确保初始化只执行一次的 sync.Once (Sync.Once for ensuring initialization is performed only once)
	ctx         context.Context        // 用于管理管道生命周期的 context (The context for managing the lifecycle of the pipeline)
	cancel      context.CancelFunc     // 用于取消管道的函数 (The function for canceling the pipeline)
	timer       atomic.Int64           // 用于跟踪管道计时器的原子整数 (Atomic integer for tracking the pipeline's timer)
	rc          atomic.Int64           // 用于跟踪管道资源消耗的原子整数 (Atomic integer for tracking the pipeline's resource consumption)
	elementpool *ElemmentExtPool       // 用于管理扩展元素的池 (The pool for managing extended elements)
	wlimit      *rate.Limiter          // 用于创建新工作者的资源速率限制 (The resource rate limit for creating new workers)
}

// NewPipeline 是一个函数，它创建并返回一个新的 Pipeline
// NewPipeline is a function, it creates and returns a new Pipeline
func NewPipeline(queue DelayingQueueInterface, conf *Config) *Pipeline {
	// 如果队列为 nil，返回 nil
	// If the queue is nil, return nil
	if queue == nil {
		return nil
	}

	// 检查配置是否有效，如果无效则返回一个默认的配置
	// Check if the configuration is valid, if not, return a default configuration
	conf = isConfigValid(conf)

	// 创建一个新的 Pipeline
	// Create a new Pipeline
	pl := Pipeline{
		queue:       queue,
		config:      conf,
		wg:          sync.WaitGroup{},
		once:        sync.Once{},
		timer:       atomic.Int64{},
		rc:          atomic.Int64{},
		elementpool: NewElementExtPool(),
		wlimit:      rate.NewLimiter(rate.Limit(defaultNewWorkersPerSecond), defaultNewWorkersBurst),
	}

	// 创建一个新的 context，并设置取消函数
	// Create a new context and set the cancel function
	pl.ctx, pl.cancel = context.WithCancel(context.Background())

	// 设置管道的计时器
	// Set the pipeline's timer
	pl.timer.Store(time.Now().UnixMilli())

	// 启动一个工作者
	// Start a worker
	pl.rc.Store(1)
	pl.wg.Add(1)
	go pl.executor()

	// 启动一个时间定时器
	// Start a time timer
	pl.wg.Add(1)
	go pl.updateTimer()

	// 返回新创建的 Pipeline
	// Return the newly created Pipeline
	return &pl
}

// Stop 是 Pipeline 的一个方法，它用于停止管道
// Stop is a method of Pipeline, it is used to stop the pipeline
func (pl *Pipeline) Stop() {
	// 使用 sync.Once 确保管道只被停止一次
	// Use sync.Once to ensure that the pipeline is stopped only once
	pl.once.Do(func() {
		// 调用 context 的 cancel 函数，发送取消信号
		// Call the cancel function of context to send a cancellation signal
		pl.cancel()

		// 等待所有的工作者完成
		// Wait for all workers to complete
		pl.wg.Wait()

		// 停止工作队列
		// Stop the work queue
		pl.queue.Stop()
	})
}

// executor 是 Pipeline 的一个方法，它执行工作管道中的任务
// executor is a method of Pipeline, it executes tasks in the work pipeline
func (pl *Pipeline) executor() {
	// 获取任务执行的启动时间点
	// Get the start time of task execution
	updateAt := pl.timer.Load()

	// 启动空闲超时定时器，每隔一段时间检查一次
	// Start the idle timeout timer, check every once in a while
	ticker := time.NewTicker(defaultWorkerStatusScanInterval)

	// 当函数退出时，减少工作者数量，减少 WaitGroup 的计数，并关闭定时器
	// When the function exits, reduce the number of workers, decrease the count of WaitGroup, and close the timer
	defer func() {
		pl.rc.Add(-1)
		pl.wg.Done()
		ticker.Stop()
	}()

	// 循环执行，直到收到 context 的 Done 信号
	// Loop execution until the Done signal from the context is received
	for {
		select {
		case <-pl.ctx.Done():
			// 收到 Done 信号，返回，结束执行
			// Received the Done signal, return, end execution
			return

		case <-ticker.C:
			// 如果空闲超时，判断当前工作者数量是否超过最小工作者数量，如果超过则返回
			// If idle timeout, judge whether the current number of workers exceeds the minimum number of workers, if it exceeds, return
			if pl.timer.Load()-updateAt >= defaultWorkerIdleTimeout && pl.rc.Load() > defaultMinWorkerNum {
				return
			}

		default:
			// 如果工作管道已经关闭，则返回
			// If the work pipeline is closed, return
			if pl.queue.IsClosed() {
				return
			}

			// 从工作管道中获取一个扩展元素
			// Get an extended element from the work pipeline
			o, err := pl.queue.Get()
			if err != nil {
				break
			}

			// 标记工作管道完成
			// Mark the work pipeline as completed
			pl.queue.Done(o)

			// 数据类型转换，将获取的元素转换为扩展元素
			// Data type conversion, convert the obtained element to an extended element
			element := o.(*ElementExt)

			// 获取数据
			// Get data
			data := element.GetData()

			// 执行回调函数 OnBefore
			// Execute the callback function OnBefore
			pl.config.callback.OnBefore(data)

			// 获取消息处理函数
			// Get the message handling function
			handleFunc := element.GetHandleFunc()

			// 如果指定的处理函数不为 nil，则执行该处理函数，否则执行配置中的处理函数
			// If the specified processing function is not nil, execute this processing function, otherwise execute the processing function in the configuration
			var result any
			if handleFunc != nil {
				result, err = handleFunc(data)
			} else {
				result, err = pl.config.handleFunc(data)
			}

			// 执行回调函数 OnAfter
			// Execute the callback function OnAfter
			pl.config.callback.OnAfter(data, result, err)

			// 将扩展元素放回对象池
			// Put the extended element back into the object pool
			pl.elementpool.Put(element)

			// 更新任务执行的时间点
			// Update the time point of task execution
			updateAt = pl.timer.Load()
		}
	}
}

// submit 是 Pipeline 的一个方法，它提交一个任务到管道中，可以指定处理函数和延迟时间
// submit is a method of Pipeline, it submits a task to the pipeline, and you can specify the processing function and delay time
func (pl *Pipeline) submit(fn MessageHandleFunc, msg any, delay time.Duration) error {
	// 如果管道已经关闭，则返回错误
	// If the pipeline is closed, return an error
	if pl.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// 从对象池中获取一个扩展元素
	// Get an extended element from the object pool
	element := pl.elementpool.Get()
	element.SetData(msg)
	element.SetHandleFunc(fn)

	// 将扩展元素添加到工作管道中, 如果延迟大于 0, 则添加到延迟队列中
	// Add the extended element to the work pipeline, if the delay is greater than 0, add it to the delay queue
	var err error
	if delay > 0 {
		err = pl.queue.AddAfter(element, delay)
	} else {
		err = pl.queue.Add(element)
	}
	if err != nil {
		// 如果添加失败，则将扩展元素放回对象池
		// If the addition fails, put the extended element back into the object pool
		pl.elementpool.Put(element)
		return err
	}

	// 判断当前工作管道中的任务数量是否超足够, 如果不足则启动一个新的工作者
	// Determine whether the number of tasks in the current work pipeline is sufficient, if not, start a new worker
	if int64(pl.config.num) > pl.rc.Load() && pl.wlimit.Allow() {
		pl.rc.Add(1)
		pl.wg.Add(1)
		go pl.executor()
	}

	// 正确执行，返回 nil
	// Execute correctly, return nil
	return nil
}

// SubmitWithFunc 是 Pipeline 的一个方法，它提交一个带有自定义处理函数的任务
// SubmitWithFunc is a method of Pipeline, it submits a task with a custom processing function
func (pl *Pipeline) SubmitWithFunc(fn MessageHandleFunc, msg any) error {
	return pl.submit(fn, msg, immediate)
}

// Submit 是 Pipeline 的一个方法，它提交一个任务
// Submit is a method of Pipeline, it submits a task
func (pl *Pipeline) Submit(msg any) error {
	return pl.SubmitWithFunc(nil, msg)
}

// SubmitAfterWithFunc 是 Pipeline 的一个方法，它在指定的延迟时间后提交一个带有自定义处理函数的任务
// SubmitAfterWithFunc is a method of Pipeline, it submits a task with a custom processing function after a specified delay time
func (pl *Pipeline) SubmitAfterWithFunc(fn MessageHandleFunc, msg any, delay time.Duration) error {
	return pl.submit(fn, msg, delay)
}

// SubmitAfter 是 Pipeline 的一个方法，它在指定的延迟时间后提交一个任务
// SubmitAfter is a method of Pipeline, it submits a task after a specified delay time
func (pl *Pipeline) SubmitAfter(msg any, delay time.Duration) error {
	return pl.SubmitAfterWithFunc(nil, msg, delay)
}

// / updateTimer 是 Pipeline 的一个方法，它是一个定时器，用于更新 Pipeline 的时间戳
// updateTimer is a method of Pipeline, it is a timer that updates the timestamp of Pipeline
func (pl *Pipeline) updateTimer() {
	// 创建一个新的定时器，每秒触发一次
	// Create a new timer that triggers every second
	ticker := time.NewTicker(time.Second)

	// 使用 defer 来确保在函数结束时停止定时器并完成 WaitGroup 的计数
	// Use defer to ensure that the timer is stopped and the count of WaitGroup is completed when the function ends
	defer func() {
		ticker.Stop()
		pl.wg.Done()
	}()

	// 循环，直到收到 context 的 Done 信号
	// Loop until the Done signal from the context is received
	for {
		select {
		case <-pl.ctx.Done():
			// 收到 Done 信号，返回，结束定时器
			// Received the Done signal, return, end the timer
			return
		case <-ticker.C:
			// 定时器触发，更新 Pipeline 的时间戳
			// The timer is triggered, update the timestamp of Pipeline
			pl.timer.Store(time.Now().UnixMilli())
		}
	}
}

// GetWorkerNumber 是 Pipeline 的一个方法，它返回当前正在运行的工作者数量
// GetWorkerNumber is a method of Pipeline, it returns the number of workers currently running
func (pl *Pipeline) GetWorkerNumber() int64 {
	// 使用原子操作来获取当前正在运行的工作者数量
	// Use atomic operations to get the number of workers currently running
	return pl.rc.Load()
}
