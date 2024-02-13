<div align="center">
	<h1>Karta</h1>
    <p>A lightweight task batch and asynchronous processing module.</p>
	<img src="assets/logo.png" alt="logo" width="300px">
</div>

# Introduction

`Karta` component is a lightweight task batch and asynchronous processing module. It very similar to `ThreadPoolExecutor` in Python. It provides a simple interface to submit tasks and get the results.

Why `Karta`? In my job, I need to do a lot of job processing. I want to use like `ThreadPoolExecutor` code to do the job. However, in golang there is no such component. So I write one.

`Karta` is very simple, it only has two processes: `Group` and `Pipeline`.

-   `Group`: tasks batch processing by `Group`.
-   `Pipeline`: task one by one processing by `Pipeline`. Any task can specify a handle function.

# Advantage

-   Simple and easy to use
-   No third-party dependencies
-   Support action callback functions

# Installation

```bash
go get github.com/shengyanli1982/karta
```

# Quick Start

`Karta` is very simple to use. Just few lines of code can be used to batch process tasks.

### Config

`Karta` has a config object, which can be used to configure the batch process behavior. The config object can be used following methods to set.

-   `WithWorkerNumber`: set the number of workers. The default number is `1`, max is `524280`
-   `WithCallback` : set the callback function. The default is `&emptyCallback{}`.
-   `WithHandleFunc` : set the handle function. The default is `defaultMsgHandleFunc`.
-   `WithResult`: flags whether all tasks result can be recorded. The default is `false`. It only works for `Group`.

### Components

#### 1. Group

`Group` is a batch processing component. It can be used to batch process tasks once at a time. `Group` use fix number of workers to process tasks.

**Methods**

-   `Map`: batch process tasks by given a slice of objects, each object is handle func parameter. The method will return a slice of results which use `WithResult` to set `true`.

**Callback**

-   `OnBefore` : callback function before task processing.
-   `OnAfter` : callback function after task processing.

**Example**

```go
package main

import (
	"time"

	k "github.com/shengyanli1982/karta"
)

func handleFunc(msg any) (any, error) {
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)
	return msg, nil
}

func main() {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	defer g.Stop()

	r0 := g.Map([]any{3, 5, 2})
	println(r0[0].(int))
}
```

**Result**

```bash
$ go run demo.go
3
```

#### 2. Pipeline

`Pipeline` is a task processing component. It can be used to process tasks one by one. `Pipeline` use dynamic number of workers to process tasks, which means the number of workers will be decreased when there is no task to process and will be increased when there are tasks to process.

If worker is idle, it will be closed after `defaultWorkerIdleTimeout` which is `10` seconds.

Long time idle, workers number will decrease to `defaultMinWorkerNum` which is `1`.

When `msg` posted by `Submit` or `SubmitWithFunc`, it will be processed by the idle worker. If there is no idle worker, a new worker will be created to process the `msg`. The number of running workers will be increased to value which set by config `WithWorkerNumber` method if the number of running workers is not enough.

`Pipeline` need a queue object to store tasks. The queue object must implement `DelayingQueueInterface` interface.

```go
// 管道接口
// pipeline interface.
type QueueInterface interface {
	Add(element any) error         // 添加元素 (add element)
	Get() (element any, err error) // 获取元素 (get element)
	Done(element any)              // 标记元素完成 (mark element done)
	Stop()                         // 停止管道 (stop pipeline)
	IsClosed() bool                // 判断管道是否已经关闭 (judge whether pipeline is closed)
}

// DelayingQueueInterface 包含延迟队列接口的定义
// DelayingQueueInterface is delaying queue interface
type DelayingQueueInterface interface {
	QueueInterface
	AddAfter(element any, delay time.Duration) error // 将元素添加到队列中，并在指定的延迟后立即可用 (add an element to the queue, making it available after the specified delay)
}
```

**Methods**

-   `SubmitWithFunc` : submit a task with a handle function. `msg` is the handle function parameter. `fn` is the handle function. If `fn` is `nil`, the handle function will be `WithHandleFunc` to set.
-   `Submit` : submit a task without a handle function. `msg` is the handle function parameter. The handle function will be `WithHandleFunc` to set.
-   `SubmitAfterWithFunc` : submit a task with a handle function after a delay. `msg` is the handle function parameter. `fn` is the handle function. If `fn` is `nil`, the handle function will be `WithHandleFunc` to set. `delay` is the delay time (`time.Duration`).
-   `SubmitAfter` : submit a task without a handle function after a delay. `msg` is the handle function parameter. The handle function will be `WithHandleFunc` to set. `delay` is the delay time (`time.Duration`).
-   `Stop` : stop the pipeline.

**Callback**

-   `OnBefore` : callback function before task processing.
-   `OnAfter` : callback function after task processing.

**Example**

```go
package main

import (
	"fmt"
	"time"

	k "github.com/shengyanli1982/karta"
	"github.com/shengyanli1982/workqueue"
)

func handleFunc(msg any) (any, error) {
	fmt.Println("default:", msg)
	return msg, nil
}

func main() {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(workqueue.NewSimpleQueue(nil))
	pl := k.NewPipeline(queue, c)

	defer func() {
		pl.Stop()
		queue.Stop()
	}()

	_ = pl.Submit("foo")
	_ = pl.SubmitWithFunc(func(msg any) (any, error) {
		fmt.Println("SpecFunc:", msg)
		return msg, nil
	}, "bar")

	time.Sleep(time.Second)
}
```

**Result**

```bash
$ go run demo.go
default: foo
SpecFunc: bar
```
