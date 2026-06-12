package middleware

import (
	"context"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

// Timeout 超时中间件：为 handler 创建 context.WithTimeout 子 context
// 超时后返回带 context.DeadlineExceeded 的 error
func Timeout[In, Out any](d time.Duration) karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			ctx2, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx2, input)
		}
	}
}
