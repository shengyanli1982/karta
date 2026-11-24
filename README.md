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
- **Per-task submit options** — `WithPriority`, `WithDelay`, `WithDeadline`, and `WithDedupKey` (singleflight deduplication) on `Submit` / `SubmitWithHandler`
- **Auto retry & dead-letter routing** — `WithAutoRetry(true)` lets the executor drive scheduler-level retries; exhausted tasks can be routed to a DLQ automatically
- **Runtime dynamics** — `Pipeline.Tune(n)` live worker resizing and `SubmitEvery` periodic submission with skip-if-busy semantics
- **LifecycleManager** — signal-aware graceful shutdown with per-component timeout
- **v1compat package** — backward-compatible wrappers for incremental migration from karta v1
- **Minimal dependencies** — core uses only `golang.org/x/time/rate` (rate limiting); middleware adds optional Prometheus and OpenTelemetry support

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

`Pipeline` supports three submission modes: `Submit` (immediate), `SubmitAfter` (delayed), and `SubmitWithHandler` (per-task handler override). `Submit` and `SubmitWithHandler` accept variadic submit options (`WithPriority`, `WithDelay`, `WithDeadline`, `WithDedupKey`), and `SubmitEvery` adds ticker-driven periodic submission. Returned `Future[T]` objects support blocking `Get(ctx)` and callback chaining with `Then(fn)`:

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
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr/funcr"
	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/karta/v2/middleware"
)

func main() {
	logger := funcr.New(func(prefix, args string) {
		fmt.Fprintln(os.Stdout, prefix, args)
	}, funcr.Options{})

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
| `WithPipelineCallback(cb Callback)`  | Lifecycle callback                               | no-op   |
| `WithPipelineMiddleware(mws ...any)` | Middleware to wrap the handler                   | none    |
| `WithAutoRetry(enabled bool)`        | Executor drives scheduler-level `Retryer` on handler error | `false` |

## Schedulers

All schedulers implement the `Scheduler` interface and are available in the `scheduler` sub-package:

```go
import "github.com/shengyanli1982/karta/v2/scheduler"
```

| Scheduler           | Constructor                                   | Description                                                  | Pipeline integration |
| ------------------- | --------------------------------------------- | ------------------------------------------------------------ | -------------------- |
| **SimpleScheduler** | `karta.NewSimpleScheduler(bufferSize)`        | Channel-based FIFO (built-in, no sub-package)                | Full — FIFO by design |
| **FIFO**            | `scheduler.NewFIFOScheduler()`                | workqueue.Queue-based FIFO                                   | Full |
| **Delay**           | `scheduler.NewDelayScheduler()`               | Delayed task support via `TaskEnvelope.Delay`                | Full — submit with `karta.WithDelay(d)`; the scheduler holds the task until due (`SubmitAfter` still waits client-side and clears `Delay`) |
| **Priority**        | `scheduler.NewPriorityScheduler()`            | Priority queue (lower number = higher priority)              | Full — submit with `karta.WithPriority(n)` (lower number = higher priority) |
| **RateLimiting**    | `scheduler.NewRateLimitingScheduler(limiter)` | Token-bucket rate-limited queue                              | Full — dequeue-side rate limiting, independent of envelope fields |
| **Timer**           | `scheduler.NewTimerScheduler()`               | Absolute deadline + relative delay scheduling                | Full — submit with `karta.WithDeadline(t)` (absolute) or `karta.WithDelay(d)` (relative) |
| **Bounded**         | `scheduler.NewBoundedScheduler(capacity)`     | Bounded blocking queue with back-pressure (may briefly block when full before returning ErrSchedulerFull) | Full — enqueue-side capacity check |
| **Retry**           | `scheduler.NewRetryScheduler(policy)`         | Automatic retry with configurable policy                     | Full — with `karta.WithAutoRetry(true)` the executor drives the `Retryer` interface automatically (default off); `middleware.Retry` remains available for in-handler retries |
| **DLQ**             | `scheduler.NewDLQScheduler(maxRetries)`       | Dead-letter queue for failed tasks                           | Full — dead letters are readable via `GetDeadLetters()` (type assertion); for automatic retry-exhaustion → dead-letter routing use `NewRetrySchedulerWithDLQ(policy)` + `karta.WithAutoRetry(true)` |
| **Lease**           | `scheduler.NewLeaseScheduler(leaseTimeout)`   | Lease-based task ownership with auto-requeue                 | At-least-once delivery semantics — `Dequeue` grants a lease, `Done` acks it; unacked tasks are automatically requeued after the lease timeout |
| **Composite**       | `scheduler.NewCompositeScheduler(scheds...)`  | Chains multiple schedulers (enqueue → first, dequeue → last) | Depends on the chained schedulers |

> **Note on Pipeline integration**: Scheduling metadata is first-class on the submit path — `Submit` / `SubmitWithHandler` accept `karta.WithPriority(n)`, `karta.WithDelay(d)`, `karta.WithDeadline(t)`, and `karta.WithDedupKey(key)` options, which are written to the `TaskEnvelope` before enqueuing, so field-driven schedulers (Priority / Delay / Timer) take effect natively behind a Pipeline. Schedulers implementing the optional `Retryer` interface (Retry, Retry+DLQ) are driven automatically by the executor when the Pipeline is created with `karta.WithAutoRetry(true)` (default off). `SubmitAfter` does not accept submit options: it waits client-side and clears `Delay` before enqueuing.

## Advanced Features

### Submit Options — Per-Task Scheduling Metadata

`Submit` / `SubmitWithHandler` accept variadic `SubmitOption`s, applied to the task envelope before enqueuing:

```go
sched := scheduler.NewPriorityScheduler()
p := karta.NewPipeline[Job, Report](handler, sched)
defer p.Stop()

p.Submit(ctx, job, karta.WithPriority(1))                         // lower number = higher priority
p.Submit(ctx, job, karta.WithDelay(5*time.Second))                // scheduler-native delay (NewDelayScheduler / NewTimerScheduler)
p.Submit(ctx, job, karta.WithDeadline(time.Now().Add(time.Hour))) // absolute release time (NewTimerScheduler)
```

### Auto Retry & Dead-Letter Queue

With `karta.WithAutoRetry(true)`, the executor drives the scheduler's `Retryer` interface whenever a handler returns an error; the `Future` resolves only after final success or retry exhaustion. `NewRetrySchedulerWithDLQ` additionally routes exhausted tasks to a dead-letter queue:

```go
sched := scheduler.NewRetrySchedulerWithDLQ(nil) // nil = workqueue default exponential backoff
p := karta.NewPipeline[In, Out](handler, sched, karta.WithAutoRetry(true))
defer p.Stop()

f, _ := p.Submit(ctx, in) // failed attempts are retried automatically

// Dead letters are exposed via type assertion (not part of the Scheduler interface)
if dlq, ok := sched.(interface{ GetDeadLetters() []*workqueue.DeadLetter }); ok {
	logger.Info("dead letters collected", "count", len(dlq.GetDeadLetters()))
}
```

### Task Deduplication (Singleflight)

`WithDedupKey` merges duplicate submissions while a task is in flight: subsequent submits with the same key are not enqueued again — they share the first task's `Future`, so all callers observe the same result. Input/handler differences on merged submits are ignored (the first registration wins). The key is released when the task finally resolves (success, error, retry exhaustion, or cancellation), allowing later submits to execute again.

```go
f1, _ := p.Submit(ctx, "user:42", karta.WithDedupKey("user:42"))
f2, _ := p.Submit(ctx, "user:42", karta.WithDedupKey("user:42"))
// f1 == f2 — the handler runs once while the key is in flight
```

### Dynamic Worker Tuning

`Tune` resizes the executor pool at runtime and `Workers` reports the current target. Scaling up spawns the delta gradually (throttled by the `WithSpawnRate` limiter); under no load, idle exit converges the live worker count to one rather than the target — incoming submissions spawn workers back up toward it. Scaling down lets surplus workers exit naturally when idle (at least one worker is always kept).

```go
if err := p.Tune(16); err != nil {
	logger.Error(err, "failed to tune workers") // ErrInvalidWorkerCount (n < 1) or ErrPipelineClosed
	os.Exit(1)
}
fmt.Println(p.Workers())         // 16 — current target (spawn cap)
fmt.Println(p.GetWorkerNumber()) // live executor goroutines (converges to target under load)
```

### Periodic Submission

`SubmitEvery` is a ticker-driven helper for periodic jobs (cache cleanup, polling, report generation). It never piles up: if the previous `Future` has not resolved yet, the tick is skipped. It returns an idempotent `stop` function, and also stops when the context is canceled or the Pipeline is closed.

```go
p := karta.NewPipeline[CleanupInput, struct{}](cleanup, karta.NewSimpleScheduler(16))
defer p.Stop()

// Run cleanup every 5 minutes; slow runs skip ticks instead of queueing up
stop := karta.SubmitEvery(ctx, p, 5*time.Minute, CleanupInput{})
defer stop()
```

## Middleware

Pre-built middleware in the `middleware` sub-package:

```go
import "github.com/shengyanli1982/karta/v2/middleware"
```

| Middleware    | Signature                                   | Description                                                   |
| ------------- | ------------------------------------------- | ------------------------------------------------------------- |
| **Recovery**  | `Recovery[In, Out]()`                       | Catches panics, returns error with stack trace                |
| **Logging**   | `Logging[In, Out](logger logr.Logger)`      | Logs input/output/elapsed time                                |
| **Timeout**   | `Timeout[In, Out](d time.Duration)`         | Sets per-handler deadline via `context.WithTimeout`           |
| **RateLimit** | `RateLimit[In, Out](limiter *rate.Limiter)` | Token-bucket rate limiting via `golang.org/x/time/rate`       |
| **Retry**     | `Retry[In, Out](opts ...RetryOption)`       | Retries on failure with configurable attempts/delay/condition |
| **Metrics**   | `Metrics[In, Out](opts ...MetricsOption)`   | Prometheus histogram + counter instrumentation                |
| **Tracing**   | `Tracing[In, Out any](tracer trace.Tracer, opts ...TracingOption)` | OpenTelemetry span creation with error status recording (input/output values are not recorded) |

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

Karta is optimized for high-throughput workloads with minimal allocation overhead. Benchmark results measured on i5-12400F / Windows / go1.26:

| Benchmark                                     | ns/op       | B/op  | allocs/op |
| --------------------------------------------- | ----------- | ----- | --------- |
| `GroupMap` (100 items)                        | ~648        | 2688  | 1         |
| `GroupMap_Parallel` (100 items)               | ~457        | 2688  | 1         |
| `GroupMap_LargeBatch` (1000 items, 8 workers) | ~12355      | 25039 | 9         |
| `PipelineSubmit`                              | ~1495       | 206   | 3         |
| `PipelineSubmit_Parallel`                     | ~1238       | 208   | 3         |
| `PipelineSubmitAfter` (1ms delay)             | ~1,777,000  | 521   | 7         |
| `FutureGet` (resolved)                        | ~35         | 80    | 1         |
| `FutureResolve`                               | ~71         | 80    | 1         |
| `FutureThen`                                  | ~254        | 48    | 1         |
| `MiddlewareChain` (3 MW)                      | ~1.35       | 0     | 0         |
| `SimpleScheduler`                             | ~91         | 0     | 0         |

<sub>Note: `FutureThen` measures the `Then` fast path on an already-resolved future; `PipelineSubmitAfter` includes the 1ms timer wait; `PipelineSubmit_Parallel` has inherent high variance (±20%).</sub>

Key optimizations:
- **Sequential fast path**: `Group.Map` bypasses goroutine scheduling for small batches (≤128 items), achieving single-digit microsecond latencies.
- **Lazy channel allocation**: `Future` only allocates a `done` channel when a `Get` caller actually blocks, saving allocations on resolved futures.
- **sync.Pool reuse**: `TaskEnvelope` and pipeline work contexts are pooled, reducing per-task allocations.
- **Hybrid wait for large batches**: `Group.Map`'s large-batch parallel path combines bounded spinning with blocking waits to balance tail latency against CPU overhead.
- **Chunked claim for large batches**: `Group.Map`'s parallel path has workers claim indices in chunks (`chunk = clamp(n/(workers×4), 1, 64)`), eliminating cache-line contention from per-item atomic claims and cutting 1000-item batch latency by ~65%.
- **Blocking dequeue**: `scheduler` sub-package adapters consume tasks via workqueue v2.3.4's native blocking `GetWithContext` instead of polling with exponential backoff, eliminating dequeue latency and idle CPU spin (significant measured drops on FIFO/Lease dequeue paths).

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
