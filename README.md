English | [中文](./README_CN.md)

<div align="center">
	<img src="assets/logo.png" alt="logo" width="550px">
</div>

[![Go Report Card](https://goreportcard.com/badge/github.com/shengyanli1982/karta)](https://goreportcard.com/report/github.com/shengyanli1982/karta)
[![Build Status](https://github.com/shengyanli1982/karta/actions/workflows/test.yaml/badge.svg)](https://github.com/shengyanli1982/karta/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/shengyanli1982/karta.svg)](https://pkg.go.dev/github.com/shengyanli1982/karta)

# Introduction

The `Karta` component is a lightweight task batch and asynchronous processing module, similar to the `ThreadPoolExecutor` in Python. It provides a simple interface to submit tasks and retrieve results.

Why `Karta`? In my work, I often need to process a large number of jobs. I wanted to use code similar to `ThreadPoolExecutor` in Python to handle these jobs. However, there was no such component available in Golang, so I decided to create one.

`Karta` is designed to be simple and consists of two main processes: `Group` and `Pipeline`.

-   `Group`: Batch processing of tasks using the `Group` component.
-   `Pipeline`: Sequential processing of tasks using the `Pipeline` component. Each task can specify a handle function.

# Advantages

-   Simple and user-friendly
-   Lightweight with no external dependencies
-   Supports callback functions for custom actions
-   Thread-safe, all components are designed with concurrency in mind

# Design

Following the design, the architecture UML diagram for `Karta` is shown below:

![design](assets/architecture.png)

# Installation

```bash
go get github.com/shengyanli1982/karta
```

# Quick Start

`Karta` is incredibly easy to use. With just a few lines of code, you can efficiently batch process tasks.

### Config

The `Karta` library provides a config object that allows you to customize the behavior of the batch processing. The config object offers the following methods for configuration:

-   `WithWorkerNumber`: Sets the number of workers. The default value is `2`, with a maximum of `524280`.
-   `WithCallback`: Sets the callback function. The default value is `&emptyCallback{}`.
-   `WithHandleFunc`: Sets the handle function. The default value is `defaultMsgHandleFunc`.
-   `WithResult`: Specifies whether to record the results of all tasks. The default value is `false`, and it only applies to `Group`.

### Components

#### 1. Group

`Group` is a batch processing component that allows you to process tasks in batches. It uses a fixed number of workers to handle the tasks.

**Methods**

-   `Map`: Processes tasks in batches by providing a slice of objects, with each object serving as a parameter for the handle function. The method returns a slice of results when `WithResult` is set to `true`.

**Callback**

-   `OnBefore`: Callback function executed before task processing.
-   `OnAfter`: Callback function executed after task processing.

**Example**

```go
package main

import (
	"time"

	k "github.com/shengyanli1982/karta"
)

// handleFunc 是一个处理函数，它接收一个任意类型的消息，暂停一段时间（消息值的100毫秒），然后返回该消息和nil错误。
// handleFunc is a handler function that takes a message of any type, sleeps for a duration (100 milliseconds of the message value), and then returns the message and a nil error.
func handleFunc(msg any) (any, error) {
	// 将消息转换为整数，然后暂停该整数值的100毫秒。
	// Convert the message to an integer, then pause for 100 milliseconds of the integer value.
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)

	// 返回接收到的消息和nil错误。
	// Return the received message and a nil error.
	return msg, nil
}

func main() {
	// 创建一个新的配置对象。
	// Create a new configuration object.
	c := k.NewConfig()
	// 设置处理函数，工作线程数量和结果处理。
	// Set the handler function, the number of worker threads, and result processing.
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	// 使用配置创建一个新的工作组。
	// Create a new work group using the configuration.
	g := k.NewGroup(c)

	// 确保在main函数结束时停止工作组。
	// Ensure the work group is stopped when the main function ends.
	defer g.Stop()

	// 将处理函数映射到一组输入值。
	// Map the handler function to a set of input values.
	r0 := g.Map([]any{3, 5, 2})

	// 打印第一个结果的整数值。
	// Print the integer value of the first result.
	println(r0[0].(int))
}
```

**Result**

```bash
$ go run demo.go
3
```

#### 2. Pipeline

`Pipeline` is a task processing component that can process tasks one by one. It dynamically adjusts the number of workers based on the availability of tasks.

Idle workers are automatically closed after `defaultWorkerIdleTimeout` (10 seconds). If there are no tasks for a long time, the number of workers will decrease to `defaultMinWorkerNum` (1).

When a task is submitted using `Submit` or `SubmitWithFunc`, it is processed by an idle worker. If there are no idle workers, a new worker is created. The number of running workers increases to the value set by the `WithWorkerNumber` method if there are not enough running workers.

`Pipeline` requires a queue object that implements the `DelayingQueue` interface to store tasks.

```go
// Queue 接口定义了一个队列应该具备的基本操作。
// The Queue interface defines the basic operations that a queue should have.
type Queue = interface {
	// Put 方法用于将元素放入队列。
	// The Put method is used to put an element into the queue.
	Put(value interface{}) error

	// Get 方法用于从队列中获取元素。
	// The Get method is used to get an element from the queue.
	Get() (value interface{}, err error)

	// Done 方法用于标记元素处理完成。
	// The Done method is used to mark the element as done.
	Done(value interface{})

	// Shutdown 方法用于关闭队列。
	// The Shutdown method is used to shut down the queue.
	Shutdown()

	// IsClosed 方法用于检查队列是否已关闭。
	// The IsClosed method is used to check if the queue is closed.
	IsClosed() bool
}

// DelayingQueue 接口继承了 Queue 接口，并添加了一个 PutWithDelay 方法，用于将元素延迟放入队列。
// The DelayingQueue interface inherits from the Queue interface and adds a PutWithDelay method to put an element into the queue with delay.
type DelayingQueue = interface {
	Queue

	// PutWithDelay 方法用于将元素延迟放入队列。
	// The PutWithDelay method is used to put an element into the queue with delay.
	PutWithDelay(value interface{}, delay int64) error
}
```

**Methods**

-   `SubmitWithFunc`: Submits a task with a handle function. `msg` is the handle function parameter. If `fn` is `nil`, the handle function will be set using `WithHandleFunc`.
-   `Submit`: Submits a task without a handle function. `msg` is the handle function parameter. The handle function will be set using `WithHandleFunc`.
-   `SubmitAfterWithFunc`: Submits a task with a handle function after a delay. `msg` is the handle function parameter. If `fn` is `nil`, the handle function will be set using `WithHandleFunc`. `delay` is the delay time (`time.Duration`).
-   `SubmitAfter`: Submits a task without a handle function after a delay. `msg` is the handle function parameter. The handle function will be set using `WithHandleFunc`. `delay` is the delay time (`time.Duration`).
-   `Stop`: Stops the pipeline.

**Callback**

-   `OnBefore`: Callback function executed before task processing.
-   `OnAfter`: Callback function executed after task processing.

**Example**

```go
package main

import (
	"fmt"
	"time"

	k "github.com/shengyanli1982/karta"
	wkq "github.com/shengyanli1982/workqueue/v2"
)

// handleFunc 是一个处理函数，它接收一个任意类型的消息，打印该消息，然后返回该消息和nil错误。
// handleFunc is a handler function that takes a message of any type, prints the message, and then returns the message and a nil error.
func handleFunc(msg any) (any, error) {
	// 打印接收到的消息。
	// Print the received message.
	fmt.Println("default:", msg)

	// 返回接收到的消息和nil错误。
	// Return the received message and a nil error.
	return msg, nil
}

func main() {
	// 创建一个新的配置对象。
	// Create a new configuration object.
	c := k.NewConfig()

	// 设置处理函数和工作线程数量。
	// Set the handler function and the number of worker threads.
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)

	// 创建一个新的假延迟队列。
	// Create a new fake delaying queue.
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	// 使用队列和配置创建一个新的管道。
	// Create a new pipeline using the queue and configuration.
	pl := k.NewPipeline(queue, c)

	// 确保在main函数结束时停止管道。
	// Ensure the pipeline is stopped when the main function ends.
	defer pl.Stop()

	// 提交一个消息到管道。
	// Submit a message to the pipeline.
	_ = pl.Submit("foo")

	// 使用特定的处理函数提交一个消息到管道。
	// Submit a message to the pipeline using a specific handler function.
	_ = pl.SubmitWithFunc(func(msg any) (any, error) {
		// 打印接收到的消息。
		// Print the received message.
		fmt.Println("SpecFunc:", msg)

		// 返回接收到的消息和nil错误。
		// Return the received message and a nil error.
		return msg, nil
	}, "bar")

	// 暂停一秒钟。
	// Pause for one second.
	time.Sleep(time.Second)
}
```

**Result**

```bash
$ go run demo.go
default: foo
SpecFunc: bar
```
