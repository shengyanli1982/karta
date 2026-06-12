package karta

import (
	"context"
	"math"
	"time"
)

const (
	defaultMinWorkerNum = int64(2)
	defaultMaxWorkerNum = int64(math.MaxUint16) * 8
	DefaultWorkers      = 2
	DefaultIdleTimeout  = 10 * time.Second
	DefaultScanInterval = 3 * time.Second
	DefaultSpawnRate    = 4
	DefaultBurstLimit   = 8
)

// --- v2 Callback interface (context-aware) ---

// Callback defines the lifecycle hooks for task execution.
type Callback interface {
	OnBefore(ctx context.Context, input any)
	OnAfter(ctx context.Context, input, output any, err error)
}

type emptyCallback struct{}

func (emptyCallback) OnBefore(context.Context, any)            {}
func (emptyCallback) OnAfter(context.Context, any, any, error) {}

// NewEmptyCallback returns a no-op Callback implementation.
func NewEmptyCallback() Callback { return &emptyCallback{} }

// --- Group Options ---

type groupOptions struct {
	workers    int
	callback   Callback
	middleware []any
}

// GroupOption is a functional option for groupOptions.
type GroupOption func(*groupOptions)

func defaultGroupOptions() *groupOptions {
	return &groupOptions{
		workers:  DefaultWorkers,
		callback: NewEmptyCallback(),
	}
}

// WithGroupWorkers sets the number of workers for a group.
// Values outside [defaultMinWorkerNum, defaultMaxWorkerNum] are ignored.
func WithGroupWorkers(n int) GroupOption {
	return func(o *groupOptions) {
		if int64(n) >= defaultMinWorkerNum && int64(n) <= defaultMaxWorkerNum {
			o.workers = n
		}
	}
}

// WithGroupCallback sets the callback for a group.
func WithGroupCallback(cb Callback) GroupOption {
	return func(o *groupOptions) {
		if cb != nil {
			o.callback = cb
		}
	}
}

// WithGroupMiddleware appends middleware to a group.
func WithGroupMiddleware(mws ...any) GroupOption {
	return func(o *groupOptions) {
		o.middleware = append(o.middleware, mws...)
	}
}

// --- Pipeline Options ---

type pipelineOptions struct {
	workers      int
	idleTimeout  time.Duration
	scanInterval time.Duration
	spawnRate    int
	burstLimit   int
	callback     Callback
	middleware   []any
}

// PipelineOption is a functional option for pipelineOptions.
type PipelineOption func(*pipelineOptions)

func defaultPipelineOptions() *pipelineOptions {
	return &pipelineOptions{
		workers:      DefaultWorkers,
		idleTimeout:  DefaultIdleTimeout,
		scanInterval: DefaultScanInterval,
		spawnRate:    DefaultSpawnRate,
		burstLimit:   DefaultBurstLimit,
		callback:     NewEmptyCallback(),
	}
}

// WithPipelineWorkers sets the number of workers for a pipeline.
// Values outside [defaultMinWorkerNum, defaultMaxWorkerNum] are ignored.
func WithPipelineWorkers(n int) PipelineOption {
	return func(o *pipelineOptions) {
		if int64(n) >= defaultMinWorkerNum && int64(n) <= defaultMaxWorkerNum {
			o.workers = n
		}
	}
}

// WithIdleTimeout sets the idle timeout for a pipeline.
// Values <= 0 are ignored.
func WithIdleTimeout(d time.Duration) PipelineOption {
	return func(o *pipelineOptions) {
		if d > 0 {
			o.idleTimeout = d
		}
	}
}

// WithScanInterval sets the scan interval for a pipeline.
// Values <= 0 are ignored.
func WithScanInterval(d time.Duration) PipelineOption {
	return func(o *pipelineOptions) {
		if d > 0 {
			o.scanInterval = d
		}
	}
}

// WithSpawnRate sets the spawn rate for a pipeline.
// Values <= 0 are ignored.
func WithSpawnRate(n int) PipelineOption {
	return func(o *pipelineOptions) {
		if n > 0 {
			o.spawnRate = n
		}
	}
}

// WithPipelineCallback sets the callback for a pipeline.
func WithPipelineCallback(cb Callback) PipelineOption {
	return func(o *pipelineOptions) {
		if cb != nil {
			o.callback = cb
		}
	}
}

// WithPipelineMiddleware appends middleware to a pipeline.
func WithPipelineMiddleware(mws ...any) PipelineOption {
	return func(o *pipelineOptions) {
		o.middleware = append(o.middleware, mws...)
	}
}

// --- v1 兼容类型 (backward compatibility) ---

// MessageHandleFunc v1 消息处理函数类型
type MessageHandleFunc = func(msg any) (any, error)

// DefaultMsgHandleFunc v1 默认消息处理函数
var DefaultMsgHandleFunc MessageHandleFunc = func(msg any) (any, error) { return msg, nil }
