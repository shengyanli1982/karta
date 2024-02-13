package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// 立即执行
// immediate execute.
const immediate = time.Duration(0)

var (
	// 管道已经关闭
	// pipeline is closed
	ErrorQueueClosed = errors.New("pipeline is closed")
)

var (
	// 默认的工作者空闲超时时间, 10 秒
	// default worker idle timeout, 10 seconds
	defaultWorkerIdleTimeout = (10 * time.Second).Milliseconds()

	// 默认新建工作者的突发数量, 8
	// default new workers burst, 8
	defaultNewWorkersBurst = 8

	// 默认每秒新建工作者的数量, 4
	// default number of new workers per second, 4
	defaultNewWorkersPerSecond = 4
)

// 管道
// pipeline.
type Pipeline struct {
	queue       DelayingQueueInterface // The queue used for storing and processing data.
	config      *Config                // The configuration settings for the pipeline.
	wg          sync.WaitGroup         // WaitGroup for managing goroutines.
	once        sync.Once              // Sync.Once for ensuring initialization is performed only once.
	ctx         context.Context        // The context for managing the lifecycle of the pipeline.
	cancel      context.CancelFunc     // The function for canceling the pipeline.
	timer       atomic.Int64           // Atomic integer for tracking the pipeline's timer.
	rc          atomic.Int64           // Atomic integer for tracking the pipeline's resource consumption.
	elementpool *ElemmentExtPool       // The pool for managing extended elements.
	wlimit      *rate.Limiter          // The resource ratelimit for create new workers.
}

// 创建一个新的管道
// create a new pipeline.
func NewPipeline(queue DelayingQueueInterface, conf *Config) *Pipeline {
	// 如果 pipeline 为 nil, 则返回 nil
	// if pipeline is nil, return nil.
	if queue == nil {
		return nil
	}
	conf = isConfigValid(conf)
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
	pl.ctx, pl.cancel = context.WithCancel(context.Background())
	pl.timer.Store(time.Now().UnixMilli())

	// 启动工作者
	// start workers.
	pl.rc.Store(1)
	pl.wg.Add(1)
	go pl.executor()

	// 启动时间定时器
	// start time timer.
	pl.wg.Add(1)
	go pl.updateTimer()

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
	// 任务执行启动时间点
	// start time of task execution.
	updateAt := pl.timer.Load()

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
			element := o.(*ElementExt)

			// 获取数据
			// get data.
			data := element.GetData()

			// 执行回调函数 OnBefore
			// execute callback function OnBefore.
			pl.config.callback.OnBefore(data)

			// 执行消息处理函数
			// execute message handle function.
			handleFunc := element.GetHandleFunc()

			// 如果指定函数不为 nil，则执行消息处理函数。 否则使用 config 中的函数
			// if handle function is not nil, execute it. otherwise use function in config.
			var result any
			if handleFunc != nil {
				result, err = handleFunc(data)
			} else {
				result, err = pl.config.handleFunc(data)
			}

			// 执行回调函数 OnAfter
			// execute callback function OnAfter.
			pl.config.callback.OnAfter(data, result, err)

			// 将扩展元素放回对象池
			// put extended element back to the pool.
			pl.elementpool.Put(element)
		}
	}
}

func (pl *Pipeline) submit(fn MessageHandleFunc, msg any, delay time.Duration) error {
	// 如果管道已经关闭，则返回错误
	// if pipeline is closed, return error.
	if pl.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// 从对象池中获取一个扩展元素
	// get an extended element from the pool.
	element := pl.elementpool.Get()
	element.SetData(msg)
	element.SetHandleFunc(fn)

	// 将扩展元素添加到工作管道中, 如果延迟大于 0, 则添加到延迟队列中
	// add extended element to the pipeline, if delay greater than 0, add to delay queue.
	var err error
	if delay > 0 {
		err = pl.queue.AddAfter(element, delay)
	} else {
		err = pl.queue.Add(element)
	}
	if err != nil {
		// 如果添加失败，则将扩展元素放回对象池
		// if add failed, put extended element back to the pool.
		pl.elementpool.Put(element)
		return err
	}

	// 判断当前工作管道中的任务数量是否超足够, 如果不足则启动一个新的工作者
	// if number of tasks in the pipeline is not enough, start a new worker.
	if int64(pl.config.num) > pl.rc.Load() && pl.wlimit.Allow() {
		pl.rc.Add(1)
		pl.wg.Add(1)
		go pl.executor()
	}

	// 正确执行，返回 nil
	// execute correctly, return nil.
	return nil
}

// 提交带有自定义处理函数的任务
// submit task with custom handle function.
func (pl *Pipeline) SubmitWithFunc(fn MessageHandleFunc, msg any) error {
	return pl.submit(fn, msg, immediate)
}

// 提交任务
// submit task.
func (pl *Pipeline) Submit(msg any) error {
	return pl.SubmitWithFunc(nil, msg)
}

// 延迟指定时间提交带有自定义处理函数的任务
// submit task with custom handle function after delay.
func (pl *Pipeline) SubmitAfterWithFunc(fn MessageHandleFunc, msg any, delay time.Duration) error {
	return pl.submit(fn, msg, delay)
}

// 延迟指定时间提交任务
// submit task after delay.
func (pl *Pipeline) SubmitAfter(msg any, delay time.Duration) error {
	return pl.SubmitAfterWithFunc(nil, msg, delay)
}

// updateTimer 是一个定时器，用于更新 Pipeline 的时间戳
// updateTimer is a timer that updates the timestamp of Pipeline
func (pl *Pipeline) updateTimer() {
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
}
