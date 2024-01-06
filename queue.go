package karta

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrorQueueClosed = errors.New("queue is closed")
)

// 扩展元素内存池
// extended element pool.
var elementExtPool = NewElementExtPool()

// 队列接口
// queue interface.
type QInterface interface {
	Add(element any) error
	Get() (element any, err error)
	Done(element any)
	IsClosed() bool
}

// 队列
// queue.
type Queue struct {
	queue  QInterface     // 工作队列，存放扩展元素
	lock   sync.Mutex     // 锁
	config *Config        // 配置
	wg     sync.WaitGroup // 等待组
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

// 创建一个新的队列
// create a new queue.
func NewQueue(queue QInterface, conf *Config) *Queue {
	// 如果 queue 为 nil, 则返回 nil
	if queue == nil {
		return nil
	}
	conf = isConfigValid(conf)
	q := Queue{
		queue:  queue,
		lock:   sync.Mutex{},
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}
	q.ctx, q.cancel = context.WithCancel(context.Background())

	// 启动工作者
	// start workers.
	q.wg.Add(conf.num)
	for i := 0; i < conf.num; i++ {
		go q.executor()
	}

	return &q
}

// 停止队列
// stop queue.
func (q *Queue) Stop() {
	q.once.Do(func() {
		q.cancel()
		q.wg.Wait()
		q.lock.Lock()
		q.queue = nil
		q.lock.Unlock()
	})
}

// 执行器，执行工作队列中的任务
// executor, execute tasks in the queue.
func (q *Queue) executor() {
	defer q.wg.Done()

	for {
		select {
		case <-q.ctx.Done():
			return
		default:
			// 如果队列已经关闭，则返回
			// if queue is closed, return.
			if q.queue.IsClosed() {
				return
			}
			// 从工作队列中获取一个扩展元素
			// get an extended element from the queue.
			o, err := q.queue.Get()
			if err != nil {
				break
			}
			// 工作队列标记完成
			// mark element done.
			q.queue.Done(o)
			// 数据类型转换
			// type conversion.
			d := o.(*elementExt)
			// 执行回调函数 OnBefore
			// execute callback function OnBefore.
			q.config.cb.OnBefore(d)
			// 执行消息处理函数
			// execute message handle function.
			h := d.Handler()
			var r any
			// 如果指定函数不为 nil，则执行消息处理函数。 否则使用 config 中的函数
			// if handle function is not nil, execute it. otherwise use function in config.
			if h != nil {
				r, err = h(d.Data())
			} else {
				r, err = q.config.h(d)
			}
			// 执行回调函数 OnAfter
			// execute callback function OnAfter.
			q.config.cb.OnAfter(d, r, err)
			// 将扩展元素放回对象池
			// put extended element back to the pool.
			elementExtPool.Put(d)
		}
	}
}

// 提交带有自定义处理函数的任务
// submit task with custom handle function.
func (q *Queue) SubmitWithFunc(fn MessageHandleFunc, msg any) error {
	// 如果队列已经关闭，则返回错误
	// if queue is closed, return error.
	if q.queue.IsClosed() {
		return ErrorQueueClosed
	}
	// 从对象池中获取一个扩展元素
	// get an extended element from the pool.
	e := elementExtPool.Get()
	e.SetData(msg)
	e.SetHandler(fn)
	// 将扩展元素添加到工作队列中
	// add extended element to the queue.
	if err := q.queue.Add(e); err != nil {
		// 如果添加失败，则将扩展元素放回对象池
		// if add failed, put extended element back to the pool.
		elementExtPool.Put(e)
		return err
	}
	return nil
}

// 提交任务
// submit task.
func (q *Queue) Submit(msg any) error {
	return q.SubmitWithFunc(nil, msg)
}
