package karta

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/shengyanli1982/karta/internal"
)

// elementPool is a global pool for reusing Element objects
// elementPool 是一个全局的 Element 对象复用池
var elementPool = internal.NewElementPool()

// Group represents a worker group that processes tasks concurrently
// Group 表示一个并发处理任务的工作组
type Group struct {
	elements []*internal.Element // slice to store task elements / 存储任务元素的切片
	lock     sync.Mutex          // mutex for protecting shared resources and ensuring exclusive execution / 用于保护共享资源和确保互斥执行的互斥锁
	config   *Config             // configuration for the group / 工作组的配置信息
	wg       sync.WaitGroup      // wait group for synchronizing goroutines / 用于同步 goroutine 的等待组
	once     sync.Once           // ensures Stop is called only once / 确保 Stop 只被调用一次
	ctx      context.Context     // context for cancellation / 用于取消操作的上下文
	cancel   context.CancelFunc  // function to cancel the context / 取消上下文的函数
}

// NewGroup creates a new Group with the given configuration
// NewGroup 使用给定的配置创建一个新的工作组
func NewGroup(config *Config) *Group {
	config = isConfigValid(config)
	group := &Group{
		elements: make([]*internal.Element, 0),
		config:   config,
	}
	group.ctx, group.cancel = context.WithCancel(context.Background())
	return group
}

// cleanup cleans up remaining elements and returns them to the pool
// cleanup 清理剩余的元素并将它们返回到对象池
func (group *Group) cleanup() {
	for i := 0; i < len(group.elements); i++ {
		if group.elements[i] != nil {
			elementPool.Put(group.elements[i])
			group.elements[i] = nil
		}
	}

	group.elements = group.elements[:0]
}

// Stop gracefully stops the group and releases resources
// Stop 优雅地停止工作组并释放资源
func (group *Group) Stop() {
	group.once.Do(func() {
		group.cancel()
		group.wg.Wait()
	})
}

// prepare initializes the elements slice with data from the input
// prepare 使用输入数据初始化元素切片
func (group *Group) prepare(elements []any) {
	count := len(elements)
	group.elements = make([]*internal.Element, count)

	for i := 0; i < count; i++ {
		element := elementPool.Get()
		element.SetData(elements[i])
		element.SetValue(int64(i))
		group.elements[i] = element
	}
}

// execute processes all tasks concurrently and returns the results
// execute 并发处理所有任务并返回结果
func (group *Group) execute() []any {
	// Get total number of tasks to process
	// 获取需要处理的总任务数
	totalTasks := len(group.elements)

	// Initialize result slice if result collection is enabled
	// 如果需要收集结果，则初始化结果切片
	var taskResults []any
	if group.config.result {
		taskResults = make([]any, totalTasks)
	}

	// Counter for tracking completed tasks, used atomically
	// 用于原子计数已完成的任务数
	var completedTaskCount int64 = 0

	// Start worker goroutines based on configured worker count
	// 根据配置的工作者数量启动工作协程
	group.wg.Add(group.config.num)
	for workerID := 0; workerID < group.config.num; workerID++ {
		go func() {
			defer group.wg.Done()

			for {
				// Get the current task index and increment the counter atomically
				// 获取当前任务索引并原子递增计数器
				taskIndex := atomic.AddInt64(&completedTaskCount, 1) - 1
				if taskIndex >= int64(totalTasks) {
					return
				}

				select {
				// Check if the context is done and return if true
				// 如果上下文已完成则返回
				case <-group.ctx.Done():
					return

				default:
					// Get the current task element and immediately check if it is nil
					// 获取当前任务元素并立即检查是否为 nil
					current := group.elements[taskIndex]
					if current == nil {
						continue
					}

					// Set the element to nil immediately to prevent double recycling
					// 立即将引用置为 nil，防止重复回收
					group.elements[taskIndex] = nil

					// Execute the task processing flow
					// 执行任务处理流程
					data := current.GetData()
					group.config.callback.OnBefore(data)
					processedResult, err := group.config.handleFunc(data)
					group.config.callback.OnAfter(data, processedResult, err)

					if group.config.result {
						taskResults[current.GetValue()] = processedResult
					}

					// Mark the element as done and recycle it
					// 标记元素为已完成并回收
					elementPool.Put(current)
				}
			}
		}()
	}

	// Wait for all workers to complete
	// 等待所有工作协程完成
	group.wg.Wait()

	return taskResults
}

// Map processes the input elements concurrently using the configured handler function
// Map 使用配置的处理函数并发处理输入元素
func (group *Group) Map(elements []any) []any {
	// Ensure exclusive execution and protect shared resources
	// 确保互斥执行并保护共享资源
	group.lock.Lock()
	defer group.lock.Unlock()

	// Check if the group has been stopped
	// 检查工作组是否已经停止
	select {
	case <-group.ctx.Done():
		return nil
	default:
	}

	// Return nil if input is empty
	// 如果输入为空则返回 nil
	if len(elements) == 0 {
		return nil
	}

	// Initialize elements and process them concurrently
	// 初始化元素并并发处理
	group.prepare(elements)
	result := group.execute()

	// Clean up elements after processing is complete
	// 处理完成后清理元素
	group.cleanup()

	return result
}
