package middleware

import (
	"context"
	"log/slog"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

// Logging 日志中间件，记录 handler 执行的输入/输出/耗时
//
// Panics: logger 不可为 nil——否则每个任务执行时才 nil 解引用 panic，
// 被 executor recover 吞为晦涩的任务错误；构造期 fail-fast 便于定位配置
// 错误（与 NewPipeline 对 nil scheduler 的校验风格一致）。
func Logging[In, Out any](logger *slog.Logger) karta.Middleware[In, Out] {
	if logger == nil {
		panic("karta middleware: logger must not be nil")
	}
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
