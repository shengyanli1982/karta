package karta

import "context"

// Handler 是 karta v2 的核心处理函数类型 (ADR-001: 强制 context.Context)
// In = 输入类型, Out = 输出类型
type Handler[In, Out any] func(ctx context.Context, input In) (Out, error)

// Middleware 是中间件类型，遵循 net/http 的包裹模式 (ADR-006)
// 接受一个 Handler 返回一个增强后的 Handler
type Middleware[In, Out any] func(Handler[In, Out]) Handler[In, Out]
