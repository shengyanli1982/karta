package v1compat

import (
	"context"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

// ---------------------------------------------------------------------------
// V1Group — v1 风格的同步批处理包装
// ---------------------------------------------------------------------------

// V1Group 包装 karta.Group[any, any]，提供 v1 兼容的 Map 语义：
// Map 返回 []any 而非 []Result[any]，错误以 error 值的形式出现在结果切片中。
type V1Group struct {
	inner *karta.Group[any, any]
}

// NewV1Group 根据 V1Config 创建并返回一个 V1Group。
// handlerFunc 必须已通过 WithHandleFunc 设置（nil 将导致运行时 panic）。
func NewV1Group(config *V1Config) *V1Group {
	handler := HandlerAdapter(config.handlerFunc)
	opts := []karta.GroupOption{karta.WithGroupWorkers(config.workers)}
	if config.callback != nil {
		opts = append(opts, karta.WithGroupCallback(config.callback))
	}
	return &V1Group{inner: karta.NewGroup[any, any](handler, opts...)}
}

// Map 并发处理 elements，返回 []any。
// v1 行为：若某项处理失败，对应下标的值为 error（而非 Result 结构体）。
// 当 elements 为空/nil 或 Group 已 Stop 时返回 nil（与 v2 行为一致）。
func (g *V1Group) Map(elements []any) []any {
	results := g.inner.Map(context.Background(), elements)
	if results == nil {
		return nil
	}
	out := make([]any, len(results))
	for i, r := range results {
		if r.Err != nil {
			out[i] = r.Err // v1 行为：error 作为 result 返回
		} else {
			out[i] = r.Value
		}
	}
	return out
}

// Stop 幂等地停止工作组，后续 Map 调用返回 nil。
func (g *V1Group) Stop() { g.inner.Stop() }

// ---------------------------------------------------------------------------
// V1Pipeline — v1 风格的异步任务管道包装
// ---------------------------------------------------------------------------

// V1Pipeline 包装 karta.Pipeline[any, any]，提供 v1 兼容的 Submit 语义：
// Submit 为 fire-and-forget（不返回 Future），SubmitWithFunc 支持 per-task handler。
type V1Pipeline struct {
	inner *karta.Pipeline[any, any]
}

// NewV1Pipeline 根据 V1Config 创建并返回一个 V1Pipeline。
// queue 参数为 v1 接口兼容占位，v2 内部使用 SimpleScheduler(256)。
func NewV1Pipeline(queue any, config *V1Config) *V1Pipeline {
	handler := HandlerAdapter(config.handlerFunc)
	sched := karta.NewSimpleScheduler(256)
	opts := []karta.PipelineOption{karta.WithPipelineWorkers(config.workers)}
	if config.callback != nil {
		opts = append(opts, karta.WithPipelineCallback(config.callback))
	}
	return &V1Pipeline{inner: karta.NewPipeline[any, any](handler, sched, opts...)}
}

// Submit fire-and-forget：提交任务至 pipeline，不返回 Future。
// 返回值为 nil 表示任务已成功入队，不代表 handler 已执行。
func (p *V1Pipeline) Submit(msg any) error {
	_, err := p.inner.Submit(context.Background(), msg)
	return err
}

// SubmitAfter 延迟 delay 后提交任务。
func (p *V1Pipeline) SubmitAfter(msg any, delay time.Duration) error {
	_, err := p.inner.SubmitAfter(context.Background(), msg, delay)
	return err
}

// SubmitWithFunc 使用指定的 fn（per-task handler）提交任务，覆盖 pipeline 默认 handler。
// 这是对 v2 SubmitWithHandler 的 v1 兼容封装；若 v2 不支持 per-task override，
// 则 fallback 到普通 Submit（当前实现使用 SubmitWithHandler，v2 已支持）。
func (p *V1Pipeline) SubmitWithFunc(fn func(any) (any, error), msg any) error {
	handler := HandlerAdapter(fn)
	_, err := p.inner.SubmitWithHandler(context.Background(), handler, msg)
	return err
}

// Stop 幂等地停止 pipeline 及其底层 scheduler。
func (p *V1Pipeline) Stop() { p.inner.Stop() }

// GetWorkerNumber 返回当前运行中的 executor goroutine 数量。
func (p *V1Pipeline) GetWorkerNumber() int64 { return p.inner.GetWorkerNumber() }
