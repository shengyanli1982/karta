<div align="center">
	<img src="assets/logo.png" alt="logo" width="550px">
</div>

[![Go Report Card](https://goreportcard.com/badge/github.com/shengyanli1982/karta)](https://goreportcard.com/report/github.com/shengyanli1982/karta)
[![Build Status](https://github.com/shengyanli1982/karta/actions/workflows/test.yaml/badge.svg)](https://github.com/shengyanli1982/karta/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/shengyanli1982/karta.svg)](https://pkg.go.dev/github.com/shengyanli1982/karta)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/shengyanli1982/karta)

# Karta

Karta is a lightweight, type-safe task batch and asynchronous processing library for Go. Rewritten from the ground up using Go 1.23+ generics, it provides compile-time type safety for both input and output — no more `any` casts, no more runtime surprises.

The library offers two core components:

- **Group[In, Out]** — concurrent batch processing with ordered results
- **Pipeline[In, Out]** — async task submission with `Future[T]` for deferred result retrieval

## Features

- **Go 1.23+ generics** — type-safe `Group[In, Out]` and `Pipeline[In, Out]`
- **Result[T] and Future[T]** — explicit result handling with `Ok()`, `Unwrap()`, `Get(ctx)`, and `Then(fn)` chaining
- **High-performance fast paths** — `Group.Map` uses a sequential fast path for small batches, eliminating goroutine overhead; `Future` uses lazy channel allocation and atomic checks to minimize allocations
- **Middleware chain** — composable `Middleware[In, Out]` wrapping pattern (Recovery, Logging, Timeout, RateLimit, Retry, Metrics, Tracing)
- **11 Scheduler implementations** — SimpleScheduler (built-in) + FIFO, Delay, Priority, RateLimiting, Timer, Bounded, Retry, DLQ, Lease, Composite
- **LifecycleManager** — signal-aware graceful shutdown with per-component timeout
- **v1compat package** — backward-compatible wrappers for incremental migration from karta v1
- **Minimal dependencies** — core uses only `gs` (lifecycle) and `golang.org/x/time/rate` (rate limiting); middleware adds optional Prometheus and OpenTelemetry support

## Installation

```bash
go get github.com/shengyanli1982/karta/v2
```

Requires Go 1.23 or later.

## Quick Start

### Group — Concurrent Batch Processing

```go
package main

import (
	"context"
	"fmt"

	karta "github.com/shengyanli1982/karta/v2"
)

func main() {
	// Create a Group[int, string] that converts integers to their string representation.
	g := karta.NewGroup[int, string](
		func(ctx context.Context, n int) (string, error) {
			return fmt.Sprintf("item-%d", n), nil
		},
		karta.WithGroupWorkers(4),
	)
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	for i, r := range results {
		if r.Ok() {
			fmt.Printf("[%d] %s\n", i, r.Value)
		} else {
			fmt.Printf("[%d] error: %v\n", i, r.Err)
		}
	}
	// Output:
	// [0] item-1
	// [1] item-2
	// [2] item-3
}
```

### Pipeline — Async Submission with Future

`Pipeline` supports three submission modes: `Submit` (immediate), `SubmitAfter` (delayed), and `SubmitWithHandler` (per-task handler override). Returned `Future[T]` objects support blocking `Get(ctx)` and callback chaining with `Then(fn)`:

```go
package main

import (
	"context"
	"fmt"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

func main() {
	sched := karta.NewSimpleScheduler(64)

	p := karta.NewPipeline[string, int](
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		sched,
		karta.WithPipelineWorkers(2),
	)
	defer p.Stop()

	ctx := context.Background()
	f, err := p.Submit(ctx, "hello")
	if err != nil {
		panic(err)
	}

	// Chain a callback when the result is ready (non-blocking)
	f.Then(func(r karta.Result[int]) {
		if r.Ok() {
			fmt.Println("async length:", r.Value)
		}
	})

	// Or block until the result is ready with a timeout.
	getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result := f.Get(getCtx)
	if result.Ok() {
		fmt.Println("length:", result.Value) // length: 5
	}
}
```

### Middleware — Composable Handler Enhancement

```go
package main

import (
	"context"
	"log/slog"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/karta/v2/middleware"
)

func main() {
	logger := slog.Default()

	g := karta.NewGroup[string, string](
		func(ctx context.Context, s string) (string, error) {
			return "processed: " + s, nil
		},
		karta.WithGroupWorkers(2),
		karta.WithGroupMiddleware(
			middleware.Recovery[string, string](),
			middleware.Logging[string, string](logger),
			middleware.Timeout[string, string](30*time.Second),
		),
	)
	defer g.Stop()

	results := g.Map(context.Background(), []string{"alpha", "beta"})
}
```

## Core Concepts

```
Handler[In, Out]          The processing function: func(ctx context.Context, input In) (Out, error)
Result[T]                 Synchronous result container (Value + Err)
Future[T]                 Asynchronous result container (Get(ctx), Then(fn))
Group[In, Out]            Batch processor: Map(ctx, inputs) -> []Result[Out]
Pipeline[In, Out]         Async processor: Submit(ctx, input) -> *Future[Out]
Scheduler                 Task queue abstraction (Enqueue, Dequeue, Done, Shutdown)
Middleware[In, Out]       Handler wrapper: func(Handler) Handler
Callback                  Lifecycle hooks: OnBefore(ctx, input), OnAfter(ctx, input, output, err)
LifecycleManager          Signal-aware graceful shutdown coordinator
```

**Relationship**: A `Group` or `Pipeline` owns a `Handler`. The `Handler` can be wrapped by a `Middleware` chain. A `Pipeline` uses a `Scheduler` for task queuing. `Group.Map` returns `[]Result[Out]`; `Pipeline.Submit` returns `*Future[Out]`.

## Configuration

### GroupOption

| Option                            | Description                    | Default |
| --------------------------------- | ------------------------------ | ------- |
| `WithGroupWorkers(n int)`         | Number of concurrent workers   | `2`     |
| `WithGroupCallback(cb Callback)`  | Lifecycle callback             | no-op   |
| `WithGroupMiddleware(mws ...any)` | Middleware to wrap the handler | none    |

### PipelineOption

| Option                               | Description                                      | Default |
| ------------------------------------ | ------------------------------------------------ | ------- |
| `WithPipelineWorkers(n int)`         | Number of executor goroutines                    | `2`     |
| `WithIdleTimeout(d time.Duration)`   | Idle worker auto-exit timeout                    | `10s`   |
| `WithScanInterval(d time.Duration)`  | Scheduler poll interval                          | `3s`    |
| `WithSpawnRate(n int)`               | Worker spawn rate limit (per second)             | `4`     |
| `WithBurstLimit(n int)`              | Maximum burst size for worker spawning           | `8`     |
| `WithPipelineCallback(cb Callback)`  | Lifecycle callback                               | no-op   |
| `WithPipelineMiddleware(mws ...any)` | Middleware to wrap the handler                   | none    |

## Schedulers

All schedulers implement the `Scheduler` interface and are available in the `scheduler` sub-package:

```go
import "github.com/shengyanli1982/karta/v2/scheduler"
```

| Scheduler           | Constructor                                   | Description                                                  |
| ------------------- | --------------------------------------------- | ------------------------------------------------------------ |
| **SimpleScheduler** | `karta.NewSimpleScheduler(bufferSize)`        | Channel-based FIFO (built-in, no sub-package)                |
| **FIFO**            | `scheduler.NewFIFOScheduler()`                | workqueue.Queue-based FIFO                                   |
| **Delay**           | `scheduler.NewDelayScheduler()`               | Delayed task support via `TaskEnvelope.Delay`                |
| **Priority**        | `scheduler.NewPriorityScheduler()`            | Priority queue (lower number = higher priority)              |
| **RateLimiting**    | `scheduler.NewRateLimitingScheduler(limiter)` | Token-bucket rate-limited queue                              |
| **Timer**           | `scheduler.NewTimerScheduler()`               | Absolute deadline + relative delay scheduling                |
| **Bounded**         | `scheduler.NewBoundedScheduler(capacity)`     | Bounded blocking queue with back-pressure (may briefly block when full before returning ErrSchedulerFull) |
| **Retry**           | `scheduler.NewRetryScheduler(policy)`         | Automatic retry with configurable policy                     |
| **DLQ**             | `scheduler.NewDLQScheduler(maxRetries)`       | Dead-letter queue for failed tasks                           |
| **Lease**           | `scheduler.NewLeaseScheduler(leaseTimeout)`   | Lease-based task ownership with auto-requeue                 |
| **Composite**       | `scheduler.NewCompositeScheduler(scheds...)`  | Chains multiple schedulers (enqueue → first, dequeue → last) |

## Middleware

Pre-built middleware in the `middleware` sub-package:

```go
import "github.com/shengyanli1982/karta/v2/middleware"
```

| Middleware    | Signature                                   | Description                                                   |
| ------------- | ------------------------------------------- | ------------------------------------------------------------- |
| **Recovery**  | `Recovery[In, Out]()`                       | Catches panics, returns error with stack trace                |
| **Logging**   | `Logging[In, Out](logger *slog.Logger)`     | Logs input/output/elapsed time                                |
| **Timeout**   | `Timeout[In, Out](d time.Duration)`         | Sets per-handler deadline via `context.WithTimeout`           |
| **RateLimit** | `RateLimit[In, Out](limiter *rate.Limiter)` | Token-bucket rate limiting via `golang.org/x/time/rate`       |
| **Retry**     | `Retry[In, Out](opts ...RetryOption)`       | Retries on failure with configurable attempts/delay/condition |
| **Metrics**   | `Metrics[In, Out](opts ...MetricsOption)`   | Prometheus histogram + counter instrumentation                |
| **Tracing**   | `Tracing[In, Out any](tracer trace.Tracer, opts ...TracingOption)` | OpenTelemetry span creation with input/output attributes |

Middleware is combined using `karta.Chain`:

```go
combined := karta.Chain(
	middleware.Recovery[MyIn, MyOut](),
	middleware.Logging[MyIn, MyOut](logger),
	middleware.Timeout[MyIn, MyOut](10*time.Second),
)
handler := combined(myHandler)
```

Or passed directly to `WithGroupMiddleware` / `WithPipelineMiddleware`.

### LifecycleManager

Coordinate graceful shutdown across multiple components:

```go
lm := karta.NewLifecycleManager(
	karta.WithSignals(os.Interrupt, syscall.SIGTERM),
	karta.WithShutdownTimeout(30*time.Second),
	karta.WithManaged(group, pipeline),
)

// Blocks until signal received, then gracefully shuts down all components
// Each component gets its own timeout budget (total / count)
lm.WaitForSignal()
```

The `Shutdown()` method is idempotent and respects the global timeout. Slow components are skipped after their individual timeout expires, preventing goroutine leaks.

## Performance

Karta is optimized for high-throughput workloads with minimal allocation overhead. Benchmark results measured on i5-12400F / Windows / go1.25:

| Benchmark                                     | ns/op  | B/op  | allocs/op |
| --------------------------------------------- | ------ | ----- | --------- |
| `GroupMap` (100 items)                        | ~639   | 2688  | 1         |
| `GroupMap_Parallel` (100 items)               | ~455   | 2688  | 1         |
| `GroupMap_LargeBatch` (1000 items, 8 workers) | ~39100 | 24927 | 9         |
| `PipelineSubmit`                              | ~1400  | 205   | 3         |
| `PipelineSubmit_Parallel`                     | ~1900  | 207   | 3         |
| `FutureGet` (resolved)                        | ~31    | 80    | 1         |
| `FutureResolve`                               | ~45    | 80    | 1         |
| `FutureThen`                                  | ~1190  | 295   | 5         |
| `MiddlewareChain` (3 MW)                      | ~1.35  | 0     | 0         |
| `SimpleScheduler`                             | ~114   | 0     | 0         |

Key optimizations:
- **Sequential fast path**: `Group.Map` bypasses goroutine scheduling for small batches (<128 items), achieving single-digit microsecond latencies.
- **Lazy channel allocation**: `Future` only allocates a `done` channel when a `Get` caller actually blocks, saving allocations on resolved futures.
- **sync.Pool reuse**: `TaskEnvelope` and pipeline work contexts are pooled, reducing per-task allocations.
- **Hybrid wait for large batches**: `Group.Map`'s large-batch parallel path combines bounded spinning with blocking waits to balance tail latency against CPU overhead.

Run `go test -bench=. -benchmem ./...` to verify on your hardware.

## v1 Compatibility

The `v1compat` sub-package provides drop-in wrappers for incremental migration from karta v1 to v2:

```go
import "github.com/shengyanli1982/karta/v2/v1compat"
```

```go
// v1-style handler and config work unchanged:
config := v1compat.NewV1Config()
config.WithHandleFunc(func(msg any) (any, error) {
	return msg, nil
}).WithWorkerNumber(4)

g := v1compat.NewV1Group(config)
defer g.Stop()

results := g.Map([]any{1, 2, 3}) // returns []any, not []Result[any]
```

`v1compat` also provides `HandlerAdapter` and `CallbackAdapter` for bridging individual v1 types to v2 interfaces. See [MIGRATION.md](./MIGRATION.md) for the full migration guide.

## License

[MIT](./LICENSE)
