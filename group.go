package karta

import (
	"context"
	"sync"
)

// 元素内存池
// element pool.
var elementPool = NewElementPool()

// 批量处理任务
// batch process task.
type Group struct {
	items  []*element     // 工作元素数组
	lock   sync.Mutex     // 锁
	config *Config        // 配置
	wg     sync.WaitGroup // 等待组
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

// 创建一个新的批量处理任务
// create a new batch process task.
func NewGroup(conf *Config) *Group {
	conf = isConfigValid(conf)
	g := Group{
		items:  []*element{},
		lock:   sync.Mutex{},
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())

	return &g
}

// 停止批量处理任务
// stop batch process task.
func (g *Group) Stop() {
	g.once.Do(func() {
		g.cancel()
		g.wg.Wait()
		g.lock.Lock()
		g.items = g.items[:0]
		g.lock.Unlock()
	})
}

// 准备工作元素
// prepare worker elements.
func (g *Group) prepare(items []any) {
	count := len(items)
	g.lock.Lock()
	// 创建待处理的数据对象数组
	// create data object array to be processed.
	g.items = make([]*element, count)
	// 创建工作元素
	// create worker elements.
	for i := 0; i < count; i++ {
		// 从内存池中获取一个元素
		// get an element from element pool.
		e := elementPool.Get()
		// 设置数据和值
		// set data and value.
		e.SetData(items[i])
		e.SetValue(int64(i))
		// 将元素放入工作元素数组
		// put element into worker element array.
		g.items[i] = e
	}
	g.lock.Unlock()
}

// 执行批量处理任务
// execute batch process task.
func (g *Group) execute() []any {
	var results []any
	// 如果需要返回结果, 则创建结果数组
	// if need return result, create result array.
	if g.config.result {
		results = make([]any, len(g.items))
	}
	// 启动工作者
	// start workers.
	g.wg.Add(g.config.num)
	for i := 0; i < g.config.num; i++ {
		go func() {
			defer g.wg.Done()
			for {
				select {
				case <-g.ctx.Done():
					return
				default:
					g.lock.Lock()
					// 如果没有元素, 则返回
					// if no element, return.
					if len(g.items) == 0 {
						g.lock.Unlock()
						return
					}
					// 从工作元素数组中获取一个元素
					// get an element from worker element array.
					item := g.items[0]
					// 从工作元素数组中移除一个元素，并将第一个元素清理
					// remove an element from worker element array and clean the first element.
					g.items[0] = nil
					g.items = g.items[1:]
					g.lock.Unlock()
					// 获取数据
					// get data.
					d := item.GetData()
					// 执行回调函数 OnBefore
					// execute callback function OnBefore.
					g.config.cb.OnBefore(d)
					// 执行消息处理函数
					// execute message handle function.
					r, err := g.config.h(d)
					// 执行回调函数 OnAfter
					// execute callback function OnAfter.
					g.config.cb.OnAfter(d, r, err)
					// 如果需要返回结果, 则将结果放入结果数组
					// if need return result, put result into result array.
					if g.config.result {
						results[item.GetValue()] = r
					}
					// 将元素放入内存池
					// put element into element pool.
					elementPool.Put(item)
				}
			}
		}()
	}
	// 等待工作者完成
	// wait workers done.
	g.wg.Wait()

	return results
}

// 批量处理
// batch process.
func (g *Group) Map(items []any) []any {
	// 如果没有元素, 直接返回
	// if no element, return directly.
	if len(items) == 0 {
		return nil
	}
	// 准备工作元素
	// prepare worker elements.
	g.prepare(items)
	// 执行批量处理任务
	// execute batch process task.
	return g.execute()
}
