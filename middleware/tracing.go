package middleware

import (
	"context"
	"fmt"

	karta "github.com/shengyanli1982/karta/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const defaultSpanName = "karta.handler"

// Tracing 链路追踪中间件
// 为每个 handler 调用创建 span，记录输入/输出/错误。
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
				trace.WithAttributes(
					attribute.String("handler.input", fmt.Sprintf("%v", input)),
				),
			)
			defer span.End()

			out, err := next(ctx, input)

			span.SetAttributes(
				attribute.String("handler.output", fmt.Sprintf("%v", out)),
			)

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
