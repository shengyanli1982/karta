# Migration Guide: v1 to v2

This guide covers migrating from `github.com/shengyanli1982/karta` (v1) to `github.com/shengyanli1982/karta/v2` (v2).

Karta v2 is a complete rewrite using Go generics. The API surface has changed significantly — handlers, results, configuration, and component constructors all now carry type parameters.

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Step 1: Update Go Module Path](#step-1-update-go-module-path)
- [Step 2: Update Handler Signatures](#step-2-update-handler-signatures)
- [Step 3: Migrate Group Usage](#step-3-migrate-group-usage)
- [Step 4: Migrate Pipeline Usage](#step-4-migrate-pipeline-usage)
- [Step 5: Update Callback Interfaces](#step-5-update-callback-interfaces)
- [Step 6: Replace Queue with Scheduler](#step-6-replace-queue-with-scheduler)
- [Breaking Changes Summary](#breaking-changes-summary)
- [Incremental Migration with v1compat](#incremental-migration-with-v1compat)

---

## Prerequisites

- **Go 1.21 or later** (v1 required Go 1.19+)
- Update your `go.mod` to use the v2 module path

Verify your Go version:

```bash
go version
# go1.21.0 or later
```

---

## Step 1: Update Go Module Path

The module path has changed to follow Go's major version convention.

**Before:**

```go
import karta "github.com/shengyanli1982/karta"
```

**After:**

```go
import karta "github.com/shengyanli1982/karta/v2"
```

Update `go.mod`:

```bash
go get github.com/shengyanli1982/karta/v2
go mod tidy
```

---

## Step 2: Update Handler Signatures

The handler function signature has changed from using `any` to type parameters, and now requires `context.Context` as the first parameter.

**Before (v1):**

```go
// v1: func(msg any) (any, error)
func handleFunc(msg any) (any, error) {
    n := msg.(int)
    return n * 2, nil
}
```

**After (v2):**

```go
// v2: func(ctx context.Context, input In) (Out, error)
func handleFunc(ctx context.Context, n int) (int, error) {
    return n * 2, nil
}
```

If you need to accept cancellation signals in your handler:

```go
func handleFunc(ctx context.Context, n int) (int, error) {
    select {
    case <-ctx.Done():
        return 0, ctx.Err()
    default:
    }
    return n * 2, nil
}
```

---

## Step 3: Migrate Group Usage

`Group` is now generic. The constructor takes a typed handler and functional options instead of a `*Config` object. `Map` requires a `context.Context` and returns `[]Result[Out]` instead of `[]any`.

**Before (v1):**

```go
c := karta.NewConfig()
c.WithHandleFunc(handleFunc).WithWorkerNumber(4).WithResult()

g := karta.NewGroup(c)
defer g.Stop()

results := g.Map([]any{1, 2, 3})
val := results[0].(int)
```

**After (v2):**

```go
g := karta.NewGroup[int, int](
    handleFunc,
    karta.WithGroupWorkers(4),
)
defer g.Stop()

results := g.Map(context.Background(), []int{1, 2, 3})
if results[0].Ok() {
    val := results[0].Value // int, no type assertion needed
}
```

### Key differences

| Aspect | v1 | v2 |
|--------|-----|-----|
| Constructor | `NewGroup(config *Config)` | `NewGroup[In, Out](handler, opts...)` |
| Map input | `Map(elements []any)` | `Map(ctx, inputs []In)` |
| Map result | `[]any` (type assertion needed) | `[]Result[Out]` (typed) |
| Result access | `results[i].(int)` | `results[i].Value` / `results[i].Unwrap()` |
| Config | `NewConfig()` builder | Functional options (`WithGroupWorkers`, etc.) |

---

## Step 4: Migrate Pipeline Usage

`Pipeline` now takes a typed handler and a `Scheduler` instead of a `Queue`/`DelayingQueue`. `Submit` returns `*Future[Out]` for deferred result retrieval.

**Before (v1):**

```go
queue := karta.NewFakeDelayingQueue(wkq.NewQueue(nil))
c := karta.NewConfig()
c.WithHandleFunc(handleFunc).WithWorkerNumber(2)

p := karta.NewPipeline(queue, c)
defer p.Stop()

_ = p.Submit("message")
_ = p.SubmitWithFunc(customFunc, "other")
```

**After (v2):**

```go
sched := karta.NewSimpleScheduler(64)

p := karta.NewPipeline[string, string](
    handleFunc,
    sched,
    karta.WithPipelineWorkers(2),
)
defer p.Stop()

ctx := context.Background()

// Submit returns a Future
f, err := p.Submit(ctx, "message")
if err != nil {
    log.Fatal(err)
}
result := f.Get(ctx)

// Per-task handler override
f2, _ := p.SubmitWithHandler(ctx, customHandler, "other")

// Delayed submission
f3, _ := p.SubmitAfter(ctx, "delayed", 5*time.Second)
```

### Key differences

| Aspect | v1 | v2 |
|--------|-----|-----|
| Constructor | `NewPipeline(queue, config)` | `NewPipeline[In, Out](handler, scheduler, opts...)` |
| Queue | `Queue` / `DelayingQueue` interfaces | `Scheduler` interface |
| Submit result | Ignored (fire-and-forget) | `(*Future[Out], error)` |
| Per-task handler | `SubmitWithFunc(fn, msg)` | `SubmitWithHandler(ctx, handler, input)` |
| Delayed submit | `SubmitAfter(msg, delay)` | `SubmitAfter(ctx, input, delay)` |

### Using Future

```go
// Blocking get with timeout
getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
result := f.Get(getCtx)

// Chained callback
f.Then(func(r karta.Result[string]) {
    if r.Ok() {
        fmt.Println(r.Value)
    }
})
```

---

## Step 5: Update Callback Interfaces

The `Callback` interface is now context-aware. Both `OnBefore` and `OnAfter` receive `context.Context` as the first parameter.

**Before (v1):**

```go
type Callback interface {
    OnBefore(msg any)
    OnAfter(msg, result any, err error)
}
```

**After (v2):**

```go
type Callback interface {
    OnBefore(ctx context.Context, input any)
    OnAfter(ctx context.Context, input, output any, err error)
}
```

### Migration

Add `context.Context` as the first parameter to your callback methods:

```go
// Before
func (c *myCallback) OnBefore(msg any) {
    log.Printf("processing: %v", msg)
}

// After
func (c *myCallback) OnBefore(ctx context.Context, input any) {
    log.Printf("processing: %v", input)
}
```

To use callbacks with `Group` or `Pipeline`:

```go
// Before
c.WithCallback(&myCallback{})

// After
karta.WithGroupCallback(&myCallback{})
karta.WithPipelineCallback(&myCallback{})
```

---

## Step 6: Replace Queue with Scheduler

v2 replaces the `Queue` / `DelayingQueue` interfaces with the `Scheduler` interface. The built-in `SimpleScheduler` covers most use cases.

**Before (v1):**

```go
// v1 required an external queue implementation
queue := karta.NewFakeDelayingQueue(wkq.NewQueue(nil))
p := karta.NewPipeline(queue, config)
```

**After (v2):**

```go
// v2 built-in scheduler (channel-based FIFO)
sched := karta.NewSimpleScheduler(256)
p := karta.NewPipeline(handler, sched)
```

For advanced scheduling, use the `scheduler` sub-package:

```go
import "github.com/shengyanli1982/karta/v2/scheduler"

// Priority-based scheduling
sched := scheduler.NewPriorityScheduler()

// Rate-limited scheduling
limiter := rate.NewLimiter(rate.Every(time.Second), 10)
sched := scheduler.NewRateLimitingScheduler(limiter)

// Delayed task scheduling
sched := scheduler.NewDelayScheduler()
```

---

## Breaking Changes Summary

| # | Change | v1 | v2 |
|---|--------|-----|-----|
| 1 | Go version | 1.19+ | 1.21+ |
| 2 | Module path | `github.com/shengyanli1982/karta` | `github.com/shengyanli1982/karta/v2` |
| 3 | Config | `NewConfig()` builder | Removed; functional options |
| 4 | Handler type | `func(any) (any, error)` | `func(ctx, In) (Out, error)` |
| 5 | Group constructor | `NewGroup(config)` | `NewGroup[In, Out](handler, opts...)` |
| 6 | Group.Map | `Map([]any) []any` | `Map(ctx, []In) []Result[Out]` |
| 7 | Pipeline constructor | `NewPipeline(queue, config)` | `NewPipeline[In, Out](handler, scheduler, opts...)` |
| 8 | Pipeline.Submit | `Submit(msg) error` | `Submit(ctx, input) (*Future[Out], error)` |
| 9 | SubmitWithFunc | `SubmitWithFunc(fn, msg)` | `SubmitWithHandler(ctx, handler, input)` |
| 10 | Queue dependency | `Queue` / `DelayingQueue` | `Scheduler` interface |
| 11 | Callback | `OnBefore(msg any)` | `OnBefore(ctx, input any)` |
| 12 | Middleware | Not available | `Middleware[In, Out]` with `Chain` |
| 13 | LifecycleManager | Not available | Signal-aware graceful shutdown |
| 14 | Worker options | `WithWorkerNumber(n)` on config | `WithGroupWorkers(n)` / `WithPipelineWorkers(n)` |
| 15 | WithHandleFunc | Required on config | Handler passed to constructor directly |

---

## Incremental Migration with v1compat

If a full migration is not feasible immediately, the `v1compat` package provides wrapper types that delegate to v2 internals while preserving the v1 API surface.

### Installation

```bash
go get github.com/shengyanli1982/karta/v2/v1compat
```

### Usage

The v1compat API mirrors v1 exactly — same constructor signatures, same `any`-based types:

```go
import "github.com/shengyanli1982/karta/v2/v1compat"

// Group: v1 API running on v2 engine
config := v1compat.NewV1Config()
config.WithHandleFunc(func(msg any) (any, error) {
    return msg, nil
}).WithWorkerNumber(4)

g := v1compat.NewV1Group(config)
defer g.Stop()
results := g.Map([]any{1, 2, 3}) // []any

// Pipeline: v1 API running on v2 engine
p := v1compat.NewV1Pipeline(nil, config)
defer p.Stop()
p.Submit("task")       // fire-and-forget
p.SubmitWithFunc(fn, "task")  // per-task handler
```

### Bridging Individual Types

Use adapters to incrementally convert individual handlers and callbacks:

```go
// Adapt a v1 handler function to v2 Handler[any, any]
handler := v1compat.HandlerAdapter(func(msg any) (any, error) {
    return processMsg(msg), nil
})

// Adapt v1 callbacks to v2 Callback interface
cb := &v1compat.CallbackAdapter{
    OnBeforeFunc: func(msg any) { log.Println("before:", msg) },
    OnAfterFunc:  func(msg, result any, err error) { log.Println("after:", msg) },
}
```

### Recommended Migration Path

1. **Phase 1**: Add `v1compat` import, replace `karta.NewGroup` → `v1compat.NewV1Group` (zero behavior change)
2. **Phase 2**: Convert handlers one by one from `func(any)(any,error)` → `func(ctx, In)(Out, error)`
3. **Phase 3**: Replace `V1Group` / `V1Pipeline` with native `Group[In, Out]` / `Pipeline[In, Out]`
4. **Phase 4**: Remove `v1compat` import
