package middleware

import (
	"context"

	karta "github.com/shengyanli1982/karta/v2"
	"golang.org/x/time/rate"
)

// RateLimit 限流中间件：调用 limiter.Wait(ctx) 等待令牌
// 若 ctx 取消或限流等待失败，返回 error
//
// Panics: limiter 不可为 nil——否则每个任务执行时才 nil 解引用 panic，
// 被 executor recover 吞为晦涩的任务错误；构造期 fail-fast 便于定位配置
// 错误（与 NewPipeline 对 nil scheduler 的校验风格一致）。
func RateLimit[In, Out any](limiter *rate.Limiter) karta.Middleware[In, Out] {
	if limiter == nil {
		panic("karta middleware: limiter must not be nil")
	}
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
