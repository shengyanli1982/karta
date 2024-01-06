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
$ go run 1.go
3
```

# Advantage

-   Simple and easy to use
-   No third-party dependencies
-   Low memory usage
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
-   `WithResult`: flags whether all tasks result can be recorded. The default is `false`.

### Components

#### 1. Group

`Group` is a batch processing component. It can be used to batch process tasks in on

**Methods**

**Example**

**Result**

#### 2. Queue

**Methods**

**Example**

**Result**
