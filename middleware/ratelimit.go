package middleware

import (
	"context"

	karta "github.com/shengyanli1982/karta/v2"
	"golang.org/x/time/rate"
)

// RateLimit 限流中间件：调用 limiter.Wait(ctx) 等待令牌
// 若 ctx 取消或限流等待失败，返回 error
func RateLimit[In, Out any](limiter *rate.Limiter) karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			if err := limiter.Wait(ctx); err != nil {
				var zero Out
				return zero, err
			}
			return next(ctx, input)
		}
	}
}
