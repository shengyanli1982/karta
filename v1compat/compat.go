package v1compat

import (
	"context"

	karta "github.com/shengyanli1982/karta/v2"
)

// CallbackAdapter 将 v1 回调风格（无 context）适配为 v2 karta.Callback 接口。
// OnBeforeFunc / OnAfterFunc 均为可选字段，nil 时对应钩子为 no-op。
type CallbackAdapter struct {
	OnBeforeFunc func(msg any)
	OnAfterFunc  func(msg, result any, err error)
}

// OnBefore 实现 karta.Callback，将 v2 的 ctx 丢弃后转发给 OnBeforeFunc。
func (a *CallbackAdapter) OnBefore(ctx context.Context, input any) {
	if a.OnBeforeFunc != nil {
		a.OnBeforeFunc(input)
	}
}

// OnAfter 实现 karta.Callback，将 v2 的 ctx 丢弃后转发给 OnAfterFunc。
func (a *CallbackAdapter) OnAfter(ctx context.Context, input, output any, err error) {
	if a.OnAfterFunc != nil {
		a.OnAfterFunc(input, output, err)
	}
}

// HandlerAdapter 将 v1 的函数签名 func(any) (any, error) 适配为 karta.Handler[any, any]。
// 返回的 Handler 在调用时忽略 ctx，仅把 input 透传给 fn。
func HandlerAdapter(fn func(msg any) (any, error)) karta.Handler[any, any] {
	return func(ctx context.Context, input any) (any, error) {
		return fn(input)
	}
}

// V1Config 是 v1 风格的构建器配置，通过链式调用设置 handler、worker 数与回调。
type V1Config struct {
	workers     int
	callback    *CallbackAdapter
	handlerFunc func(any) (any, error)
	withResult  bool
}

// NewV1Config 返回一个默认 workers=2 的配置（与 v2 DefaultWorkers 一致）。
func NewV1Config() *V1Config {
	return &V1Config{workers: 2}
}

// WithWorkerNumber 设置 worker 数量，仅当 n > 0 时生效。
// 注意：v2 内部对 workers 有 [2, maxUint16*8] 的下限约束，
// 若 n < 2 则 v2 侧会回退到默认值 2。
func (c *V1Config) WithWorkerNumber(n int) *V1Config {
	if n > 0 {
		c.workers = n
	}
	return c
}

// WithCallback 设置 v1 风格回调适配器。
func (c *V1Config) WithCallback(cb CallbackAdapter) *V1Config {
	c.callback = &cb
	return c
}

// WithHandleFunc 设置 v1 风格的处理函数。
func (c *V1Config) WithHandleFunc(fn func(any) (any, error)) *V1Config {
	c.handlerFunc = fn
	return c
}

// WithResult 标记是否在结果中保留错误信息（与 v1 行为一致）。
func (c *V1Config) WithResult() *V1Config {
	c.withResult = true
	return c
}
