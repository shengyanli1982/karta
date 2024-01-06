<div align="center">
	<h1>Karta</h1>
	<img src="assets/logo.png" alt="logo" width="300px">
	<h4>A lightweight mapping component for tasks</h4>
</div>

# Introduction

`Karta` component is a lightweight mapping component for tasks. It very similar to `ThreadPoolExecutor` in Python. It provides a simple interface to submit tasks and get the results.

Why `Karta`? In my job, I need to do a lot of job processing. I want to use like `ThreadPoolExecutor` code to do the job. However, in golang there is no such component. So I write one.

`Karta` is very simple, it only has two processes: `Group` and `Queue`.

-   `Group`: tasks batch processing by `Group`.
-   `Queue`: task one by one processing by `Queue`. Any task can specify a handle function.

### Example

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

-   `WithWorkerNumber`: set the number of workers. The default is the number is `2`.
-   `WithCallback` : set the callback function. The default is `&emptyCallback{}`.
-   `WithHandleFunc` : set the handle function. The default is `defaultMsgHandleFunc`.
-   `WithResult`: flags whether all tasks result can be recorded. The default is `false`. It only works for `Group`.

### Components

#### 1. Group

`Group` is a batch processing component. It can be used to batch process tasks once at a time.

**Methods**

-   `Map`: batch process tasks by given a slice of objects, each object is handle func parameter. The method will return a slice of results which use `WithResult` to set `true`.

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

#### 2. Queue

`Queue` is a task processing component. It can be used to process tasks one by one.

**Methods**

-   `SubmitWithFunc`: submit a task with a handle function. `data` is the handle function parameter. `fn` is the handle function. If `fn` is `nil`, the handle function will be `WithHandleFunc` to set.
-   `Submit`: submit a task without a handle function. `data` is the handle function parameter. The handle function will be `WithHandleFunc` to set.

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
	q0 := workqueue.NewSimpleQueue(nil)
	q := k.NewQueue(q0, c)

	defer func() {
		q.Stop()
		q0.Stop()
	}()

	_ = q.Submit("foo")
	_ = q.SubmitWithFunc(func(msg any) (any, error) {
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
