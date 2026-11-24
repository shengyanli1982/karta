package middleware

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	karta "github.com/shengyanli1982/karta/v2"
)

// Logging 日志中间件，记录 handler 执行的输入/输出/耗时
//
// Panics: logger 不可为零值——否则所有日志将被静默丢弃（logr 零值
// Logger 的 Info/Error 为 no-op），配置错误直到运行期都无从发现；
// 构造期 fail-fast 便于定位配置错误（与 NewPipeline 对 nil scheduler
// 的校验风格一致）。
func Logging[In, Out any](logger logr.Logger) karta.Middleware[In, Out] {
	if logger.IsZero() {
		panic("karta middleware: logger must not be the zero value")
	}
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			start := time.Now()
			out, err := next(ctx, input)
			elapsed := time.Since(start)

			if err != nil {
				logger.Error(err, "handler failed",
					"input", input,
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
