package middleware

import (
	"context"
	"log/slog"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

// Logging 日志中间件，记录 handler 执行的输入/输出/耗时
func Logging[In, Out any](logger *slog.Logger) karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			start := time.Now()
			out, err := next(ctx, input)
			elapsed := time.Since(start)

			if err != nil {
				logger.Error("handler failed",
					"input", input,
					"error", err,
					"elapsed", elapsed,
				)
			} else {
				logger.Info("handler completed",
					"input", input,
					"output", out,
					"elapsed", elapsed,
				)
			}
			return out, err
		}
	}
}
