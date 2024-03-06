package karta

import (
	"context"
	"sync"
)

// 元素内存池
// Element memory pool
var elementpool = NewElementPool()

// 批量处理任务
// Batch processing task
type Group struct {
	elements []*Element         // 工作元素数组 (Array of worker elements)
	lock     sync.Mutex         // 锁 (Lock for synchronization)
	config   *Config            // 配置 (Configuration settings)
	wg       sync.WaitGroup     // 等待组 (Wait group for synchronization)
	once     sync.Once          // 用于确保某个操作只执行一次 (Ensures an operation is performed only once)
	ctx      context.Context    // 上下文 (Context for managing goroutine lifetimes)
	cancel   context.CancelFunc // 取消函数 (Function to cancel the context)
}

// 创建一个新的批量处理任务
// Create a new batch processing task
func NewGroup(conf *Config) *Group {
	// 验证配置是否有效，如果无效则返回一个默认的配置
	// Validate the configuration, if invalid, return a default configuration
	conf = isConfigValid(conf)

	// 初始化 Group 结构体
	// Initialize the Group struct
	gr := Group{
		elements: []*Element{},
		lock:     sync.Mutex{},
		config:   conf,
		wg:       sync.WaitGroup{},
		once:     sync.Once{},
	}

	// 创建一个新的上下文，该上下文在调用 cancel 函数时被取消
	// Create a new context that is cancelled when the cancel function is called
	gr.ctx, gr.cancel = context.WithCancel(context.Background())

	return &gr
}

// 停止批量处理任务
// Stop the batch processing task
func (gr *Group) Stop() {
	gr.once.Do(func() {
		// 取消上下文
		// Cancel the context
		gr.cancel()

		// 等待所有 goroutine 完成
		// Wait for all goroutines to complete
		gr.wg.Wait()

		gr.lock.Lock()
		// 重置工作元素数组长度，帮助垃圾回收
		// Reset the length of the worker element array to help garbage collection
		gr.elements = gr.elements[:0]
		gr.lock.Unlock()
	})
}

// 准备工作元素
// Prepare worker elements
func (gr *Group) prepare(elements []any) {
	count := len(elements)
	gr.lock.Lock()

	// 创建待处理的数据对象数组
	// Create an array of data objects to be processed
	gr.elements = make([]*Element, count)

	// 创建工作元素
	// Create worker elements
	for i := 0; i < count; i++ {
		// 从内存池中获取一个元素
		// Get an element from the memory pool
		element := elementpool.Get()

		// 设置数据和值
		// Set the data and value
		element.SetData(elements[i])
		element.SetValue(int64(i))

		// 将元素放入工作元素数组
		// Put the element into the worker element array
		gr.elements[i] = element
	}

	gr.lock.Unlock()
}

// 执行批量处理任务
// Execute batch processing task
func (gr *Group) execute() []any {
	var results []any
	// 如果需要返回结果, 则创建结果数组
	// If results are needed, create a results array
	if gr.config.result {
		results = make([]any, len(gr.elements))
	}

	// 启动工作者
	// Start workers
	gr.wg.Add(gr.config.num)
	for i := 0; i < gr.config.num; i++ {
		// 启动一个 goroutine
		// Start a goroutine
		go func() {
			// 当函数退出时，减少等待组的计数
			// When the function exits, reduce the count of the wait group
			defer gr.wg.Done()

			// 无限循环
			// Infinite loop
			for {
				select {
				case <-gr.ctx.Done():
					// 如果上下文已经被取消，返回
					// If the context has been cancelled, return
					return

				default:
					gr.lock.Lock()
					// 如果没有元素, 则返回
					// If there are no elements, return
					if len(gr.elements) == 0 {
						gr.lock.Unlock()
						return
					}

					// 从工作元素数组中获取一个元素
					// Get an element from the worker element array
					element := gr.elements[0]

					// 从工作元素数组中移除一个元素，并将第一个元素清理
					// Remove an element from the worker element array and clean the first element
					gr.elements[0] = nil
					gr.elements = gr.elements[1:]
					gr.lock.Unlock()

					// 获取数据
					// Get data
					data := element.GetData()

					// 执行回调函数 OnBefore
					// Execute callback function OnBefore
					gr.config.callback.OnBefore(data)

					// 执行消息处理函数
					// Execute message handle function
					result, err := gr.config.handleFunc(data)

					// 执行回调函数 OnAfter
					// Execute callback function OnAfter
					gr.config.callback.OnAfter(data, result, err)

					// 如果需要返回结果, 则将结果放入结果数组
					// If results are needed, put the result into the results array
					if gr.config.result {
						results[element.GetValue()] = result
					}

					// 将元素放入内存池
					// Put the element into the memory pool
					elementpool.Put(element)
				}
			}
		}()
	}

	// 等待工作者完成
	// Wait for workers to finish
	gr.wg.Wait()

	// 重置工作元素数组长度，帮助 GC
	// Reset the length of the worker element array to help GC
	gr.elements = gr.elements[:0]

	// 返回结果数组
	// Return the results array
	return results
}

// Map 方法用于批量处理元素
// The Map method is used for batch processing of elements
func (gr *Group) Map(elements []any) []any {
	// 如果没有元素, 直接返回
	// If there are no elements, return directly
	if len(elements) == 0 {
		return nil
	}

	// 准备工作元素，这个步骤会将输入的元素转换为工作元素
	// Prepare worker elements, this step will convert the input elements into worker elements
	gr.prepare(elements)

	// 执行批量处理任务，这个步骤会启动多个 goroutine 来并发处理工作元素，并返回处理结果
	// Execute batch processing task, this step will start multiple goroutines to concurrently process worker elements and return the processing results
	return gr.execute()
}
