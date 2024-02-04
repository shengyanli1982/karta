package karta

import (
	"context"
	"sync"
)

// 元素内存池
// element pool.
var elementpool = NewElementPool()

// 批量处理任务
// batch process task.
type Group struct {
	elements []*Element     // 工作元素数组 (worker element array)
	lock     sync.Mutex     // 锁 (lock)
	config   *Config        // 配置 (configuration)
	wg       sync.WaitGroup // 等待组 (wait group)
	once     sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
}

// 创建一个新的批量处理任务
// create a new batch process task.
func NewGroup(conf *Config) *Group {
	conf = isConfigValid(conf)
	gr := Group{
		elements: []*Element{},
		lock:     sync.Mutex{},
		config:   conf,
		wg:       sync.WaitGroup{},
		once:     sync.Once{},
	}
	gr.ctx, gr.cancel = context.WithCancel(context.Background())

	return &gr
}

// 停止批量处理任务
// stop batch process task.
func (gr *Group) Stop() {
	gr.once.Do(func() {
		gr.cancel()
		gr.wg.Wait()
		gr.lock.Lock()
		// 重置工作元素数组长度，回收内存
		// reset the length of worker element array to help GC.
		gr.elements = gr.elements[:0]
		gr.lock.Unlock()
	})
}

// 准备工作元素
// prepare worker elements.
func (gr *Group) prepare(elements []any) {
	count := len(elements)
	gr.lock.Lock()

	// 创建待处理的数据对象数组
	// create data object array to be processed.
	gr.elements = make([]*Element, count)

	// 创建工作元素
	// create worker elements.
	for i := 0; i < count; i++ {
		// 从内存池中获取一个元素
		// get an element from element pool.
		element := elementpool.Get()

		// 设置数据和值
		// set data and value.
		element.SetData(elements[i])
		element.SetValue(int64(i))

		// 将元素放入工作元素数组
		// put element into worker element array.
		gr.elements[i] = element
	}

	gr.lock.Unlock()
}

// 执行批量处理任务
// execute batch process task.
func (gr *Group) execute() []any {
	var results []any
	// 如果需要返回结果, 则创建结果数组
	// if need return result, create result array.
	if gr.config.result {
		results = make([]any, len(gr.elements))
	}

	// 启动工作者
	// start workers.
	gr.wg.Add(gr.config.num)
	for i := 0; i < gr.config.num; i++ {
		go func() {
			defer gr.wg.Done()
			for {
				select {
				case <-gr.ctx.Done():
					return

				default:
					gr.lock.Lock()
					// 如果没有元素, 则返回
					// if no element, return.
					if len(gr.elements) == 0 {
						gr.lock.Unlock()
						return
					}

					// 从工作元素数组中获取一个元素
					// get an element from worker element array.
					element := gr.elements[0]

					// 从工作元素数组中移除一个元素，并将第一个元素清理
					// remove an element from worker element array and clean the first element.
					gr.elements[0] = nil
					gr.elements = gr.elements[1:]
					gr.lock.Unlock()

					// 获取数据
					// get data.
					data := element.GetData()

					// 执行回调函数 OnBefore
					// execute callback function OnBefore.
					gr.config.callback.OnBefore(data)

					// 执行消息处理函数
					// execute message handle function.
					result, err := gr.config.handleFunc(data)

					// 执行回调函数 OnAfter
					// execute callback function OnAfter.
					gr.config.callback.OnAfter(data, result, err)

					// 如果需要返回结果, 则将结果放入结果数组
					// if need return result, put result into result array.
					if gr.config.result {
						results[element.GetValue()] = result
					}

					// 将元素放入内存池
					// put element into element pool.
					elementpool.Put(element)
				}
			}
		}()
	}

	// 等待工作者完成
	// wait workers done.
	gr.wg.Wait()

	// 重置工作元素数组长度，帮助 GC
	// reset the length of worker element array to help GC.
	gr.elements = gr.elements[:0]

	// 返回结果数组
	// return result array.
	return results
}

// 批量处理
// batch process.
func (gr *Group) Map(elements []any) []any {
	// 如果没有元素, 直接返回
	// if no element, return directly.
	if len(elements) == 0 {
		return nil
	}

	// 准备工作元素
	// prepare worker elements.
	gr.prepare(elements)

	// 执行批量处理任务
	// execute batch process task.
	return gr.execute()
}
