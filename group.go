package karta

import (
	"context"
	"sync"

	"github.com/shengyanli1982/karta/internal"
)

// 元素内存池
// Element memory pool
var elementpool = internal.NewElementPool()

// Group 是一个用于批量处理任务的结构体
// Group is a struct for batch processing tasks
type Group struct {
	// elements 是一个 Element 类型的切片，用于存储 Group 中的所有元素
	// elements is a slice of type Element, used to store all elements in the Group
	elements []*internal.Element

	// lock 是一个互斥锁，用于保护 Group 结构体的并发访问
	// lock is a mutex, used to protect concurrent access to the Group struct
	lock sync.Mutex

	// config 是 Group 的配置，包括处理函数、回调函数等
	// config is the configuration of Group, including processing functions, callback functions, etc.
	config *Config

	// wg 是一个 WaitGroup，用于等待所有的工作完成
	// wg is a WaitGroup, used to wait for all work to be completed
	wg sync.WaitGroup

	// once 是一个 Once，用于确保某个操作只执行一次，例如停止 Group
	// once is a Once, used to ensure that an operation is performed only once, such as stopping the Group
	once sync.Once

	// ctx 是一个上下文，用于管理 goroutine 的生命周期
	// ctx is a context, used to manage the lifetimes of goroutines
	ctx context.Context

	// cancel 是一个取消函数，用于取消 ctx
	// cancel is a function to cancel ctx
	cancel context.CancelFunc
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
		// elements 是一个 Element 类型的切片，用于存储 Group 中的所有元素
		// elements is a slice of type Element, used to store all elements in the Group
		elements: []*internal.Element{},

		// lock 是一个互斥锁，用于保护 Group 结构体的并发访问
		// lock is a mutex, used to protect concurrent access to the Group struct
		lock: sync.Mutex{},

		// config 是 Group 的配置，包括处理函数、回调函数等
		// config is the configuration of Group, including processing functions, callback functions, etc.
		config: conf,

		// wg 是一个 WaitGroup，用于等待所有的工作完成
		// wg is a WaitGroup, used to wait for all work to be completed
		wg: sync.WaitGroup{},

		// once 是一个 Once，用于确保某个操作只执行一次，例如停止 Group
		// once is a Once, used to ensure that an operation is performed only once, such as stopping the Group
		once: sync.Once{},
	}

	// 创建一个新的上下文，该上下文在调用 cancel 函数时被取消
	// Create a new context that is cancelled when the cancel function is called
	gr.ctx, gr.cancel = context.WithCancel(context.Background())

	return &gr
}

// 停止批量处理任务
// Stop the batch processing task
func (gr *Group) Stop() {
	// 使用 once.Do 方法确保以下操作只执行一次
	// Use the once.Do method to ensure that the following operations are performed only once
	gr.once.Do(func() {
		// 调用 cancel 方法取消上下文，这将导致所有依赖该上下文的 goroutine 收到取消信号
		// Call the cancel method to cancel the context, which will cause all goroutines that depend on this context to receive a cancellation signal
		gr.cancel()

		// 调用 wg.Wait 方法等待所有 goroutine 完成
		// Call the wg.Wait method to wait for all goroutines to complete
		gr.wg.Wait()

		// 调用 lock 方法获取 Group 结构体的互斥锁，以保护并发访问
		// Call the lock method to acquire the mutex of the Group struct to protect concurrent access
		gr.lock.Lock()

		// 重置工作元素数组的长度为 0，这将帮助垃圾回收器回收这些元素
		// Reset the length of the worker element array to 0, which will help the garbage collector to recycle these elements
		gr.elements = gr.elements[:0]

		// 调用 Unlock 方法释放 Group 结构体的互斥锁，以结束对其的保护
		// Call the Unlock method to release the mutex of the Group struct to end its protection
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
	gr.elements = make([]*internal.Element, count)

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
					// 使用 lock 方法获取 Group 结构体的互斥锁，以保护并发访问
					// Use the lock method to acquire the mutex of the Group struct to protect concurrent access
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

					// 使用 Unlock 方法释放 Group 结构体的互斥锁，以结束对其的保护
					// Use the Unlock method to release the mutex of the Group struct to end its protection
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
