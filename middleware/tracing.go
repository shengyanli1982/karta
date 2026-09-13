package middleware

import (
	"context"

	karta "github.com/shengyanli1982/karta/v2"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const defaultSpanName = "karta.handler"

// Tracing 链路追踪中间件
// 为每个 handler 调用创建 span，记录错误状态。
// 不记录输入/输出值属性：热路径上的 fmt.Sprintf 格式化开销大，
// 且可能将敏感数据泄漏到追踪后端。
func Tracing[In, Out any](tracer trace.Tracer, opts ...TracingOption) karta.Middleware[In, Out] {
	cfg := &tracingConfig{
		spanName: defaultSpanName,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			ctx, span := tracer.Start(ctx, cfg.spanName,
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()

			out, err := next(ctx, input)

			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			} else {
				span.SetStatus(codes.Ok, "")
			}

			return out, err
		}
	}
}

// TracingOption 配置追踪中间件。
type TracingOption func(*tracingConfig)

type tracingConfig struct {
	spanName string
}

// WithSpanName 设置 span 名称。
func WithSpanName(name string) TracingOption {
	return func(c *tracingConfig) {
		c.spanName = name
	}
}
