package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrorQueueClosed = errors.New("pipeline is closed")
)

var (
	// 默认的工作者空闲超时时间, 10 秒 (default worker idle timeout, 10 seconds)
	defaultWorkerIdleTimeout = (10 * time.Second).Milliseconds()
	// 默认的最小工作者数量 (default minimum number of workers)
	defaultMinWorkerNum = int64(1)
)

// 扩展元素内存池
// extended element pool.
var elementExtPool = NewElementExtPool()

// 管道接口
// pipeline interface.
type QInterface interface {
	Add(element any) error         // 添加元素 (add element)
	Get() (element any, err error) // 获取元素 (get element)
	Done(element any)              // 标记元素完成 (mark element done)
	Stop()                         // 停止管道 (stop pipeline)
	IsClosed() bool                // 判断管道是否已经关闭 (judge whether pipeline is closed)
}

// 管道
// pipeline.
type Pipeline struct {
	queue  QInterface     // 工作管道，存放扩展元素
	config *Config        // 配置
	wg     sync.WaitGroup // 等待组
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	timer  atomic.Int64
	rc     atomic.Int64 // 正在运行 Worker 的数量
}

// 创建一个新的管道
// create a new pipeline.
func NewPipeline(queue QInterface, conf *Config) *Pipeline {
	// 如果 pipeline 为 nil, 则返回 nil
	if queue == nil {
		return nil
	}
	conf = isConfigValid(conf)
	pl := Pipeline{
		queue:  queue,
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
		timer:  atomic.Int64{},
		rc:     atomic.Int64{},
	}
	pl.ctx, pl.cancel = context.WithCancel(context.Background())
	pl.timer.Store(time.Now().UnixMilli())

	// 启动工作者
	// start workers.
	pl.rc.Store(int64(conf.num))
	pl.wg.Add(conf.num)
	for i := 0; i < conf.num; i++ {
		go pl.executor()
	}

	// 启动时间定时器
	pl.wg.Add(1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer func() {
			ticker.Stop()
			pl.wg.Done()
		}()

		for {
			select {
			case <-pl.ctx.Done():
				return
			case <-ticker.C:
				pl.timer.Store(time.Now().UnixMilli())
			}
		}
	}()

	return &pl
}

// 停止管道
// stop pipeline.
func (pl *Pipeline) Stop() {
	pl.once.Do(func() {
		pl.cancel()
		pl.wg.Wait()
		pl.queue.Stop()
	})
}

// 执行器，执行工作管道中的任务
// executor, execute tasks in the pipeline.
func (pl *Pipeline) executor() {
	// 任务执行最后一次启动时间点
	// last start time of task execution.
	updateAt := time.Now().UnixMilli()

	// 启动空闲超时定时器
	// start idle timeout timer.
	ticker := time.NewTicker(3 * time.Second)

	defer func() {
		pl.wg.Done()
		ticker.Stop()
	}()

	for {
		select {
		case <-pl.ctx.Done():
			return
		case <-ticker.C:
			// 如果空闲超时，则判断当前工作者数量是否超过最小工作者数量，如果超过则返回
			// if idle timeout, judge whether number of workers is greater than minimum number of workers, if greater than, return.
			if pl.timer.Load()-updateAt >= defaultWorkerIdleTimeout && pl.rc.Load() > defaultMinWorkerNum {
				return
			}
		default:
			// 更新时间
			updateAt = pl.timer.Load()
			// 如果管道已经关闭，则返回
			// if pipeline is closed, return.
			if pl.queue.IsClosed() {
				return
			}
			// 从工作管道中获取一个扩展元素
			// get an extended element from the pipeline.
			o, err := pl.queue.Get()
			if err != nil {
				break
			}
			// 工作管道标记完成
			// mark element done.
			pl.queue.Done(o)
			// 数据类型转换
			// type conversion.
			d := o.(*elementExt)
			// 执行回调函数 OnBefore
			// execute callback function OnBefore.
			pl.config.cb.OnBefore(d)
			// 执行消息处理函数
			// execute message handle function.
			h := d.GetHandleFunc()
			var r any
			// 如果指定函数不为 nil，则执行消息处理函数。 否则使用 config 中的函数
			// if handle function is not nil, execute it. otherwise use function in config.
			if h != nil {
				r, err = h(d.GetData())
			} else {
				r, err = pl.config.h(d.GetData())
			}
			// 执行回调函数 OnAfter
			// execute callback function OnAfter.
			pl.config.cb.OnAfter(d, r, err)
			// 将扩展元素放回对象池
			// put extended element back to the pool.
			elementExtPool.Put(d)
		}
	}
}

// 提交带有自定义处理函数的任务
// submit task with custom handle function.
func (pl *Pipeline) SubmitWithFunc(fn MessageHandleFunc, msg any) error {
	// 如果管道已经关闭，则返回错误
	// if pipeline is closed, return error.
	if pl.queue.IsClosed() {
		return ErrorQueueClosed
	}
	// 从对象池中获取一个扩展元素
	// get an extended element from the pool.
	e := elementExtPool.Get()
	e.SetData(msg)
	e.SetHandleFunc(fn)
	// 将扩展元素添加到工作管道中
	// add extended element to the pipeline.
	if err := pl.queue.Add(e); err != nil {
		// 如果添加失败，则将扩展元素放回对象池
		// if add failed, put extended element back to the pool.
		elementExtPool.Put(e)
		return err
	}
	// 判断当前工作管道中的任务数量是否超足够, 如果不足则启动一个新的工作者
	// if number of tasks in the pipeline is not enough, start a new worker.
	if int64(pl.config.num) > pl.rc.Load() {
		pl.rc.Add(1)
		pl.wg.Add(1)
		go pl.executor()
	}
	// 正确执行，返回 nil
	// execute correctly, return nil.
	return nil
}

// 提交任务
// submit task.
func (pl *Pipeline) Submit(msg any) error {
	return pl.SubmitWithFunc(nil, msg)
}
